// Package fleet is Airship's scheduler. It turns GitHub workflow_job events
// into runner demand, hands that demand to data plane agents, and keeps the
// runners table honest when webhooks, agents or instances go missing.
//
// The central rule, learned from every runner autoscaler: a runner is not
// bound to the job that caused it. GitHub gives any queued job to any idle
// runner with matching labels, so Airship creates one runner per queued job
// and learns which job a runner actually ran from the in_progress event.
//
// All state lives in Postgres. Every transition is a conditional UPDATE, so
// webhooks, agent syncs and the reconciler can race freely: the loser's
// update matches no rows.
package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner states, mirroring the runners.state check constraint.
const (
	StatePending  = "pending"
	StateAssigned = "assigned"
	StateRunning  = "running"
	StateBusy     = "busy"
	StateFinished = "finished"
)

// Finish reasons, mirroring the runners.finish_reason check constraint.
const (
	ReasonCompleted    = "completed"
	ReasonCancelled    = "cancelled"
	ReasonIdle         = "idle"
	ReasonLaunchFailed = "launch_failed"
	ReasonLeaseExpired = "lease_expired"
	ReasonLost         = "lost"
	ReasonMaxLifetime  = "max_lifetime"
)

// GitHub is what the scheduler needs from GitHub, per installation.
type GitHub interface {
	GenerateJITConfig(ctx context.Context, installationID int64, repo github.Repo, name string, labels []string) (github.JITConfig, error)
	GetRunner(ctx context.Context, installationID int64, repo github.Repo, runnerID int64) (github.Runner, error)
	FindRunnerByName(ctx context.Context, installationID int64, repo github.Repo, name string) (github.Runner, bool, error)
	DeleteRunner(ctx context.Context, installationID int64, repo github.Repo, runnerID int64) error
	GetJob(ctx context.Context, installationID int64, repo github.Repo, jobID int64) (github.WorkflowJob, error)
	GetInstallation(ctx context.Context, installationID int64) (github.Installation, error)
}

// AppGitHub adapts a *github.App to GitHub.
type AppGitHub struct {
	App *github.App
}

// GenerateJITConfig implements GitHub.
func (a AppGitHub) GenerateJITConfig(ctx context.Context, inst int64, repo github.Repo, name string, labels []string) (github.JITConfig, error) {
	return a.App.Installation(inst).GenerateJITConfig(ctx, repo, name, labels) //nolint:wrapcheck // already descriptive
}

// GetRunner implements GitHub.
func (a AppGitHub) GetRunner(ctx context.Context, inst int64, repo github.Repo, id int64) (github.Runner, error) {
	return a.App.Installation(inst).GetRunner(ctx, repo, id) //nolint:wrapcheck // already descriptive
}

// FindRunnerByName implements GitHub.
func (a AppGitHub) FindRunnerByName(ctx context.Context, inst int64, repo github.Repo, name string) (github.Runner, bool, error) {
	return a.App.Installation(inst).FindRunnerByName(ctx, repo, name) //nolint:wrapcheck // already descriptive
}

// DeleteRunner implements GitHub.
func (a AppGitHub) DeleteRunner(ctx context.Context, inst int64, repo github.Repo, id int64) error {
	return a.App.Installation(inst).DeleteRunner(ctx, repo, id) //nolint:wrapcheck // already descriptive
}

// GetJob implements GitHub.
func (a AppGitHub) GetJob(ctx context.Context, inst int64, repo github.Repo, id int64) (github.WorkflowJob, error) {
	return a.App.Installation(inst).GetJob(ctx, repo, id) //nolint:wrapcheck // already descriptive
}

// GetInstallation implements GitHub.
func (a AppGitHub) GetInstallation(ctx context.Context, inst int64) (github.Installation, error) {
	return a.App.GetInstallation(ctx, inst) //nolint:wrapcheck // already descriptive
}

// Config tunes the scheduler. The zero value is not usable; start from
// DefaultConfig.
type Config struct {
	// LeaseDuration is how long an agent has to report an instance for a
	// runner it was handed before the runner is presumed never launched.
	LeaseDuration time.Duration
	// InstanceGrace covers cloud APIs that list new instances late, and
	// instances that are slow to disappear from listings.
	InstanceGrace time.Duration
	// IdleTimeout is how long a launched runner may wait for a job.
	IdleTimeout time.Duration
	// BusyLostAfter is how long a busy runner whose instance vanished waits
	// for GitHub's completed event before it is written off.
	BusyLostAfter time.Duration
	// MaxLifetime caps a runner's life; instances also enforce it on
	// themselves so they die even when the control plane cannot reach them.
	MaxLifetime time.Duration
	// DeregisterAfter delays removing a finished runner from GitHub, so a
	// runner whose lease expired but did launch can still be recognized.
	DeregisterAfter time.Duration
	// StaleJobAfter is how long a queued job goes without news before the
	// reconciler asks GitHub about it directly.
	StaleJobAfter time.Duration
	// MaxAttempts caps failed launches for one job.
	MaxAttempts int64
	// RunnerVersion is the actions/runner release agents should run.
	RunnerVersion string
	// PollRecheck bounds how stale a long poll's view of the queue gets.
	PollRecheck time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		LeaseDuration:   5 * time.Minute,
		InstanceGrace:   2 * time.Minute,
		IdleTimeout:     10 * time.Minute,
		BusyLostAfter:   10 * time.Minute,
		MaxLifetime:     8 * time.Hour,
		DeregisterAfter: 2 * time.Minute,
		StaleJobAfter:   10 * time.Minute,
		MaxAttempts:     3,
		RunnerVersion:   "2.338.2",
		PollRecheck:     2 * time.Second,
	}
}

// Service is the scheduler.
type Service struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	github   GitHub
	notifier *Notifier
	cfg      Config
	log      *slog.Logger
}

// New returns a Service.
func New(pool *pgxpool.Pool, gh GitHub, cfg Config, logger *slog.Logger) *Service {
	return &Service{
		pool:     pool,
		q:        db.New(pool),
		github:   gh,
		notifier: NewNotifier(),
		cfg:      cfg,
		log:      logger,
	}
}

// Config returns the scheduler's settings.
func (s *Service) Config() Config {
	return s.cfg
}

// txAttempts bounds retries of transactions that lost a deadlock.
const txAttempts = 3

// inTx runs fn in a transaction. Webhooks for two jobs that swapped runners
// can lock the same rows in opposite order; Postgres aborts one of them,
// and it is retried here.
func (s *Service) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	var err error
	for range txAttempts {
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return fn(s.q.WithTx(tx))
		})
		if !isRetryableTx(err) {
			break
		}
	}

	return err //nolint:wrapcheck // fn's errors are already wrapped
}

// newRunnerName returns a GitHub runner name that is unique across Airship.
func newRunnerName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])

	return "airship-" + hex.EncodeToString(b[:])
}

// createRunner queues a runner for a job, unless one is already live for it
// or the job has used up its attempts. It returns whether a runner was added.
func (s *Service) createRunner(ctx context.Context, q *db.Queries, job db.Job, profile db.Profile) (bool, error) {
	failed, err := q.CountFailedRunnersForJob(ctx, &job.ID)
	if err != nil {
		return false, fmt.Errorf("count attempts: %w", err)
	}
	if failed >= s.cfg.MaxAttempts {
		return false, nil
	}

	_, err = q.CreateRunner(ctx, db.CreateRunnerParams{
		InstallationID:     job.InstallationID,
		ProfileID:          &profile.ID,
		ProfileLabel:       profile.Label,
		DataPlaneID:        profile.DataPlaneID,
		RepositoryID:       job.RepositoryID,
		RepositoryFullName: job.RepositoryFullName,
		TriggerJobID:       &job.ID,
		Name:               newRunnerName(),
	})
	if isNoRows(err) || isForeignKeyViolation(err) {
		// A live runner already serves the job, or the profile was just
		// deleted.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create runner: %w", err)
	}

	return true, nil
}

// retryJob queues a fresh runner for the job a failed runner was serving, if
// that job is still waiting.
func (s *Service) retryJob(ctx context.Context, failed db.Runner) error {
	if failed.TriggerJobID == nil || failed.ProfileID == nil {
		return nil
	}
	job, err := s.q.GetJob(ctx, *failed.TriggerJobID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	if job.Status != "queued" {
		return nil
	}
	profile, err := s.q.GetProfile(ctx, db.GetProfileParams{InstallationID: failed.InstallationID, ID: *failed.ProfileID})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get profile: %w", err)
	}
	added, err := s.createRunner(ctx, s.q, job, profile)
	if err != nil {
		return err
	}
	if added {
		s.notifier.Notify(profile.DataPlaneID)
	}

	return nil
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}

// ErrProfileNotFound is returned for a profile outside the installation.
var ErrProfileNotFound = errors.New("profile not found")

// ProfileBusyError is returned when a profile still has active runners.
type ProfileBusyError struct {
	Active int64
}

func (e *ProfileBusyError) Error() string {
	return fmt.Sprintf("%d runners of this profile are still active", e.Active)
}

// DeleteProfile deletes a profile that has no active runners. The profile
// row stays locked between the check and the delete, so a webhook cannot
// queue a runner for it in between. Finished runners keep their history
// (profile_id becomes null) and are still removed from GitHub.
func (s *Service) DeleteProfile(ctx context.Context, installationID int64, id uuid.UUID) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockProfile(ctx, db.LockProfileParams{InstallationID: installationID, ID: id}); err != nil {
			if isNoRows(err) {
				return ErrProfileNotFound
			}

			return fmt.Errorf("lock profile: %w", err)
		}
		active, err := q.CountActiveRunnersForProfile(ctx, &id)
		if err != nil {
			return fmt.Errorf("count runners: %w", err)
		}
		if active > 0 {
			return &ProfileBusyError{Active: active}
		}
		if _, err := q.DeleteProfile(ctx, db.DeleteProfileParams{InstallationID: installationID, ID: id}); err != nil {
			return fmt.Errorf("delete profile: %w", err)
		}

		return nil
	})
}
