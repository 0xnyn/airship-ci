package fleet_test

import (
	"slices"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

const daytonaConfig = `
runners:
  - name: box
    cloud: daytona
    instance_type: 4vcpu-8gb
    machine_image: "ghcr.io/acme/ci:2026.10"
    region: eu
    labels: [airship-box]
    extra_config:
      docker: true
      disk: 20
      cache: true
      volumes:
        - {volume: ccache, mount_path: /mnt/ccache}
  - name: golden
    cloud: daytona
    instance_type: ci-golden
    gpu: H100
    preemptible: true
    labels: [airship-golden]
`

// A Daytona definition launches a sandbox shaped as the config says, in
// the region it names or the credential's default, with its extra_config
// carried to the provider; cache: true is a warning, not a proxy, since
// Daytona has no object storage (the harness registers it without cache
// support, as the real provider is).
func TestDaytonaRunnersLaunchAsSandboxes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.connectDaytona("us")
	h.setConfig(headSHA, daytonaConfig)

	h.queue("airship-box")
	h.queue("airship-golden")
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	instances := h.daytona.Instances()
	if len(instances) != 2 || len(h.aws.Instances()) != 0 || len(h.gcp.Instances()) != 0 {
		t.Fatalf("daytona instances = %d, aws = %d, gcp = %d", len(instances), len(h.aws.Instances()), len(h.gcp.Instances()))
	}
	byType := map[string]cloud.Launch{}
	for _, instance := range instances {
		byType[instance.Launch.Machine.InstanceType] = instance.Launch
	}
	box, golden := byType["4vcpu-8gb"], byType["ci-golden"]
	d := box.Machine.Daytona
	if box.Region != "eu" || box.Machine.Image != "ghcr.io/acme/ci:2026.10" || box.Machine.Arch != "x64" || box.Machine.Disk.SizeGB != 20 || box.Machine.Disk.Type != "" ||
		d == nil || d.CPU != 4 || d.MemoryGB != 8 || d.Snapshot != "" || !d.Docker || len(d.Volumes) != 1 || d.Volumes[0].Volume != "ccache" || d.Volumes[0].MountPath != "/mnt/ccache" {
		t.Errorf("box launch = %+v, machine %+v, daytona %+v", box, box.Machine, d)
	}
	if box.Boot.Cache != nil || box.Boot.PullFromAPI || box.Boot.MaxLifetimeSeconds <= 0 {
		t.Errorf("box boot = %+v", box.Boot)
	}
	g := golden.Machine.Daytona
	if golden.Region != "us" || golden.Machine.Image != "" || golden.Machine.GPUType != "H100" || !golden.Machine.Spot || !golden.Boot.GPU ||
		g == nil || g.Snapshot != "ci-golden" || g.CPU != 0 || g.Docker {
		t.Errorf("golden launch = %+v, machine %+v, daytona %+v", golden, golden.Machine, g)
	}
	boxRunner, _ := h.gh.RunnerByJIT(box.Boot.JITConfig)
	for _, want := range []string{"self-hosted", "x64", "daytona", "cpu", "airship-box"} {
		if !slices.Contains(boxRunner.Labels, want) {
			t.Errorf("registered labels %v lack %s", boxRunner.Labels, want)
		}
	}
	var warnings int
	if err := h.pool.QueryRow(h.ctx, "select count(*) from events where kind = 'cache.unavailable'").Scan(&warnings); err != nil || warnings != 1 {
		t.Errorf("cache.unavailable events = %d, %v", warnings, err)
	}

	// The scan keeps both; a sandbox that stops itself is gone, and the
	// runner lost after the grace period.
	h.reconcile()
	wantStates(t, h, "running", "running")
	h.daytona.Exit(instances[0].ID)
	h.daytona.Exit(instances[1].ID)
	h.exec("update runners set running_at = now() - interval '1 hour'")
	h.reconcile()
	wantStates(t, h, "finished:lost", "finished:lost", "pending", "pending")
}
