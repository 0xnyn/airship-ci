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
      - cirun-small
`)
	r := f.Runners[0]
	if r.Name != "small-runner" || r.Cloud != CloudAWS || len(r.Variants) != 1 {
		t.Fatalf("parsed %+v", r)
	}
	v := r.Variants[0]
	if v.InstanceType != "c5.xlarge" || v.Image != "ami-0c55b159cbfafe1f0" || v.Region != "us-east-1" || v.Spot || v.Arch != "x64" || v.Disk.SizeGB != DefaultDiskGB {
		t.Fatalf("variant %+v", v)
	}
}

// Cirun's fallback example #3: lists are zipped, scalars repeat.
func TestFallbacksZip(t *testing.T) {
	f := mustParse(t, `
runners:
- name: "aws-multiple-runner"
  cloud: "aws"
  instance_type: ["t3.small", "t3.medium", "c7g.large"]
  machine_image: "ami-0123456789abcdef0"
  region: "eu-west-1"
  preemptible: [true, true, false]
  labels: ["cirun-multiple-runner-options"]
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
    labels: [cirun-a]
    instance_tpye: typo
`, "unknown field instance_tpye"},
		"unsupported cloud": {`
runners:
  - {name: a, cloud: gcp, instance_type: n1-standard-1, labels: [cirun-a]}
`, `cloud "gcp" is not supported`},
		"label prefix": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [airship-a]}
`, `must start with "cirun-"`},
		"suffix separator in label": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [cirun-a--b]}
`, `may not contain "--"`},
		"uneven fallbacks": {`
runners:
  - name: a
    cloud: aws
    instance_type: [c7i.large, c7i.xlarge]
    region: [us-east-1, us-east-2, eu-west-1]
    labels: [cirun-a]
`, "fallback lists must be the same length"},
		"bad ami": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, machine_image: ubuntu, labels: [cirun-a]}
`, "must be an AMI ID"},
		"duplicate name": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, labels: [cirun-a]}
  - {name: A, cloud: aws, instance_type: c7i.large, labels: [cirun-b]}
`, "defined twice"},
		"reserved env": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config:
      env_vars: {GITHUB_TOKEN: x}
`, "reserved for the runner"},
		"env newline": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config:
      env_vars: {FOO: "a\nb"}
`, "line breaks"},
		"gcp extra config": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config: {baseDisk: {diskSizeGb: 10}}
`, "unknown field baseDisk"},
		"bad provision_via": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config: {provision_via: push}
`, "provision_via must be pull_from_api"},
		"bad runner path": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config: {runner_path: "C:\\r\"; calc"}
`, "runner_path must be a Windows path"},
		"bad region": {`
runners:
  - {name: a, cloud: aws, instance_type: c7i.large, region: RegionOne, labels: [cirun-a]}
`, "not an AWS region"},
		"bad init rate": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    extra_config:
      BlockDeviceMappings: [{DeviceName: /dev/sda1, VolumeSize: 100, VolumeInitializationRate: 50}]
`, "VolumeInitializationRate must be between 100 and 300"},
		"two idle forms": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    idle:
      replicas: 1
      usage_based: {trigger: runner_allocated, replicas: 1, duration: 10}
`, "exactly one of"},
		"bad timezone": {`
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-a]
    idle:
      schedule: [{day: mon-fri, time: 9-17, timezone: Mars/Olympus, replicas: 2}]
`, "not a time zone"},
		"on_prem": {`
runners:
  - {name: a, cloud: on_prem, instance_type: 4vcpu-8gb, labels: [cirun-a]}
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
	b.WriteString("runners:\n  - {name: r0, cloud: aws, instance_type: c7i.large, labels: [cirun-a], gpu: &a0 [x,x,x,x,x,x,x,x,x]}\n")
	for i := 1; i <= 9; i++ {
		prev := fmt.Sprintf("*a%d", i-1)
		fmt.Fprintf(&b, "  - {name: r%d, cloud: aws, instance_type: c7i.large, labels: [cirun-a], gpu: &a%d [%s]}\n",
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

// Cirun's AWS extra_config and gpu fields all land on the runner.
func TestParseExtraConfig(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: gpu
    cloud: aws
    gpu: nvidia-tesla-t4
    instance_type: g4dn.xlarge
    machine_image: ami-00ac0c28c01352e53
    region: eu-west-1
    labels: [cirun-gpu]
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
	plan := Plan([]string{"cirun-gpu"}, []Candidate{{Source: SourceRepository, Runner: r}})
	if len(plan) != 1 || strings.Join(plan[0].Labels, ",") != "self-hosted,x64,aws,gpu,cirun-gpu" {
		t.Fatalf("labels = %v", plan)
	}
}

func TestOneLine(t *testing.T) {
	cases := map[string]Variant{
		"cirun-aws-t2-large-ubuntu2204--123":  {InstanceType: "t2.large", Image: "ubuntu2204", Arch: "x64"},
		"cirun-aws-c7g-xlarge-spot":           {InstanceType: "c7g.xlarge", Arch: "arm64", Spot: true},
		"cirun-aws-m7i-metal-24xl":            {InstanceType: "m7i.metal-24xl", Arch: "x64"},
		"cirun-aws-g5-xlarge-ubuntu2404-spot": {InstanceType: "g5.xlarge", Image: "ubuntu2404", Arch: "x64", Spot: true},
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
	for _, label := range []string{"cirun-aws", "cirun-aws-large", "cirun-aws-t2-huge", "cirun-onprem-4vcpu-8gb", "cirun-gpu", "cirun-aws-t2-large-windows"} {
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
		if got := ArchForInstanceType(instance); got != want {
			t.Errorf("%s: %s, want %s", instance, got, want)
		}
	}
}

func TestIdleSchedule(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: aws-cpu-runner
    cloud: aws
    instance_type: t3.medium
    labels: [cirun-runner]
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
    labels: [cirun-always]
    idle: {replicas: 2}
  - name: dev
    cloud: aws
    instance_type: t3.medium
    labels: [cirun-dev]
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
    labels: [cirun-runner]
`)
	global := mustParse(t, `
runners:
  - name: shared-arm
    cloud: aws
    instance_type: c7g.large
    labels: [cirun-runner, cirun-arm]
  - name: other
    cloud: aws
    instance_type: c7i.large
    labels: [cirun-other]
`)
	candidates := []Candidate{{Source: SourceRepository, Runner: repo.Runners[0]}}
	for _, r := range global.Runners {
		candidates = append(candidates, Candidate{Source: SourceOrganization, Runner: r})
	}

	plan := Plan([]string{"cirun-runner--12345"}, candidates)
	if len(plan) != 3 {
		t.Fatalf("plan has %d attempts", len(plan))
	}
	order := []string{plan[0].Variant.InstanceType, plan[1].Variant.InstanceType, plan[2].Variant.InstanceType}
	if strings.Join(order, ",") != "c7i.large,c7i.xlarge,c7g.large" {
		t.Fatalf("order %v", order)
	}
	// The runner registers with the job's own suffixed label, so only jobs
	// from the same run are handed to it.
	labels := strings.Join(plan[0].Labels, ",")
	if labels != "self-hosted,x64,aws,cpu,cirun-runner,cirun-runner--12345" {
		t.Fatalf("labels %s", labels)
	}

	// An arm64 job label rules out the x64 variants.
	plan = Plan([]string{"self-hosted", "ARM64", "cirun-runner"}, candidates)
	if len(plan) != 1 || plan[0].Runner.Name != "shared-arm" {
		t.Fatalf("arm plan %+v", plan)
	}
	// Labels from two definitions never combine into one machine.
	if plan := Plan([]string{"cirun-runner", "cirun-other"}, candidates); len(plan) != 0 {
		t.Fatalf("combined plan %+v", plan)
	}
	if plan := Plan([]string{"ubuntu-latest"}, candidates); plan != nil {
		t.Fatal("planned a non-Airship job")
	}
	if plan := Plan([]string{"cirun-runner", "gpu"}, candidates); len(plan) != 0 {
		t.Fatal("planned a job asking for a label no runner has")
	}
}

func TestPlanMatrixLabelsStayDistinct(t *testing.T) {
	f := mustParse(t, `
runners:
  - {name: cirun-aws, cloud: aws, instance_type: t3.2xlarge, labels: [cirun-runner]}
`)
	c := []Candidate{{Source: SourceRepository, Runner: f.Runners[0]}}
	a := Plan([]string{"cirun-runner--10-999"}, c)
	b := Plan([]string{"cirun-runner--12-999"}, c)
	if len(a) != 1 || len(b) != 1 || strings.Join(a[0].Labels, ",") == strings.Join(b[0].Labels, ",") {
		t.Fatalf("matrix jobs share labels: %v %v", a, b)
	}
}

func TestWantsConfig(t *testing.T) {
	if WantsConfig([]string{"ubuntu-latest", "self-hosted"}) {
		t.Fatal("ordinary job wants config")
	}
	if !WantsConfig([]string{"Cirun-GPU--1"}) {
		t.Fatal("airship job does not want config")
	}
}

func TestAccess(t *testing.T) {
	a, err := ParseAccess([]byte(`
version: 1
policies:
  - id: sandbox
    repo: cirun-sandbox
    teams: [core, alpha]
  - id: demo
    repo: cirun-demo
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
	if _, restricted := a.Restricted("aws-runner", "cirun-demo"); restricted {
		t.Fatal("runner without a rule is restricted")
	}
	policies, restricted := a.Restricted("GPU-Runner", "cirun-demo")
	if !restricted || len(policies) != 1 || policies[0].ID != "demo" || !policies[0].PullRequest {
		t.Fatalf("gpu on demo: %+v %v", policies, restricted)
	}
	if policies, restricted := a.Restricted("gpu-runner", "cirun-sandbox"); !restricted || len(policies) != 0 {
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
	f, err := Parse(GlobalFile, []byte("runners:\n  - {name: shared, cloud: aws, instance_type: c7i.large, labels: [cirun-shared], idle: {replicas: 2}}\n"))
	if err != nil || f.Runners[0].Idle == nil || f.Runners[0].Idle.Always != 2 {
		t.Fatalf("organization idle runners rejected: %v", err)
	}
}
