package fleet_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

const (
	repoID   = int64(9)
	headSHA  = "head0000"
	cpuLabel = "airship-cpu"
)

const fallbackConfig = `
runners:
  - name: cpu
    cloud: aws
    instance_type: [c7i.large, m7i.large]
    machine_image: ami-0123456789abcdef0
    labels: [airship-cpu]
    extra_config:
      env_vars:
        GREETING: hello
`

// configHarness is a harness whose repository serves jobs from config
// files: activated, connected, with a run attempt GitHub can describe.
func newConfigHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, 10)
	h.gh.AddRepository(installationID, github.Repository{ID: repoID, FullName: repoName, Private: true, DefaultBranch: "main"})
	h.activate(false)
	h.setRun("push", repoID, "alice")
	h.sync(nil, 0) // the agent reports its provider

	return h
}

func (h *harness) activate(allowPublic bool) {
	h.t.Helper()
	if _, err := h.q.ActivateRepository(h.ctx, db.ActivateRepositoryParams{
		ID: repoID, InstallationID: installationID, FullName: repoName, Private: true, DefaultBranch: "main", AllowPublic: allowPublic,
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) setRun(event string, headRepoID int64, actor string) {
	head := &github.Repository{ID: headRepoID}
	h.gh.SetRunAttempt(repoName, github.RunAttempt{
		ID: 1, RunAttempt: 1, Event: event, HeadSHA: headSHA,
		Repository: github.Repository{ID: repoID}, HeadRepository: head, Actor: &github.Actor{Login: actor},
	})
}

func (h *harness) queueAt(sha string, labels ...string) github.WorkflowJob {
	h.t.Helper()
	job := h.newJob(labels...)
	job.HeadSHA = sha
	h.event("queued", job, true)

	return job
}

func (h *harness) job(id int64) db.Job {
	h.t.Helper()
	row, err := h.q.GetJob(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}

	return row
}

func wantSkip(t *testing.T, h *harness, job github.WorkflowJob, reason, detail string) {
	t.Helper()
	row := h.job(job.ID)
	if row.SkipReason == nil || *row.SkipReason != reason {
		t.Fatalf("skip reason = %v, want %s (detail %v)", row.SkipReason, reason, row.SkipDetail)
	}
	if detail != "" && (row.SkipDetail == nil || !strings.Contains(*row.SkipDetail, detail)) {
		t.Fatalf("skip detail = %v, want it to mention %q", row.SkipDetail, detail)
	}
	if n := len(h.runners()); n != 0 {
		t.Fatalf("%d runners for a skipped job", n)
	}
}

func TestConfigFallbackAndSuffix(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))

	suffixed := cpuLabel + "--1234"
	job := h.queueAt(headSHA, suffixed)
	resp := h.sync(nil, 5)
	if len(resp.Launch) != 1 {
		t.Fatalf("launches = %d, want 1", len(resp.Launch))
	}
	first := resp.Launch[0]
	if first.Profile.InstanceType != "c7i.large" || first.Profile.Label != "cpu" || first.Profile.Env["GREETING"] != "hello" {
		t.Fatalf("first launch profile = %+v", first.Profile)
	}
	registered, _ := h.gh.RunnerByJIT(first.JITConfig)
	if !slices.Contains(registered.Labels, suffixed) || !slices.Contains(registered.Labels, cpuLabel) {
		t.Fatalf("registered labels = %v, want the suffixed and plain label", registered.Labels)
	}

	// Capacity for this instance type is gone: not worth retrying as is,
	// but the plan has another type to try.
	resp, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{
		AgentVersion: "test", Provider: "fake", Capacity: 5,
		Failures: []agentproto.LaunchFailure{{RunnerID: first.RunnerID, Error: "InsufficientInstanceCapacity", Retryable: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Launch) != 1 || resp.Launch[0].Profile.InstanceType != "m7i.large" {
		t.Fatalf("fallback launch = %+v, want m7i.large", resp.Launch)
	}

	// The second option fails the same way: the plan is used up.
	resp, err = h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{
		AgentVersion: "test", Provider: "fake", Capacity: 5,
		Failures: []agentproto.LaunchFailure{{RunnerID: resp.Launch[0].RunnerID, Error: "InsufficientInstanceCapacity"}},
	})
	if err != nil || len(resp.Launch) != 0 {
		t.Fatalf("launches after both options failed = %+v, %v", resp.Launch, err)
	}
	wantStates(t, h, "finished:launch_failed", "finished:launch_failed")
	if row := h.job(job.ID); row.Plan == nil {
		t.Fatal("job plan was not stored")
	}
}

func TestConfigNotActivated(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))
	if _, err := h.q.DeactivateRepository(h.ctx, db.DeactivateRepositoryParams{InstallationID: installationID, ID: repoID}); err != nil {
		t.Fatal(err)
	}

	job := h.queueAt(headSHA, cpuLabel)
	wantSkip(t, h, job, "repository_inactive", "not activated")
	if calls := h.gh.Calls("GET contents"); calls != 0 {
		t.Fatalf("read config %d times for an inactive repository", calls)
	}
}

func TestConfigIgnoresUnprefixedLabels(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	job := h.queueAt(headSHA, "ubuntu-latest")
	if row := h.job(job.ID); row.SkipReason != nil || row.Plan != nil {
		t.Fatalf("job for a GitHub-hosted label was planned or skipped: %+v", row)
	}
	if calls := h.gh.Calls("GET contents") + h.gh.Calls("GET run attempt"); calls != 0 {
		t.Fatalf("made %d GitHub calls for a job that is not Airship's", calls)
	}
}

func TestConfigInvalid(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte("runners:\n  - name: cpu\n    cloud: gcp\n"))
	job := h.queueAt(headSHA, cpuLabel)
	wantSkip(t, h, job, "config_invalid", ".airship.yml")
}

// A pull request from a fork must not choose the machines its code runs
// on, or pass itself secrets in env_vars: it gets the default branch's
// config, never its own.
func TestConfigForkUsesDefaultBranch(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.setRun("pull_request", 555, "mallory")
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(strings.ReplaceAll(fallbackConfig, "[c7i.large, m7i.large]", "p5.48xlarge")))
	h.gh.SetFile(repoName, "main", runnerconfig.RepoFile, []byte(fallbackConfig))

	h.queueAt(headSHA, cpuLabel)
	resp := h.sync(nil, 5)
	if len(resp.Launch) != 1 || resp.Launch[0].Profile.InstanceType != "c7i.large" {
		t.Fatalf("fork launch = %+v, want the default branch's c7i.large", resp.Launch)
	}
}

func TestConfigPublicRepository(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))
	job := h.newJob(cpuLabel)
	job.HeadSHA = headSHA
	h.event("queued", job, false)
	wantSkip(t, h, job, "public_repository", "")

	h.activate(true)
	job = h.newJob(cpuLabel)
	job.HeadSHA = headSHA
	h.event("queued", job, false)
	if n := len(h.runners()); n != 1 {
		t.Fatalf("runners = %d after allowing public access, want 1", n)
	}
}

func TestConfigNoMatchingDataPlane(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(strings.ReplaceAll(fallbackConfig, "labels:", "data_plane: staging\n    labels:")))
	job := h.queueAt(headSHA, cpuLabel)
	wantSkip(t, h, job, "no_data_plane", "data plane staging")
}

const globalConfig = `
runners:
  - name: gpu
    cloud: aws
    instance_type: g5.xlarge
    machine_image: ami-0123456789abcdef0
    labels: [airship-gpu]
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
	h := newConfigHarness(t)
	h.gh.SetFile("acme/.airship", "", runnerconfig.GlobalFile, []byte(globalConfig))
	h.gh.SetFile("acme/.airship", "", runnerconfig.AccessFile, []byte(accessConfig))

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
		job := h.newJob("airship-gpu")
		job.RunID, job.HeadSHA = run, headSHA
		h.event("queued", job, true)
		row := h.job(job.ID)
		if served := row.Plan != nil; served != tc.served {
			t.Fatalf("%s: served = %v, want %v (skip %v: %v)", tc.name, served, tc.served, row.SkipReason, row.SkipDetail)
		}
		if !tc.served && (row.SkipReason == nil || *row.SkipReason != "access_denied") {
			t.Fatalf("%s: skip reason = %v, want access_denied", tc.name, row.SkipReason)
		}
	}
}

// A broken .access.yml closes organization runners rather than opening
// them to every repository.
func TestOrganizationAccessFailsClosed(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile("acme/.airship", "", runnerconfig.GlobalFile, []byte(globalConfig))
	h.gh.SetFile("acme/.airship", "", runnerconfig.AccessFile, []byte("version: 2\n"))

	job := h.queueAt(headSHA, "airship-gpu")
	wantSkip(t, h, job, "unsatisfiable_labels", "closed")
}

const poolConfig = `
runners:
  - name: cpu
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [airship-cpu]
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
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) poolRunners() (idle, reserved int) {
	h.t.Helper()
	err := h.pool.QueryRow(h.ctx, `select
		count(*) filter (where trigger_job_id is null),
		count(*) filter (where trigger_job_id is not null)
		from runners where pool_id is not null and state in ('pending', 'assigned', 'running')`).Scan(&idle, &reserved)
	if err != nil {
		h.t.Fatal(err)
	}

	return idle, reserved
}

func TestWarmPool(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, "main", runnerconfig.RepoFile, []byte(poolConfig))
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(poolConfig))

	h.refreshPools()
	if idle, _ := h.poolRunners(); idle != 2 {
		t.Fatalf("idle pool runners = %d, want 2", idle)
	}
	h.refreshPools()
	if idle, _ := h.poolRunners(); idle != 2 {
		t.Fatalf("idle pool runners after a second pass = %d, want still 2", idle)
	}

	// A job with the plain label takes a pool runner instead of a new one,
	// and the pool replaces it straight away.
	h.queueAt(headSHA, cpuLabel)
	if idle, reserved := h.poolRunners(); idle != 2 || reserved != 1 {
		t.Fatalf("after a job: idle = %d, reserved = %d; want 2 and 1", idle, reserved)
	}
	if n := len(h.runners()); n != 3 {
		t.Fatalf("runners = %d, want 3 (no on-demand runner)", n)
	}

	// A suffixed job cannot use the pool: its runner must carry the suffix.
	h.queueAt(headSHA, cpuLabel+"--77")
	if n := len(h.runners()); n != 4 {
		t.Fatalf("runners = %d, want an on-demand runner for the suffixed job", n)
	}

	// Scaling the pool to zero drops the idle runners that never launched.
	h.gh.SetFile(repoName, "main", runnerconfig.RepoFile, []byte(strings.ReplaceAll(poolConfig, "replicas: 2", "replicas: 0")))
	h.refreshPools()
	if idle, reserved := h.poolRunners(); idle != 0 || reserved != 1 {
		t.Fatalf("after scaling to zero: idle = %d, reserved = %d; want 0 and 1", idle, reserved)
	}
}

// A pool runner reserved for one job that GitHub hands another goes back
// to the pool rather than idling until it is reaped.
func TestWarmPoolRunnerSwap(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, "main", runnerconfig.RepoFile, []byte(poolConfig))
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(poolConfig))
	h.refreshPools()

	job := h.queueAt(headSHA, cpuLabel)
	resp := h.sync(nil, 10)
	if len(resp.Launch) != 3 {
		t.Fatalf("launches = %d, want 3", len(resp.Launch))
	}
	var instances []agentproto.Instance
	var other string
	for _, launch := range resp.Launch {
		instances = append(instances, instanceFor(launch))
	}
	h.sync(instances, 0)
	// Pick a runner that was not reserved for the job.
	var reserved string
	if err := h.pool.QueryRow(h.ctx, "select name from runners where trigger_job_id = $1", job.ID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	for _, launch := range resp.Launch {
		if launch.RunnerName != reserved {
			other = launch.RunnerName
		}
	}
	h.start(job, other)
	if idle, reserved := h.poolRunners(); idle != 2 || reserved != 0 {
		t.Fatalf("after the swap: idle = %d, reserved = %d; want 2 and 0", idle, reserved)
	}
}

func TestDataPlaneMaxRunners(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))
	if _, err := h.q.SetDataPlaneMaxRunners(h.ctx, db.SetDataPlaneMaxRunnersParams{InstallationID: installationID, ID: h.dp.ID, MaxRunners: 2}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		h.queueAt(headSHA, cpuLabel)
	}
	h.queue(label) // a profile job shares the same cap
	if resp := h.sync(nil, 10); len(resp.Launch) != 2 {
		t.Fatalf("launches = %d, want the data plane's cap of 2", len(resp.Launch))
	}
}

func TestPlanIsStoredOnce(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))
	job := h.queueAt(headSHA, cpuLabel)
	before := h.job(job.ID).Plan

	// Redelivery after the config changed keeps the original plan.
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(strings.ReplaceAll(fallbackConfig, "c7i.large", "c7i.4xlarge")))
	h.event("queued", job, true)
	if after := h.job(job.ID).Plan; string(after) != string(before) || strings.Contains(string(after), "4xlarge") {
		t.Fatalf("plan changed on redelivery: %s", after)
	}
}

// A job skipped because its repository was not activated is served when a
// later delivery finds it activated, and loses the old reason.
func TestSkippedJobIsPlannedLater(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(fallbackConfig))
	if _, err := h.q.DeactivateRepository(h.ctx, db.DeactivateRepositoryParams{InstallationID: installationID, ID: repoID}); err != nil {
		t.Fatal(err)
	}
	job := h.queueAt(headSHA, cpuLabel)
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
	h := newConfigHarness(t)
	h.gh.SetFile(repoName, "main", runnerconfig.RepoFile, []byte(poolConfig))
	h.gh.SetFile(repoName, headSHA, runnerconfig.RepoFile, []byte(poolConfig))
	h.refreshPools()

	job := h.newJob(cpuLabel)
	job.HeadSHA = headSHA
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
	h := newConfigHarness(t) // no config file anywhere

	label := "airship-aws-m7g-xlarge-ubuntu2404-spot--42"
	h.queueAt(headSHA, label)
	resp := h.sync(nil, 5)
	if len(resp.Launch) != 1 {
		t.Fatalf("launches = %d, want 1", len(resp.Launch))
	}
	p := resp.Launch[0].Profile
	if p.InstanceType != "m7g.xlarge" || !p.Spot || p.Arch != "arm64" || !strings.Contains(p.Image, "24.04/stable/current/arm64") {
		t.Fatalf("profile = %+v", p)
	}
	registered, _ := h.gh.RunnerByJIT(resp.Launch[0].JITConfig)
	if !slices.Contains(registered.Labels, label) {
		t.Fatalf("registered labels = %v, want %s", registered.Labels, label)
	}
}

func TestOneLineRunnerRefusedForForks(t *testing.T) {
	t.Parallel()
	h := newConfigHarness(t)
	h.setRun("pull_request", 555, "mallory")

	job := h.queueAt(headSHA, "airship-aws-p5-48xlarge")
	wantSkip(t, h, job, "unsatisfiable_labels", "forks cannot use one-line labels")
}
