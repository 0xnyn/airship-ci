// Package fleet is the scheduler. It turns GitHub workflow_job events into
// runner demand, launches runner machines in the customer's cloud with the
// credentials they connected, and keeps the runners table honest when
// webhooks, launches or instances go missing.
//
// The central rule, learned from every runner autoscaler: a runner is not
// bound to the job that caused it. GitHub gives any queued job to any idle
// runner with matching labels, so one runner is created per queued job and
// which job a runner actually ran is learned from the in_progress event.
//
// All state lives in Postgres. Every transition is a conditional UPDATE, so
// webhooks, launchers and the reconciler can race freely: the loser's
// update matches no rows.
package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/secrets"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runner states, mirroring the runners.state check constraint.
const (
	StatePending   = "pending"
	StateLaunching = "launching"
	StateRunning   = "running"
	StateBusy      = "busy"
	StateFinished  = "finished"
)

// Registration scopes, mirroring runners.scope.
const (
	ScopeRepository   = "repository"
	ScopeOrganization = "organization"
)

// Finish reasons, mirroring the runners.finish_reason check constraint.
const (
	ReasonCompleted    = "completed"
	ReasonCancelled    = "cancelled"
	ReasonIdle         = "idle"
	ReasonLaunchFailed = "launch_failed"
	// ReasonLaunchUnconfirmed: the cloud's answer to the launch was lost
	// and the machine may exist. The inventory scan revives the runner if
	// it lists the instance and marks it gone otherwise.
	ReasonLaunchUnconfirmed = "launch_unconfirmed"
	ReasonLeaseExpired      = "lease_expired"
	ReasonLost              = "lost"
	ReasonMaxLifetime       = "max_lifetime"
	ReasonDeleted           = "deleted"
)

// GitHub is what the scheduler needs from GitHub, per installation.
type GitHub interface {
	GenerateJITConfig(ctx context.Context, installationID int64, repo github.Repo, name string, labels []string) (github.JITConfig, error)
	GetRunner(ctx context.Context, installationID int64, repo github.Repo, runnerID int64) (github.Runner, error)
	FindRunnerByName(ctx context.Context, installationID int64, repo github.Repo, name string) (github.Runner, bool, error)
	DeleteRunner(ctx context.Context, installationID int64, repo github.Repo, runnerID int64) error
	GetJob(ctx context.Context, installationID int64, repo github.Repo, jobID int64) (github.WorkflowJob, error)
	GetInstallation(ctx context.Context, installationID int64) (github.Installation, error)
	GetFile(ctx context.Context, installationID int64, repo github.Repo, path, ref string, limit int) (github.File, bool, error)
	GetRunAttempt(ctx context.Context, installationID int64, repo github.Repo, runID int64, attempt int32) (github.RunAttempt, error)
	TeamMember(ctx context.Context, installationID int64, org, team, user string) (bool, error)
	RepoRoles(ctx context.Context, installationID int64, repo github.Repo, user string) ([]string, error)
	ListRepositories(ctx context.Context, installationID int64) ([]github.Repository, error)
	GetRepositoryByID(ctx context.Context, installationID int64, id int64) (github.Repository, error)
	// Organization runners, for idle pools from airship.global.yaml.
	GenerateOrgJITConfig(ctx context.Context, installationID int64, org, name string, groupID int64, labels []string) (github.JITConfig, error)
	GetOrgRunner(ctx context.Context, installationID int64, org string, runnerID int64) (github.Runner, error)
	FindOrgRunnerByName(ctx context.Context, installationID int64, org, name string) (github.Runner, bool, error)
	DeleteOrgRunner(ctx context.Context, installationID int64, org string, runnerID int64) error
	EnsureRunnerGroup(ctx context.Context, installationID int64, org, name string, repoIDs []int64) (int64, error)
}

// AppGitHub adapts a *github.App to GitHub.
type AppGitHub struct {
	App *github.App
}

//nolint:wrapcheck // the github package's errors are already descriptive
func (a AppGitHub) GenerateJITConfig(ctx context.Context, inst int64, repo github.Repo, name string, labels []string) (github.JITConfig, error) {
	return a.App.Installation(inst).GenerateJITConfig(ctx, repo, name, labels)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetRunner(ctx context.Context, inst int64, repo github.Repo, id int64) (github.Runner, error) {
	return a.App.Installation(inst).GetRunner(ctx, repo, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) FindRunnerByName(ctx context.Context, inst int64, repo github.Repo, name string) (github.Runner, bool, error) {
	return a.App.Installation(inst).FindRunnerByName(ctx, repo, name)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) DeleteRunner(ctx context.Context, inst int64, repo github.Repo, id int64) error {
	return a.App.Installation(inst).DeleteRunner(ctx, repo, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetJob(ctx context.Context, inst int64, repo github.Repo, id int64) (github.WorkflowJob, error) {
	return a.App.Installation(inst).GetJob(ctx, repo, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetInstallation(ctx context.Context, inst int64) (github.Installation, error) {
	return a.App.GetInstallation(ctx, inst)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetFile(ctx context.Context, inst int64, repo github.Repo, path, ref string, limit int) (github.File, bool, error) {
	return a.App.Installation(inst).GetFile(ctx, repo, path, ref, limit)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetRunAttempt(ctx context.Context, inst int64, repo github.Repo, runID int64, attempt int32) (github.RunAttempt, error) {
	return a.App.Installation(inst).GetRunAttempt(ctx, repo, runID, attempt)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) TeamMember(ctx context.Context, inst int64, org, team, user string) (bool, error) {
	return a.App.Installation(inst).TeamMember(ctx, org, team, user)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) RepoRoles(ctx context.Context, inst int64, repo github.Repo, user string) ([]string, error) {
	return a.App.Installation(inst).RepoRoles(ctx, repo, user)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) ListRepositories(ctx context.Context, inst int64) ([]github.Repository, error) {
	return a.App.Installation(inst).ListRepositories(ctx)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetRepositoryByID(ctx context.Context, inst int64, id int64) (github.Repository, error) {
	return a.App.Installation(inst).GetRepositoryByID(ctx, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GenerateOrgJITConfig(ctx context.Context, inst int64, org, name string, groupID int64, labels []string) (github.JITConfig, error) {
	return a.App.Installation(inst).GenerateOrgJITConfig(ctx, org, name, groupID, labels)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetOrgRunner(ctx context.Context, inst int64, org string, id int64) (github.Runner, error) {
	return a.App.Installation(inst).GetOrgRunner(ctx, org, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) FindOrgRunnerByName(ctx context.Context, inst int64, org, name string) (github.Runner, bool, error) {
	return a.App.Installation(inst).FindOrgRunnerByName(ctx, org, name)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) DeleteOrgRunner(ctx context.Context, inst int64, org string, id int64) error {
	return a.App.Installation(inst).DeleteOrgRunner(ctx, org, id)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) EnsureRunnerGroup(ctx context.Context, inst int64, org, name string, repoIDs []int64) (int64, error) {
	return a.App.Installation(inst).EnsureRunnerGroup(ctx, org, name, repoIDs)
}

// Config tunes the scheduler. The zero value is not usable; start from
// DefaultConfig.
type Config struct {
	// LeaseDuration is how long a launcher has to finish a launch it
	// claimed before the runner is presumed never launched.
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
	// TerminateRetryAfter is how long after asking the cloud to terminate
	// an instance that is still listed the request is repeated.
	TerminateRetryAfter time.Duration
	// StaleJobAfter is how long a queued job goes without news before the
	// reconciler asks GitHub about it directly.
	StaleJobAfter time.Duration
	// MaxAttempts caps failed launches for one job.
	MaxAttempts int64
	// RunnerVersion is the actions/runner release machines install.
	RunnerVersion string
	// LaunchConcurrency bounds parallel cloud and GitHub calls per launch
	// pass. GitHub's secondary rate limits punish bursts of POSTs.
	LaunchConcurrency int
	// LaunchBatch bounds runners claimed per pass.
	LaunchBatch int32
	// EventRetention is how long dashboard events are kept.
	EventRetention time.Duration
	// PublicURL is where runner machines reach the control plane, for the
	// cache proxy's credentials and binary.
	PublicURL string
	// CacheRetentionDays is the lifecycle rule on cache buckets.
	CacheRetentionDays int
	// CacheGrantTTL is how long a runner's cache credentials last; the
	// proxy refreshes them before they expire.
	CacheGrantTTL time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		LeaseDuration:       5 * time.Minute,
		InstanceGrace:       2 * time.Minute,
		IdleTimeout:         10 * time.Minute,
		BusyLostAfter:       10 * time.Minute,
		MaxLifetime:         8 * time.Hour,
		DeregisterAfter:     2 * time.Minute,
		TerminateRetryAfter: 5 * time.Minute,
		StaleJobAfter:       10 * time.Minute,
		MaxAttempts:         3,
		RunnerVersion:       "2.338.2",
		LaunchConcurrency:   8,
		LaunchBatch:         50,
		EventRetention:      30 * 24 * time.Hour,
		CacheRetentionDays:  7,
		CacheGrantTTL:       time.Hour,
	}
}

// Service is the scheduler.
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	github  GitHub
	clouds  cloud.Registry
	secrets *secrets.Keyring
	cfg     Config
	log     *slog.Logger
	configs *configCache
	// wake nudges the launcher when new runners are pending.
	wake chan struct{}
}

// New returns a Service.
func New(pool *pgxpool.Pool, gh GitHub, clouds cloud.Registry, keyring *secrets.Keyring, cfg Config, logger *slog.Logger) *Service {
	return &Service{
		pool:    pool,
		q:       db.New(pool),
		github:  gh,
		clouds:  clouds,
		secrets: keyring,
		cfg:     cfg,
		log:     logger,
		configs: newConfigCache(),
		wake:    make(chan struct{}, 1),
	}
}

// Config returns the scheduler's settings.
func (s *Service) Config() Config {
	return s.cfg
}

// wakeLauncher asks the launcher to look for pending runners now rather
// than on its next tick. It is an in-process hint: other replicas see the
// work on their own tick.
func (s *Service) wakeLauncher() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
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

// createRunner queues a runner for a job from its plan, unless one is
// already live for it or the job has had maxAttempts failed runners. Each
// failure moves on to the plan's next attempt. It returns the runner added.
func (s *Service) createRunner(ctx context.Context, q *db.Queries, job db.Job, plan Plan, maxAttempts int64) (*db.Runner, error) {
	failed, err := q.CountFailedRunnersForJob(ctx, &job.ID)
	if err != nil {
		return nil, fmt.Errorf("count attempts: %w", err)
	}
	if failed >= maxAttempts {
		return nil, nil
	}
	attempt := plan.at(failed)
	spec, err := json.Marshal(attempt.Spec)
	if err != nil {
		return nil, fmt.Errorf("encode spec: %w", err)
	}

	runner, err := q.CreateRunner(ctx, db.CreateRunnerParams{
		InstallationID:     job.InstallationID,
		Definition:         attempt.Name,
		Source:             attempt.Source,
		Scope:              ScopeRepository,
		Cloud:              attempt.Cloud,
		CredentialID:       uuidPtr(attempt.CredentialID),
		Region:             attempt.Region,
		Spec:               spec,
		Labels:             attempt.Labels,
		Attempt:            int32(failed), //nolint:gosec // bounded by maxAttempts
		RepositoryID:       job.RepositoryID,
		RepositoryFullName: job.RepositoryFullName,
		TriggerJobID:       &job.ID,
		Name:               newRunnerName(),
	})
	if isNoRows(err) || isForeignKeyViolation(err) {
		// A live runner already serves the job, or its credential was
		// just deleted.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	return &runner, nil
}

// retryJob queues a fresh runner for the job a failed runner was serving, if
// that job is still waiting. A failure that would recur on retry only moves
// on to the plan's other attempts.
func (s *Service) retryJob(ctx context.Context, failed db.Runner, retryable bool) error {
	if failed.TriggerJobID == nil {
		return nil // an idle pool runner; its pool replaces it
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
	plan, err := decodePlan(job.Plan)
	if err != nil || len(plan) == 0 {
		return err
	}
	limit := plan.maxAttempts(s.cfg.MaxAttempts)
	if !retryable {
		limit = int64(len(plan))
	}
	var added bool
	var fromPool bool
	err = s.inTx(ctx, func(q *db.Queries) error {
		var err error
		added, fromPool, err = s.serveJob(ctx, q, job, plan, limit)

		return err
	})
	if err != nil {
		return err
	}
	if added {
		s.wakeLauncher()
	}
	if fromPool {
		s.fillRepositoryPools(ctx, job.InstallationID, job.RepositoryID)
	}
	if !added && !fromPool {
		s.record(ctx, job.InstallationID, "job.failed", levelError,
			fmt.Sprintf("%s could not be started after %d failed launches; last error: %s", jobName(job), limit, deref(failed.Error)),
			eventFields{repository: job.RepositoryFullName, jobID: &job.ID, runnerID: &failed.ID})
	}

	return nil
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}

	return &id
}

func jobName(job db.Job) string {
	name := job.Name
	if job.WorkflowName != nil {
		name = *job.WorkflowName + " / " + name
	}

	return name
}
