package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/google/uuid"
)

// maxSkipDetail is the jobs.skip_detail column's limit.
const maxSkipDetail = 2000

// jobStatus maps a workflow_job action or REST status to the job states
// Airship tracks. ok is false for states Airship ignores, such as "waiting"
// (pending deployment approval): GitHub sends "queued" once it is approved.
func jobStatus(status string) (string, bool) {
	switch status {
	case "queued", "in_progress", "completed":
		return status, true
	default:
		return "", false
	}
}

// HandleWorkflowJob folds one workflow_job webhook into Airship's state.
// It is idempotent and tolerates events arriving in any order.
func (s *Service) HandleWorkflowJob(ctx context.Context, event github.WorkflowJobEvent) error {
	if event.Installation == nil {
		return nil
	}
	status, ok := jobStatus(event.Action)
	if !ok {
		return nil
	}
	installation, err := s.ensureInstallation(ctx, event.Installation.ID)
	if err != nil {
		return err
	}
	if installation.DeletedAt != nil {
		return nil
	}

	return s.applyJob(ctx, installation, event.Repository, event.WorkflowJob, status)
}

// applyJob records a job observation and reacts to it. A job is planned
// once, when first seen queued: later events and refreshes reuse the plan,
// so a config change never changes how a job already waiting is served.
func (s *Service) applyJob(ctx context.Context, installation db.Installation, repo github.Repository, job github.WorkflowJob, status string) error {
	var plan Plan
	var why verdict
	existing, err := s.q.GetJob(ctx, job.ID)
	switch {
	case err == nil && existing.Plan != nil:
		if plan, err = decodePlan(existing.Plan); err != nil {
			return err
		}
	case err != nil && !isNoRows(err):
		return fmt.Errorf("get job: %w", err)
	case status == "queued":
		if plan, why, err = s.planJob(ctx, installation, repo, job); err != nil {
			return err
		}
	}
	rawPlan, err := encodePlan(plan)
	if err != nil {
		return err
	}

	var wake, refill bool
	var finished *uuid.UUID
	err = s.inTx(ctx, func(q *db.Queries) error {
		wake, refill, finished = false, false, nil
		row, err := q.UpsertJob(ctx, db.UpsertJobParams{
			ID:                 job.ID,
			InstallationID:     installation.ID,
			RepositoryID:       repo.ID,
			RepositoryFullName: repo.FullName,
			RunID:              job.RunID,
			RunAttempt:         max(job.RunAttempt, 1),
			WorkflowName:       job.WorkflowName,
			Name:               job.Name,
			HeadBranch:         job.HeadBranch,
			HeadSha:            nilIfEmpty(job.HeadSHA),
			HtmlUrl:            job.HTMLURL,
			Labels:             job.Labels,
			Status:             status,
			Conclusion:         job.Conclusion,
			Plan:               rawPlan,
			SkipReason:         nilIfEmpty(why.reason),
			SkipDetail:         nilIfEmpty(truncate(why.detail, maxSkipDetail)),
			RunnerName:         job.RunnerName,
			QueuedAt:           job.CreatedAt,
			StartedAt:          job.StartedAt,
			CompletedAt:        job.CompletedAt,
		})
		if err != nil {
			return fmt.Errorf("upsert job: %w", err)
		}

		// React to the folded row, not the event: a late "queued" for a job
		// that already started must not create a runner.
		if row.Status == "queued" {
			stored, err := decodePlan(row.Plan)
			if err != nil || len(stored) == 0 {
				return err
			}
			added, fromPool, err := s.serveJob(ctx, q, row, stored, s.cfg.MaxAttempts)
			wake, refill = added, fromPool

			return err
		}

		runner, ours, err := s.lookupRunner(ctx, q, row)
		if err != nil {
			return err
		}
		if ours {
			if err := s.handOff(ctx, q, row, runner); err != nil {
				return err
			}
		}
		// The job has a runner now (ours or not). Pool runners waiting for it
		// go back to their pool, and a runner still queued for it is surplus.
		// One already launched may pick up another job, and is reaped by the
		// idle timeout if it does not.
		if _, err := q.ReturnPoolRunners(ctx, db.ReturnPoolRunnersParams{JobID: &row.ID, ExceptID: runner.ID}); err != nil {
			return fmt.Errorf("return pool runners: %w", err)
		}
		if _, err := q.CancelPendingRunnersForJob(ctx, &row.ID); err != nil {
			return fmt.Errorf("cancel pending runners: %w", err)
		}
		if !ours {
			return nil
		}

		var done bool
		if refill, done, err = s.bindRunner(ctx, q, row, runner); err != nil {
			return err
		}
		if done {
			finished = &runner.ID
		}

		return nil
	})
	if err != nil {
		return err
	}
	if wake {
		s.wakeLauncher()
	}
	if finished != nil {
		// Destroy the machine now rather than on the next reconcile pass.
		s.terminateSoon(ctx, *finished)
	}
	if refill {
		// A pool runner was taken, or a usage-based pool just woke up.
		s.fillRepositoryPools(ctx, installation.ID, repo.ID)
	}
	if why.reason != "" && (existing.SkipReason == nil || *existing.SkipReason != why.reason) {
		s.record(ctx, installation.ID, "job.skipped", levelWarning,
			fmt.Sprintf("%s in %s was not started (%s): %s", jobName(dbJobFrom(job)), repo.FullName, why.reason, cmpOr(why.detail, "see the job")),
			eventFields{repository: repo.FullName, jobID: &job.ID})
	}

	return nil
}

func dbJobFrom(job github.WorkflowJob) db.Job {
	return db.Job{Name: job.Name, WorkflowName: job.WorkflowName}
}

// serveJob gives a queued job a runner: an idle one from a warm pool if
// one fits, otherwise a new one from the plan. It reports whether a new
// runner was queued, and whether the job took a pool runner.
func (s *Service) serveJob(ctx context.Context, q *db.Queries, job db.Job, plan Plan, maxAttempts int64) (added bool, fromPool bool, err error) {
	if err := q.LockJob(ctx, job.ID); err != nil {
		return false, false, fmt.Errorf("lock job: %w", err)
	}
	// Creating a runner references a credential; the installation lock is
	// what disconnecting a cloud takes while it checks nothing does.
	if _, err := q.LockInstallation(ctx, job.InstallationID); err != nil {
		return false, false, fmt.Errorf("lock installation: %w", err)
	}
	reserved, err := s.reservePoolRunner(ctx, q, job, plan)
	if err != nil || reserved {
		return false, reserved, err
	}
	runner, err := s.createRunner(ctx, q, job, plan, maxAttempts)

	return runner != nil, false, err
}

// lookupRunner finds the Airship runner GitHub says a job ran on.
func (s *Service) lookupRunner(ctx context.Context, q *db.Queries, job db.Job) (db.Runner, bool, error) {
	if job.RunnerName == nil {
		return db.Runner{}, false, nil
	}
	runner, err := q.GetRunnerByName(ctx, db.GetRunnerByNameParams{InstallationID: job.InstallationID, Name: *job.RunnerName})
	if isNoRows(err) {
		return db.Runner{}, false, nil // a runner that is not Airship's
	}
	if err != nil {
		return db.Runner{}, false, fmt.Errorf("get runner: %w", err)
	}

	return runner, true, nil
}

// handOff handles GitHub giving job a runner that was created for another
// job. That other job just lost its runner, while job's own runner is now
// spare, so the spare is handed over instead of being cancelled. Without
// this, the other job would wait for the stale-job reconciler.
func (s *Service) handOff(ctx context.Context, q *db.Queries, job db.Job, runner db.Runner) error {
	if runner.TriggerJobID == nil || *runner.TriggerJobID == job.ID {
		return nil
	}
	robbed, err := q.GetJob(ctx, *runner.TriggerJobID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get robbed job: %w", err)
	}
	if robbed.Status != "queued" {
		return nil
	}
	// Free the robbed job's slot first: at most one live runner per job.
	if err := q.ClearRunnerTrigger(ctx, runner.ID); err != nil {
		return fmt.Errorf("clear runner trigger: %w", err)
	}
	// The robbed job may already have a replacement (the stolen runner's
	// lease expired or its launch went unconfirmed, and a retry was
	// queued); then job's spare is simply surplus and idles out.
	live, err := q.JobHasLiveRunner(ctx, &robbed.ID)
	if err != nil {
		return fmt.Errorf("check robbed job: %w", err)
	}
	if live {
		return nil
	}
	if _, err := q.MoveLiveRunners(ctx, db.MoveLiveRunnersParams{FromJobID: &job.ID, ToJobID: &robbed.ID}); err != nil {
		return fmt.Errorf("hand off runner: %w", err)
	}

	return nil
}

// bindRunner links a started or finished job to the runner it ran on and
// advances that runner. extended reports that a usage-based pool for the
// runner's definition was extended and may need filling; finished that
// the runner just finished and its machine should go.
func (s *Service) bindRunner(ctx context.Context, q *db.Queries, job db.Job, runner db.Runner) (extended bool, finished bool, err error) {
	if err := q.SetJobRunner(ctx, db.SetJobRunnerParams{ID: job.ID, RunnerID: &runner.ID}); err != nil {
		return false, false, fmt.Errorf("link job to runner: %w", err)
	}

	if job.Status == "in_progress" {
		busy, err := q.MarkRunnerBusy(ctx, runner.ID)
		if isNoRows(err) {
			return false, false, nil
		}
		if err != nil {
			return false, false, fmt.Errorf("mark runner busy: %w", err)
		}
		extended, err = s.extendUsagePools(ctx, q, busy)

		return extended, false, err
	}

	// Completed. GitHub removes an ephemeral runner after its job, so it is
	// already deregistered.
	_, err = q.FinishRunner(ctx, db.FinishRunnerParams{
		ID:           runner.ID,
		Reason:       ReasonCompleted,
		Deregistered: true,
		FromStates:   []string{StateLaunching, StateRunning, StateBusy},
	})
	if isNoRows(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("finish runner: %w", err)
	}

	return false, true, nil
}

// ensureInstallation returns the installation, fetching it from GitHub when
// its installation.created webhook was missed.
func (s *Service) ensureInstallation(ctx context.Context, id int64) (db.Installation, error) {
	installation, err := s.q.GetInstallation(ctx, id)
	if err == nil {
		return installation, nil
	}
	if !isNoRows(err) {
		return db.Installation{}, fmt.Errorf("get installation: %w", err)
	}
	remote, err := s.github.GetInstallation(ctx, id)
	if err != nil {
		return db.Installation{}, fmt.Errorf("fetch installation %d: %w", id, err)
	}

	return s.SaveInstallation(ctx, remote)
}

// SaveInstallation records an installation GitHub told us about.
func (s *Service) SaveInstallation(ctx context.Context, inst github.Installation) (db.Installation, error) {
	accountType := inst.Account.Type
	if accountType != "User" {
		accountType = "Organization"
	}
	row, err := s.q.UpsertInstallation(ctx, db.UpsertInstallationParams{
		ID:           inst.ID,
		AccountID:    inst.Account.ID,
		AccountLogin: inst.Account.Login,
		AccountType:  accountType,
		SuspendedAt:  inst.SuspendedAt,
	})
	if err != nil {
		return db.Installation{}, fmt.Errorf("save installation: %w", err)
	}

	return row, nil
}

// HandleInstallation reacts to the app being installed, changed or removed.
func (s *Service) HandleInstallation(ctx context.Context, event github.InstallationEvent) error {
	switch event.Action {
	case "deleted":
		// Keep the rows: every runner is finished, and the reconciler
		// terminates their instances with the credentials still stored.
		id := event.Installation.ID
		err := s.inTx(ctx, func(q *db.Queries) error {
			if err := q.MarkInstallationDeleted(ctx, id); err != nil {
				return fmt.Errorf("mark installation deleted: %w", err)
			}
			if err := q.DeleteInstallationMemberships(ctx, id); err != nil {
				return fmt.Errorf("delete memberships: %w", err)
			}
			if _, err := q.FinishInstallationRunners(ctx, id); err != nil {
				return fmt.Errorf("finish runners: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}
		s.log.InfoContext(ctx, "installation removed", slog.Int64("installation", event.Installation.ID))

		return nil
	case "suspend":
		now := time.Now()

		return s.wrap("suspend installation", s.q.SetInstallationSuspended(ctx, db.SetInstallationSuspendedParams{ID: event.Installation.ID, SuspendedAt: &now}))
	case "unsuspend":
		if err := s.q.SetInstallationSuspended(ctx, db.SetInstallationSuspendedParams{ID: event.Installation.ID}); err != nil {
			return fmt.Errorf("unsuspend installation: %w", err)
		}
		s.retrySkippedJobs(ctx, event.Installation.ID, nil, SkipInstallationSuspended)

		return nil
	default:
		_, err := s.SaveInstallation(ctx, event.Installation)

		return err
	}
}

// HandleInstallationRepositories deactivates repositories the app lost
// access to: nothing can be read or registered there any more, and a pool
// would keep failing to launch for it.
func (s *Service) HandleInstallationRepositories(ctx context.Context, event github.InstallationRepositoriesEvent) error {
	if event.Action != "removed" {
		return nil
	}
	for _, repo := range event.RepositoriesRemoved {
		err := s.DeactivateRepository(ctx, event.Installation.ID, repo.ID)
		if err != nil && !errors.Is(err, ErrRepositoryNotFound) {
			return err
		}
	}

	return nil
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func (s *Service) wrap(what string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	return nil
}
