package fleet_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/fake"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
	"github.com/wizenheimer/airship/apps/api/internal/secrets"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/pgtest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	installationID = int64(42)
	repoID         = int64(9)
	repoName       = "acme/widgets"
	headSHA        = "head0000"
	cpuLabel       = "cirun-cpu"
	region         = "us-east-1"
)

// baseConfig is the repository's .cirun.yml in most tests: one runner with
// two instance types to fall back through.
const baseConfig = `
runners:
  - name: cpu
    cloud: aws
    instance_type: [c7i.large, m7i.large]
    machine_image: ami-0123456789abcdef0
    labels: [cirun-cpu]
    extra_config:
      env_vars:
        GREETING: hello
`

type harness struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	q       *db.Queries
	gh      *fakegithub.Server
	svc     *fleet.Service
	aws     *fake.Provider
	nextJob int64
	lastJob github.WorkflowJob
}

// newHarness builds an installation with AWS connected and a repository
// activated with baseConfig at headSHA, so a job on cpuLabel can be served.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.New(t)
	q := db.New(pool)
	gh := fakegithub.New(t)

	inst := github.Installation{ID: installationID, Account: github.Account{ID: 7, Login: "acme", Type: "Organization"}}
	gh.AddInstallation(inst)
	gh.AddRepository(installationID, github.Repository{ID: repoID, FullName: repoName, Private: true, DefaultBranch: "main"})

	key, _ := secrets.ParseKey(secrets.NewKey())
	keyring, _ := secrets.New(key)
	aws := fake.New(cloud.AWS)
	cfg := fleet.DefaultConfig()
	cfg.PublicURL = "https://api.test"
	svc := fleet.New(pool, fleet.AppGitHub{App: gh.App(t)}, cloud.Registry{cloud.AWS: aws}, keyring, cfg, slog.New(slog.DiscardHandler))
	if _, err := svc.SaveInstallation(ctx, inst); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ctx: ctx, pool: pool, q: q, gh: gh, svc: svc, aws: aws, nextJob: 100}
	h.connect()
	h.activate(false)
	h.setRun("push", repoID, "alice")
	h.setConfig(headSHA, baseConfig)

	return h
}

func (h *harness) connect() {
	h.t.Helper()
	secret, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIATEST", SecretAccessKey: "secret"})
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.AWS, secret, region, nil); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) disconnect() {
	h.t.Helper()
	if err := h.svc.DisconnectCloud(h.ctx, installationID, cloud.AWS); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) activate(allowPublic bool) {
	h.t.Helper()
	if _, err := h.q.ActivateRepository(h.ctx, db.ActivateRepositoryParams{
		ID: repoID, InstallationID: installationID, FullName: repoName, Private: true, DefaultBranch: "main", AllowPublic: allowPublic,
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) deactivate() {
	h.t.Helper()
	if _, err := h.q.DeactivateRepository(h.ctx, db.DeactivateRepositoryParams{InstallationID: installationID, ID: repoID}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) setConfig(ref, content string) {
	h.gh.SetFile(repoName, ref, runnerconfig.RepoFile, []byte(content))
}

func (h *harness) setRun(event string, headRepoID int64, actor string) {
	head := &github.Repository{ID: headRepoID}
	h.gh.SetRunAttempt(repoName, github.RunAttempt{
		ID: 1, RunAttempt: 1, Event: event, HeadSHA: headSHA,
		Repository: github.Repository{ID: repoID}, HeadRepository: head, Actor: &github.Actor{Login: actor},
	})
}

// event sends a workflow_job webhook for a job.
func (h *harness) event(action string, job github.WorkflowJob, private bool) {
	h.t.Helper()
	err := h.svc.HandleWorkflowJob(h.ctx, github.WorkflowJobEvent{
		Action:       action,
		WorkflowJob:  job,
		Repository:   github.Repository{ID: repoID, FullName: repoName, Private: private},
		Installation: &github.InstallationRef{ID: installationID},
	})
	if err != nil {
		h.t.Fatalf("%s: %v", action, err)
	}
}

func (h *harness) newJob(labels ...string) github.WorkflowJob {
	h.nextJob++
	if len(labels) == 0 {
		labels = []string{cpuLabel}
	}

	return github.WorkflowJob{ID: h.nextJob, RunID: 1, RunAttempt: 1, Name: "build", Labels: labels, Status: "queued", HeadSHA: headSHA, CreatedAt: time.Now()}
}

func (h *harness) queue(labels ...string) github.WorkflowJob {
	h.t.Helper()
	job := h.newJob(labels...)
	h.event("queued", job, true)
	h.lastJob = job

	return job
}

func (h *harness) start(job github.WorkflowJob, runnerName string) {
	h.t.Helper()
	now := time.Now()
	job.Status, job.StartedAt, job.RunnerName = "in_progress", &now, &runnerName
	h.event("in_progress", job, true)
}

func (h *harness) complete(job github.WorkflowJob, runnerName *string, conclusion string) {
	h.t.Helper()
	now := time.Now()
	job.Status, job.CompletedAt, job.RunnerName, job.Conclusion = "completed", &now, runnerName, &conclusion
	h.event("completed", job, true)
}

// launch runs one launcher pass and returns the machines it started, in
// launch order.
func (h *harness) launch() []fake.Instance {
	h.t.Helper()
	before := h.aws.Instances()
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		h.t.Fatalf("launch: %v", err)
	}
	var out []fake.Instance
	for _, instance := range h.aws.Instances() {
		if !slices.ContainsFunc(before, func(b fake.Instance) bool { return b.ID == instance.ID }) {
			out = append(out, instance)
		}
	}

	return out
}

func (h *harness) reconcile() {
	h.t.Helper()
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) runners() []db.Runner {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, "select id, name, state, coalesce(finish_reason, ''), trigger_job_id, coalesce(instance_id, ''), coalesce(error, '') from runners order by created_at")
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []db.Runner
	for rows.Next() {
		var r db.Runner
		var reason, instance, errText string
		if err := rows.Scan(&r.ID, &r.Name, &r.State, &reason, &r.TriggerJobID, &instance, &errText); err != nil {
			h.t.Fatal(err)
		}
		if reason != "" {
			r.FinishReason = &reason
		}
		if instance != "" {
			r.InstanceID = &instance
		}
		if errText != "" {
			r.Error = &errText
		}
		out = append(out, r)
	}

	return out
}

func (h *harness) job(id int64) db.Job {
	h.t.Helper()
	row, err := h.q.GetJob(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}

	return row
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func states(runners []db.Runner) []string {
	out := make([]string, 0, len(runners))
	for _, r := range runners {
		s := r.State
		if r.FinishReason != nil {
			s += ":" + *r.FinishReason
		}
		out = append(out, s)
	}

	return out
}

func wantStates(t *testing.T, h *harness, want ...string) {
	t.Helper()
	got := states(h.runners())
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("runner states = %v, want %v", got, want)
	}
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

func TestHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	job := h.queue()
	h.queue() // a second, independent job
	h.event("queued", job, true)
	wantStates(t, h, "pending", "pending") // duplicate delivery adds nothing

	launched := h.launch()
	if len(launched) != 2 {
		t.Fatalf("launched = %d, want 2", len(launched))
	}
	wantStates(t, h, "running", "running")
	// Launches run in parallel, so pick the machine of the first runner row.
	firstRunner := h.runners()[0]
	i := slices.IndexFunc(launched, func(l fake.Instance) bool { return l.Launch.RunnerID == firstRunner.ID.String() })
	first := launched[i].Launch
	registered, ok := h.gh.RunnerByJIT(first.Boot.JITConfig)
	if !ok || registered.Repo != repoName || registered.Name != first.RunnerName {
		t.Fatalf("jit config does not match a registration: %+v", registered)
	}
	if !slices.Contains(registered.Labels, cpuLabel) || !slices.Contains(registered.Labels, "self-hosted") {
		t.Fatalf("registered labels = %v", registered.Labels)
	}
	if first.Machine.InstanceType != "c7i.large" || first.Region != region || first.InstallationID != installationID ||
		first.Boot.MaxLifetimeSeconds <= 0 || first.Boot.RunnerVersion == "" || first.Boot.Env["GREETING"] != "hello" {
		t.Fatalf("launch incomplete: %+v", first)
	}
	for _, runner := range h.runners() {
		i := slices.IndexFunc(launched, func(l fake.Instance) bool { return l.Launch.RunnerID == runner.ID.String() })
		if i < 0 || runner.InstanceID == nil || *runner.InstanceID != launched[i].ID {
			t.Fatalf("instance id not recorded: %+v", runner)
		}
	}

	h.start(job, first.RunnerName)
	wantStates(t, h, "busy", "running")
	stored := h.job(job.ID)
	if stored.RunnerID == nil || stored.Status != "in_progress" {
		t.Fatalf("job not linked to runner: %+v", stored)
	}

	h.complete(job, &first.RunnerName, "success")
	wantStates(t, h, "finished:completed", "running")
	// The machine is gone without waiting for a reconcile pass.
	if left := h.aws.Instances(); len(left) != 1 || left[0].ID == launched[i].ID {
		t.Fatalf("finished runner's instance not terminated: %+v", left)
	}
	h.reconcile()
	wantStates(t, h, "finished:completed", "running")
}

func TestOutOfOrderEventsNeverCreateRunners(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	job := h.newJob()
	h.start(job, "someone-elses-runner")
	h.event("queued", job, true) // late
	wantStates(t, h)

	if stored := h.job(job.ID); stored.Status != "in_progress" {
		t.Fatalf("status moved backwards: %+v", stored)
	}
}

func TestCancelledBeforeLaunch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	job := h.queue()
	h.complete(job, nil, "cancelled")
	wantStates(t, h, "finished:cancelled")
	if launched := h.launch(); len(launched) != 0 {
		t.Fatalf("launched a runner for a cancelled job")
	}
	if h.gh.Calls("POST generate-jitconfig") != 0 {
		t.Fatal("registered a runner for a cancelled job")
	}
}

func TestStolenRunnerIsReplacedImmediately(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	a := h.queue()
	b := h.queue()
	h.exec("update installations set max_runners = 1")
	launched := h.launch() // created for job a
	if len(launched) != 1 {
		t.Fatalf("launched = %d, want 1 (max_runners)", len(launched))
	}
	// GitHub gives a's runner to job b instead. b's own runner is now spare
	// and goes to a, rather than a waiting for the reconciler.
	h.start(b, launched[0].Launch.RunnerName)
	runners := h.runners()
	if runners[0].State != "busy" || runners[1].State != "pending" || runners[1].TriggerJobID == nil || *runners[1].TriggerJobID != a.ID {
		t.Fatalf("spare runner not handed to the robbed job: %v trigger=%v", states(runners), runners[1].TriggerJobID)
	}
	h.exec("update installations set max_runners = 10")
	second := h.launch()
	h.start(a, second[0].Launch.RunnerName)
	wantStates(t, h, "busy", "busy")
}

func TestCrossedRunnersBothStayServed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	a := h.queue()
	b := h.queue()
	launched := h.launch()
	ra, rb := launched[0].Launch, launched[1].Launch

	// Each job lands on the other's runner, with the events racing.
	var wg sync.WaitGroup
	wg.Go(func() { h.start(b, ra.RunnerName) })
	wg.Go(func() { h.start(a, rb.RunnerName) })
	wg.Wait()
	wantStates(t, h, "busy", "busy")
}

func TestMaxRunnersIsEnforcedUnderConcurrentLaunchers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.exec("update installations set max_runners = 3")
	for range 40 {
		h.queue()
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		wg.Go(func() {
			<-start
			if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if n := h.aws.Launches(); n != 3 {
		t.Fatalf("launched %d runners across concurrent launchers, want exactly max_runners=3", n)
	}
}

func TestSkippedJobs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	public := h.newJob()
	h.event("queued", public, false)
	unsatisfiable := h.queue(cpuLabel, "gpu")
	other := h.queue("ubuntu-latest") // not ours at all
	wantStates(t, h)

	for id, want := range map[int64]string{public.ID: fleet.SkipPublicRepository, unsatisfiable.ID: fleet.SkipUnsatisfiableLabels} {
		job := h.job(id)
		if job.SkipReason == nil || *job.SkipReason != want {
			t.Fatalf("job %d skip reason = %v, want %s", id, job.SkipReason, want)
		}
	}
	if row := h.job(other.ID); row.SkipReason != nil || row.Plan != nil {
		t.Fatalf("job for a GitHub-hosted label was planned or skipped: %+v", row)
	}
	if calls := h.gh.Calls("GET contents"); calls == 0 {
		t.Fatal("config was never read")
	}

	h.activate(true)
	h.event("queued", public, false)
	if n := len(h.runners()); n != 1 {
		t.Fatalf("runners = %d after allowing public access, want 1", n)
	}
}

func TestNoCloudConnected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.disconnect()
	job := h.queue()
	wantSkip(t, h, job, fleet.SkipNoCloud, "connect aws")
	cancelled := h.queue()
	wantSkip(t, h, cancelled, fleet.SkipNoCloud, "")

	// Connecting the cloud plans the jobs still queued on GitHub, and
	// leaves alone the one GitHub has since cancelled.
	h.gh.SetJob(job)
	done := cancelled
	done.Status, done.Conclusion = "completed", ptr("cancelled")
	h.gh.SetJob(done)
	h.connect()
	if row := h.job(job.ID); row.Plan == nil || row.SkipReason != nil {
		t.Fatalf("job not planned after connecting: %+v", row)
	}
	if row := h.job(cancelled.ID); row.Status != "completed" || row.Plan != nil {
		t.Fatalf("cancelled job after connecting: status %s, plan %v", row.Status, row.Plan != nil)
	}
	wantStates(t, h, "pending")
}

func ptr[T any](v T) *T { return &v }

// Credentials for another account cannot replace the ones live runners
// were launched with, since they could not terminate those machines; a
// rotated key in the same account can.
func TestConnectAnotherAccountRefusedWhileRunnersLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.launch()
	secret, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIAROTATED", SecretAccessKey: "secret2"})
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.AWS, secret, region, nil); err != nil {
		t.Fatalf("rotating the key: %v", err)
	}
	h.aws.AccountID = "999999999999"
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.AWS, secret, region, nil); !errors.Is(err, fleet.ErrCloudInUse) {
		t.Fatalf("connecting another account with a live runner = %v, want ErrCloudInUse", err)
	}
	h.complete(h.lastJob, &h.runners()[0].Name, "success")
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.AWS, secret, region, nil); err != nil {
		t.Fatalf("connecting another account with no live runner: %v", err)
	}
}

// Repositories removed from the app's access are deactivated, so their
// pools stop trying to launch runners nobody can register.
func TestRemovedRepositoriesAreDeactivated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	err := h.svc.HandleInstallationRepositories(h.ctx, github.InstallationRepositoriesEvent{
		Action: "removed", Installation: github.InstallationRef{ID: installationID},
		RepositoriesRemoved: []github.Repository{{ID: repoID, FullName: repoName}, {ID: 404, FullName: "acme/never-activated"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.q.GetRepository(h.ctx, db.GetRepositoryParams{InstallationID: installationID, ID: repoID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("repository still activated after removal: %v", err)
	}
	job := h.queue()
	wantSkip(t, h, job, fleet.SkipRepositoryInactive, "")
}

func TestDisconnectRefusedWhileRunnersLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.launch()
	if err := h.svc.DisconnectCloud(h.ctx, installationID, cloud.AWS); !errors.Is(err, fleet.ErrCloudInUse) {
		t.Fatalf("disconnect with a live runner = %v", err)
	}
}

func TestJITFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.queue()
	h.gh.FailNext("POST generate-jitconfig", http.StatusInternalServerError)
	if launched := h.launch(); len(launched) != 0 {
		t.Fatal("launched despite a jit failure")
	}
	wantStates(t, h, "pending") // transient: back in line, after a short backoff
	if launched := h.launch(); len(launched) != 0 {
		t.Fatal("retried without backing off")
	}
	h.exec("update runners set not_before = now() - interval '1 second'")
	if launched := h.launch(); len(launched) != 1 {
		t.Fatal("transient failure was not retried")
	}

	h.queue()
	h.gh.FailNext("POST generate-jitconfig", http.StatusNotFound)
	h.launch()
	// A permanent refusal fails the attempt; the plan's next instance type
	// is tried.
	wantStates(t, h, "running", "finished:launch_failed", "pending")
}

func TestJITConflictRecoversFromALostResponse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.gh.FailNext("POST generate-jitconfig", http.StatusConflict)
	if launched := h.launch(); len(launched) != 1 {
		t.Fatal("409 from an earlier registration was not recovered")
	}
}

// Capacity errors retry the same shape; the plan's fallback order and
// the attempt cap bound the total.
func TestLaunchFailuresFallBackAndStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.aws.FailLaunch = func(cloud.Launch) error {
		return &cloud.LaunchError{Err: errors.New("InsufficientInstanceCapacity"), Retryable: true}
	}
	job := h.queue()
	for attempt := 1; attempt <= 4; attempt++ {
		n, err := h.svc.LaunchOnce(h.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if attempt > 3 && n != 0 {
			t.Fatalf("attempt %d launched; max attempts is 3", attempt)
		}
	}
	wantStates(t, h, "finished:launch_failed", "finished:launch_failed", "finished:launch_failed")
	var types []string
	for _, r := range h.runners() {
		var spec struct {
			InstanceType string `json:"instanceType"`
		}
		h.pool.QueryRow(h.ctx, "select spec from runners where id = $1", r.ID).Scan(&spec) //nolint:errcheck // test
		types = append(types, spec.InstanceType)
	}
	if strings.Join(types, ",") != "c7i.large,m7i.large,c7i.large" {
		t.Fatalf("fallback order = %v", types)
	}
	if row := h.job(job.ID); row.Plan == nil {
		t.Fatal("job plan was not stored")
	}

	// A failure that would recur stops once every shape was tried.
	h.aws.FailLaunch = func(cloud.Launch) error { return &cloud.LaunchError{Err: errors.New("InvalidAMIID.NotFound")} }
	h.queue()
	h.launch()
	h.launch()
	if n, _ := h.svc.LaunchOnce(h.ctx); n != 0 {
		t.Fatal("kept retrying a permanent failure")
	}
	if got := states(h.runners()); len(got) != 5 {
		t.Fatalf("runners = %v, want two more failed attempts", got)
	}
	if h.gh.Calls("POST generate-jitconfig") != 5 {
		t.Fatalf("registrations = %d, want one per attempt", h.gh.Calls("POST generate-jitconfig"))
	}
}

func TestLeaseExpiryRetriesAndALateListingRevives(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	// The launcher died between claiming and recording the instance.
	h.aws.FailLaunch = func(cloud.Launch) error { return errors.New("process crashed") }
	h.exec("update runners set state = 'launching', launching_at = now(), lease_expires_at = now() - interval '1 second'")
	h.aws.FailLaunch = nil
	h.reconcile()
	wantStates(t, h, "finished:lease_expired", "pending")

	// The machine was created after all: the cloud lists it.
	runners := h.runners()
	if _, err := h.aws.Launch(h.ctx, nil, cloud.Launch{RunnerID: runners[0].ID.String(), RunnerName: runners[0].Name, InstallationID: installationID, Region: region}); err != nil {
		t.Fatal(err)
	}
	h.exec("update runners set github_runner_id = 1 where id = $1", runners[0].ID)
	h.reconcile()
	runners = h.runners()
	if runners[0].State != "running" || runners[0].TriggerJobID != nil {
		t.Fatalf("late listing did not revive the runner: %v", states(runners))
	}
	if runners[1].State != "finished" {
		t.Fatalf("retry runner for the same job not cancelled: %v", states(runners))
	}
}

func TestVanishedInstances(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	job := h.queue()
	launched := h.launch()
	h.aws.Exit(launched[0].ID)

	// Within the grace period, a missing instance is not yet gone.
	h.reconcile()
	wantStates(t, h, "running")

	h.exec("update runners set running_at = now() - interval '1 hour'")
	h.reconcile()
	wantStates(t, h, "finished:lost", "pending") // and the job gets another runner

	second := h.launch()
	h.start(job, second[0].Launch.RunnerName)
	h.aws.Exit(second[0].ID)
	h.exec("update runners set running_at = now() - interval '1 hour' where state = 'busy'")
	h.reconcile() // busy runner's instance vanished: wait for GitHub
	wantStates(t, h, "finished:lost", "busy")
	h.exec("update runners set instance_gone_at = now() - interval '1 hour' where state = 'busy'")
	h.reconcile()
	wantStates(t, h, "finished:lost", "finished:lost")
}

func TestIdleRunnersAreReaped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.queue()
	launched := h.launch()

	// The second runner was picked up per GitHub, but its webhook was lost.
	busy, _ := h.gh.RunnerByJIT(launched[1].Launch.Boot.JITConfig)
	h.gh.SetRunnerBusy(busy.ID, true)
	h.exec("update runners set running_at = now() - interval '1 hour'")
	h.reconcile()
	for _, runner := range h.runners() {
		want := "finished"
		if runner.ID.String() == launched[1].Launch.RunnerID {
			want = "busy"
		}
		if runner.State != want {
			t.Fatalf("runner %s state = %s, want %s", runner.ID, runner.State, want)
		}
	}
	if len(h.gh.Runners()) != 1 {
		t.Fatalf("idle runner not deleted from GitHub: %+v", h.gh.Runners())
	}
	if left := h.aws.Instances(); len(left) != 1 || left[0].ID != launched[1].ID {
		t.Fatalf("idle runner's instance not terminated: %+v", left)
	}
}

func TestStaleQueuedJobIsRefreshed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	job := h.queue()

	// GitHub cancelled the job but the webhook never arrived.
	cancelled := job
	cancelled.Status = "completed"
	conclusion := "cancelled"
	cancelled.Conclusion = &conclusion
	h.gh.SetJob(cancelled)
	h.exec("update jobs set updated_at = now() - interval '1 hour'")
	h.reconcile()
	wantStates(t, h, "finished:cancelled")
}

func TestUninstallTearsEverythingDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.queue()
	h.exec("update installations set max_runners = 1")
	h.launch()

	if err := h.svc.HandleInstallation(h.ctx, github.InstallationEvent{Action: "deleted", Installation: github.Installation{ID: installationID}}); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:deleted", "finished:deleted")
	h.reconcile()
	if left := h.aws.Instances(); len(left) != 0 {
		t.Fatalf("instances after uninstall: %+v", left)
	}
	if n, _ := h.svc.LaunchOnce(h.ctx); n != 0 {
		t.Fatal("launched for an uninstalled installation")
	}
}

// Instances the cloud lists under our tag that belong to no runner are
// terminated: they cost money and nothing will ever clean them up.
func TestUnknownInstancesAreTerminated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.launch() // so the (credential, region) is scanned
	if _, err := h.aws.Launch(h.ctx, nil, cloud.Launch{RunnerID: "not-a-runner", InstallationID: installationID, Region: region}); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	if left := h.aws.Instances(); len(left) != 1 || left[0].Launch.RunnerID == "not-a-runner" {
		t.Fatalf("unknown instance survived: %+v", left)
	}
	wantStates(t, h, "running")
}

func TestLauncherWakesOnNewWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.svc.RunLauncher(ctx, time.Hour)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	h.queue()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.aws.Instances()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(h.aws.Instances()) != 1 {
		t.Fatal("launcher did not wake for new work")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("launcher took %v to notice new work", waited)
	}
	cancel()
	<-done
}

func TestEventsAreRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	h.launch()
	rows, err := h.q.ListEvents(h.ctx, db.ListEventsParams{InstallationID: installationID, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, row := range rows {
		kinds = append(kinds, row.Kind)
	}
	if !slices.Contains(kinds, "runner.launched") || !slices.Contains(kinds, "cloud.connected") {
		t.Fatalf("events = %v", kinds)
	}
}

// extra_config.cache: true gives the machine a token; its cache proxy
// trades it for credentials scoped to the repository's prefix, for as
// long as the runner lives.
func TestCacheGrant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setConfig(headSHA, strings.ReplaceAll(baseConfig, "extra_config:", "extra_config:\n      cache: true"))
	job := h.queue()
	launched := h.launch()
	if len(launched) != 1 || launched[0].Launch.Boot.Cache == nil {
		t.Fatalf("cache boot missing: %+v", launched)
	}
	boot := launched[0].Launch.Boot.Cache
	if boot.ControlPlaneURL != "https://api.test" || !strings.HasPrefix(boot.Token, fleet.CacheTokenPrefix) {
		t.Fatalf("cache boot = %+v", boot)
	}
	if buckets := h.aws.Buckets(); len(buckets) != 1 || !strings.HasPrefix(buckets[0], "airship-cache-us-east-1-") {
		t.Fatalf("buckets = %v", buckets)
	}

	grant, err := h.svc.CacheGrant(h.ctx, boot.Token)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Prefix != "" || grant.Region != region || grant.Bucket != h.aws.Buckets()[0] || grant.AccessKeyID == "" {
		t.Fatalf("grant = %+v", grant)
	}
	if _, err := h.svc.CacheGrant(h.ctx, "asr_forged"); !errors.Is(err, fleet.ErrNoCacheGrant) {
		t.Fatalf("forged token = %v", err)
	}

	// A second cache-enabled launch in the same region reuses the bucket.
	h.queue()
	h.launch()
	if buckets := h.aws.Buckets(); len(buckets) != 1 {
		t.Fatalf("bucket ensured again: %v", buckets)
	}

	// Once the runner is done its token is dead.
	h.start(job, launched[0].Launch.RunnerName)
	h.complete(job, &launched[0].Launch.RunnerName, "success")
	if _, err := h.svc.CacheGrant(h.ctx, boot.Token); !errors.Is(err, fleet.ErrNoCacheGrant) {
		t.Fatalf("finished runner's token still works: %v", err)
	}
}
