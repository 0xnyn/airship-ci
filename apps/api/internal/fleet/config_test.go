package fleet_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

func TestConfigFallbackAndSuffix(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	suffixed := cpuLabel + "--1234"
	job := h.queue(suffixed)
	launched := h.launch()
	if len(launched) != 1 {
		t.Fatalf("launches = %d, want 1", len(launched))
	}
	first := launched[0].Launch
	if first.Machine.InstanceType != "c7i.large" || first.Boot.Env["GREETING"] != "hello" {
		t.Fatalf("first launch = %+v", first)
	}
	registered, _ := h.gh.RunnerByJIT(first.Boot.JITConfig)
	if !slices.Contains(registered.Labels, suffixed) || !slices.Contains(registered.Labels, cpuLabel) {
		t.Fatalf("registered labels = %v, want the suffixed and plain label", registered.Labels)
	}
	if row := h.job(job.ID); row.Plan == nil {
		t.Fatal("job plan was not stored")
	}
}

func TestConfigNotActivated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.deactivate()

	job := h.queue()
	wantSkip(t, h, job, "repository_inactive", "not activated")
	if calls := h.gh.Calls("GET contents"); calls != 0 {
		t.Fatalf("read config %d times for an inactive repository", calls)
	}
}

func TestConfigIgnoresUnprefixedLabels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	job := h.queue("ubuntu-latest")
	if row := h.job(job.ID); row.SkipReason != nil || row.Plan != nil {
		t.Fatalf("job for a GitHub-hosted label was planned or skipped: %+v", row)
	}
	if calls := h.gh.Calls("GET contents") + h.gh.Calls("GET run attempt"); calls != 0 {
		t.Fatalf("made %d GitHub calls for a job that is not ours", calls)
	}
}

func TestConfigInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig(headSHA, "runners:\n  - name: cpu\n    cloud: gcp\n")
	job := h.queue()
	wantSkip(t, h, job, "config_invalid", ".cirun.yml")
}

// A pull request from a fork must not choose the machines its code runs
// on, or pass itself secrets in env_vars: it gets the default branch's
// config, never its own.
func TestConfigForkUsesDefaultBranch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setRun("pull_request", 555, "mallory")
	h.setConfig(headSHA, strings.ReplaceAll(baseConfig, "[c7i.large, m7i.large]", "p5.48xlarge"))
	h.setConfig("main", baseConfig)

	h.queue()
	launched := h.launch()
	if len(launched) != 1 || launched[0].Launch.Machine.InstanceType != "c7i.large" {
		t.Fatalf("fork launch = %+v, want the default branch's c7i.large", launched)
	}
}

// The region comes from the file, or the connected cloud's default.
func TestConfigRegion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig(headSHA, strings.ReplaceAll(baseConfig, "labels:", "region: [eu-west-1, eu-west-1]\n    labels:"))
	h.queue()
	launched := h.launch()
	if len(launched) != 1 || launched[0].Launch.Region != "eu-west-1" {
		t.Fatalf("launch region = %+v, want eu-west-1", launched)
	}
}

const globalConfig = `
runners:
  - name: gpu
    cloud: aws
    gpu: nvidia-tesla-t4
    instance_type: g5.xlarge
    machine_image: ami-0123456789abcdef0
    labels: [cirun-gpu]
`

const accessConfig = `
version: 1
policies:
  - id: widgets-ml
    repo: widgets
    users: [alice]
    teams: [ml]
access_control:
  - resource: gpu
    policies: [widgets-ml]
`

func TestOrganizationRunnerAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.gh.SetFile("acme/.cirun", "", runnerconfig.GlobalFile, []byte(globalConfig))
	h.gh.SetFile("acme/.cirun", "", runnerconfig.AccessFile, []byte(accessConfig))

	cases := []struct {
		name, event, actor string
		team               bool
		served             bool
	}{
		{"listed user", "push", "alice", false, true},
		{"team member", "push", "bob", true, true},
		{"anyone else", "push", "carol", false, false},
		// The policy does not set pull_request: true.
		{"listed user on a pull request", "pull_request", "alice", false, false},
	}
	for i, tc := range cases {
		run := int64(10 + i)
		h.gh.SetTeamMember("acme", "ml", tc.actor, tc.team)
		h.gh.SetRunAttempt(repoName, github.RunAttempt{
			ID: run, RunAttempt: 1, Event: tc.event, Repository: github.Repository{ID: repoID},
			HeadRepository: &github.Repository{ID: repoID}, Actor: &github.Actor{Login: tc.actor},
		})
		job := h.newJob("cirun-gpu")
		job.RunID = run
		h.event("queued", job, true)
		row := h.job(job.ID)
		if served := row.Plan != nil; served != tc.served {
			t.Fatalf("%s: served = %v, want %v (skip %v: %v)", tc.name, served, tc.served, row.SkipReason, row.SkipDetail)
		}
		if !tc.served && (row.SkipReason == nil || *row.SkipReason != "access_denied") {
			t.Fatalf("%s: skip reason = %v, want access_denied", tc.name, row.SkipReason)
		}
	}
	launched := h.launch()
	if len(launched) != 2 || !launched[0].Launch.Boot.GPU {
		t.Fatalf("gpu launches = %+v", launched)
	}
	registered, _ := h.gh.RunnerByJIT(launched[0].Launch.Boot.JITConfig)
	if !slices.Contains(registered.Labels, "gpu") {
		t.Fatalf("gpu runner labels = %v", registered.Labels)
	}
}

// A broken .access.yml closes organization runners rather than opening
// them to every repository.
func TestOrganizationAccessFailsClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.gh.SetFile("acme/.cirun", "", runnerconfig.GlobalFile, []byte(globalConfig))
	h.gh.SetFile("acme/.cirun", "", runnerconfig.AccessFile, []byte("version: 2\n"))

	job := h.queue("cirun-gpu")
	wantSkip(t, h, job, "unsatisfiable_labels", "closed")
}

const poolConfig = `
runners:
  - name: cpu
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [cirun-cpu]
    idle:
      replicas: 2
`

func (h *harness) refreshPools() {
	h.t.Helper()
	repo, err := h.q.GetRepository(h.ctx, db.GetRepositoryParams{InstallationID: installationID, ID: repoID})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.svc.RefreshRepositoryConfig(h.ctx, repo); err != nil {
		h.t.Fatal(err)
	}
	h.reconcile()
}

func (h *harness) poolRunners() (idle, reserved int) {
	h.t.Helper()
	err := h.pool.QueryRow(h.ctx, `select
		count(*) filter (where trigger_job_id is null),
		count(*) filter (where trigger_job_id is not null)
		from runners where pool_id is not null and state in ('pending', 'launching', 'running')`).Scan(&idle, &reserved)
	if err != nil {
		h.t.Fatal(err)
	}

	return idle, reserved
}

func TestWarmPool(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig("main", poolConfig)
	h.setConfig(headSHA, poolConfig)

	h.refreshPools()
	if idle, _ := h.poolRunners(); idle != 2 {
		t.Fatalf("idle pool runners = %d, want 2", idle)
	}
	h.refreshPools()
	if idle, _ := h.poolRunners(); idle != 2 {
		t.Fatalf("idle pool runners after a second pass = %d, want still 2", idle)
	}
	if launched := h.launch(); len(launched) != 2 {
		t.Fatalf("pool launched %d machines, want 2", len(launched))
	}

	// A job with the plain label takes a pool runner instead of a new one,
	// and the pool replaces it straight away.
	h.queue()
	if idle, reserved := h.poolRunners(); idle != 2 || reserved != 1 {
		t.Fatalf("after a job: idle = %d, reserved = %d; want 2 and 1", idle, reserved)
	}
	if n := len(h.runners()); n != 3 {
		t.Fatalf("runners = %d, want 3 (no on-demand runner)", n)
	}

	// A suffixed job cannot use the pool: its runner must carry the suffix.
	h.queue(cpuLabel + "--77")
	if n := len(h.runners()); n != 4 {
		t.Fatalf("runners = %d, want an on-demand runner for the suffixed job", n)
	}

	// Scaling the pool to zero drops the idle runners: the one that never
	// launched outright, the launched one by terminating it.
	h.setConfig("main", strings.ReplaceAll(poolConfig, "replicas: 2", "replicas: 0"))
	h.refreshPools()
	if idle, reserved := h.poolRunners(); idle != 0 || reserved != 1 {
		t.Fatalf("after scaling to zero: idle = %d, reserved = %d; want 0 and 1", idle, reserved)
	}
}

// A pool runner reserved for one job that GitHub hands another goes back
// to the pool rather than idling until it is reaped.
func TestWarmPoolRunnerSwap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig("main", poolConfig)
	h.setConfig(headSHA, poolConfig)
	h.refreshPools()

	job := h.queue()
	launched := h.launch()
	if len(launched) != 3 {
		t.Fatalf("launches = %d, want 3", len(launched))
	}
	// Pick a runner that was not reserved for the job.
	var reserved string
	if err := h.pool.QueryRow(h.ctx, "select name from runners where trigger_job_id = $1", job.ID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	var other string
	for _, instance := range launched {
		if instance.Launch.RunnerName != reserved {
			other = instance.Launch.RunnerName
		}
	}
	h.start(job, other)
	if idle, reserved := h.poolRunners(); idle != 2 || reserved != 0 {
		t.Fatalf("after the swap: idle = %d, reserved = %d; want 2 and 0", idle, reserved)
	}
}

// Pools wait for a cloud and fill as soon as one is connected.
func TestWarmPoolWaitsForCloud(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.disconnect()
	h.setConfig("main", poolConfig)
	h.refreshPools()
	if idle, _ := h.poolRunners(); idle != 0 {
		t.Fatalf("pool runners without a cloud = %d", idle)
	}
	h.connect()
	if idle, _ := h.poolRunners(); idle != 2 {
		t.Fatalf("pool runners after connecting = %d, want 2", idle)
	}
}

func TestPlanIsStoredOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	job := h.queue()
	before := h.job(job.ID).Plan

	// Redelivery after the config changed keeps the original plan.
	h.setConfig(headSHA, strings.ReplaceAll(baseConfig, "c7i.large", "c7i.4xlarge"))
	h.event("queued", job, true)
	if after := h.job(job.ID).Plan; string(after) != string(before) || strings.Contains(string(after), "4xlarge") {
		t.Fatalf("plan changed on redelivery: %s", after)
	}
}

// A job skipped because its repository was not activated is served when a
// later delivery finds it activated, and loses the old reason.
func TestSkippedJobIsPlannedLater(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.deactivate()
	job := h.queue()
	wantSkip(t, h, job, "repository_inactive", "")

	h.activate(false)
	h.event("queued", job, true)
	row := h.job(job.ID)
	if row.Plan == nil || row.SkipReason != nil || row.SkipDetail != nil {
		t.Fatalf("after activation: plan %v, skip %v (%v)", row.Plan != nil, row.SkipReason, row.SkipDetail)
	}
	if n := len(h.runners()); n != 1 {
		t.Fatalf("runners = %d, want 1", n)
	}
}

// Concurrent deliveries of one queued job take one pool runner between
// them, never two, and never fail on the one-live-runner-per-job index.
func TestConcurrentDeliveriesReserveOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig("main", poolConfig)
	h.setConfig(headSHA, poolConfig)
	h.refreshPools()

	job := h.newJob()
	const deliveries = 8
	errs := make(chan error, deliveries)
	for range deliveries {
		go func() {
			errs <- h.svc.HandleWorkflowJob(h.ctx, github.WorkflowJobEvent{
				Action: "queued", WorkflowJob: job,
				Repository:   github.Repository{ID: repoID, FullName: repoName, Private: true},
				Installation: &github.InstallationRef{ID: installationID},
			})
		}()
	}
	for range deliveries {
		if err := <-errs; err != nil {
			t.Fatalf("delivery failed: %v", err)
		}
	}
	var live int
	if err := h.pool.QueryRow(h.ctx, "select count(*) from runners where trigger_job_id = $1 and state <> 'finished'", job.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live runners for the job = %d, want 1", live)
	}
}

func TestOneLineRunner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig(headSHA, "") // no config file anywhere
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, nil)

	label := "cirun-aws-m7g-xlarge-ubuntu2404-spot--42"
	h.queue(label)
	launched := h.launch()
	if len(launched) != 1 {
		t.Fatalf("launches = %d, want 1", len(launched))
	}
	m := launched[0].Launch.Machine
	if m.InstanceType != "m7g.xlarge" || !m.Spot || m.Arch != "arm64" || m.Image != "ubuntu2404" || launched[0].Launch.Region != region {
		t.Fatalf("machine = %+v", m)
	}
	registered, _ := h.gh.RunnerByJIT(launched[0].Launch.Boot.JITConfig)
	if !slices.Contains(registered.Labels, label) {
		t.Fatalf("registered labels = %v, want %s", registered.Labels, label)
	}
}

func TestOneLineRunnerRefusedForForks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setRun("pull_request", 555, "mallory")

	job := h.queue("cirun-aws-p5-48xlarge")
	wantSkip(t, h, job, "unsatisfiable_labels", "forks cannot use one-line labels")
}

// Every extra_config knob reaches the cloud launch.
func TestConfigReachesTheLaunch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig(headSHA, `
runners:
  - name: win
    cloud: aws
    instance_type: t3.large
    machine_image: ami-0f9c44e98edf38a2b
    labels: [cirun-win]
    extra_config:
      provision_via: pull_from_api
      run_as: interactive
      runner_path: "C:\\r"
      SubnetId: subnet-0123456789abcdef0
      IamInstanceProfile: ci
      BlockDeviceMappings:
        - {DeviceName: /dev/sda1, VolumeSize: 120, VolumeType: gp3, Throughput: 500}
`)
	h.queue("cirun-win")
	launched := h.launch()
	if len(launched) != 1 {
		t.Fatalf("launches = %d", len(launched))
	}
	l := launched[0].Launch
	want := cloud.Machine{InstanceType: "t3.large", Image: "ami-0f9c44e98edf38a2b", Arch: "x64", Disk: cloud.Disk{SizeGB: 120, Type: "gp3", Throughput: 500}, SubnetID: "subnet-0123456789abcdef0", InstanceProfile: "ci"}
	if l.Machine.InstanceType != want.InstanceType || l.Machine.Disk != want.Disk || l.Machine.SubnetID != want.SubnetID || l.Machine.InstanceProfile != want.InstanceProfile {
		t.Fatalf("machine = %+v", l.Machine)
	}
	if !l.Boot.PullFromAPI || !l.Boot.Windows.Interactive || l.Boot.Windows.Path != `C:\r` {
		t.Fatalf("boot = %+v", l.Boot)
	}
}
