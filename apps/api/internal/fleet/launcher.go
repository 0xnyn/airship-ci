package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/google/uuid"
)

// RunLauncher launches pending runners until ctx is done: on every tick,
// and sooner when a webhook creates demand on this replica.
func (s *Service) RunLauncher(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := s.LaunchOnce(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "launch", slog.Any("error", err))
		}
		if n > 0 {
			continue // there may be more than one batch
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

// LaunchOnce claims one batch of pending runners and launches them. It
// returns how many it claimed.
func (s *Service) LaunchOnce(ctx context.Context) (int, error) {
	installations, err := s.q.ListInstallationsWithPendingRunners(ctx)
	if err != nil {
		return 0, fmt.Errorf("list pending installations: %w", err)
	}
	var batch []db.Runner
	for _, installationID := range installations {
		claimed, err := s.claim(ctx, installationID, s.cfg.LaunchBatch-int32(len(batch))) //nolint:gosec // bounded by LaunchBatch
		if err != nil {
			return 0, err
		}
		batch = append(batch, claimed...)
		if int32(len(batch)) >= s.cfg.LaunchBatch { //nolint:gosec // bounded by LaunchBatch
			break
		}
	}
	if len(batch) == 0 {
		return 0, nil
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, max(s.cfg.LaunchConcurrency, 1))
	for _, runner := range batch {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			s.launch(ctx, runner)
		})
	}
	wg.Wait()

	return len(batch), nil
}

// claim moves up to capacity pending runners of one installation to
// launching, within its max_runners. The installation row is locked while
// counting, so launchers on several replicas cannot both take the last slot.
func (s *Service) claim(ctx context.Context, installationID int64, capacity int32) ([]db.Runner, error) {
	if capacity <= 0 {
		return nil, nil
	}
	var batch []db.Runner
	err := s.inTx(ctx, func(q *db.Queries) error {
		batch = nil
		maxRunners, err := q.LockInstallation(ctx, installationID)
		if err != nil {
			return fmt.Errorf("lock installation: %w", err)
		}
		active, err := q.CountActiveRunners(ctx, installationID)
		if err != nil {
			return fmt.Errorf("count active runners: %w", err)
		}
		left := min(capacity, maxRunners-active)
		if left <= 0 {
			return nil
		}
		batch, err = q.ClaimRunners(ctx, db.ClaimRunnersParams{
			InstallationID: installationID, MaxResults: left, LeaseSeconds: s.cfg.LeaseDuration.Seconds(),
		})
		if err != nil {
			return fmt.Errorf("claim runners: %w", err)
		}

		return nil
	})

	return batch, err
}

// launchLeaseMargin is how long before its lease expires a launch must have
// its answer, leaving time to record the instance.
const launchLeaseMargin = 30 * time.Second

// launch registers one claimed runner with GitHub and starts its machine.
// On failure the runner is put back in the queue (a transient GitHub
// error), or finished and its job retried on the plan's next attempt.
func (s *Service) launch(ctx context.Context, runner db.Runner) {
	log := s.log.With(slog.String("runner", runner.ID.String()), slog.String("repository", runner.RepositoryFullName))

	reg, err := s.registrationFor(ctx, runner, true)
	if err != nil {
		s.failLaunch(ctx, runner, err.Error(), false)

		return
	}
	spec, err := decodeSpec(runner.Spec)
	if err != nil {
		s.failLaunch(ctx, runner, err.Error(), false)

		return
	}
	provider, secret, err := s.credentialFor(ctx, runner)
	if err != nil {
		s.failLaunch(ctx, runner, err.Error(), false)

		return
	}

	jit, err := s.generateJIT(ctx, reg)
	if err != nil {
		if isPermanent(err) {
			log.WarnContext(ctx, "github refused to register runner", slog.Any("error", err))
			s.failLaunch(ctx, runner, "GitHub refused to register the runner: "+err.Error(), false)

			return
		}
		backoff := backoffFor(err)
		log.WarnContext(ctx, "could not register runner, will retry", slog.Any("error", err), slog.Duration("retry_in", backoff))
		if err := s.q.ReleaseRunner(context.WithoutCancel(ctx), db.ReleaseRunnerParams{ID: runner.ID, RetryAfterSeconds: backoff.Seconds()}); err != nil {
			log.ErrorContext(ctx, "release runner", slog.Any("error", err))
		}

		return
	}
	if err := s.q.SetRunnerGitHubID(ctx, db.SetRunnerGitHubIDParams{ID: runner.ID, GithubRunnerID: &jit.Runner.ID}); err != nil {
		// Without the id the runner could not be cleaned up from GitHub, so do
		// not launch it. The lease expires and the job is retried.
		log.ErrorContext(ctx, "save github runner id", slog.Any("error", err))

		return
	}

	// The registration took a round trip, during which the job may have
	// been cancelled or started on another runner. A machine for it would
	// only idle, so none is made. The trigger is re-read: a hand-off may
	// have moved this runner to another job meanwhile.
	movedOn, err := s.jobMovedOn(ctx, runner.ID)
	if err != nil {
		log.WarnContext(ctx, "check job before launch", slog.Any("error", err)) // launch anyway
	} else if movedOn {
		s.cancelLaunch(ctx, runner, reg, jit.Runner.ID)

		return
	}

	boot := cloud.Boot{
		JITConfig:          jit.EncodedConfig,
		RunnerVersion:      s.cfg.RunnerVersion,
		Env:                spec.Env,
		MaxLifetimeSeconds: int(s.cfg.MaxLifetime.Seconds()),
		GPU:                spec.GPU,
		PullFromAPI:        spec.ProvisionVia != "",
		Windows:            spec.Windows,
	}
	if spec.Cache {
		cacheBoot, err := s.prepareCache(ctx, runner, provider, secret)
		if err != nil {
			// The cache is an accelerator; a job is worth running without it.
			log.WarnContext(ctx, "cache unavailable for this runner", slog.Any("error", err))
			s.record(ctx, runner.InstallationID, "cache.unavailable", levelWarning,
				fmt.Sprintf("%s launches without cache acceleration: %s", runner.Name, err.Error()),
				eventFields{repository: runner.RepositoryFullName, runnerID: &runner.ID})
		} else {
			boot.Cache = cacheBoot
		}
	}
	if boot.PullFromAPI {
		// The image fetches its script from the developer API; keep what
		// it needs until then.
		if err := s.storeProvisionSecret(ctx, runner, boot, spec.Arch); err != nil {
			log.ErrorContext(ctx, "store provision secret", slog.Any("error", err))

			return
		}
	}
	// The cloud call must answer before the lease runs out, or the
	// reconciler gives the runner up as lease_expired while the launch is
	// still in flight (and a machine it then makes is revived by the scan,
	// after a retry was already queued). Cutting the call short is safe:
	// the providers settle an interrupted insert by looking the machine up.
	launchCtx := ctx
	if runner.LeaseExpiresAt != nil {
		var cancel context.CancelFunc
		launchCtx, cancel = context.WithDeadline(ctx, runner.LeaseExpiresAt.Add(-launchLeaseMargin))
		defer cancel()
	}
	instanceID, err := provider.Launch(launchCtx, secret, cloud.Launch{
		RunnerID:       runner.ID.String(),
		RunnerName:     runner.Name,
		InstallationID: runner.InstallationID,
		Repository:     runner.RepositoryFullName,
		Region:         runner.Region,
		Machine:        spec.machine(),
		Boot:           boot,
	})
	if err != nil {
		retryable := cloud.IsRetryable(err)
		log.WarnContext(ctx, "launch failed", slog.String("instance_type", spec.InstanceType), slog.String("region", runner.Region),
			slog.Bool("retryable", retryable), slog.Bool("ambiguous", cloud.IsAmbiguous(err)), slog.Any("error", err))
		if cloud.IsAmbiguous(err) {
			s.unconfirmLaunch(ctx, runner, truncate(err.Error(), 2000))

			return
		}
		s.failLaunch(ctx, runner, truncate(err.Error(), 2000), retryable)

		return
	}
	if _, err := s.q.MarkRunnerRunning(context.WithoutCancel(ctx), db.MarkRunnerRunningParams{ID: runner.ID, InstanceID: &instanceID}); err != nil {
		// The machine exists. The lease expires, and the inventory scan
		// revives the runner when it lists the instance.
		log.ErrorContext(ctx, "record launched instance", slog.String("instance", instanceID), slog.Any("error", err))

		return
	}
	log.InfoContext(ctx, "launched runner", slog.String("instance", instanceID), slog.String("instance_type", spec.InstanceType), slog.String("region", runner.Region))
	s.record(ctx, runner.InstallationID, "runner.launched", levelInfo,
		fmt.Sprintf("Launched %s (%s, %s) for %s", runner.Name, spec.InstanceType, runner.Region, runner.RepositoryFullName),
		eventFields{repository: runner.RepositoryFullName, runnerID: &runner.ID, jobID: runner.TriggerJobID})
}

// jobMovedOn reports whether the job a launching runner is for is no
// longer queued (cancelled, or started elsewhere).
func (s *Service) jobMovedOn(ctx context.Context, runnerID uuid.UUID) (bool, error) {
	current, err := s.q.GetRunner(ctx, runnerID)
	if err != nil {
		return false, fmt.Errorf("get runner: %w", err)
	}
	if current.TriggerJobID == nil {
		return false, nil // a pool runner; it waits for any job
	}
	job, err := s.q.GetJob(ctx, *current.TriggerJobID)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get job: %w", err)
	}

	return job.Status != "queued", nil
}

// cancelLaunch finishes a launching runner whose job moved on before its
// machine was made, and removes its registration; if GitHub cannot be
// reached the reconciler removes it later.
func (s *Service) cancelLaunch(ctx context.Context, runner db.Runner, reg registration, githubRunnerID int64) {
	ctx = context.WithoutCancel(ctx)
	deregistered := reg.remove(ctx, githubRunnerID) == nil
	cancelled, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
		ID:           runner.ID,
		Reason:       ReasonCancelled,
		Deregistered: deregistered,
		FromStates:   []string{StateLaunching},
	})
	if isNoRows(err) {
		return
	}
	if err != nil {
		s.log.ErrorContext(ctx, "cancel launch", slog.String("runner", runner.ID.String()), slog.Any("error", err))

		return
	}
	s.log.InfoContext(ctx, "launch cancelled: the job moved on", slog.String("runner", cancelled.ID.String()), slog.Bool("deregistered", deregistered))
}

// credentialFor opens the cloud credential a runner launches with.
func (s *Service) credentialFor(ctx context.Context, runner db.Runner) (cloud.Provider, []byte, error) {
	provider, err := s.clouds.Get(runner.Cloud)
	if err != nil {
		return nil, nil, err
	}
	if runner.CredentialID == nil {
		return nil, nil, errors.New("the cloud credentials this runner was planned with were removed")
	}
	cred, err := s.q.GetCloudCredentialByID(ctx, *runner.CredentialID)
	if isNoRows(err) {
		return nil, nil, errors.New("the cloud credentials this runner was planned with were removed")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get credential: %w", err)
	}
	secret, err := s.secrets.Open(cred.Secret, credentialContext(cred.InstallationID, cred.Cloud))
	if err != nil {
		return nil, nil, fmt.Errorf("open credential: %w", err)
	}

	return provider, secret, nil
}

// failLaunch finishes a launching runner and retries its job.
func (s *Service) failLaunch(ctx context.Context, runner db.Runner, message string, retryable bool) {
	ctx = context.WithoutCancel(ctx)
	failed, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
		ID:         runner.ID,
		Reason:     ReasonLaunchFailed,
		Error:      &message,
		FromStates: []string{StateLaunching},
	})
	if isNoRows(err) {
		return
	}
	if err != nil {
		s.log.ErrorContext(ctx, "fail runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))

		return
	}
	s.record(ctx, runner.InstallationID, "runner.launch_failed", levelWarning,
		fmt.Sprintf("Launch of %s for %s failed: %s", runner.Name, runner.RepositoryFullName, message),
		eventFields{repository: runner.RepositoryFullName, runnerID: &runner.ID, jobID: runner.TriggerJobID})
	if err := s.retryJob(ctx, failed, retryable); err != nil {
		s.log.ErrorContext(ctx, "retry job after failed launch", slog.String("runner", runner.ID.String()), slog.Any("error", err))
	}
}

// unconfirmLaunch finishes a launching runner whose cloud call got no
// answer. The machine may exist: the runner stays revivable (as after an
// expired lease) until the inventory scan lists its instance or the grace
// period passes, and its job is retried meanwhile. If both the machine
// and the retry come up, the revival cancels the retry while it is still
// pending; otherwise one of the two idles out.
func (s *Service) unconfirmLaunch(ctx context.Context, runner db.Runner, message string) {
	ctx = context.WithoutCancel(ctx)
	unconfirmed, err := s.q.FinishRunner(ctx, db.FinishRunnerParams{
		ID:              runner.ID,
		Reason:          ReasonLaunchUnconfirmed,
		Error:           &message,
		FromStates:      []string{StateLaunching},
		InstanceUnknown: true,
	})
	if isNoRows(err) {
		return
	}
	if err != nil {
		s.log.ErrorContext(ctx, "unconfirm runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))

		return
	}
	s.record(ctx, runner.InstallationID, "runner.launch_unconfirmed", levelWarning,
		fmt.Sprintf("Launch of %s for %s got no answer from the cloud; the machine is settled by the next inventory scan: %s", runner.Name, runner.RepositoryFullName, message),
		eventFields{repository: runner.RepositoryFullName, runnerID: &runner.ID, jobID: runner.TriggerJobID})
	if err := s.retryJob(ctx, unconfirmed, true); err != nil {
		s.log.ErrorContext(ctx, "retry job after unconfirmed launch", slog.String("runner", runner.ID.String()), slog.Any("error", err))
	}
}

// generateJIT registers the runner. A 409 means a registration with this
// name already exists, which happens when an earlier attempt succeeded at
// GitHub but its response was lost; remove that one and register again.
func (s *Service) generateJIT(ctx context.Context, reg registration) (github.JITConfig, error) {
	jit, err := reg.generate(ctx)
	if github.StatusOf(err) != http.StatusConflict {
		return jit, err
	}
	existing, found, err := reg.find(ctx)
	if err != nil {
		return github.JITConfig{}, err
	}
	if found {
		if existing.Busy {
			return github.JITConfig{}, fmt.Errorf("runner %s is already registered and busy", reg.runner.Name)
		}
		if err := reg.remove(ctx, existing.ID); err != nil {
			return github.JITConfig{}, err
		}
	}

	return reg.generate(ctx)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}

	return *p
}
