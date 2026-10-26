package fleet_test

import (
	"errors"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
)

// syncAck syncs as an agent that acknowledges the launches it received.
func (h *harness) syncAck(instances []agentproto.Instance, received []string, capacity int) agentproto.SyncResponse {
	h.t.Helper()
	if received == nil {
		received = []string{}
	}
	resp, err := h.svc.Sync(h.ctx, h.dp, agentproto.SyncRequest{
		AgentVersion: "test", Provider: "fake", Instances: instances, Received: received, Capacity: capacity,
	})
	if err != nil {
		h.t.Fatalf("sync: %v", err)
	}

	return resp
}

func TestLostSyncResponseIsResentNotExpired(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.queue(label)
	lost := h.syncAck(nil, nil, 10).Launch
	if len(lost) != 1 {
		t.Fatalf("launches = %d, want 1", len(lost))
	}

	// The agent never got that response, so it acknowledges nothing. The
	// same runner is handed out again with a fresh registration.
	again := h.syncAck(nil, nil, 10).Launch
	if len(again) != 1 || again[0].RunnerID != lost[0].RunnerID {
		t.Fatalf("lost launch was not resent: %+v", again)
	}
	if again[0].JITConfig == lost[0].JITConfig {
		t.Fatal("resent launch reused a JIT config that may already be spent")
	}
	if n := len(h.gh.Runners()); n != 1 {
		t.Fatalf("GitHub has %d registrations for one runner, want 1", n)
	}
	wantStates(t, h, "assigned")

	// Once acknowledged, it is left alone.
	if resp := h.syncAck(nil, []string{again[0].RunnerID}, 10); len(resp.Launch) != 0 {
		t.Fatalf("acknowledged runner was resent: %+v", resp.Launch)
	}
	wantStates(t, h, "assigned")

	// A runner assigned after the request arrived may be in a response
	// still on its way; it is not requeued.
	h.exec("update runners set assigned_at = now() + interval '1 minute'")
	if resp := h.syncAck(nil, nil, 0); len(resp.Launch) != 0 {
		t.Fatal("runner assigned after the request was requeued")
	}
	wantStates(t, h, "assigned")
}

func TestLateReportStaysWithinMaxRunners(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1)
	h.queue(label)
	launch := h.sync(nil, 10).Launch[0]

	h.exec("update runners set lease_expires_at = now() - interval '1 second'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	wantStates(t, h, "finished:lease_expired", "pending")

	// The first instance shows up after all. Its retry has not launched, so
	// it is cancelled rather than started alongside it.
	h.sync([]agentproto.Instance{instanceFor(launch)}, 10)
	wantStates(t, h, "running", "finished:cancelled")
}

func TestRateLimitedRegistrationWaitsForRetryAfter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	h.gh.FailNext("POST generate-jitconfig", 429)
	h.queue(label)
	if resp := h.sync(nil, 10); len(resp.Launch) != 0 {
		t.Fatal("launched despite the rate limit")
	}
	if resp := h.sync(nil, 10); len(resp.Launch) != 0 {
		t.Fatal("retried before Retry-After passed")
	}
	if calls := h.gh.Calls("POST generate-jitconfig"); calls != 1 {
		t.Fatalf("generate-jitconfig called %d times, want 1", calls)
	}
	var wait float64
	if err := h.pool.QueryRow(h.ctx, "select extract(epoch from not_before - now()) from runners").Scan(&wait); err != nil {
		t.Fatal(err)
	}
	if wait < fakegithub.RetryAfterSeconds-5 || wait > fakegithub.RetryAfterSeconds {
		t.Fatalf("runner held back %.0fs, want about %ds", wait, fakegithub.RetryAfterSeconds)
	}

	h.exec("update runners set not_before = now() - interval '1 second'")
	if resp := h.sync(nil, 10); len(resp.Launch) != 1 {
		t.Fatal("runner was not launched once the wait was over")
	}
}

func TestDeletingAProfileKeepsRunnerHistory(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	job := h.queue(label)
	launch := h.sync(nil, 10).Launch[0]
	h.sync([]agentproto.Instance{instanceFor(launch)}, 0)

	var busy *fleet.ProfileBusyError
	if err := h.svc.DeleteProfile(h.ctx, installationID, h.profile.ID); !errors.As(err, &busy) || busy.Active != 1 {
		t.Fatalf("deleting a profile with an active runner: %v", err)
	}

	h.start(job, launch.RunnerName)
	h.complete(job, &launch.RunnerName, "success")
	if err := h.svc.DeleteProfile(h.ctx, installationID, h.profile.ID); err != nil {
		t.Fatal(err)
	}
	var label string
	var profileGone bool
	if err := h.pool.QueryRow(h.ctx, "select profile_label, profile_id is null from runners").Scan(&label, &profileGone); err != nil {
		t.Fatalf("runner history was deleted with the profile: %v", err)
	}
	if label != h.profile.Label || !profileGone {
		t.Fatalf("runner kept label %q, profile cleared %v", label, profileGone)
	}
	if err := h.svc.DeleteProfile(h.ctx, installationID, h.profile.ID); !errors.Is(err, fleet.ErrProfileNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// Jobs for the deleted label are left to GitHub.
	h.queue(label)
	wantStates(t, h, "finished:completed")
}

func TestStaleJobRefreshIsCappedPerInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10)
	for range 5 {
		h.queue(label)
	}
	h.exec("update jobs set updated_at = now() - interval '1 hour'")
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	if calls := h.gh.Calls("GET job"); calls != 2 {
		t.Fatalf("one pass asked GitHub about %d jobs of one installation, want 2", calls)
	}
	// Each refreshed job was touched, so the next pass moves on to others.
	time.Sleep(10 * time.Millisecond)
	if err := h.svc.ReconcileOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := h.pool.QueryRow(h.ctx, "select count(*) from jobs where updated_at < now() - interval '30 minutes'").Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 1 {
		t.Fatalf("%d jobs still stale after two passes, want 1", stale)
	}
}
