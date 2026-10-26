package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
)

const (
	// reconcileLockKey is the advisory lock that elects one replica to run
	// the reconciler at a time. The steps are safe to run concurrently; the
	// lock only avoids duplicate GitHub calls.
	reconcileLockKey = 0x41495253 // "AIRS"
	reconcileBatch   = 50
	// staleJobBatch bounds GitHub reads per pass, across installations.
	staleJobBatch = 50
	// staleJobsPerInstallation bounds one installation's reads per pass, so
	// a backlog costs it at most a few hundred of its 5,000 calls an hour
	// and cannot delay other installations' recovery.
	staleJobsPerInstallation = 2
)

// Run reconciles every interval until ctx is done.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.ReconcileOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "reconcile", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileOnce runs one pass if this replica holds the reconcile lock.
func (s *Service) ReconcileOnce(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "select pg_try_advisory_lock($1)", reconcileLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("take reconcile lock: %w", err)
	}
	if !locked {
		return nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", reconcileLockKey)
	}()

	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"expire leases", s.expireLeases},
		{"reap idle runners", s.reapIdle},
		{"reap lost runners", s.reapLostBusy},
		{"deregister runners", s.deregister},
		{"refresh stale jobs", s.refreshStaleJobs},
		{"refresh configs", s.refreshConfigs},
		{"balance pools", s.balancePools},
		{"expire sessions", s.expireSessions},
	}
	var errs []error
	for _, step := range steps {
		if err := step.run(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", step.name, err))
		}
	}

	return errors.Join(errs...)
}

// expireLeases finishes runners an agent was handed but never reported.
// If the launch did happen after all, the next report revives the runner.
func (s *Service) expireLeases(ctx context.Context) error {
	expired, err := s.q.ExpireRunnerLeases(ctx)
	if err != nil {
		return fmt.Errorf("expire leases: %w", err)
	}
	for _, runner := range expired {
		s.log.WarnContext(ctx, "runner lease expired", slog.String("runner", runner.ID.String()))
		if err := s.retryJob(ctx, runner, true); err != nil {
			return err
		}
	}

	return nil
}

// reapIdle removes launched runners that never got a job. The runner is
// deleted from GitHub first, so GitHub cannot hand it a job while its
// machine is being torn down; a runner GitHub says is busy is kept.
func (s *Service) reapIdle(ctx context.Context) error {
	idle, err := s.q.ListIdleRunners(ctx, db.ListIdleRunnersParams{
		RunningBefore: time.Now().Add(-s.cfg.IdleTimeout),
		MaxResults:    reconcileBatch,
	})
	if err != nil {
		return fmt.Errorf("list idle runners: %w", err)
	}
	for _, runner := range idle {
		if err := s.reapIdleRunner(ctx, runner); err != nil {
			s.log.WarnContext(ctx, "reap idle runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))
		}
	}

	return nil
}

func (s *Service) reapIdleRunner(ctx context.Context, runner db.Runner) error {
	finish := func() error {
		_, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
			ID: runner.ID, Reason: ReasonIdle, Deregistered: true, FromStates: []string{StateRunning},
		})
		if err != nil && !isNoRows(err) {
			return fmt.Errorf("finish idle runner: %w", err)
		}

		return nil
	}
	if runner.GithubRunnerID == nil {
		return finish()
	}
	repo, err := github.ParseRepo(runner.RepositoryFullName)
	if err != nil {
		return finish()
	}

	remote, err := s.github.GetRunner(ctx, runner.InstallationID, repo, *runner.GithubRunnerID)
	switch {
	case github.IsNotFound(err):
		return finish()
	case err != nil:
		return fmt.Errorf("get runner: %w", err)
	case remote.Busy:
		// The in_progress webhook was lost or is late.
		if _, err := s.q.MarkRunnerBusy(ctx, runner.ID); err != nil && !isNoRows(err) {
			return fmt.Errorf("mark busy: %w", err)
		}

		return nil
	}

	if err := s.github.DeleteRunner(ctx, runner.InstallationID, repo, *runner.GithubRunnerID); err != nil {
		if github.StatusOf(err) == http.StatusUnprocessableEntity {
			return nil // GitHub assigned it a job in the meantime
		}

		return fmt.Errorf("delete runner: %w", err)
	}
	s.log.InfoContext(ctx, "reaped idle runner", slog.String("runner", runner.ID.String()))

	return finish()
}

// reapLostBusy writes off busy runners whose instance vanished long ago
// without a completed event, and runners past their maximum lifetime.
func (s *Service) reapLostBusy(ctx context.Context) error {
	now := time.Now()
	lost, err := s.q.ListLostBusyRunners(ctx, db.ListLostBusyRunnersParams{
		GoneBefore: now.Add(-s.cfg.BusyLostAfter),
		BusyBefore: now.Add(-s.cfg.MaxLifetime),
		MaxResults: reconcileBatch,
	})
	if err != nil {
		return fmt.Errorf("list lost runners: %w", err)
	}
	for _, runner := range lost {
		reason := ReasonLost
		if runner.InstanceGoneAt == nil {
			reason = ReasonMaxLifetime
		}
		if _, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
			ID: runner.ID, Reason: reason, FromStates: []string{StateBusy},
		}); err != nil && !isNoRows(err) {
			return fmt.Errorf("finish lost runner: %w", err)
		}
	}

	return nil
}

// deregister removes finished runners from GitHub so the repository's
// runner list does not fill with offline registrations.
func (s *Service) deregister(ctx context.Context) error {
	runners, err := s.q.ListRunnersToDeregister(ctx, db.ListRunnersToDeregisterParams{
		FinishedBefore: time.Now().Add(-s.cfg.DeregisterAfter),
		MaxResults:     reconcileBatch,
	})
	if err != nil {
		return fmt.Errorf("list runners to deregister: %w", err)
	}
	for _, runner := range runners {
		repo, err := github.ParseRepo(runner.RepositoryFullName)
		if err == nil {
			err = s.github.DeleteRunner(ctx, runner.InstallationID, repo, *runner.GithubRunnerID)
		}
		if err != nil && !isPermanent(err) {
			s.log.WarnContext(ctx, "deregister runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))

			continue
		}
		if github.StatusOf(err) == http.StatusUnprocessableEntity {
			continue // still running a job; its instance is being terminated
		}
		if err := s.q.MarkRunnerDeregistered(ctx, runner.ID); err != nil {
			return fmt.Errorf("mark deregistered: %w", err)
		}
	}

	return nil
}

// refreshStaleJobs asks GitHub about queued jobs that have gone quiet, in
// case a webhook was lost: the job may have started, finished or been
// cancelled. Jobs still queued get a runner if theirs failed.
func (s *Service) refreshStaleJobs(ctx context.Context) error {
	jobs, err := s.q.ListStaleQueuedJobs(ctx, db.ListStaleQueuedJobsParams{
		Before:          time.Now().Add(-s.cfg.StaleJobAfter),
		PerInstallation: staleJobsPerInstallation,
		MaxResults:      staleJobBatch,
	})
	if err != nil {
		return fmt.Errorf("list stale jobs: %w", err)
	}
	for _, row := range jobs {
		if err := s.refreshJob(ctx, row.Job); err != nil {
			s.log.WarnContext(ctx, "refresh job", slog.Int64("job", row.Job.ID), slog.Any("error", err))
		}
	}

	return nil
}

func (s *Service) refreshJob(ctx context.Context, job db.Job) error {
	// Touch first so a job GitHub keeps failing on does not starve the rest.
	if err := s.q.TouchJob(ctx, job.ID); err != nil {
		return fmt.Errorf("touch job: %w", err)
	}
	installation, err := s.q.GetInstallation(ctx, job.InstallationID)
	if err != nil {
		return fmt.Errorf("get installation: %w", err)
	}
	if installation.DeletedAt != nil {
		return nil
	}
	repo, err := github.ParseRepo(job.RepositoryFullName)
	if err != nil {
		return err
	}
	remote, err := s.github.GetJob(ctx, job.InstallationID, repo, job.ID)
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	status, ok := jobStatus(remote.Status)
	if !ok {
		return nil
	}
	if status == "queued" {
		live, err := s.q.JobHasLiveRunner(ctx, &job.ID)
		if err != nil {
			return fmt.Errorf("check live runner: %w", err)
		}
		if live {
			return nil
		}
	}

	// The job was already matched to a profile, so the repository passed
	// the visibility check then; reuse that verdict.
	return s.applyJob(ctx, installation, github.Repository{ID: job.RepositoryID, FullName: job.RepositoryFullName, Private: true}, remote, status)
}

func (s *Service) expireSessions(ctx context.Context) error {
	if _, err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}

	return nil
}
