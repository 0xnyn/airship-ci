package runnerconfig

import (
	"strings"
	"testing"
)

// A Daytona definition names a sandbox shape or a snapshot as its
// instance_type, a container image as its machine_image, and carries the
// sandbox settings in extra_config.
func TestDaytonaRunner(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: sandbox
    cloud: daytona
    instance_type: [4vcpu-8gb, ci-golden]
    machine_image: ["ghcr.io/acme/ci:2026.10", ""]
    region: [eu, us]
    gpu: h100
    preemptible: [true, false]
    labels: [airship-sandbox]
    extra_config:
      cache: true
      docker: true
      disk: 20
      volumes:
        - {volume: ccache, mount_path: /mnt/ccache, subpath: /go/}
      network_allow_list: "140.82.112.0/20, 185.199.108.0/22"
      env_vars: {CI_TIER: fast}
`)
	r := f.Runners[0]
	if r.Cloud != CloudDaytona || !r.GPU || r.GPUType != "H100" || !r.Cache || r.Env["CI_TIER"] != "fast" {
		t.Fatalf("runner = %+v", r)
	}
	if len(r.Variants) != 2 {
		t.Fatalf("variants = %+v", r.Variants)
	}
	shape, snapshot := r.Variants[0], r.Variants[1]
	if shape.InstanceType != "4vcpu-8gb" || shape.CPU != 4 || shape.MemoryGB != 8 || shape.Snapshot != "" || shape.Image != "ghcr.io/acme/ci:2026.10" ||
		shape.Region != "eu" || !shape.Spot || shape.Arch != "x64" || shape.Disk.SizeGB != 20 || shape.Disk.Type != "" {
		t.Errorf("shape variant = %+v", shape)
	}
	if snapshot.InstanceType != "ci-golden" || snapshot.Snapshot != "ci-golden" || snapshot.CPU != 0 || snapshot.Image != "" || snapshot.Region != "us" || snapshot.Spot {
		t.Errorf("snapshot variant = %+v", snapshot)
	}
	d := r.Daytona
	if d == nil || !d.Docker || d.NetworkAllowList != "140.82.112.0/20,185.199.108.0/22" || d.DomainAllowList != "" {
		t.Fatalf("daytona = %+v", d)
	}
	domains := mustParse(t, `
runners:
  - name: a
    cloud: daytona
    instance_type: 2vcpu-4gb
    labels: [airship-a]
    extra_config: {domain_allow_list: "github.com, *.githubusercontent.com,GITHUB.com"}
`).Runners[0].Daytona
	if domains == nil || domains.DomainAllowList != "github.com,*.githubusercontent.com" || domains.NetworkAllowList != "" {
		t.Errorf("domain list = %+v", domains)
	}
	if len(d.Volumes) != 1 || d.Volumes[0] != (DaytonaVolume{Volume: "ccache", MountPath: "/mnt/ccache", Subpath: "go"}) {
		t.Errorf("volumes = %+v", d.Volumes)
	}

	plain := mustParse(t, `
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a]}
`).Runners[0]
	v := plain.Variants[0]
	if v.Region != "" || v.CPU != 2 || v.MemoryGB != 4 || v.Image != "" || v.Disk.SizeGB != DaytonaDefaultDiskGB || v.Disk.Type != "" || plain.Daytona != nil || plain.GPUType != "" {
		t.Errorf("plain daytona runner = %+v, variant %+v", plain, v)
	}
	aliased := mustParse(t, `
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: ubuntu2404, labels: [airship-a]}
`).Runners[0]
	if aliased.Variants[0].Image != "ubuntu:24.04" {
		t.Errorf("alias = %q", aliased.Variants[0].Image)
	}
	for _, image := range []string{"ubuntu:22.04", "debian:13-slim", "ghcr.io/acme/ci:v1.2.3", "localhost:5000/ci:dev", "ubuntu@sha256:" + strings.Repeat("ab", 32)} {
		mustParse(t, "runners:\n  - {name: a, cloud: daytona, instance_type: 1vcpu-2gb, machine_image: "+image+", labels: [airship-a]}\n")
	}
	if ArchForInstanceType(CloudDaytona, "8vcpu-16gb") != "x64" || HasGPU(CloudDaytona, "8vcpu-16gb") {
		t.Error("daytona shapes are x64 and carry no GPU of their own")
	}
}

func TestDaytonaRejects(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"shape or snapshot": {`
runners:
  - {name: a, cloud: daytona, instance_type: "c7i.large!", labels: [airship-a]}
`, "not a sandbox shape"},
		"shape too big": {`
runners:
  - {name: a, cloud: daytona, instance_type: 999vcpu-8gb, labels: [airship-a]}
`, "more than"},
		"image without tag": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: ubuntu, labels: [airship-a]}
`, "with a tag or digest"},
		"latest image": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: "ubuntu:latest", labels: [airship-a]}
`, "not latest"},
		"ami on daytona": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: ami-0c55b159cbfafe1f0, labels: [airship-a]}
`, "container image"},
		"windows image": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: "mcr.microsoft.com/windows/servercore:ltsc2022", labels: [airship-a]}
`, "Windows sandboxes are not supported"},
		"snapshot with image": {`
runners:
  - {name: a, cloud: daytona, instance_type: ci-golden, machine_image: "ubuntu:22.04", labels: [airship-a]}
`, "fixes the image"},
		"snapshot with disk": {`
runners:
  - {name: a, cloud: daytona, instance_type: ci-golden, labels: [airship-a], extra_config: {disk: 20}}
`, "fixes the disk"},
		"region": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, region: "US East", labels: [airship-a]}
`, "not a Daytona region"},
		"gpu name": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, gpu: nvidia-tesla-t4, labels: [airship-a]}
`, "not a Daytona GPU type"},
		"spot without gpu": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, preemptible: true, labels: [airship-a]}
`, "GPU sandboxes only"},
		"provision_via": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {provision_via: pull_from_api}}
`, "provision_via is not supported on daytona"},
		"windows settings": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {run_as: interactive}}
`, "Windows settings do not apply"},
		"aws key": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {SubnetId: subnet-0123456789abcdef0}}
`, "SubnetId is an AWS setting; this runner's cloud is daytona"},
		"gcp key": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {project_id: acme-ci-123456}}
`, "project_id is a GCP setting; this runner's cloud is daytona"},
		"daytona key on aws": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [airship-a], extra_config: {docker: true}}
`, "docker is a Daytona setting; this runner's cloud is aws"},
		"daytona key on gcp": {`
runners:
  - {name: a, cloud: gcp, instance_type: e2-medium, labels: [airship-a], extra_config: {disk: 20}}
`, "disk is a Daytona setting; this runner's cloud is gcp"},
		"disk size": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {disk: 99999}}
`, "disk must be between"},
		"volume mount": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {volumes: [{volume: v, mount_path: "relative/path"}]}}
`, "mount_path must be an absolute path"},
		"volume twice": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {volumes: [{volume: v, mount_path: /mnt/a}, {volume: w, mount_path: /mnt/a}]}}
`, "mounted twice"},
		"cidr": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {network_allow_list: "github.com"}}
`, "not an IPv4 CIDR"},
		"domain": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {domain_allow_list: "http://github.com"}}
`, "not a domain"},
		"both allow lists": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {network_allow_list: "140.82.112.0/20", domain_allow_list: "github.com"}}
`, "not both"},
		"ipv6 cidr": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {network_allow_list: "2606:50c0::/32"}}
`, "IPv4 CIDR"},
		"too many cidrs": {`
runners:
  - {name: a, cloud: daytona, instance_type: 2vcpu-4gb, labels: [airship-a], extra_config: {network_allow_list: "10.0.0.0/24,10.0.1.0/24,10.0.2.0/24,10.0.3.0/24,10.0.4.0/24,10.0.5.0/24,10.0.6.0/24,10.0.7.0/24,10.0.8.0/24,10.0.9.0/24,10.0.10.0/24"}}
`, "at most 10 entries"},
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

// A Daytona runner registers with daytona as its cloud label and is Linux
// whatever its image, so a job asking for linux matches it and one asking
// for windows does not.
func TestDaytonaLabels(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: box, cloud: daytona, instance_type: 2vcpu-4gb, machine_image: "ghcr.io/acme/ci:1", labels: [airship-box]}
  - {name: gpu, cloud: daytona, instance_type: 2vcpu-4gb, gpu: H100, labels: [airship-gpu]}
`)
	var candidates []Candidate
	for _, r := range f.Runners {
		candidates = append(candidates, Candidate{Source: SourceRepository, Runner: r})
	}
	attempts := Plan([]string{"self-hosted", "linux", "x64", "daytona", "airship-box"}, candidates)
	if len(attempts) != 1 || attempts[0].Runner.Name != "box" {
		t.Fatalf("linux box: %+v", attempts)
	}
	for _, want := range []string{"self-hosted", "x64", "daytona", "cpu", "airship-box"} {
		if !contains(attempts[0].Labels, want) {
			t.Errorf("labels %v lack %s", attempts[0].Labels, want)
		}
	}
	if attempts := Plan([]string{"windows", "airship-box"}, candidates); len(attempts) != 0 {
		t.Errorf("a windows job matched a daytona runner: %+v", attempts)
	}
	if attempts := Plan([]string{"gpu", "airship-gpu"}, candidates); len(attempts) != 1 || !contains(attempts[0].Labels, "gpu") {
		t.Errorf("gpu: %+v", attempts)
	}
}

func contains(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}

	return false
}
