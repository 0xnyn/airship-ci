package runnerconfig

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, src string) File {
	t.Helper()
	f, err := Parse(RepoFile, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	return f
}

func TestParseMinimal(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: small-runner
    cloud: aws
    instance_type: c5.xlarge
    machine_image: ami-0c55b159cbfafe1f0
    region: us-east-1
    preemptible: false
    labels:
      - airship-small
`)
	r := f.Runners[0]
	if r.Name != "small-runner" || r.Cloud != CloudAWS || len(r.Variants) != 1 {
		t.Fatalf("parsed %+v", r)
	}
	v := r.Variants[0]
	if v.InstanceType != "c5.xlarge" || v.Image != "ami-0c55b159cbfafe1f0" || v.Region != "us-east-1" || v.Spot || v.Arch != "x64" || v.Disk.SizeGB != DefaultDiskGB || v.Disk.Type != "gp2" {
		t.Fatalf("variant %+v", v)
	}
}

// Fallback example: lists are zipped, scalars repeat.
func TestFallbacksZip(t *testing.T) {
	f := mustParse(t, `
runners:
- name: "aws-multiple-runner"
  cloud: "aws"
  instance_type: ["t3.small", "t3.medium", "c7g.large"]
  machine_image: "ami-0123456789abcdef0"
  region: "eu-west-1"
  preemptible: [true, true, false]
  labels: ["airship-multiple-runner-options"]
`)
	got := f.Runners[0].Variants
	want := []struct {
		instance, arch string
		spot           bool
	}{{"t3.small", "x64", true}, {"t3.medium", "x64", true}, {"c7g.large", "arm64", false}}
	if len(got) != len(want) {
		t.Fatalf("got %d variants", len(got))
	}
	for i, w := range want {
		if got[i].InstanceType != w.instance || got[i].Spot != w.spot || got[i].Arch != w.arch || got[i].Region != "eu-west-1" {
			t.Errorf("variant %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unknown field": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    instance_tpye: typo
`, "unknown field instance_tpye"},
		"unsupported cloud": {`
runners:
  - {name: a, cloud: openstack, instance_type: m1.small, labels: [airship-a]}
`, `cloud "openstack" is not supported`},
		"gcp zone": {`
runners:
  - {name: a, cloud: gcp, instance_type: n1-standard-1, region: us-east-1, labels: [airship-a]}
`, `is not a GCP zone or region`},
		"gcp machine type": {`
runners:
  - {name: a, cloud: gcp, instance_type: c7i.large, labels: [airship-a]}
`, `is not a Compute Engine machine type`},
		"gcp image": {`
runners:
  - {name: a, cloud: gcp, instance_type: n1-standard-1, machine_image: "My Image", labels: [airship-a]}
`, `must be an image or family name`},
		"gcp gpu name": {`
runners:
  - {name: a, cloud: gcp, instance_type: n1-standard-1, gpu: "Tesla T4", labels: [airship-a]}
`, `is not a Compute Engine GPU name`},
		"aws key on gcp": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n1-standard-1
    labels: [airship-a]
    extra_config: {SubnetId: subnet-0123456789abcdef0}
`, "SubnetId is an AWS setting"},
		"existing disk source": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n1-standard-1
    labels: [airship-a]
    extra_config: {existingDisk: {deviceName: data, diskSizeGb: 122}}
`, "source must be a disk reference"},
		"existing disk zone conflict": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n1-standard-1
    region: us-central1-a
    labels: [airship-a]
    extra_config: {existingDisk: {source: projects/my-project/zones/us-central1-b/disks/data}}
`, "existingDisk is in zone us-central1-b"},
		"existing disk region conflict": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n1-standard-1
    region: europe-west4
    labels: [airship-a]
    extra_config: {existingDisk: {source: regions/us-central1/disks/data}}
`, "existingDisk is in region us-central1"},
		"existing disk mode": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n1-standard-1
    labels: [airship-a]
    extra_config: {existingDisk: {source: zones/us-central1-b/disks/data, mode: READ_MANY}}
`, "mode must be READ_WRITE or READ_ONLY"},
		"migrate with spot": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n2-standard-4
    preemptible: true
    labels: [airship-a]
    extra_config: {scheduling: {onHostMaintenance: MIGRATE}}
`, "MIGRATE is not allowed on preemptible"},
		"migrate with gpu": {`
runners:
  - name: a
    cloud: gcp
    instance_type: a2-highgpu-1g
    labels: [airship-a]
    extra_config: {scheduling: {onHostMaintenance: MIGRATE}}
`, "MIGRATE is not allowed on a2-highgpu-1g"},
		"public ip conflict": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n2-standard-4
    labels: [airship-a]
    extra_config:
      enable_public_ip: false
      networkInterfaces:
        - network: default
          accessConfigs: [{type: ONE_TO_ONE_NAT}]
`, "conflicts with enable_public_ip: false"},
		"bad service account": {`
runners:
  - name: a
    cloud: gcp
    instance_type: n2-standard-4
    labels: [airship-a]
    extra_config:
      serviceAccounts: [{email: someone@example.com}]
`, "email must be a service account address"},
		"label prefix": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [other-a]}
`, `must start with "airship-"`},
		"suffix separator in label": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [airship-a--b]}
`, `may not contain "--"`},
		"uneven fallbacks": {`
runners:
  - name: a
    cloud: aws
    instance_type: [c7i.large, c7i.xlarge]
    region: [us-east-1, us-east-2, eu-west-1]
    labels: [airship-a]
`, "fallback lists must be the same length"},
		"bad ami": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, machine_image: ubuntu, labels: [airship-a]}
`, "must be an AMI ID"},
		"duplicate name": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [airship-a]}
  - {name: A, cloud: aws, instance_type: c7i.large, labels: [airship-b]}
`, "defined twice"},
		"reserved env": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config:
      env_vars: {GITHUB_TOKEN: x}
`, "reserved for the runner"},
		"env newline": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config:
      env_vars: {FOO: "a\nb"}
`, "line breaks"},
		"gcp extra config": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config: {baseDisk: {diskSizeGb: 10}}
`, "baseDisk is a GCP setting"},
		"bad provision_via": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config: {provision_via: push}
`, "provision_via must be pull_from_api"},
		"bad runner path": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config: {runner_path: "C:\\r\"; calc"}
`, "runner_path must be a Windows path"},
		"bad region": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, region: RegionOne, labels: [airship-a]}
`, "not an AWS region"},
		"bad init rate": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config:
      BlockDeviceMappings: [{DeviceName: /dev/sda1, VolumeSize: 100, VolumeInitializationRate: 50}]
`, "VolumeInitializationRate must be between 100 and 300"},
		"two idle forms": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    idle:
      replicas: 1
      usage_based: {trigger: runner_allocated, replicas: 1, duration: 10}
`, "exactly one of"},
		"bad timezone": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    idle:
      schedule: [{day: mon-fri, time: 9-17, timezone: Mars/Olympus, replicas: 2}]
`, "not a time zone"},
		"on_prem": {`
runners:
  - {name: a, cloud: on_prem, instance_type: 4vcpu-8gb, labels: [airship-a]}
`, `cloud "on_prem" is not supported yet`},
		"empty":      {"", "file is empty"},
		"no runners": {"runners: []", "at least one runner"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(RepoFile, []byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestParseRejectsHugeAndAliasBombs(t *testing.T) {
	if _, err := Parse(RepoFile, []byte(strings.Repeat("#", MaxFileBytes+1))); err == nil {
		t.Fatal("oversized file accepted")
	}
	// gpu is the one field that accepts nested YAML, so the bomb lives
	// there: each runner's gpu aliases the previous one nine times.
	var b strings.Builder
	b.WriteString("runners:\n  - {name: r0, cloud: aws, instance_type: c7i.large, labels: [airship-a], gpu: &a0 [x,x,x,x,x,x,x,x,x]}\n")
	for i := 1; i <= 9; i++ {
		prev := fmt.Sprintf("*a%d", i-1)
		fmt.Fprintf(&b, "  - {name: r%d, cloud: aws, instance_type: c7i.large, labels: [airship-a], gpu: &a%d [%s]}\n",
			i, i, strings.Repeat(prev+",", 8)+prev)
	}
	bomb := b.String()
	done := make(chan error, 1)
	go func() {
		_, err := Parse(RepoFile, []byte(bomb))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("alias bomb accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alias bomb hung the parser")
	}
}

// The AWS extra_config and gpu fields all land on the runner.
func TestParseExtraConfig(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: gpu
    cloud: aws
    gpu: nvidia-tesla-t4
    instance_type: g4dn.xlarge
    machine_image: ami-00ac0c28c01352e53
    region: eu-west-1
    labels: [airship-gpu]
    extra_config:
      cache: true
      provision_via: pull_from_api
      run_as: interactive
      runner_user: runner
      runner_path: "C:\\r"
      SubnetId: subnet-0123456789abcdef0
      SecurityGroupIds: [sg-0123456789abcdef0]
      IamInstanceProfile: ci-runner
      BlockDeviceMappings:
        - DeviceName: /dev/sda1
          VolumeSize: 100
          VolumeType: gp3
          Iops: 6000
          Throughput: 1000
          VolumeInitializationRate: 300
      env_vars:
        NODE_ENV: production
`)
	r := f.Runners[0]
	if !r.GPU || !r.Cache || r.ProvisionVia != ProvisionPullFromAPI || !r.Windows.Interactive || r.Windows.User != "runner" || r.Windows.Path != `C:\r` {
		t.Fatalf("runner = %+v", r)
	}
	if r.SubnetID != "subnet-0123456789abcdef0" || r.SecurityGroupIDs[0] != "sg-0123456789abcdef0" || r.InstanceProfile != "ci-runner" || r.Env["NODE_ENV"] != "production" {
		t.Fatalf("runner = %+v", r)
	}
	d := r.Variants[0].Disk
	if d.SizeGB != 100 || d.Type != "gp3" || d.IOPS != 6000 || d.Throughput != 1000 || d.InitRate != 300 {
		t.Fatalf("disk = %+v", d)
	}
	plan := Plan([]string{"airship-gpu"}, []Candidate{{Source: SourceRepository, Runner: r}})
	if len(plan) != 1 || strings.Join(plan[0].Labels, ",") != "self-hosted,x64,aws,gpu,airship,airship-gpu" {
		t.Fatalf("labels = %v", plan)
	}
}

// gpu is a flag whose value is a GPU name; explicit false and null turn
// it off, as a Python truthiness check would.
func TestGPUFlag(t *testing.T) {
	for value, want := range map[string]bool{"nvidia-tesla-t4": true, "true": true, "false": false, "no": false, "null": false} {
		f := mustParse(t, `
runners:
  - name: a
    cloud: aws
    instance_type: g4dn.xlarge
    labels: [airship-a]
    gpu: `+value+`
`)
		if f.Runners[0].GPU != want {
			t.Errorf("gpu: %s = %v, want %v", value, f.Runners[0].GPU, want)
		}
	}
}

// Volume settings EC2 would refuse are config errors, not failed launches.
func TestDiskPerformanceRules(t *testing.T) {
	cases := map[string]string{
		"VolumeType: gp2\n          Iops: 3000":                             "Iops only applies to gp3",
		"VolumeType: io1":                                                   "io1 volumes need Iops",
		"VolumeType: gp3\n          Iops: 100":                              "between 3000 and 16000",
		"VolumeType: gp3\n          Throughput: 100":                        "between 125 and 1000",
		"VolumeType: gp3\n          Throughput: 1000":                       "needs Iops of at least 4000",
		"VolumeType: gp3\n          Iops: 4000\n          Throughput: 1000": "",
		"VolumeType: io2\n          Iops: 20000":                            "",
	}
	for disk, want := range cases {
		_, err := Parse(RepoFile, []byte(`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    extra_config:
      BlockDeviceMappings:
        - `+disk+`
`))
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: unexpected error %v", disk, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: error %v, want it to mention %q", disk, err, want)
		}
	}
}

// Schedule windows are read strictly: a trailing character in the time
// is an error, and replicas cannot be left out.
func TestIdleScheduleStrict(t *testing.T) {
	for window, want := range map[string]string{
		"{day: mon-fri, time: 9-17abc, replicas: 1}": "time must be a range",
		"{day: mon-fri, time: 9-17}":                 "replicas is required",
		"{day: mon-fri, time: 17-9, replicas: 1}":    "time must be a range",
		"{day: mon-fri, time: 9-17, replicas: 1}":    "",
	} {
		_, err := Parse(RepoFile, []byte(`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
    idle:
      schedule:
        - `+window+`
`))
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", window, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: error %v, want it to mention %q", window, err, want)
		}
	}
}

// A file full of problems gets a bounded error, with no Go type names.
func TestParseErrorIsBoundedAndPlain(t *testing.T) {
	var src strings.Builder
	src.WriteString("runners:\n")
	for i := range 40 {
		fmt.Fprintf(&src, "  - name: r%d\n    cloud: aws\n    instance_type: c7i.large\n    labels: [airship-a]\n    extra_config:\n      nope%d: 1\n", i, i)
	}
	_, err := Parse(RepoFile, []byte(src.String()))
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 2000 || !strings.Contains(err.Error(), "more") {
		t.Fatalf("error is %d bytes: %s", len(err.Error()), err)
	}
	_, err = Parse(RepoFile, []byte("runners:\n  - name: a\n    cloud: aws\n    instance_type: c7i.large\n    labels: [airship-a]\n    extra_config:\n      BlockDeviceMappings: {VolumeSize: 1}\n"))
	if err == nil || strings.Contains(err.Error(), "runnerconfig.") || !strings.Contains(err.Error(), "expected a list, not a mapping") {
		t.Fatalf("error = %v", err)
	}
}

func TestOneLine(t *testing.T) {
	cases := map[string]Variant{
		"airship-aws-t2-large-ubuntu2204--123":  {InstanceType: "t2.large", Image: "ubuntu2204", Arch: "x64"},
		"airship-aws-c7g-xlarge-spot":           {InstanceType: "c7g.xlarge", Arch: "arm64", Spot: true},
		"airship-aws-m7i-metal-24xl":            {InstanceType: "m7i.metal-24xl", Arch: "x64"},
		"airship-aws-g5-xlarge-ubuntu2404-spot": {InstanceType: "g5.xlarge", Image: "ubuntu2404", Arch: "x64", Spot: true},
	}
	for label, want := range cases {
		r, ok := OneLine(label)
		if !ok {
			t.Fatalf("%s: not a one-line label", label)
		}
		got := r.Variants[0]
		got.Disk = Disk{}
		if got != want {
			t.Errorf("%s = %+v, want %+v", label, got, want)
		}
	}
	for _, label := range []string{"airship-aws", "airship-aws-large", "airship-aws-t2-huge", "airship-onprem-4vcpu-8gb", "airship-gpu", "airship-aws-t2-large-windows"} {
		if _, ok := OneLine(label); ok {
			t.Errorf("%s parsed as a one-line label", label)
		}
	}
}

func TestArchForInstanceType(t *testing.T) {
	cases := map[string]string{
		"c7g.large": "arm64", "m6gd.xlarge": "arm64", "t4g.small": "arm64", "c7gn.2xlarge": "arm64",
		"x2gd.medium": "arm64", "is4gen.large": "arm64", "a1.medium": "arm64", "g5g.xlarge": "arm64",
		"r8g.large": "arm64", "hpc7g.4xlarge": "arm64",
		"c7i.large": "x64", "g5.xlarge": "x64", "g4dn.xlarge": "x64", "m5dn.large": "x64",
		"p5en.48xlarge": "x64", "gr6.4xlarge": "x64", "c7i-flex.large": "x64", "inf2.xlarge": "x64",
	}
	for instance, want := range cases {
		if got := ArchForInstanceType(CloudAWS, instance); got != want {
			t.Errorf("%s: %s, want %s", instance, got, want)
		}
	}
	gcp := map[string]string{"t2a-standard-4": "arm64", "c4a-standard-8": "arm64", "n2-standard-4": "x64", "a2-highgpu-1g": "x64", "e2-medium": "x64"}
	for instance, want := range gcp {
		if got := ArchForInstanceType(CloudGCP, instance); got != want {
			t.Errorf("gcp %s: %s, want %s", instance, got, want)
		}
	}
	for instance, want := range map[string]bool{"a2-highgpu-1g": true, "g2-standard-4": true, "a3-highgpu-8g": true, "n1-standard-4": false, "g5.xlarge": false} {
		if got := HasGPU(CloudGCP, instance); got != want {
			t.Errorf("HasGPU(gcp, %s) = %v", instance, got)
		}
	}
}

// A GCP definition: region is a zone, gpu names the
// accelerator, and extra_config carries Compute Engine settings.
func TestGCPRunner(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: gpu
    cloud: gcp
    instance_type: [n1-standard-4, t2a-standard-4]
    machine_image: ["my-project:ci-image", ubuntu2204]
    region: [us-central1-b, us-central1]
    preemptible: [true, false]
    gpu: nvidia-tesla-t4
    labels: [airship-gpu]
    extra_config:
      cache: true
      baseDisk: {diskSizeGb: 101, diskType: pd-ssd}
      additionalDisk: {diskSizeGb: 42, diskType: "projects/my-project/zones/us-central1-b/diskTypes/pd-balanced"}
      project_id: my-project
      enable_public_ip: false
      networkInterfaces:
        - nicType: GVNIC
          network: ci
          subnetwork: "projects/my-project/regions/us-central1/subnetworks/ci"
          stackType: IPV4_ONLY
      serviceAccounts:
        - email: runner@my-project.iam.gserviceaccount.com
          scopes: ["https://www.googleapis.com/auth/cloud-platform"]
      scheduling: {onHostMaintenance: TERMINATE}
      existingDisk:
        deviceName: ccache
        diskSizeGb: 122
        source: "projects/my-project/zones/us-central1-b/disks/ccache"
        type: PERSISTENT
        mode: READ_WRITE
        interface: SCSI
`)
	r := f.Runners[0]
	if r.Cloud != CloudGCP || !r.GPU || r.GPUType != "nvidia-tesla-t4" || !r.Cache {
		t.Fatalf("runner = %+v", r)
	}
	if len(r.Variants) != 2 {
		t.Fatalf("variants = %+v", r.Variants)
	}
	first, second := r.Variants[0], r.Variants[1]
	if first.Region != "us-central1" || first.Zone != "us-central1-b" || first.InstanceType != "n1-standard-4" || first.Image != "my-project:ci-image" || !first.Spot || first.Arch != "x64" {
		t.Errorf("first variant = %+v", first)
	}
	if second.Region != "us-central1" || second.Zone != "" || second.Image != "ubuntu2204" || second.Spot || second.Arch != "arm64" {
		t.Errorf("second variant = %+v", second)
	}
	if first.Disk.SizeGB != 101 || first.Disk.Type != "pd-ssd" {
		t.Errorf("disk = %+v", first.Disk)
	}
	g := r.GCP
	if g == nil || g.Project != "my-project" || !g.NoPublicIP || g.OnHostMaintenance != "TERMINATE" {
		t.Fatalf("gcp = %+v", g)
	}
	if g.AdditionalDisk == nil || g.AdditionalDisk.SizeGB != 42 || !strings.HasSuffix(g.AdditionalDisk.Type, "/diskTypes/pd-balanced") {
		t.Errorf("additional disk = %+v", g.AdditionalDisk)
	}
	if len(g.NetworkInterfaces) != 1 || g.NetworkInterfaces[0].Network != "global/networks/ci" || g.NetworkInterfaces[0].NicType != "GVNIC" || g.NetworkInterfaces[0].StackType != "IPV4_ONLY" {
		t.Errorf("network interfaces = %+v", g.NetworkInterfaces)
	}
	if len(g.ServiceAccounts) != 1 || g.ServiceAccounts[0].Email != "runner@my-project.iam.gserviceaccount.com" || len(g.ServiceAccounts[0].Scopes) != 1 {
		t.Errorf("service accounts = %+v", g.ServiceAccounts)
	}
	if d := g.ExistingDisk; d == nil || d.Zone != "us-central1-b" || d.Region != "us-central1" || d.DeviceName != "ccache" || d.Mode != "READ_WRITE" || d.Interface != "SCSI" || !strings.HasSuffix(d.Source, "/disks/ccache") {
		t.Errorf("existing disk = %+v", g.ExistingDisk)
	}

	plain := mustParse(t, `
runners:
  - {name: a, cloud: gcp, instance_type: e2-medium, labels: [airship-a]}
`).Runners[0]
	v := plain.Variants[0]
	if v.Region != "" || v.Zone != "" || v.Disk.SizeGB != DefaultDiskGB || v.Disk.Type != "" || plain.GCP != nil || plain.GPUType != "" {
		t.Errorf("plain gcp runner = %+v, variant %+v", plain, v)
	}
	for _, image := range []string{"ubuntu-2404-lts-amd64", "projects/my-project/global/machineImages/golden", "https://www.googleapis.com/compute/beta/projects/my-project/global/machineImages/golden", "debian-12"} {
		mustParse(t, "runners:\n  - {name: a, cloud: gcp, instance_type: e2-medium, machine_image: "+image+", labels: [airship-a]}\n")
	}
}

// A GCP runner matches jobs the same way, with gcp as its cloud label and
// the OS taken from the image name when it is a public family.
func TestGCPLabels(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: linux, cloud: gcp, instance_type: n2-standard-4, machine_image: ubuntu-2404-lts-amd64, labels: [airship-linux]}
  - {name: win, cloud: gcp, instance_type: n2-standard-4, machine_image: windows-2022, labels: [airship-win]}
  - {name: gpu, cloud: gcp, instance_type: g2-standard-4, labels: [airship-gpu]}
`)
	var candidates []Candidate
	for _, r := range f.Runners {
		candidates = append(candidates, Candidate{Source: SourceRepository, Runner: r})
	}
	match := func(labels ...string) string {
		attempts := Plan(labels, candidates)
		if len(attempts) == 0 {
			return ""
		}

		return attempts[0].Runner.Name
	}
	if got := match("self-hosted", "linux", "airship-linux"); got != "linux" {
		t.Errorf("linux match = %q", got)
	}
	if got := match("windows", "airship-linux"); got != "" {
		t.Errorf("an ubuntu family matched a windows job: %q", got)
	}
	if got := match("windows", "airship-win"); got != "win" {
		t.Errorf("windows match = %q", got)
	}
	if got := match("linux", "airship-win"); got != "" {
		t.Errorf("a windows image matched a linux job: %q", got)
	}
	if got := match("gpu", "gcp", "x64", "airship-gpu"); got != "gpu" {
		t.Errorf("gpu match = %q", got)
	}
}

func TestIdleSchedule(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: aws-cpu-runner
    cloud: aws
    instance_type: t3.medium
    labels: [airship-runner]
    idle:
      schedule:
        - {day: mon-fri, time: 9-17, timezone: US/Eastern, replicas: 4}
        - {day: sat-sun, time: 9-17, timezone: UTC, replicas: 1}
        - {day: fri-mon, time: 0-24, timezone: UTC, replicas: 2}
`)
	idle := f.Runners[0].Idle
	at := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}

		return ts
	}
	cases := map[string]int{
		"2026-10-28T13:00:00Z": 4, // Wednesday 9:00 Eastern
		"2026-10-28T12:59:00Z": 0, // Wednesday 8:59 Eastern
		"2026-10-28T21:00:00Z": 0, // Wednesday 17:00 Eastern: end is exclusive
		"2026-10-31T10:00:00Z": 2, // Saturday: 1 and 2 overlap, the larger wins
		"2026-10-30T13:00:00Z": 4, // Friday 9:00 Eastern beats the wrapped fri-mon window
		"2026-10-29T03:00:00Z": 0, // Thursday night
	}
	for ts, want := range cases {
		if got := idle.Replicas(at(ts)); got != want {
			t.Errorf("%s: %d replicas, want %d", ts, got, want)
		}
	}
}

func TestIdleAlwaysAndUsage(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: always
    cloud: aws
    instance_type: t3.medium
    machine_image: ubuntu2404
    region: eu-west-1
    labels: [airship-always]
    idle: {replicas: 2}
  - name: dev
    cloud: aws
    instance_type: t3.medium
    labels: [airship-dev]
    idle:
      usage_based: {trigger: runner_allocated, replicas: 2, duration: 180}
`)
	if got := f.Runners[0].Idle.Replicas(time.Now()); got != 2 {
		t.Fatalf("always-on idle = %d", got)
	}
	usage := f.Runners[1].Idle.Usage
	if usage == nil || usage.Replicas != 2 || usage.Duration != 3*time.Hour {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestPlanSuffixAndFallbackOrder(t *testing.T) {
	repo := mustParse(t, `
runners:
  - name: local
    cloud: aws
    instance_type: [c7i.large, c7i.xlarge]
    labels: [airship-runner]
`)
	global := mustParse(t, `
runners:
  - name: shared-arm
    cloud: aws
    instance_type: c7g.large
    labels: [airship-runner, airship-arm]
  - name: other
    cloud: aws
    instance_type: c7i.large
    labels: [airship-other]
`)
	candidates := []Candidate{{Source: SourceRepository, Runner: repo.Runners[0]}}
	for _, r := range global.Runners {
		candidates = append(candidates, Candidate{Source: SourceOrganization, Runner: r})
	}

	plan := Plan([]string{"airship-runner--12345"}, candidates)
	if len(plan) != 3 {
		t.Fatalf("plan has %d attempts", len(plan))
	}
	order := []string{plan[0].Variant.InstanceType, plan[1].Variant.InstanceType, plan[2].Variant.InstanceType}
	if strings.Join(order, ",") != "c7i.large,c7i.xlarge,c7g.large" {
		t.Fatalf("order %v", order)
	}
	// The runner registers with the job's own suffixed label and not the
	// plain one, so only jobs from the same run are handed to it.
	labels := strings.Join(plan[0].Labels, ",")
	if labels != "self-hosted,x64,aws,cpu,airship,airship-runner--12345" {
		t.Fatalf("labels %s", labels)
	}
	if plain := Plan([]string{"airship-runner"}, candidates); strings.Join(plain[0].Labels, ",") != "self-hosted,x64,aws,cpu,airship,airship-runner" {
		t.Fatalf("plain labels %v", plain[0].Labels)
	}

	// An arm64 job label rules out the x64 variants.
	plan = Plan([]string{"self-hosted", "ARM64", "airship-runner"}, candidates)
	if len(plan) != 1 || plan[0].Runner.Name != "shared-arm" {
		t.Fatalf("arm plan %+v", plan)
	}
	// Labels from two definitions never combine into one machine.
	if plan := Plan([]string{"airship-runner", "airship-other"}, candidates); len(plan) != 0 {
		t.Fatalf("combined plan %+v", plan)
	}
	if plan := Plan([]string{"ubuntu-latest"}, candidates); plan != nil {
		t.Fatal("planned a non-Airship job")
	}
	if plan := Plan([]string{"airship-runner", "gpu"}, candidates); len(plan) != 0 {
		t.Fatal("planned a job asking for a label no runner has")
	}
}

func TestPlanMatrixLabelsStayDistinct(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: airship-aws, cloud: aws, instance_type: t3.2xlarge, labels: [airship-runner]}
`)
	c := []Candidate{{Source: SourceRepository, Runner: f.Runners[0]}}
	a := Plan([]string{"airship-runner--10-999"}, c)
	b := Plan([]string{"airship-runner--12-999"}, c)
	if len(a) != 1 || len(b) != 1 || strings.Join(a[0].Labels, ",") == strings.Join(b[0].Labels, ",") {
		t.Fatalf("matrix jobs share labels: %v %v", a, b)
	}
}

func TestWantsConfig(t *testing.T) {
	if WantsConfig([]string{"ubuntu-latest", "self-hosted"}) {
		t.Fatal("ordinary job wants config")
	}
	if !WantsConfig([]string{"Airship-GPU--1"}) {
		t.Fatal("airship job does not want config")
	}
}

func TestAccess(t *testing.T) {
	a, err := ParseAccess([]byte(`
version: 1
policies:
  - id: sandbox
    repo: airship-sandbox
    teams: [core, alpha]
  - id: demo
    repo: airship-demo
    teams: [core, demo]
    pull_request: true
access_control:
  - resource: gpu-runner
    policies: [demo]
  - resource: cpu-runner
    policies: [sandbox, demo]
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, restricted := a.Restricted("aws-runner", "airship-demo"); restricted {
		t.Fatal("runner without a rule is restricted")
	}
	policies, restricted := a.Restricted("GPU-Runner", "airship-demo")
	if !restricted || len(policies) != 1 || policies[0].ID != "demo" || !policies[0].PullRequest {
		t.Fatalf("gpu on demo: %+v %v", policies, restricted)
	}
	if policies, restricted := a.Restricted("gpu-runner", "airship-sandbox"); !restricted || len(policies) != 0 {
		t.Fatalf("gpu on sandbox should be closed: %+v", policies)
	}

	for src, want := range map[string]string{
		"version: 2": "version must be 1",
		"version: 1\naccess_control: [{resource: gpu, policies: [nope]}]": `no policy has id "nope"`,
		"version: 1\npolicies: [{id: x, repo: acme/api}]":                 "repo must be a repository name",
	} {
		if _, err := ParseAccess([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", src, err, want)
		}
	}
}

func TestAccessDocumentApply(t *testing.T) {
	var doc AccessDocument
	yes := true
	if err := doc.Apply(AccessChange{Repository: "acme/api", Resources: []string{"gpu", "cpu"}, Teams: []string{"ml"}, PullRequest: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Apply(AccessChange{Repository: "api", Resources: []string{"gpu"}, Users: []string{"alice"}, Teams: []string{"ML"}}); err != nil {
		t.Fatal(err)
	}
	if len(doc.Policies) != 1 || doc.Policies[0].ID != "api-policy" || len(doc.Policies[0].Teams) != 1 || doc.Policies[0].Users[0] != "alice" || !doc.Policies[0].PullRequest {
		t.Fatalf("policies = %+v", doc.Policies)
	}
	if len(doc.AccessControl) != 2 || doc.AccessControl[0].Resource != "gpu" || len(doc.AccessControl[0].Policies) != 1 {
		t.Fatalf("rules = %+v", doc.AccessControl)
	}
	if _, err := doc.Compile(); err != nil {
		t.Fatal(err)
	}
	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseAccess(encoded)
	if err != nil {
		t.Fatalf("%v:\n%s", err, encoded)
	}
	if policies, restricted := again.Restricted("gpu", "api"); !restricted || len(policies) != 1 {
		t.Fatalf("round trip lost the rule: %s", encoded)
	}

	if err := doc.Apply(AccessChange{Repository: "api", Resources: []string{"gpu"}, Remove: true}); err != nil {
		t.Fatal(err)
	}
	if len(doc.AccessControl) != 1 || doc.AccessControl[0].Resource != "cpu" {
		t.Fatalf("after revoke: %+v", doc.AccessControl)
	}
	if err := doc.Apply(AccessChange{Repository: "bad/name/here", Resources: []string{"gpu"}}); err == nil {
		t.Fatal("bad repository accepted")
	}
	if err := doc.Apply(AccessChange{Repository: "api"}); err == nil {
		t.Fatal("empty resources accepted")
	}
}

func TestGlobalFileAllowsIdle(t *testing.T) {
	f, err := Parse(GlobalFile, []byte("runners:\n  - {name: shared, cloud: aws, instance_type: c7i.large, labels: [airship-shared], idle: {replicas: 2}}\n"))
	if err != nil || f.Runners[0].Idle == nil || f.Runners[0].Idle.Always != 2 {
		t.Fatalf("organization idle runners rejected: %v", err)
	}
}

// Every runner registers the product label,
// so a job may ask for any Airship runner next to its config label.
func TestPlanProductLabel(t *testing.T) {
	r := Runner{Name: "build", Cloud: CloudAWS, Labels: []string{"airship-build"}, Variants: []Variant{{InstanceType: "c7i.large", Arch: "x64"}}}
	candidates := []Candidate{{Source: SourceRepository, Runner: r}}
	if plan := Plan([]string{"self-hosted", ProductLabel, "airship-build"}, candidates); len(plan) != 1 {
		t.Fatalf("plan = %v", plan)
	}
	if plan := Plan([]string{"self-hosted", ProductLabel}, candidates); plan != nil {
		t.Fatalf("the product label alone reads no config: %v", plan)
	}
}
