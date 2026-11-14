package runloop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/runloop/runlooptest"
)

const testKey = "ak_0123456789abcdef0123456789"

func init() {
	pollInterval = time.Millisecond
}

func setup(t *testing.T) (*Provider, *runlooptest.Server, []byte) {
	t.Helper()
	fake := runlooptest.New(t, testKey)
	secret, err := EncodeSecret(Secret{APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}

	return New(Options{Endpoint: fake.URL}), fake, secret
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       "0190c0de-0000-7000-8000-000000000001",
		RunnerName:     "airship-acme-api-1",
		InstallationID: 42,
		Repository:     "acme/api",
		Region:         cloud.SandboxRegion,
		Machine: cloud.Machine{
			InstanceType: "4vcpu-16gb",
			Arch:         "x64",
			Disk:         cloud.Disk{SizeGB: 32},
			Sandbox:      &cloud.SandboxOptions{Template: "ci-ubuntu", CPU: 4, MemoryGB: 16, Docker: true},
		},
		Boot: cloud.Boot{JITConfig: "aml0LXNlY3JldA==", RunnerVersion: "2.330.0", MaxLifetimeSeconds: 3600},
	}
}

// A launch creates one devbox shaped as asked, whose entrypoint waits for
// the boot script and holds no secret, then writes the script and the
// marker; a repeated launch finds the same devbox.
func TestLaunchCreatesBootsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	launch := testLaunch()

	id, err := p.Launch(context.Background(), secret, launch)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Launch(context.Background(), secret, launch)
	if err != nil || again != id {
		t.Fatalf("second launch = %q, %v; want %q", again, err, id)
	}
	boxes := fake.Devboxes()
	if len(boxes) != 1 || fake.Creates() != 1 {
		t.Fatalf("devboxes = %d, creates = %d", len(boxes), fake.Creates())
	}
	d := boxes[0]
	if d.Name != launch.RunnerName || d.Metadata[MetaInstallation] != "42" || d.Metadata[MetaRunner] != launch.RunnerID || d.Metadata[MetaRepository] != "acme/api" {
		t.Errorf("devbox = %+v", d)
	}
	lp, _ := d.Request["launch_parameters"].(map[string]any)
	if lp["resource_size_request"] != "CUSTOM_SIZE" || lp["custom_cpu_cores"] != 4.0 || lp["custom_gb_memory"] != 16.0 || lp["custom_disk_size"] != 32.0 ||
		lp["keep_alive_time_seconds"] != 4500.0 || lp["architecture"] != "x86_64" {
		t.Errorf("launch_parameters = %v", lp)
	}
	if d.Request["blueprint_name"] != "ci-ubuntu" || d.Request["blueprint_id"] != nil {
		t.Errorf("blueprint = %v / %v", d.Request["blueprint_name"], d.Request["blueprint_id"])
	}
	entry, _ := d.Request["entrypoint"].(string)
	if strings.Contains(entry, "aml0LXNlY3JldA==") || !strings.Contains(entry, readyPath(launch.RunnerID)) {
		t.Errorf("entrypoint = %q", entry)
	}
	script := d.Files[scriptPath(launch.RunnerID)]
	if !strings.Contains(script, "ACTIONS_RUNNER_INPUT_JITCONFIG='aml0LXNlY3JldA=='") || !strings.Contains(script, "docker ready") || strings.Contains(script, "kill -TERM 1") {
		t.Errorf("boot script missing the runner, Docker, or carrying Daytona's stop: %q", script[:min(len(script), 200)])
	}
	if d.Files[readyPath(launch.RunnerID)] == "" {
		t.Error("the ready marker was not written")
	}
}

// Named sizes and blueprint IDs go to their own fields.
func TestRequestSizesAndBlueprints(t *testing.T) {
	t.Parallel()
	launch := testLaunch()
	launch.Machine.Sandbox = &cloud.SandboxOptions{Template: "bpt_123abc", Size: "LARGE"}
	body, err := buildRequest(launch)
	if err != nil {
		t.Fatal(err)
	}
	if body.BlueprintID != "bpt_123abc" || body.BlueprintName != "" || body.LaunchParameters.ResourceSizeRequest != "LARGE" || body.LaunchParameters.CustomCPUCores != 0 {
		t.Errorf("body = %+v", body)
	}
	launch.Boot.MaxLifetimeSeconds = 100 * 3600
	launch.Machine.Sandbox = &cloud.SandboxOptions{Size: "SMALL"}
	body, _ = buildRequest(launch)
	if body.LaunchParameters.KeepAliveTimeSeconds != int(MaxKeepAlive.Seconds()) || body.BlueprintName != "" {
		t.Errorf("keep-alive = %d, blueprint %q", body.LaunchParameters.KeepAliveTimeSeconds, body.BlueprintName)
	}
	launch.Machine.Sandbox = &cloud.SandboxOptions{Size: "HUGE"}
	if _, err := buildRequest(launch); err == nil {
		t.Error("an unknown size was accepted")
	}
	launch.Machine.Sandbox = &cloud.SandboxOptions{}
	if _, err := buildRequest(launch); err == nil {
		t.Error("a devbox with no size was accepted")
	}
}

// A create whose answer is lost still finds the devbox it made.
func TestLostCreateAnswerFindsTheDevbox(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.LoseCreateAnswer = true
	id, err := p.Launch(context.Background(), secret, testLaunch())
	if err != nil {
		t.Fatal(err)
	}
	if fake.Creates() != 1 || fake.Devboxes()[0].ID != id {
		t.Fatalf("creates = %d, id %s", fake.Creates(), id)
	}
}

// A devbox that cannot be booted is shut down, and the error is retryable.
func TestFailedBootShutsTheDevboxDown(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.FailWrites = true
	_, err := p.Launch(context.Background(), secret, testLaunch())
	if !cloud.IsRetryable(err) {
		t.Fatalf("err = %v, want retryable", err)
	}
	if boxes := fake.Devboxes(); len(boxes) != 1 || boxes[0].Status != "shutdown" {
		t.Fatalf("devboxes = %+v", boxes)
	}
}

// A devbox that fails to start is a retryable failure; a refused create
// is not.
func TestStartFailureAndRefusal(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.StartAs = "failure"
	if _, err := p.Launch(context.Background(), secret, testLaunch()); !cloud.IsRetryable(err) {
		t.Errorf("failed start = %v, want retryable", err)
	}
	p2, fake2, secret2 := setup(t)
	fake2.FailCreate = 400
	_, err := p2.Launch(context.Background(), secret2, testLaunch())
	if err == nil || cloud.IsRetryable(err) || cloud.IsAmbiguous(err) {
		t.Errorf("refused create = %v, want permanent", err)
	}
	fake2.FailCreate = 0
	if _, err := p.Launch(context.Background(), secret, cloud.Launch{RunnerID: "x", RunnerName: "x", Region: "global"}); err == nil {
		t.Error("a bad runner ID was accepted")
	}
}

// List reports only the installation's live devboxes, across pages, with
// suspended ones as stopped; Terminate refuses another installation's.
func TestListAndTerminate(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.PageSize = 2
	for i, d := range []runlooptest.Devbox{
		{ID: "dbx_a", Status: "running", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r1"}},
		{ID: "dbx_b", Status: "running", Metadata: map[string]string{MetaInstallation: "7", MetaRunner: "r2"}},
		{ID: "dbx_c", Status: "suspended", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r3"}},
		{ID: "dbx_d", Status: "shutdown", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r4"}},
		{ID: "dbx_e", Status: "running", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r5"}},
		{ID: "dbx_f", Status: "running", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r6"}},
	} {
		d.CreateTimeMs = int64(i)
		fake.Add(d)
	}
	instances, err := p.List(context.Background(), secret, cloud.SandboxRegion, 42)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]cloud.Instance{}
	for _, i := range instances {
		got[i.InstanceID] = i
	}
	if len(got) != 4 || got["dbx_a"].RunnerID != "r1" || !got["dbx_c"].Stopped || got["dbx_a"].Stopped || got["dbx_e"].RunnerID != "r5" || got["dbx_f"].RunnerID != "r6" {
		t.Fatalf("instances = %+v", instances)
	}
	if _, err := p.List(context.Background(), secret, "us", 42); err == nil {
		t.Error("a region was accepted")
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "dbx_b", 42); err == nil {
		t.Error("another installation's devbox was shut down")
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "dbx_a", 42); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dbx_a", "dbx_d", "dbx_missing"} {
		if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, id, 42); err != nil {
			t.Errorf("terminate %s again = %v", id, err)
		}
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	p, _, secret := setup(t)
	id, err := p.Verify(context.Background(), secret, cloud.SandboxRegion)
	if err != nil || id.Principal != "API key ak_***789" {
		t.Fatalf("verify = %+v, %v", id, err)
	}
	if _, err := p.Verify(context.Background(), secret, "us"); err == nil {
		t.Error("a region was accepted")
	}
	wrong, _ := EncodeSecret(Secret{APIKey: "ak_wrongwrongwrongwrong"})
	if _, err := p.Verify(context.Background(), wrong, cloud.SandboxRegion); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("wrong key = %v", err)
	}
	if _, err := EncodeSecret(Secret{APIKey: "short"}); err == nil {
		t.Error("a short key was accepted")
	}
	if _, err := EncodeSecret(Secret{APIKey: testKey, APIURL: "ftp://x"}); err == nil {
		t.Error("an ftp URL was accepted")
	}
}
