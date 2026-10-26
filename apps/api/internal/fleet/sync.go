package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/google/uuid"
)

// jitConcurrency bounds parallel generate-jitconfig calls per sync. GitHub's
// secondary rate limits punish bursts of POSTs.
const jitConcurrency = 4

// DataPlane is an authenticated agent's data plane.
type DataPlane struct {
	db.DataPlane

	// Retired is set when the installation was removed: the agent should
	// tear everything down and get no new work.
	Retired bool
	// Paused is set while the installation is suspended.
	Paused bool
}

// Sync is one round of the agent protocol: record what the agent sees,
// tell it what to terminate, and hand it runners to launch. With
// WaitSeconds set it long-polls until there is work or time runs out.
func (s *Service) Sync(ctx context.Context, dp DataPlane, req agentproto.SyncRequest) (agentproto.SyncResponse, error) {
	resp := agentproto.SyncResponse{DataPlaneID: dp.ID.String(), Launch: []agentproto.Launch{}, Terminate: []agentproto.Terminate{}}

	received, err := s.q.TouchDataPlane(ctx, db.TouchDataPlaneParams{ID: dp.ID, AgentVersion: &req.AgentVersion, AgentProvider: &req.Provider, AgentRegion: nilIfEmpty(req.Region)})
	if err != nil {
		return resp, fmt.Errorf("touch data plane: %w", err)
	}
	if err := s.recordFailures(ctx, dp, req.Failures); err != nil {
		return resp, err
	}
	if req.InventoryUnknown {
		return resp, nil
	}
	if err := s.requeueUnacknowledged(ctx, dp, req, received); err != nil {
		return resp, err
	}

	deadline := time.Now().Add(time.Duration(min(req.WaitSeconds, agentproto.MaxWaitSeconds)) * time.Second)
	for {
		// Subscribe before looking so a notification in between is not lost.
		wake := s.notifier.Wait(dp.ID)

		terminate, err := s.reconcileInstances(ctx, dp, req.Instances)
		if err != nil {
			return resp, err
		}
		resp.Terminate = terminate
		if !dp.Retired && !dp.Paused && req.Capacity > 0 {
			launch, err := s.claim(ctx, dp, req.Capacity)
			if err != nil {
				return resp, err
			}
			resp.Launch = launch
		}
		if len(resp.Launch) > 0 || len(resp.Terminate) > 0 {
			return resp, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return resp, nil
		}

		timer := time.NewTimer(min(remaining, s.cfg.PollRecheck))
		select {
		case <-ctx.Done():
			timer.Stop()

			return resp, nil
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *Service) recordFailures(ctx context.Context, dp DataPlane, failures []agentproto.LaunchFailure) error {
	for _, failure := range failures {
		id, err := uuid.Parse(failure.RunnerID)
		if err != nil {
			continue
		}
		message := failure.Error
		runner, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
			ID:          id,
			Reason:      ReasonLaunchFailed,
			Error:       &message,
			FromStates:  []string{StateAssigned},
			DataPlaneID: &dp.ID,
		})
		if isNoRows(err) {
			continue // already handled, or not this data plane's runner
		}
		if err != nil {
			return fmt.Errorf("record launch failure: %w", err)
		}
		s.log.InfoContext(ctx, "launch failed", slog.String("runner", id.String()), slog.String("error", message))
		if err := s.retryJob(ctx, runner, failure.Retryable); err != nil {
			return err
		}
	}

	return nil
}

// requeueUnacknowledged puts back in the queue runners this data plane was
// handed in a response it never received, so a dropped response costs a
// sync round instead of a lease and a launch attempt. The agent syncs one
// request at a time, so any runner assigned before this request arrived
// was in a response the agent either received (and lists) or lost.
//
// The runner keeps its name: claiming it again replaces its JIT config,
// and generateJIT removes the stale registration.
func (s *Service) requeueUnacknowledged(ctx context.Context, dp DataPlane, req agentproto.SyncRequest, arrived time.Time) error {
	if req.Received == nil {
		return nil // an agent that does not acknowledge launches
	}
	acknowledged := make([]uuid.UUID, 0, len(req.Received)+len(req.Instances))
	for _, id := range req.Received {
		if parsed, err := uuid.Parse(id); err == nil {
			acknowledged = append(acknowledged, parsed)
		}
	}
	for _, instance := range req.Instances {
		if parsed, err := uuid.Parse(instance.RunnerID); err == nil {
			acknowledged = append(acknowledged, parsed)
		}
	}
	requeued, err := s.q.RequeueUnacknowledgedRunners(ctx, db.RequeueUnacknowledgedRunnersParams{
		DataPlaneID:    dp.ID,
		AssignedBefore: arrived,
		Acknowledged:   acknowledged,
	})
	if err != nil {
		return fmt.Errorf("requeue unacknowledged runners: %w", err)
	}
	for _, runner := range requeued {
		s.log.InfoContext(ctx, "agent never received runner, queued again", slog.String("runner", runner.ID.String()))
	}

	return nil
}

// reconcileInstances compares the agent's inventory with the runners table
// and returns the instances that should not exist.
func (s *Service) reconcileInstances(ctx context.Context, dp DataPlane, instances []agentproto.Instance) ([]agentproto.Terminate, error) {
	terminate := []agentproto.Terminate{}
	ids := make([]uuid.UUID, 0, len(instances))
	instanceIDs := make([]string, 0, len(instances))
	reported := map[uuid.UUID]agentproto.Instance{}
	for _, instance := range instances {
		id, err := uuid.Parse(instance.RunnerID)
		if err != nil {
			terminate = appendTerminate(terminate, instance, "unknown runner")

			continue
		}
		ids = append(ids, id)
		instanceIDs = append(instanceIDs, instance.InstanceID)
		reported[id] = instance
	}

	if err := s.reviveRunners(ctx, dp, ids); err != nil {
		return nil, err
	}
	known, err := s.q.ReportInstances(ctx, db.ReportInstancesParams{DataPlaneID: dp.ID, RunnerIds: ids, InstanceIds: instanceIDs})
	if err != nil {
		return nil, fmt.Errorf("report instances: %w", err)
	}
	unknown := maps.Clone(reported)
	for _, runner := range known {
		delete(unknown, runner.ID)
		if runner.State == StateFinished {
			terminate = appendTerminate(terminate, reported[runner.ID], deref(runner.FinishReason))
		}
	}
	// Whatever is left was launched for a runner this data plane does not
	// own (deleted, or never existed). It must not keep running.
	for _, instance := range unknown {
		terminate = appendTerminate(terminate, instance, "unknown runner")
	}

	gone, err := s.q.MarkInstancesGone(ctx, db.MarkInstancesGoneParams{
		DataPlaneID: dp.ID,
		ReportedIds: ids,
		SeenBefore:  time.Now().Add(-s.cfg.InstanceGrace),
	})
	if err != nil {
		return nil, fmt.Errorf("mark instances gone: %w", err)
	}
	for _, runner := range gone {
		if runner.State == StateFinished && deref(runner.FinishReason) == ReasonLost {
			s.log.InfoContext(ctx, "runner instance disappeared before taking a job", slog.String("runner", runner.ID.String()))
			if err := s.retryJob(ctx, runner, true); err != nil {
				return nil, err
			}
		}
	}

	return terminate, nil
}

// reviveRunners brings back runners whose lease expired but whose instance
// did launch. When the lease expired a retry runner was queued for the same
// job; if it has not been claimed yet it is cancelled, so the profile does
// not run more machines than max_runners. (If the revived runner then takes
// some other job, the stale job refresh queues a runner for this one.)
func (s *Service) reviveRunners(ctx context.Context, dp DataPlane, ids []uuid.UUID) error {
	revived, err := s.q.ReviveRunners(ctx, db.ReviveRunnersParams{DataPlaneID: dp.ID, RunnerIds: ids})
	if err != nil {
		return fmt.Errorf("revive runners: %w", err)
	}
	for _, runner := range revived {
		s.log.InfoContext(ctx, "runner reported after its lease expired, revived", slog.String("runner", runner.ID.String()))
		if runner.OldTriggerJobID == nil {
			continue
		}
		if _, err := s.q.CancelPendingRunnersForJob(ctx, runner.OldTriggerJobID); err != nil {
			return fmt.Errorf("cancel retry runner: %w", err)
		}
	}

	return nil
}

// claimScan bounds how many pending runners one claim looks at.
const claimScan = 500

// claim assigns up to capacity pending runners to the agent, within the
// data plane's max_runners and each profile's, and mints their JIT
// configs. The data plane row is locked while counting, so concurrent
// syncs cannot both take the last slot.
func (s *Service) claim(ctx context.Context, dp DataPlane, capacity int) ([]agentproto.Launch, error) {
	var batch []db.Runner
	err := s.inTx(ctx, func(q *db.Queries) error {
		batch = nil
		maxRunners, err := q.LockDataPlane(ctx, dp.ID)
		if err != nil {
			return fmt.Errorf("lock data plane: %w", err)
		}
		limits, err := q.ListDataPlaneProfileLimits(ctx, dp.ID)
		if err != nil {
			return fmt.Errorf("list profile limits: %w", err)
		}
		profileMax := map[uuid.UUID]int32{}
		for _, limit := range limits {
			profileMax[limit.ID] = limit.MaxRunners
		}
		counts, err := q.CountActiveRunnersByProfile(ctx, dp.ID)
		if err != nil {
			return fmt.Errorf("count active runners: %w", err)
		}
		active := map[uuid.UUID]int32{}
		var total int32
		for _, count := range counts {
			total += count.Active
			if count.ProfileID != nil {
				active[*count.ProfileID] = count.Active
			}
		}

		left := min(int32(capacity), maxRunners-total) //nolint:gosec // capacity is validated to <= 100
		if left <= 0 {
			return nil
		}
		pending, err := q.ListClaimableRunners(ctx, db.ListClaimableRunnersParams{DataPlaneID: dp.ID, MaxResults: claimScan})
		if err != nil {
			return fmt.Errorf("list claimable runners: %w", err)
		}
		ids := make([]uuid.UUID, 0, left)
		for _, runner := range pending {
			if int32(len(ids)) >= left { //nolint:gosec // bounded by left
				break
			}
			if runner.ProfileID != nil {
				limit, ok := profileMax[*runner.ProfileID]
				if !ok || active[*runner.ProfileID] >= limit {
					continue
				}
				active[*runner.ProfileID]++
			}
			ids = append(ids, runner.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		batch, err = q.AssignRunners(ctx, db.AssignRunnersParams{Ids: ids, LeaseSeconds: s.cfg.LeaseDuration.Seconds()})
		if err != nil {
			return fmt.Errorf("assign runners: %w", err)
		}

		return nil
	})
	if err != nil || len(batch) == 0 {
		return nil, err
	}

	// Mint JIT configs outside the transaction: they are network calls.
	launches := make([]*agentproto.Launch, len(batch))
	var wg sync.WaitGroup
	sem := make(chan struct{}, jitConcurrency)
	for i, runner := range batch {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			launches[i] = s.prepareLaunch(ctx, runner)
		})
	}
	wg.Wait()

	out := make([]agentproto.Launch, 0, len(batch))
	for _, launch := range launches {
		if launch != nil {
			out = append(out, *launch)
		}
	}

	return out, nil
}

// prepareLaunch registers the runner with GitHub. On failure the runner is
// put back in the queue (transient) or finished (permanent), and nil is
// returned.
func (s *Service) prepareLaunch(ctx context.Context, runner db.Runner) *agentproto.Launch {
	log := s.log.With(slog.String("runner", runner.ID.String()), slog.String("repository", runner.RepositoryFullName))

	repo, err := github.ParseRepo(runner.RepositoryFullName)
	if err != nil {
		s.failRunner(ctx, runner, err.Error())

		return nil
	}
	spec, err := decodeSpec(runner.Spec)
	if err != nil {
		s.failRunner(ctx, runner, err.Error())

		return nil
	}
	jit, err := s.generateJIT(ctx, runner, repo)
	if err != nil {
		if isPermanent(err) {
			log.WarnContext(ctx, "github refused to register runner", slog.Any("error", err))
			s.failRunner(ctx, runner, "GitHub refused to register the runner: "+err.Error())

			return nil
		}
		backoff := backoffFor(err)
		log.WarnContext(ctx, "could not register runner, will retry", slog.Any("error", err), slog.Duration("retry_in", backoff))
		if err := s.q.ReleaseRunner(context.WithoutCancel(ctx), db.ReleaseRunnerParams{ID: runner.ID, RetryAfterSeconds: backoff.Seconds()}); err != nil {
			log.ErrorContext(ctx, "release runner", slog.Any("error", err))
		}

		return nil
	}
	if err := s.q.SetRunnerGitHubID(ctx, db.SetRunnerGitHubIDParams{ID: runner.ID, GithubRunnerID: &jit.Runner.ID}); err != nil {
		// Without the id the runner could not be cleaned up from GitHub, so do
		// not launch it. The lease expires and the job is retried.
		log.ErrorContext(ctx, "save github runner id", slog.Any("error", err))

		return nil
	}

	return &agentproto.Launch{
		RunnerID:           runner.ID.String(),
		RunnerName:         runner.Name,
		Repository:         runner.RepositoryFullName,
		JITConfig:          jit.EncodedConfig,
		RunnerVersion:      s.cfg.RunnerVersion,
		Profile:            spec.launchProfile(runner.ProfileLabel),
		MaxLifetimeSeconds: int(s.cfg.MaxLifetime.Seconds()),
	}
}

// generateJIT registers the runner. A 409 means a registration with this
// name already exists, which happens when an earlier attempt succeeded at
// GitHub but its response was lost; remove that one and register again.
func (s *Service) generateJIT(ctx context.Context, runner db.Runner, repo github.Repo) (github.JITConfig, error) {
	labels := runner.Labels
	jit, err := s.github.GenerateJITConfig(ctx, runner.InstallationID, repo, runner.Name, labels)
	if github.StatusOf(err) != http.StatusConflict {
		return jit, err
	}
	existing, found, err := s.github.FindRunnerByName(ctx, runner.InstallationID, repo, runner.Name)
	if err != nil {
		return github.JITConfig{}, err
	}
	if found {
		if existing.Busy {
			return github.JITConfig{}, fmt.Errorf("runner %s is already registered and busy", runner.Name)
		}
		if err := s.github.DeleteRunner(ctx, runner.InstallationID, repo, existing.ID); err != nil {
			return github.JITConfig{}, err
		}
	}

	return s.github.GenerateJITConfig(ctx, runner.InstallationID, repo, runner.Name, labels)
}

func (s *Service) failRunner(ctx context.Context, runner db.Runner, message string) {
	_, err := s.q.FinishRunner(context.WithoutCancel(ctx), db.FinishRunnerParams{
		ID:         runner.ID,
		Reason:     ReasonLaunchFailed,
		Error:      &message,
		FromStates: []string{StateAssigned},
	})
	if err != nil && !isNoRows(err) {
		s.log.ErrorContext(ctx, "fail runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))
	}
}

// appendTerminate asks for an instance to go, unless it is already going.
func appendTerminate(out []agentproto.Terminate, instance agentproto.Instance, reason string) []agentproto.Terminate {
	if instance.Stopping {
		return out
	}

	return append(out, agentproto.Terminate{RunnerID: instance.RunnerID, InstanceID: instance.InstanceID, Reason: reason})
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}

	return *p
}
