package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/google/uuid"
)

// scanInstances compares what each cloud lists against the runners table,
// one (credential, region) at a time: instances for finished or unknown
// runners are terminated, runners whose instance vanished are marked, and
// runners given up on that did launch after all are revived.
//
// The scan is level-triggered and idempotent, so a cloud API error on one
// pass costs only latency.
func (s *Service) scanInstances(ctx context.Context) error {
	scopes, err := s.q.ListInventoryScopes(ctx)
	if err != nil {
		return fmt.Errorf("list inventory scopes: %w", err)
	}
	var errs []error
	for _, scope := range scopes {
		if scope.CredentialID == nil {
			continue
		}
		if err := s.scanScope(ctx, *scope.CredentialID, scope.Region); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", scope.Region, err))
		}
	}

	return errors.Join(errs...)
}

func (s *Service) scanScope(ctx context.Context, credentialID uuid.UUID, region string) error {
	cred, err := s.q.GetCloudCredentialByID(ctx, credentialID)
	if isNoRows(err) {
		return nil // deleted; its runners lost their credential_id too
	}
	if err != nil {
		return fmt.Errorf("get credential: %w", err)
	}
	provider, err := s.clouds.Get(cred.Cloud)
	if err != nil {
		return err
	}
	secret, err := s.secrets.Open(cred.Secret, credentialContext(cred.InstallationID, cred.Cloud))
	if err != nil {
		return fmt.Errorf("open credential: %w", err)
	}
	instances, err := provider.List(ctx, secret, region, cred.InstallationID)
	if err != nil {
		// Never act on a partial view: an instance missing from a list
		// would be presumed gone.
		return fmt.Errorf("list instances: %w", err)
	}

	ids := make([]uuid.UUID, 0, len(instances))
	instanceIDs := make([]string, 0, len(instances))
	reported := map[uuid.UUID]cloud.Instance{}
	var terminate []cloud.Instance
	for _, instance := range instances {
		id, err := uuid.Parse(instance.RunnerID)
		if err != nil {
			terminate = append(terminate, instance) // tagged for us, but not a runner we know

			continue
		}
		if instance.Stopped {
			// A stopped machine runs no jobs and still costs its volume:
			// its runner is as good as gone, and the machine goes.
			terminate = append(terminate, instance)

			continue
		}
		ids = append(ids, id)
		instanceIDs = append(instanceIDs, instance.InstanceID)
		reported[id] = instance
	}

	if err := s.reviveRunners(ctx, credentialID, ids); err != nil {
		return err
	}
	known, err := s.q.ReportInstances(ctx, db.ReportInstancesParams{CredentialID: &credentialID, Region: region, RunnerIds: ids, InstanceIds: instanceIDs})
	if err != nil {
		return fmt.Errorf("report instances: %w", err)
	}
	unknown := maps.Clone(reported)
	for _, runner := range known {
		delete(unknown, runner.ID)
		if runner.State == StateFinished {
			terminate = append(terminate, reported[runner.ID])
		}
	}
	// Whatever is left was launched for a runner this credential does not
	// own in this region (deleted, or never existed). It must not keep
	// running.
	for _, instance := range unknown {
		terminate = append(terminate, instance)
	}
	for _, instance := range terminate {
		if instance.Stopping {
			continue
		}
		if err := provider.Terminate(ctx, secret, region, instance.InstanceID, cred.InstallationID); err != nil {
			s.log.WarnContext(ctx, "terminate instance", slog.String("instance", instance.InstanceID), slog.Any("error", err))

			continue
		}
		s.log.InfoContext(ctx, "terminated instance", slog.String("instance", instance.InstanceID), slog.String("runner", instance.RunnerID), slog.String("region", region))
		if id, err := uuid.Parse(instance.RunnerID); err == nil {
			_ = s.q.MarkTerminateRequested(ctx, id)
		}
	}

	// A cloud whose machines are built from several resources sweeps up
	// the parts a failed launch left behind. A sweep that fails is logged
	// and tried on the next scan; the instances above were still handled.
	if sweeper, ok := provider.(cloud.Sweeper); ok {
		if err := sweeper.Sweep(ctx, secret, region, cred.InstallationID); err != nil {
			s.log.WarnContext(ctx, "sweep orphaned resources", slog.String("cloud", cred.Cloud), slog.String("region", region), slog.Any("error", err))
		}
	}

	gone, err := s.q.MarkInstancesGone(ctx, db.MarkInstancesGoneParams{
		CredentialID: &credentialID,
		Region:       region,
		ReportedIds:  ids,
		SeenBefore:   time.Now().Add(-s.cfg.InstanceGrace),
	})
	if err != nil {
		return fmt.Errorf("mark instances gone: %w", err)
	}
	for _, runner := range gone {
		if runner.State == StateFinished && deref(runner.FinishReason) == ReasonLost {
			s.log.InfoContext(ctx, "runner instance disappeared before taking a job", slog.String("runner", runner.ID.String()))
			s.record(ctx, runner.InstallationID, "runner.lost", levelWarning,
				fmt.Sprintf("%s (%s) disappeared from %s before taking a job", runner.Name, deref(runner.InstanceID), region),
				eventFields{repository: runner.RepositoryFullName, runnerID: &runner.ID, jobID: runner.TriggerJobID})
			if err := s.retryJob(ctx, runner, true); err != nil {
				return err
			}
		}
	}

	return nil
}

// reviveRunners brings back runners whose lease expired or whose launch
// went unconfirmed but whose instance did launch. GitHub is asked first
// whether the registration still exists: a machine without one can never
// take a job, so it is left finished (and its instance terminated), and
// the retry runner its job already has is kept. If GitHub cannot answer
// the runner is revived anyway, as before the check: the machine is real,
// and a wrong revival only idles out. When the lease expired a retry
// runner was queued for the same job; if it has not been claimed yet it
// is cancelled, so the installation does not run more machines than
// max_runners. (If the revived runner then takes some other job, the
// stale job refresh queues a runner for this one.)
func (s *Service) reviveRunners(ctx context.Context, credentialID uuid.UUID, ids []uuid.UUID) error {
	candidates, err := s.q.ListRevivableRunners(ctx, db.ListRevivableRunnersParams{CredentialID: &credentialID, RunnerIds: ids})
	if err != nil {
		return fmt.Errorf("list revivable runners: %w", err)
	}
	var confirmed []uuid.UUID
	for _, runner := range candidates {
		registered, err := s.registrationExists(ctx, runner)
		switch {
		case err != nil:
			s.log.WarnContext(ctx, "cannot check registration of a listed runner; reviving it", slog.String("runner", runner.ID.String()), slog.Any("error", err))
			confirmed = append(confirmed, runner.ID)
		case registered:
			confirmed = append(confirmed, runner.ID)
		default:
			s.log.InfoContext(ctx, "runner listed after it was given up, but its registration is gone; not revived", slog.String("runner", runner.ID.String()))
			if err := s.q.MarkRunnerDeregistered(ctx, runner.ID); err != nil {
				return fmt.Errorf("mark deregistered: %w", err)
			}
		}
	}
	if len(confirmed) == 0 {
		return nil
	}
	revived, err := s.q.ReviveRunners(ctx, db.ReviveRunnersParams{CredentialID: &credentialID, RunnerIds: confirmed})
	if err != nil {
		return fmt.Errorf("revive runners: %w", err)
	}
	for _, runner := range revived {
		s.log.InfoContext(ctx, "runner listed after it was given up, revived", slog.String("runner", runner.ID.String()))
		if runner.OldTriggerJobID == nil {
			continue
		}
		if _, err := s.q.CancelPendingRunnersForJob(ctx, runner.OldTriggerJobID); err != nil {
			return fmt.Errorf("cancel retry runner: %w", err)
		}
	}

	return nil
}

// registrationExists asks GitHub whether a runner's registration is still
// there. A runner that never got one has nothing to revive.
func (s *Service) registrationExists(ctx context.Context, runner db.Runner) (bool, error) {
	if runner.GithubRunnerID == nil {
		return false, nil
	}
	reg, err := s.registrationFor(ctx, runner, false)
	if err != nil {
		return false, nil //nolint:nilerr // a malformed scope is permanent: nothing to revive
	}
	_, err = reg.get(ctx, *runner.GithubRunnerID)
	if github.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get runner: %w", err)
	}

	return true, nil
}

// terminateFinished asks the cloud to destroy the instances of finished
// runners that are still around: those whose job completed, were reaped,
// or were lost, plus any whose earlier request did not take.
func (s *Service) terminateFinished(ctx context.Context) error {
	runners, err := s.q.ListRunnersToTerminate(ctx, db.ListRunnersToTerminateParams{
		RequestedBefore: time.Now().Add(-s.cfg.TerminateRetryAfter),
		MaxResults:      reconcileBatch,
	})
	if err != nil {
		return fmt.Errorf("list runners to terminate: %w", err)
	}
	for _, runner := range runners {
		s.terminateRunner(ctx, runner)
	}

	return nil
}

// terminateRunner destroys a finished runner's instance. Errors are logged;
// the reconciler tries again, and the instance also shuts itself down when
// its runner exits or its deadline passes.
func (s *Service) terminateRunner(ctx context.Context, runner db.Runner) {
	if runner.InstanceID == nil || runner.State != StateFinished {
		return
	}
	log := s.log.With(slog.String("runner", runner.ID.String()), slog.String("instance", *runner.InstanceID))
	provider, secret, err := s.credentialFor(ctx, runner)
	if err != nil {
		log.WarnContext(ctx, "cannot terminate instance", slog.Any("error", err))
		// Nothing more can be done; the instance's own deadline ends it.
		_ = s.q.MarkTerminateRequested(ctx, runner.ID)

		return
	}
	if err := provider.Terminate(ctx, secret, runner.Region, *runner.InstanceID, runner.InstallationID); err != nil {
		log.WarnContext(ctx, "terminate instance", slog.Any("error", err))

		return
	}
	log.InfoContext(ctx, "terminated instance", slog.String("reason", deref(runner.FinishReason)))
	if err := s.q.MarkTerminateRequested(ctx, runner.ID); err != nil {
		log.WarnContext(ctx, "mark terminate requested", slog.Any("error", err))
	}
}

// terminateSoon destroys a runner's instance without waiting for the next
// reconcile pass, as when its job just completed.
func (s *Service) terminateSoon(ctx context.Context, runnerID uuid.UUID) {
	runner, err := s.q.GetRunner(ctx, runnerID)
	if err != nil {
		return
	}
	s.terminateRunner(context.WithoutCancel(ctx), runner)
}
