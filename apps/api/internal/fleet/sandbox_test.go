package fleet_test

import (
	"slices"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	e2bcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/e2b"
	modalcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/modal"
	namespacecloud "github.com/wizenheimer/airship/apps/api/internal/cloud/namespace"
	runloopcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/runloop"
	tenkicloud "github.com/wizenheimer/airship/apps/api/internal/cloud/tenki"
)

const sandboxConfig = `
runners:
  - name: devbox
    cloud: runloop
    instance_type: 4vcpu-16gb
    machine_image: ci-ubuntu
    labels: [airship-devbox]
    extra_config:
      docker: true
      disk: 32
      cache: true
  - name: micro
    cloud: e2b
    instance_type: ci-runner:4vcpu
    labels: [airship-micro]
`

// connectSandboxes connects the fake Runloop and E2B providers with real
// credential encodings, in their one region.
func (h *harness) connectSandboxes() {
	h.t.Helper()
	runloopKey, err := runloopcloud.EncodeSecret(runloopcloud.Secret{APIKey: "ak_0123456789abcdef0123456789"})
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.Runloop, runloopKey, cloud.SandboxRegion, nil); err != nil {
		h.t.Fatal(err)
	}
	e2bKey, err := e2bcloud.EncodeSecret(e2bcloud.Secret{APIKey: "e2b_0123456789abcdef0123456789"})
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.E2B, e2bKey, cloud.SandboxRegion, nil); err != nil {
		h.t.Fatal(err)
	}
}

// Runloop and E2B definitions launch sandboxes shaped as the config says,
// in the clouds' one region, with cache: true a warning since neither has
// object storage; the credentials show their masked keys.
func TestSandboxRunnersLaunch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.connectSandboxes()
	h.setConfig(headSHA, sandboxConfig)

	h.queue("airship-devbox")
	h.queue("airship-micro")
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	devboxes, sandboxes := h.runloop.Instances(), h.e2b.Instances()
	if len(devboxes) != 1 || len(sandboxes) != 1 || len(h.aws.Instances()) != 0 {
		t.Fatalf("runloop = %d, e2b = %d, aws = %d", len(devboxes), len(sandboxes), len(h.aws.Instances()))
	}
	devbox := devboxes[0].Launch
	s := devbox.Machine.Sandbox
	if devbox.Region != cloud.SandboxRegion || devbox.Machine.Arch != "x64" || devbox.Machine.Disk.SizeGB != 32 ||
		s == nil || s.Template != "ci-ubuntu" || s.CPU != 4 || s.MemoryGB != 16 || s.Size != "" || !s.Docker || devbox.Boot.Cache != nil {
		t.Errorf("devbox launch = %+v, sandbox %+v", devbox, s)
	}
	micro := sandboxes[0].Launch
	if m := micro.Machine.Sandbox; micro.Region != cloud.SandboxRegion || m == nil || m.Template != "ci-runner" || m.CPU != 4 || m.Docker {
		t.Errorf("e2b launch = %+v, sandbox %+v", micro, m)
	}
	runner, _ := h.gh.RunnerByJIT(micro.Boot.JITConfig)
	if !slices.Contains(runner.Labels, "e2b") || !slices.Contains(runner.Labels, "airship-micro") {
		t.Errorf("registered labels = %v", runner.Labels)
	}
	var warnings int
	if err := h.pool.QueryRow(h.ctx, "select count(*) from events where kind = 'cache.unavailable'").Scan(&warnings); err != nil || warnings != 1 {
		t.Errorf("cache.unavailable events = %d, %v", warnings, err)
	}
	var identities []string
	rows, err := h.pool.Query(h.ctx, "select identity from cloud_credentials where cloud in ('runloop', 'e2b') order by cloud")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		identities = append(identities, id)
	}
	if !slices.Equal(identities, []string{"e2b_***6789", "ak_***789"}) {
		t.Errorf("identities = %v", identities)
	}
}

// Novita, Lelantos, Namespace, Tenki and Modal definitions launch on their
// clouds shaped as the config says; Namespace in the region it names,
// Modal with its GPU.
func TestPhaseTwoSandboxRunnersLaunch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	connect := func(name string, secret []byte, region string) {
		t.Helper()
		if _, err := h.svc.ConnectCloud(h.ctx, installationID, name, secret, region, nil); err != nil {
			t.Fatal(err)
		}
	}
	novitaKey, _ := e2bcloud.FlavorNovita.EncodeSecret(e2bcloud.Secret{APIKey: "sk_0123456789abcdef0123456789"})
	lelantosKey, _ := e2bcloud.FlavorLelantos.EncodeSecret(e2bcloud.Secret{APIKey: "lel_0123456789abcdef0123456789"})
	namespaceKey, _ := namespacecloud.EncodeSecret(namespacecloud.Secret{Token: "nsct_0123456789abcdef0123456789"})
	tenkiKey, _ := tenkicloud.EncodeSecret(tenkicloud.Secret{APIKey: "tk_0123456789abcdef0123456789"})
	modalKey, _ := modalcloud.EncodeSecret(modalcloud.Secret{TokenID: "ak-0123456789abcdef", TokenSecret: "as-0123456789abcdef"})
	connect(cloud.Novita, novitaKey, cloud.SandboxRegion)
	connect(cloud.Lelantos, lelantosKey, cloud.SandboxRegion)
	connect(cloud.Namespace, namespaceKey, "us")
	connect(cloud.Tenki, tenkiKey, cloud.SandboxRegion)
	connect(cloud.Modal, modalKey, cloud.SandboxRegion)
	h.setConfig(headSHA, `
runners:
  - {name: nov, cloud: novita, instance_type: ci-runner, labels: [airship-nov]}
  - {name: lel, cloud: lelantos, instance_type: base, labels: [airship-lel]}
  - {name: ns, cloud: namespace, instance_type: 8vcpu-16gb, region: eu, labels: [airship-ns], extra_config: {docker: true}}
  - {name: tk, cloud: tenki, instance_type: 4vcpu-8gb, labels: [airship-tk], extra_config: {disk: 40}}
  - {name: md, cloud: modal, instance_type: 4vcpu-32gb, gpu: H100, labels: [airship-md]}
`)
	for _, label := range []string{"airship-nov", "airship-lel", "airship-ns", "airship-tk", "airship-md"} {
		h.queue(label)
	}
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	one := func(name string) cloud.Launch {
		t.Helper()
		instances := h.others[name].Instances()
		if len(instances) != 1 {
			t.Fatalf("%s instances = %d", name, len(instances))
		}

		return instances[0].Launch
	}
	if l := one(cloud.Novita); l.Region != cloud.SandboxRegion || l.Machine.Sandbox == nil || l.Machine.Sandbox.Template != "ci-runner" {
		t.Errorf("novita = %+v", l.Machine)
	}
	if l := one(cloud.Lelantos); l.Machine.Sandbox == nil || l.Machine.Sandbox.Template != "base" {
		t.Errorf("lelantos = %+v", l.Machine)
	}
	if l := one(cloud.Namespace); l.Region != "eu" || l.Machine.Sandbox == nil || l.Machine.Sandbox.CPU != 8 || l.Machine.Sandbox.MemoryGB != 16 || !l.Machine.Sandbox.Docker {
		t.Errorf("namespace = %+v region %s", l.Machine, l.Region)
	}
	if l := one(cloud.Tenki); l.Machine.Disk.SizeGB != 40 || l.Machine.Sandbox == nil || l.Machine.Sandbox.CPU != 4 {
		t.Errorf("tenki = %+v", l.Machine)
	}
	if l := one(cloud.Modal); l.Machine.GPUType != "H100" || !l.Boot.GPU || l.Machine.Sandbox == nil || l.Machine.Sandbox.MemoryGB != 32 {
		t.Errorf("modal = %+v", l.Machine)
	}
}
