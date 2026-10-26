package fleet_test

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/pgtest"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	installationID = int64(42)
	repoName       = "acme/widgets"
	label          = "airship-4cpu"
)

type harness struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	q       *db.Queries
	gh      *fakegithub.Server
	svc     *fleet.Service
	dp      fleet.DataPlane
	profile db.Profile
	nextJob int64
}

func newHarness(t *testing.T, maxRunners int32) *harness {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.New(t)
	q := db.New(pool)
	gh := fakegithub.New(t)

	inst := github.Installation{ID: installationID, Account: github.Account{ID: 7, Login: "acme", Type: "Organization"}}
	gh.AddInstallation(inst)

	svc := fleet.New(pool, fleet.AppGitHub{App: gh.App(t)}, fleet.DefaultConfig(), slog.New(slog.DiscardHandler))
	if _, err := svc.SaveInstallation(ctx, inst); err != nil {
		t.Fatal(err)
	}
	plane, err := q.CreateDataPlane(ctx, db.CreateDataPlaneParams{InstallationID: installationID, Name: "prod", TokenHash: []byte("hash"), TokenHint: "asd_x"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := q.CreateProfile(ctx, db.CreateProfileParams{
		InstallationID: installationID, DataPlaneID: plane.ID, Label: label, Arch: "x64",
		InstanceType: "c7i.xlarge", DiskGb: 100, MaxRunners: maxRunners,
	})
	if err != nil {
		t.Fatal(err)
	}

	return &harness{t: t, ctx: ctx, pool: pool, q: q, gh: gh, svc: svc, dp: fleet.DataPlane{DataPlane: plane}, profile: profile, nextJob: 100}
}

// event sends a workflow_job webhook for a job.
func (h *harness) event(action string, job github.WorkflowJob, private bool) {
	h.t.Helper()
	err := h.svc.HandleWorkflowJob(h.ctx, github.WorkflowJobEvent{
		Action:       action,
		WorkflowJob:  job,
		Repository:   github.Repository{ID: 9, FullName: repoName, Private: private},
		Installation: &github.InstallationRef{ID: installationID},
	})
	if err != nil {
		h.t.Fatalf("%s: %v", action, err)
	}
}

func (h *harness) newJob(labels ...string) github.WorkflowJob {
	h.nextJob++
	if len(labels) == 0 {
		labels = []string{label}
	}

	return github.WorkflowJob{ID: h.nextJob, RunID: 1, RunAttempt: 1, Name: "build", Labels: labels, Status: "queued", CreatedAt: time.Now()}
}

func (h *harness) queue(labels ...string) github.WorkflowJob {
	h.t.Helper()
	job := h.newJob(labels...)
	h.event("queued", job, true)

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

func (h *harness) sync(instances []agentproto.Instance, capacity int) agentproto.SyncResponse {
	h.t.Helper()
	resp, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{AgentVersion: "test", Provider: "fake", Instances: instances, Capacity: capacity})
	if err != nil {
		h.t.Fatalf("sync: %v", err)
	}

	return resp
}

func (h *harness) runners() []db.Runner {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, "select id, state, coalesce(finish_reason, ''), trigger_job_id from runners order by created_at")
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []db.Runner
	for rows.Next() {
		var r db.Runner
		var reason string
		if err := rows.Scan(&r.ID, &r.State, &reason, &r.TriggerJobID); err != nil {
			h.t.Fatal(err)
		}
		if reason != "" {
			r.FinishReason = &reason
		}
		out = append(out, r)
	}

	return out
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func instanceFor(launch agentproto.Launch) agentproto.Instance {
	return agentproto.Instance{RunnerID: launch.RunnerID, InstanceID: "i-" + launch.RunnerName}
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
	if len(got) != len(want) {
		t.Fatalf("runner states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runner states = %v, want %v", got, want)
		}
	}
}

func TestHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	job := h.queue(label)
	h.queue(label) // a second, independent job
	h.event("queued", job, true)
	wantStates(t, h, "pending", "pending") // duplicate delivery adds nothing

	resp := h.sync(nil, 1)
	if len(resp.Launch) != 1 {
		t.Fatalf("launches = %d, want 1 (capacity)", len(resp.Launch))
	}
	launch := resp.Launch[0]
	registered, ok := h.gh.RunnerByJIT(launch.JITConfig)
	if !ok || registered.Repo != repoName || registered.Name != launch.RunnerName {
		t.Fatalf("jit config does not match a registration: %+v", registered)
	}
	if got := registered.Labels; len(got) != 4 || got[3] != label {
		t.Fatalf("registered labels = %v", got)
	}
	if launch.Profile.InstanceType != "c7i.xlarge" || launch.MaxLifetimeSeconds <= 0 || launch.RunnerVersion == "" {
		t.Fatalf("launch spec incomplete: %+v", launch)
	}

	instances := []agentproto.Instance{instanceFor(launch)}
	resp = h.sync(instances, 0)
	if len(resp.Terminate) != 0 {
		t.Fatalf("unexpected terminate: %+v", resp.Terminate)
	}
	wantStates(t, h, "running", "pending")

	h.start(job, launch.RunnerName)
	wantStates(t, h, "busy", "pending")
	stored, err := h.q.GetJob(h.ctx, job.ID)
	if err != nil || stored.RunnerID == nil || stored.Status != "in_progress" {
		t.Fatalf("job not linked to runner: %+v %v", stored, err)
	}

	h.complete(job, &launch.RunnerName, "success")
	wantStates(t, h, "finished:completed", "pending")

	resp = h.sync(instances, 0)
	if len(resp.Terminate) != 1 || resp.Terminate[0].InstanceID != instances[0].InstanceID {
		t.Fatalf("finished runner's instance not terminated: %+v", resp.Terminate)
	}
	// Once the instance is shutting down it is not terminated again.
	instances[0].Stopping = true
	if resp = h.sync(instances, 0); len(resp.Terminate) != 0 {
		t.Fatalf("stopping instance terminated again: %+v", resp.Terminate)
	}
}

func TestOutOfOrderEventsNeverCreateRunners(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	job := h.newJob(label)
	h.start(job, "someone-elses-runner")
	h.event("queued", job, true) // late
	wantStates(t, h)

	stored, err := h.q.GetJob(h.ctx, job.ID)
	if err != nil || stored.Status != "in_progress" {
		t.Fatalf("status moved backwards: %+v %v", stored, err)
	}
}

func TestCancelledBeforeLaunch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	job := h.queue(label)
	h.complete(job, nil, "cancelled")
	wantStates(t, h, "finished:cancelled")
	if resp := h.sync(nil, 10); len(resp.Launch) != 0 {
		t.Fatalf("launched a runner for a cancelled job")
	}
	if h.gh.Calls("POST generate-jitconfig") != 0 {
		t.Fatal("registered a runner for a cancelled job")
	}
}

func TestStolenRunnerIsReplacedImmediately(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	a := h.queue(label)
	b := h.queue(label)
	launch := h.sync(nil, 1).Launch[0] // created for job a
	// GitHub gives a's runner to job b instead. b's own runner is now spare
	// and goes to a, rather than a waiting for the reconciler.
	h.start(b, launch.RunnerName)
	runners := h.runners()
	if runners[0].State != "busy" || runners[1].State != "pending" || runners[1].TriggerJobID == nil || *runners[1].TriggerJobID != a.ID {
		t.Fatalf("spare runner not handed to the robbed job: %v trigger=%v", states(runners), runners[1].TriggerJobID)
	}
	second := h.sync(nil, 10).Launch[0]
	h.sync([]agentproto.Instance{instanceFor(launch), instanceFor(second)}, 0)
	h.start(a, second.RunnerName)
	wantStates(t, h, "busy", "busy")
}

func TestCrossedRunnersBothStayServed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	a := h.queue(label)
	b := h.queue(label)
	resp := h.sync(nil, 10)
	ra, rb := resp.Launch[0], resp.Launch[1]
	h.sync([]agentproto.Instance{instanceFor(ra), instanceFor(rb)}, 0)

	// Each job lands on the other's runner, with the events racing.
	var wg sync.WaitGroup
	wg.Go(func() { h.start(b, ra.RunnerName) })
	wg.Go(func() { h.start(a, rb.RunnerName) })
	wg.Wait()
	wantStates(t, h, "busy", "busy")
}

func TestMaxRunnersIsEnforcedUnderConcurrentSyncs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	for range 40 {
		h.queue(label)
	}

	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 40 {
		wg.Go(func() {
			<-start
			resp, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{Provider: "fake", Capacity: 10})
			if err != nil {
				t.Error(err)

				return
			}
			mu.Lock()
			total += len(resp.Launch)
			mu.Unlock()
		})
	}
	close(start)
	wg.Wait()
	if total != 3 {
		t.Fatalf("launched %d runners across concurrent syncs, want exactly max_runners=3", total)
	}
}

func TestSkippedJobs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	public := h.newJob(label)
	h.event("queued", public, false)
	unsatisfiable := h.queue(label, "gpu")
	h.queue("ubuntu-latest") // not ours at all
	wantStates(t, h)

	for id, want := range map[int64]string{public.ID: fleet.SkipPublicRepository, unsatisfiable.ID: fleet.SkipUnsatisfiableLabels} {
		job, err := h.q.GetJob(h.ctx, id)
		if err != nil || job.SkipReason == nil || *job.SkipReason != want {
			t.Fatalf("job %d skip reason = %v, want %s", id, job.SkipReason, want)
		}
	}
}

func TestJITFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	h.queue(label)
	h.gh.FailNext("POST generate-jitconfig", http.StatusInternalServerError)
	if resp := h.sync(nil, 10); len(resp.Launch) != 0 {
		t.Fatal("launched despite a jit failure")
	}
	wantStates(t, h, "pending") // transient: back in line, after a short backoff
	if resp := h.sync(nil, 10); len(resp.Launch) != 0 {
		t.Fatal("retried without backing off")
	}
	h.exec("update runners set not_before = now() - interval '1 second'")
	if resp := h.sync(nil, 10); len(resp.Launch) != 1 {
		t.Fatal("transient failure was not retried")
	}

	h.queue(label)
	h.gh.FailNext("POST generate-jitconfig", http.StatusNotFound)
	h.sync(nil, 10)
	wantStates(t, h, "assigned", "finished:launch_failed")
}

func TestJITConflictRecoversFromALostResponse(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	h.gh.FailNext("POST generate-jitconfig", http.StatusConflict)
	if resp := h.sync(nil, 10); len(resp.Launch) != 1 {
		t.Fatal("409 from an earlier registration was not recovered")
	}
}

func TestLaunchFailuresRetryUpToMaxAttempts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)

	for attempt := 1; attempt <= 4; attempt++ {
		resp := h.sync(nil, 10)
		if attempt > 3 {
			if len(resp.Launch) != 0 {
				t.Fatalf("attempt %d launched; max attempts is 3", attempt)
			}

			break
		}
		if len(resp.Launch) != 1 {
			t.Fatalf("attempt %d: no launch", attempt)
		}
		_, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{Provider: "fake", Failures: []agentproto.LaunchFailure{
			{RunnerID: resp.Launch[0].RunnerID, Error: "InsufficientInstanceCapacity", Retryable: true},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	wantStates(t, h, "finished:launch_failed", "finished:launch_failed", "finished:launch_failed")
}

func TestOtherDataPlanesCannotTouchRunners(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	launch := h.sync(nil, 10).Launch[0]

	other, err := h.q.CreateDataPlane(h.ctx, db.CreateDataPlaneParams{InstallationID: installationID, Name: "other", TokenHash: []byte("other"), TokenHint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := h.svc.Sync(h.ctx, fleet.DataPlane{DataPlane: other}, agentproto.SyncRequest{
		Provider:  "fake",
		Instances: []agentproto.Instance{instanceFor(launch)},
		Failures:  []agentproto.LaunchFailure{{RunnerID: launch.RunnerID, Error: "forged"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "assigned")
	if len(resp.Terminate) != 1 || resp.Terminate[0].Reason != "unknown runner" {
		t.Fatalf("instance claimed by another data plane should be terminated: %+v", resp.Terminate)
	}
}

func TestLeaseExpiryRetriesAndALateReportRevives(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	launch := h.sync(nil, 10).Launch[0]

	h.exec("update runners set lease_expires_at = now() - interval '1 second'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:lease_expired", "pending")

	// The launch did happen; the agent just reported it late.
	h.sync([]agentproto.Instance{instanceFor(launch)}, 0)
	runners := h.runners()
	if runners[0].State != "running" || runners[0].TriggerJobID != nil {
		t.Fatalf("late report did not revive the runner: %v", states(runners))
	}
}

func TestVanishedInstances(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	job := h.queue(label)
	launch := h.sync(nil, 10).Launch[0]
	h.sync([]agentproto.Instance{instanceFor(launch)}, 0)

	// Within the grace period, a missing instance is not yet gone.
	h.sync(nil, 0)
	wantStates(t, h, "running")

	h.exec("update runners set running_at = now() - interval '1 hour'")
	h.sync(nil, 0)
	wantStates(t, h, "finished:lost", "pending") // and the job gets another runner

	second := h.sync(nil, 10).Launch[0]
	h.sync([]agentproto.Instance{instanceFor(second)}, 0)
	h.start(job, second.RunnerName)
	h.exec("update runners set running_at = now() - interval '1 hour' where state = 'busy'")
	h.sync(nil, 0) // busy runner's instance vanished: wait for GitHub
	wantStates(t, h, "finished:lost", "busy")
	h.exec("update runners set instance_gone_at = now() - interval '1 hour' where state = 'busy'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:lost", "finished:lost")
}

func TestIdleRunnersAreReaped(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	h.queue(label)
	resp := h.sync(nil, 10)
	instances := []agentproto.Instance{instanceFor(resp.Launch[0]), instanceFor(resp.Launch[1])}
	h.sync(instances, 0)

	// The second runner was picked up per GitHub, but its webhook was lost.
	busy, _ := h.gh.RunnerByJIT(resp.Launch[1].JITConfig)
	h.gh.SetRunnerBusy(busy.ID, true)
	h.exec("update runners set running_at = now() - interval '1 hour'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	for _, runner := range h.runners() {
		want := "finished"
		if runner.ID.String() == resp.Launch[1].RunnerID {
			want = "busy"
		}
		if runner.State != want {
			t.Fatalf("runner %s state = %s, want %s", runner.ID, runner.State, want)
		}
	}
	if len(h.gh.Runners()) != 1 {
		t.Fatalf("idle runner not deleted from GitHub: %+v", h.gh.Runners())
	}
	if got := h.sync(instances, 0); len(got.Terminate) != 1 || got.Terminate[0].RunnerID != resp.Launch[0].RunnerID {
		t.Fatalf("idle runner's instance not terminated: %+v", got.Terminate)
	}
}

func TestStaleQueuedJobIsRefreshed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	job := h.queue(label)

	// GitHub cancelled the job but the webhook never arrived.
	cancelled := job
	cancelled.Status = "completed"
	conclusion := "cancelled"
	cancelled.Conclusion = &conclusion
	h.gh.SetJob(cancelled)
	h.exec("update jobs set updated_at = now() - interval '1 hour'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:cancelled")
}

func TestUninstallTearsEverythingDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	h.queue(label)
	launch := h.sync(nil, 1).Launch[0]
	h.sync([]agentproto.Instance{instanceFor(launch)}, 0)

	if err := h.svc.HandleInstallation(h.ctx, github.InstallationEvent{Action: "deleted", Installation: github.Installation{ID: installationID}}); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:deleted", "finished:deleted")
	h.dp.Retired = true
	resp := h.sync([]agentproto.Instance{instanceFor(launch)}, 10)
	if len(resp.Terminate) != 1 || len(resp.Launch) != 0 {
		t.Fatalf("retired data plane: %+v", resp)
	}
}

func TestLongPollWakesOnNewWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)

	done := make(chan agentproto.SyncResponse, 1)
	go func() {
		resp, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{Provider: "fake", Capacity: 1, WaitSeconds: 20})
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	h.queue(label)
	select {
	case resp := <-done:
		if len(resp.Launch) != 1 {
			t.Fatalf("long poll returned without the new runner: %+v", resp)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Fatalf("long poll took %v to notice new work", waited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("long poll did not wake")
	}
}
