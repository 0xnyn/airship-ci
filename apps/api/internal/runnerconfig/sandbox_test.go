package runnerconfig

import (
	"strings"
	"testing"
)

// A Runloop definition names a devbox size or a shape, and a blueprint as
// its image; an E2B definition names a template, optionally with vCPUs.
// Neither has regions.
func TestSandboxRunners(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: devbox
    cloud: runloop
    instance_type: [4vcpu-16gb, X-Large]
    machine_image: [ci-ubuntu, bpt_123]
    labels: [airship-devbox]
    extra_config:
      cache: true
      docker: true
      disk: 32
  - name: micro
    cloud: e2b
    instance_type: [ci-runner:4vcpu, base]
    region: global
    labels: [airship-micro]
    extra_config: {docker: true, env_vars: {TIER: e2b}}
`)
	devbox, micro := f.Runners[0], f.Runners[1]
	if devbox.Cloud != CloudRunloop || devbox.Sandbox == nil || !devbox.Sandbox.Docker || !devbox.Cache || len(devbox.Variants) != 2 {
		t.Fatalf("devbox = %+v", devbox)
	}
	shape, size := devbox.Variants[0], devbox.Variants[1]
	if shape.CPU != 4 || shape.MemoryGB != 16 || shape.Size != "" || shape.Template != "ci-ubuntu" || shape.Disk.SizeGB != 32 || shape.Arch != "x64" || shape.Region != "" {
		t.Errorf("shape = %+v", shape)
	}
	if size.Size != "X_LARGE" || size.CPU != 0 || size.Template != "bpt_123" {
		t.Errorf("size = %+v", size)
	}
	if micro.Cloud != CloudE2B || micro.Sandbox == nil || !micro.Sandbox.Docker || micro.Env["TIER"] != "e2b" {
		t.Fatalf("micro = %+v", micro)
	}
	if v := micro.Variants[0]; v.Template != "ci-runner" || v.CPU != 4 || v.Region != "global" || v.Image != "" {
		t.Errorf("e2b variant = %+v", v)
	}
	if v := micro.Variants[1]; v.Template != "base" || v.CPU != 0 {
		t.Errorf("e2b base variant = %+v", v)
	}
	plain := mustParse(t, `
runners:
  - {name: a, cloud: e2b, instance_type: base, labels: [airship-a]}
`).Runners[0]
	if plain.Sandbox != nil || plain.Variants[0].Disk.SizeGB != 0 {
		t.Errorf("plain e2b = %+v", plain)
	}
}

func TestSandboxRejects(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"runloop instance type": {`
runners:
  - {name: a, cloud: runloop, instance_type: c7i.large, labels: [airship-a]}
`, "not a Runloop size"},
		"runloop odd cpu": {`
runners:
  - {name: a, cloud: runloop, instance_type: 3vcpu-12gb, labels: [airship-a]}
`, "even number"},
		"runloop ratio": {`
runners:
  - {name: a, cloud: runloop, instance_type: 2vcpu-32gb, labels: [airship-a]}
`, "between 2 and 8 GB per vCPU"},
		"runloop too big": {`
runners:
  - {name: a, cloud: runloop, instance_type: 32vcpu-64gb, labels: [airship-a]}
`, "up to 16"},
		"runloop alias image": {`
runners:
  - {name: a, cloud: runloop, instance_type: small, machine_image: ubuntu2204, labels: [airship-a]}
`, "blueprint"},
		"runloop disk with a named size": {`
runners:
  - {name: a, cloud: runloop, instance_type: small, labels: [airship-a], extra_config: {disk: 20}}
`, "fixes its disk"},
		"runloop odd disk": {`
runners:
  - {name: a, cloud: runloop, instance_type: 2vcpu-8gb, labels: [airship-a], extra_config: {disk: 21}}
`, "even number of GB"},
		"region": {`
runners:
  - {name: a, cloud: runloop, instance_type: small, region: us-east-1, labels: [airship-a]}
`, "no regions"},
		"preemptible": {`
runners:
  - {name: a, cloud: e2b, instance_type: base, preemptible: true, labels: [airship-a]}
`, "no preemptible"},
		"gpu": {`
runners:
  - {name: a, cloud: e2b, instance_type: base, gpu: h100, labels: [airship-a]}
`, "no GPUs"},
		"e2b image": {`
runners:
  - {name: a, cloud: e2b, instance_type: base, machine_image: "ubuntu:22.04", labels: [airship-a]}
`, "fixes the image"},
		"e2b template": {`
runners:
  - {name: a, cloud: e2b, instance_type: "base template", labels: [airship-a]}
`, "not a template name or ID on E2B"},
		"e2b too many cpus": {`
runners:
  - {name: a, cloud: e2b, instance_type: "base:64vcpu", labels: [airship-a]}
`, "more than 32"},
		"e2b disk": {`
runners:
  - {name: a, cloud: e2b, instance_type: base, labels: [airship-a], extra_config: {disk: 20}}
`, "Daytona setting"},
		"daytona key on runloop": {`
runners:
  - {name: a, cloud: runloop, instance_type: small, labels: [airship-a], extra_config: {volumes: [{volume: v, mount_path: /v}]}}
`, "Daytona setting"},
		"aws key on e2b": {`
runners:
  - {name: a, cloud: e2b, instance_type: base, labels: [airship-a], extra_config: {SubnetId: subnet-0123456789abcdef0}}
`, "AWS setting"},
		"provision_via": {`
runners:
  - {name: a, cloud: runloop, instance_type: small, labels: [airship-a], extra_config: {provision_via: pull_from_api}}
`, "provision_via is not supported on runloop"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(RepoFile, []byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// Sandbox runners register with their cloud as a label and are Linux.
func TestSandboxLabels(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: box, cloud: runloop, instance_type: small, labels: [airship-box]}
  - {name: vm, cloud: e2b, instance_type: base, labels: [airship-vm]}
`)
	var candidates []Candidate
	for _, r := range f.Runners {
		candidates = append(candidates, Candidate{Source: SourceRepository, Runner: r})
	}
	for _, tc := range []struct{ label, cloud string }{{"airship-box", "runloop"}, {"airship-vm", "e2b"}} {
		attempts := Plan([]string{"self-hosted", "linux", "x64", tc.label}, candidates)
		if len(attempts) != 1 || !contains(attempts[0].Labels, tc.cloud) || !contains(attempts[0].Labels, "x64") {
			t.Errorf("%s: %+v", tc.label, attempts)
		}
		if attempts := Plan([]string{"windows", tc.label}, candidates); len(attempts) != 0 {
			t.Errorf("a windows job matched %s", tc.label)
		}
	}
}

// Novita and Lelantos take E2B templates; Namespace, Tenki and Modal take
// shapes and container images; only Namespace has regions and only Modal
// GPUs.
func TestPhaseTwoSandboxRunners(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: nov, cloud: novita, instance_type: "ci:2vcpu", labels: [airship-nov]}
  - {name: lel, cloud: lelantos, instance_type: base, labels: [airship-lel]}
  - name: ns
    cloud: namespace
    instance_type: [8vcpu-16gb, 4vcpu-8gb]
    machine_image: ["ghcr.io/acme/ci:2026.11", ubuntu2404]
    region: [eu, us]
    labels: [airship-ns]
    extra_config: {docker: true}
  - {name: tk, cloud: tenki, instance_type: 4vcpu-8gb, labels: [airship-tk], extra_config: {disk: 40}}
  - {name: md, cloud: modal, instance_type: 4vcpu-32gb, gpu: a100-80gb, labels: [airship-md]}
`)
	nov, lel, ns, tk, md := f.Runners[0], f.Runners[1], f.Runners[2], f.Runners[3], f.Runners[4]
	if v := nov.Variants[0]; nov.Cloud != CloudNovita || v.Template != "ci" || v.CPU != 2 {
		t.Errorf("novita = %+v", v)
	}
	if v := lel.Variants[0]; lel.Cloud != CloudLelantos || v.Template != "base" {
		t.Errorf("lelantos = %+v", v)
	}
	if ns.Sandbox == nil || !ns.Sandbox.Docker || len(ns.Variants) != 2 {
		t.Fatalf("namespace = %+v", ns)
	}
	if v := ns.Variants[0]; v.CPU != 8 || v.MemoryGB != 16 || v.Region != "eu" || v.Image != "ghcr.io/acme/ci:2026.11" || v.Arch != "x64" {
		t.Errorf("namespace variant = %+v", v)
	}
	if v := ns.Variants[1]; v.Image != "ubuntu:24.04" || v.Region != "us" {
		t.Errorf("namespace alias variant = %+v", v)
	}
	if v := tk.Variants[0]; v.CPU != 4 || v.MemoryGB != 8 || v.Disk.SizeGB != 40 || v.Image != "" {
		t.Errorf("tenki = %+v", v)
	}
	if v := md.Variants[0]; !md.GPU || md.GPUType != "A100-80GB" || v.CPU != 4 || v.MemoryGB != 32 {
		t.Errorf("modal = %+v %+v", md, v)
	}
}

func TestPhaseTwoSandboxRejects(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"namespace region": {`
runners:
  - {name: a, cloud: namespace, instance_type: 2vcpu-4gb, region: global, labels: [airship-a]}
`, "not a Namespace region"},
		"namespace size name": {`
runners:
  - {name: a, cloud: namespace, instance_type: small, labels: [airship-a]}
`, "not a shape on Namespace"},
		"latest image": {`
runners:
  - {name: a, cloud: modal, instance_type: 2vcpu-4gb, machine_image: "python:latest", labels: [airship-a]}
`, "not latest"},
		"gpu off modal": {`
runners:
  - {name: a, cloud: tenki, instance_type: 2vcpu-4gb, gpu: h100, labels: [airship-a]}
`, "no GPUs"},
		"modal gpu name": {`
runners:
  - {name: a, cloud: modal, instance_type: 2vcpu-4gb, gpu: nvidia-tesla-t4, labels: [airship-a]}
`, "not a Modal GPU"},
		"tenki disk": {`
runners:
  - {name: a, cloud: tenki, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {disk: 500}}
`, "between 5 and 100"},
		"disk on modal": {`
runners:
  - {name: a, cloud: modal, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {disk: 20}}
`, "this runner's cloud is modal"},
		"novita image": {`
runners:
  - {name: a, cloud: novita, instance_type: base, machine_image: "ubuntu:22.04", labels: [airship-a]}
`, "fixes the image on Novita"},
		"spot on namespace": {`
runners:
  - {name: a, cloud: namespace, instance_type: 2vcpu-4gb, preemptible: true, labels: [airship-a]}
`, "no preemptible"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(RepoFile, []byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
