package fleet_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

const gcpConfig = `
runners:
  - name: pinned
    cloud: gcp
    instance_type: n1-standard-4
    region: europe-west4-b
    gpu: nvidia-tesla-t4
    labels: [cirun-pinned]
    extra_config:
      project_id: acme-runners-1
      enable_public_ip: false
      baseDisk: {diskSizeGb: 100}
  - name: anywhere
    cloud: gcp
    instance_type: t2a-standard-4
    labels: [cirun-anywhere]
`

// A GCP definition launches with the zone it names (or the credential's
// default zone) pinned, in the project extra_config picks, and its runners
// are recorded under a region scope that carries that project, so the
// inventory scan lists the right project.
func TestGCPRunnersLaunchInTheirZoneAndProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.connectGCP("acme-ci-123456", "us-central1-b")
	h.setConfig(headSHA, gcpConfig)

	h.queue("cirun-pinned")
	h.queue("cirun-anywhere")
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	instances := h.gcp.Instances()
	if len(instances) != 2 || len(h.aws.Instances()) != 0 {
		t.Fatalf("gcp instances = %d, aws instances = %d", len(instances), len(h.aws.Instances()))
	}
	byType := map[string]cloud.Launch{}
	for _, instance := range instances {
		byType[instance.Launch.Machine.InstanceType] = instance.Launch
	}
	pinned, anywhere := byType["n1-standard-4"], byType["t2a-standard-4"]
	if pinned.Region != "europe-west4@acme-runners-1" || pinned.Machine.Zone != "europe-west4-b" || pinned.Machine.GCP == nil ||
		pinned.Machine.GCP.Project != "acme-runners-1" || !pinned.Machine.GCP.NoPublicIP || pinned.Machine.Disk.SizeGB != 100 || pinned.Machine.Disk.Type != "" ||
		pinned.Machine.GPUType != "nvidia-tesla-t4" || !pinned.Boot.GPU {
		t.Errorf("pinned launch = %+v, machine %+v, gcp %+v", pinned, pinned.Machine, pinned.Machine.GCP)
	}
	if anywhere.Region != "us-central1" || anywhere.Machine.Zone != "us-central1-b" || anywhere.Machine.Arch != "arm64" || anywhere.Machine.GCP != nil {
		t.Errorf("default-location launch = %+v, machine %+v", anywhere, anywhere.Machine)
	}
	var regions []string
	rows, err := h.pool.Query(h.ctx, "select region from runners order by region")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var region string
		_ = rows.Scan(&region)
		regions = append(regions, region)
	}
	rows.Close()
	if len(regions) != 2 || regions[0] != "europe-west4@acme-runners-1" || regions[1] != "us-central1" {
		t.Errorf("runner regions = %v", regions)
	}
	pinnedRunner, _ := h.gh.RunnerByJIT(pinned.Boot.JITConfig)
	for _, want := range []string{"self-hosted", "x64", "gcp", "gpu", "cirun-pinned"} {
		if !slices.Contains(pinnedRunner.Labels, want) {
			t.Errorf("registered labels %v lack %s", pinnedRunner.Labels, want)
		}
	}

	// The scan lists each scope, including the other project's, and keeps
	// both runners.
	h.reconcile()
	wantStates(t, h, "running", "running")
	h.gcp.Exit(instances[0].ID)
	h.gcp.Exit(instances[1].ID)
	h.exec("update runners set running_at = now() - interval '1 hour'")
	h.reconcile()
	wantStates(t, h, "finished:lost", "finished:lost", "pending", "pending")
}

// A launch whose answer was lost may or may not have made a machine. The
// runner is finished as launch_unconfirmed with its instance unknown and
// the job retried; when the inventory scan lists the machine the runner is
// revived and the retry cancelled, and when it does not the runner is
// marked gone after the grace period.
func TestUnconfirmedLaunchIsSettledByTheInventoryScan(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.aws.FailLaunch = func(cloud.Launch) error {
		return &cloud.LaunchError{Err: errors.New("connection reset by peer"), Retryable: true, Ambiguous: true}
	}
	h.queue()
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:launch_unconfirmed", "pending")
	var gone bool
	if err := h.pool.QueryRow(h.ctx, "select instance_gone_at is not null from runners where state = 'finished'").Scan(&gone); err != nil || gone {
		t.Fatalf("unconfirmed runner marked gone = %v, %v; its machine may exist", gone, err)
	}
	if n := len(h.aws.Instances()); n != 1 {
		t.Fatalf("the fake should hold the machine the lost call made, has %d", n)
	}

	// The machine did exist: it is listed, the runner revived, the retry
	// cancelled.
	h.aws.FailLaunch = nil
	h.reconcile()
	runners := h.runners()
	if len(runners) != 2 || runners[0].State != "running" || runners[0].InstanceID == nil || runners[1].State != "finished" || *runners[1].FinishReason != "cancelled" {
		t.Fatalf("after listing the machine: %v", states(runners))
	}

	// The machine did not exist: after the grace period the runner is
	// gone for good and the retry serves the job.
	g := newHarness(t)
	g.aws.FailLaunch = func(cloud.Launch) error {
		return &cloud.LaunchError{Err: errors.New("connection reset by peer"), Retryable: true, Ambiguous: true}
	}
	g.queue()
	if _, err := g.svc.LaunchOnce(g.ctx); err != nil {
		t.Fatal(err)
	}
	for _, instance := range g.aws.Instances() {
		g.aws.Exit(instance.ID)
	}
	g.aws.FailLaunch = nil
	g.reconcile()
	wantStates(t, g, "finished:launch_unconfirmed", "pending") // within the grace period nothing is decided
	g.exec("update runners set finished_at = now() - interval '1 hour' where state = 'finished'")
	g.reconcile()
	if err := g.pool.QueryRow(g.ctx, "select instance_gone_at is not null from runners where state = 'finished'").Scan(&gone); err != nil || !gone {
		t.Fatalf("unconfirmed runner marked gone = %v, %v after the grace period", gone, err)
	}
	g.launch()
	wantStates(t, g, "finished:launch_unconfirmed", "running")

	// Unconfirmed launches count toward the attempt cap like failed ones.
	k := newHarness(t)
	k.aws.FailLaunch = func(cloud.Launch) error {
		return &cloud.LaunchError{Err: errors.New("connection reset by peer"), Retryable: true, Ambiguous: true}
	}
	k.queue()
	for range 4 {
		if _, err := k.svc.LaunchOnce(k.ctx); err != nil {
			t.Fatal(err)
		}
	}
	wantStates(t, k, "finished:launch_unconfirmed", "finished:launch_unconfirmed", "finished:launch_unconfirmed")
}

// GitHub reporting a job on a runner Airship had given up on (its launch
// went unconfirmed) proves the machine exists: the webhook revives the
// runner as busy without waiting for the inventory scan, the retry queued
// for the job is cancelled, and the completed event finishes it as usual.
func TestWebhookRevivesAnUnconfirmedRunner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.aws.FailLaunch = func(cloud.Launch) error {
		return &cloud.LaunchError{Err: errors.New("connection reset by peer"), Retryable: true, Ambiguous: true}
	}
	job := h.queue()
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:launch_unconfirmed", "pending")
	runners := h.runners()

	h.start(job, runners[0].Name)
	wantStates(t, h, "busy", "finished:cancelled")
	if got := h.runners(); got[0].TriggerJobID != nil || got[0].FinishReason != nil {
		t.Fatalf("revived runner keeps a job trigger or finish reason: %+v", got[0])
	}
	if j := h.job(job.ID); j.RunnerID == nil || *j.RunnerID != runners[0].ID {
		t.Fatalf("job not linked to the revived runner: %+v", j.RunnerID)
	}

	// The scan then lists its machine and records it, without disturbing
	// the busy runner.
	h.aws.FailLaunch = nil
	h.reconcile()
	if got := h.runners(); got[0].State != "busy" || got[0].InstanceID == nil {
		t.Fatalf("after the scan: %v, instance %v", states(got), got[0].InstanceID)
	}
	h.complete(job, &runners[0].Name, "success")
	wantStates(t, h, "finished:completed", "finished:cancelled")
}

// A stolen runner whose job already has a retry: the robbed job keeps its
// retry, the thief's spare is surplus, and the webhook succeeds rather
// than tripping over one-live-runner-per-job.
func TestHandOffLeavesAJobWithARetryAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := h.queue()
	// The launcher died between claiming a's runner and recording its
	// machine, which the cloud does not list.
	h.exec("update runners set state = 'launching', launching_at = now(), lease_expires_at = now() - interval '1 second'")
	h.reconcile() // a's runner is given up; a retry is queued for a
	wantStates(t, h, "finished:lease_expired", "pending")
	thief := h.runners()[0].Name
	b := h.queue()
	h.launch() // a's retry and b's runner
	wantStates(t, h, "finished:lease_expired", "running", "running")

	// The given-up machine was alive after all and GitHub gave it b.
	h.start(b, thief)
	runners := h.runners()
	if states(runners)[0] != "busy" || runners[0].TriggerJobID != nil {
		t.Fatalf("thief not revived as busy: %v", states(runners))
	}
	if runners[1].TriggerJobID == nil || *runners[1].TriggerJobID != a.ID {
		t.Fatalf("a's retry lost its job: trigger %v", runners[1].TriggerJobID)
	}
	if runners[2].State != "running" || runners[2].TriggerJobID == nil || *runners[2].TriggerJobID != b.ID {
		t.Fatalf("b's own runner = %s trigger %v; it is surplus and idles out", runners[2].State, runners[2].TriggerJobID)
	}
}
