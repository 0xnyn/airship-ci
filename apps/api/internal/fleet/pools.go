package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/google/uuid"
)

const (
	// poolFailureWindow and poolFailureLimit pause a pool whose runners
	// keep failing to launch, so a bad image does not loop forever.
	poolFailureWindow = 10 * time.Minute
	poolFailureLimit  = 3
	// configResync rereads every activated repository's config this often,
	// in case a push webhook was lost.
	configResync = time.Hour
	// configRetry spaces out rereads of a config GitHub would not serve.
	configRetry = time.Minute
	configBatch = 20
)

// IdleSpec is a pool's idle: section as stored in idle_pools.idle.
type IdleSpec struct {
	Always        int          `json:"always,omitempty"`
	Schedule      []IdleWindow `json:"schedule,omitempty"`
	UsageReplicas int          `json:"usageReplicas,omitempty"`
	UsageMinutes  int          `json:"usageMinutes,omitempty"`
}

// IdleWindow is one scheduled window; Days is indexed by time.Weekday.
type IdleWindow struct {
	Days      [7]bool `json:"days"`
	StartHour int     `json:"startHour"`
	EndHour   int     `json:"endHour"`
	Timezone  string  `json:"timezone"`
	Replicas  int     `json:"replicas"`
}

func idleSpecFor(idle *runnerconfig.Idle) IdleSpec {
	out := IdleSpec{Always: idle.Always}
	if idle.Usage != nil {
		out.UsageReplicas = idle.Usage.Replicas
		out.UsageMinutes = int(idle.Usage.Duration / time.Minute)
	}
	for _, w := range idle.Schedule {
		out.Schedule = append(out.Schedule, IdleWindow{
			Days: w.Days, StartHour: w.StartHour, EndHour: w.EndHour, Timezone: w.Location.String(), Replicas: w.Replicas,
		})
	}

	return out
}

// Desired is how many idle runners the pool wants at now.
func (i IdleSpec) Desired(now time.Time, usageUntil *time.Time) int {
	if i.UsageMinutes > 0 {
		if usageUntil != nil && now.Before(*usageUntil) {
			return i.UsageReplicas
		}

		return 0
	}
	if len(i.Schedule) == 0 {
		return i.Always
	}
	wanted := 0
	for _, w := range i.Schedule {
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			continue // validated when parsed; a tz database without it is not ours to fix
		}
		local := now.In(loc)
		if w.Days[local.Weekday()] && local.Hour() >= w.StartHour && local.Hour() < w.EndHour {
			wanted = max(wanted, w.Replicas)
		}
	}

	return wanted
}

// reservePoolRunner points an idle runner from a matching pool at the
// job: the repository's own pools for repository definitions, the
// organization's for organization definitions. Only a job whose labels the
// pool's runners carry exactly (no per-run suffix) can use one.
func (s *Service) reservePoolRunner(ctx context.Context, q *db.Queries, job db.Job, plan Plan) (bool, error) {
	var repoPools, orgPools []db.IdlePool
	for _, a := range plan {
		var err error
		switch {
		case a.Source == SourceRepository && repoPools == nil:
			repoPools, err = q.ListRepositoryIdlePools(ctx, &job.RepositoryID)
		case a.Source == SourceOrganization && orgPools == nil:
			orgPools, err = q.ListOrganizationIdlePools(ctx, job.InstallationID)
		}
		if err != nil {
			return false, fmt.Errorf("list pools: %w", err)
		}
	}
	for _, a := range plan {
		pools := repoPools
		if a.Source == SourceOrganization {
			pools = orgPools
		}
		for _, pool := range pools {
			if pool.Name != a.Name || !poolServes(pool, job.Labels) {
				continue
			}
			live, err := q.JobHasLiveRunner(ctx, &job.ID)
			if err != nil {
				return false, fmt.Errorf("check live runner: %w", err)
			}
			if live {
				return false, nil
			}
			_, err = q.ReserveIdleRunner(ctx, db.ReserveIdleRunnerParams{JobID: &job.ID, PoolID: &pool.ID})
			if isNoRows(err) {
				continue // the pool is empty right now
			}
			if err != nil {
				return false, fmt.Errorf("reserve pool runner: %w", err)
			}

			return true, nil
		}
	}

	return false, nil
}

// poolServes reports whether every runner the pool launches can take a
// job with jobLabels.
func poolServes(pool db.IdlePool, jobLabels []string) bool {
	var wants []want
	if err := json.Unmarshal(pool.Plan, &wants); err != nil || len(wants) == 0 {
		return false
	}
	for _, w := range wants {
		if !runnerconfig.Covers(w.Labels, jobLabels) {
			return false
		}
	}

	return true
}

// extendUsagePools keeps usage-based pools warm after a runner from the
// same repository definition is allocated a job.
func (s *Service) extendUsagePools(ctx context.Context, q *db.Queries, busy db.Runner) (bool, error) {
	var n int64
	var err error
	if busy.Source == SourceOrganization {
		n, err = q.ExtendOrganizationUsagePools(ctx, db.ExtendOrganizationUsagePoolsParams{InstallationID: busy.InstallationID, Name: busy.Definition})
	} else {
		n, err = q.ExtendUsagePools(ctx, db.ExtendUsagePoolsParams{RepositoryID: &busy.RepositoryID, Name: busy.Definition})
	}
	if err != nil {
		return false, fmt.Errorf("extend usage pools: %w", err)
	}

	return n > 0, nil
}

// fillRepositoryPools tops up the pools a job in a repository can draw
// from (the repository's own and the organization's) now rather than on
// the next reconcile pass. Failures are logged; the reconciler retries.
func (s *Service) fillRepositoryPools(ctx context.Context, installationID, repositoryID int64) {
	pools, err := s.q.ListRepositoryIdlePools(ctx, &repositoryID)
	if err == nil {
		var orgPools []db.IdlePool
		orgPools, err = s.q.ListOrganizationIdlePools(ctx, installationID)
		pools = append(pools, orgPools...)
	}
	if err == nil && len(pools) > 0 {
		var creds []db.CloudCredential
		creds, err = s.q.ListCloudCredentials(ctx, installationID)
		for _, pool := range pools {
			if err != nil {
				break
			}
			err = s.balancePool(ctx, pool.ID, creds, time.Now())
		}
	}
	if err != nil {
		s.log.WarnContext(ctx, "fill pools", slog.Int64("repository", repositoryID), slog.Any("error", err))
	}
}

// fillInstallationPools tops up every pool of an installation, as after
// its cloud was connected.
func (s *Service) fillInstallationPools(ctx context.Context, installationID int64) {
	pools, err := s.q.ListInstallationIdlePools(ctx, installationID)
	if err == nil && len(pools) > 0 {
		var creds []db.CloudCredential
		creds, err = s.q.ListCloudCredentials(ctx, installationID)
		for _, pool := range pools {
			if err != nil {
				break
			}
			err = s.balancePool(ctx, pool.ID, creds, time.Now())
		}
	}
	if err != nil {
		s.log.WarnContext(ctx, "fill pools", slog.Int64("installation", installationID), slog.Any("error", err))
	}
}

// balancePools brings every pool to the size it wants now.
func (s *Service) balancePools(ctx context.Context) error {
	pools, err := s.q.ListIdlePools(ctx)
	if err != nil {
		return fmt.Errorf("list pools: %w", err)
	}
	creds := map[int64][]db.CloudCredential{}
	now := time.Now()
	for _, pool := range pools {
		if _, ok := creds[pool.InstallationID]; !ok {
			list, err := s.q.ListCloudCredentials(ctx, pool.InstallationID)
			if err != nil {
				return fmt.Errorf("list cloud credentials: %w", err)
			}
			creds[pool.InstallationID] = list
		}
		if err := s.balancePool(ctx, pool.ID, creds[pool.InstallationID], now); err != nil {
			s.log.WarnContext(ctx, "balance pool", slog.String("pool", pool.ID.String()), slog.Any("error", err))
		}
	}

	return nil
}

// balancePool queues runners for a pool short of its size and trims one
// over it. The pool row is locked throughout, so concurrent passes cannot
// both act on the same shortfall.
func (s *Service) balancePool(ctx context.Context, poolID uuid.UUID, creds []db.CloudCredential, now time.Time) error {
	var wake bool
	var trim []db.Runner
	err := s.inTx(ctx, func(q *db.Queries) error {
		wake, trim = false, nil
		pool, err := q.LockIdlePool(ctx, poolID)
		if isNoRows(err) {
			return nil // deleted by a config refresh
		}
		if err != nil {
			return fmt.Errorf("lock pool: %w", err)
		}
		installation, err := q.GetInstallation(ctx, pool.InstallationID)
		if err != nil {
			return fmt.Errorf("get installation: %w", err)
		}
		var spec IdleSpec
		if err := json.Unmarshal(pool.Idle, &spec); err != nil {
			return fmt.Errorf("decode idle: %w", err)
		}
		desired := spec.Desired(now, pool.UsageUntil)
		if installation.DeletedAt != nil || installation.SuspendedAt != nil {
			desired = 0
		}
		idle, err := q.CountIdlePoolRunners(ctx, &pool.ID)
		if err != nil {
			return fmt.Errorf("count pool runners: %w", err)
		}

		switch {
		case int(idle) < desired:
			wake, err = s.growPool(ctx, q, pool, creds, desired-int(idle), now)

			return err
		case int(idle) > desired:
			surplus, err := q.ListSurplusPoolRunners(ctx, db.ListSurplusPoolRunnersParams{PoolID: &pool.ID, MaxResults: idle - int32(desired)}) //nolint:gosec // desired <= idle here
			if err != nil {
				return fmt.Errorf("list surplus: %w", err)
			}
			for _, runner := range surplus {
				if runner.State == StatePending {
					if _, err := q.FinishRunner(ctx, db.FinishRunnerParams{ID: runner.ID, Reason: ReasonCancelled, FromStates: []string{StatePending}}); err != nil && !isNoRows(err) {
						return fmt.Errorf("cancel pool runner: %w", err)
					}

					continue
				}
				// Out of the pool first, so no job can reserve it while it
				// is being removed from GitHub.
				detached, err := q.DetachPoolRunner(ctx, runner.ID)
				if isNoRows(err) {
					continue
				}
				if err != nil {
					return fmt.Errorf("detach pool runner: %w", err)
				}
				trim = append(trim, detached)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}
	if wake {
		s.wakeLauncher()
	}
	for _, runner := range trim {
		if err := s.reapIdleRunner(ctx, runner); err != nil {
			// Detached, so the idle reaper finishes the job.
			s.log.WarnContext(ctx, "trim pool runner", slog.String("runner", runner.ID.String()), slog.Any("error", err))
		}
	}

	return nil
}

func (s *Service) growPool(ctx context.Context, q *db.Queries, pool db.IdlePool, creds []db.CloudCredential, count int, now time.Time) (bool, error) {
	failures, err := q.CountRecentPoolFailures(ctx, db.CountRecentPoolFailuresParams{PoolID: &pool.ID, Since: now.Add(-poolFailureWindow)})
	if err != nil {
		return false, fmt.Errorf("count pool failures: %w", err)
	}
	if failures >= poolFailureLimit {
		return false, nil
	}
	var wants []want
	if err := json.Unmarshal(pool.Plan, &wants); err != nil {
		return false, fmt.Errorf("decode pool plan: %w", err)
	}
	plan, _ := resolveClouds(wants, creds)
	if len(plan) == 0 {
		return false, nil // no cloud connected yet; the next pass looks again
	}
	// Repository pools register on their repository; organization pools on
	// the organization, in the pool's runner group.
	var repositoryID int64
	var registeredOn string
	source, scope := SourceRepository, ScopeRepository
	if pool.RepositoryID != nil {
		repository, err := q.GetRepository(ctx, db.GetRepositoryParams{InstallationID: pool.InstallationID, ID: *pool.RepositoryID})
		if err != nil {
			return false, fmt.Errorf("get repository: %w", err)
		}
		repositoryID, registeredOn = repository.ID, repository.FullName
	} else {
		if pool.RunnerGroupID == nil {
			return false, nil // the group is created on the next organization config refresh
		}
		installation, err := q.GetInstallation(ctx, pool.InstallationID)
		if err != nil {
			return false, fmt.Errorf("get installation: %w", err)
		}
		registeredOn = installation.AccountLogin
		source, scope = SourceOrganization, ScopeOrganization
	}
	// Failures move the pool on to its next fallback, as they do for jobs.
	attempt := plan.at(failures)
	spec, err := json.Marshal(attempt.Spec)
	if err != nil {
		return false, fmt.Errorf("encode spec: %w", err)
	}
	for range count {
		_, err := q.CreateRunner(ctx, db.CreateRunnerParams{
			InstallationID:     pool.InstallationID,
			Definition:         attempt.Name,
			Source:             source,
			Scope:              scope,
			Cloud:              attempt.Cloud,
			CredentialID:       uuidPtr(attempt.CredentialID),
			Region:             attempt.Region,
			Spec:               spec,
			Labels:             attempt.Labels,
			Attempt:            int32(failures), //nolint:gosec // below poolFailureLimit
			PoolID:             &pool.ID,
			RepositoryID:       repositoryID,
			RepositoryFullName: registeredOn,
			Name:               newRunnerName(),
		})
		if err != nil {
			return false, fmt.Errorf("create pool runner: %w", err)
		}
	}

	return true, nil
}

// refreshConfigs rereads the default-branch config of repositories that
// were pushed to, activated, or not read for a while, and updates their
// pools.
func (s *Service) refreshConfigs(ctx context.Context) error {
	now := time.Now()
	repos, err := s.q.ListStaleRepositoryConfigs(ctx, db.ListStaleRepositoryConfigsParams{
		RetryBefore:   now.Add(-configRetry),
		CheckedBefore: now.Add(-configResync),
		MaxResults:    configBatch,
	})
	if err != nil {
		return fmt.Errorf("list stale configs: %w", err)
	}
	for _, repo := range repos {
		if err := s.RefreshRepositoryConfig(ctx, repo); err != nil {
			s.log.WarnContext(ctx, "refresh config", slog.String("repository", repo.FullName), slog.Any("error", err))
		}
	}

	return nil
}

// RefreshRepositoryConfig reads a repository's .cirun.yml on its default
// branch and makes its pools match. A file that does not parse keeps the
// pools it had: a typo should not drop warm capacity.
func (s *Service) RefreshRepositoryConfig(ctx context.Context, repo db.Repository) error {
	// Record the attempt first, so a repository GitHub keeps failing on
	// waits configRetry instead of holding up the rest.
	if err := s.q.TouchRepositoryConfig(ctx, repo.ID); err != nil {
		return fmt.Errorf("touch repository: %w", err)
	}
	ghRepo, err := github.ParseRepo(repo.FullName)
	if err != nil {
		return err
	}
	s.ForgetConfig(repo.InstallationID, repo.FullName)
	file, found, err := s.github.GetFile(ctx, repo.InstallationID, ghRepo, runnerconfig.RepoFile, repo.DefaultBranch, runnerconfig.MaxFileBytes)
	var parsed runnerconfig.File
	var problem error
	switch {
	case errors.Is(err, github.ErrFileTooLarge):
		problem = &runnerconfig.Error{File: runnerconfig.RepoFile, Detail: fmt.Sprintf("file is over %d bytes", runnerconfig.MaxFileBytes)}
	case err != nil:
		return fmt.Errorf("read config: %w", err)
	case found:
		parsed, problem = runnerconfig.Parse(runnerconfig.RepoFile, file.Content)
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if problem == nil {
			keep := []string{}
			for _, r := range parsed.Runners {
				if r.Idle == nil {
					continue
				}
				plan, err := json.Marshal(wantsFor(runnerconfig.PoolAttempts(runnerconfig.SourceRepository, r)))
				if err != nil {
					return fmt.Errorf("encode pool plan: %w", err)
				}
				idle, err := json.Marshal(idleSpecFor(r.Idle))
				if err != nil {
					return fmt.Errorf("encode idle: %w", err)
				}
				if _, err := q.UpsertIdlePool(ctx, db.UpsertIdlePoolParams{
					InstallationID: repo.InstallationID, RepositoryID: &repo.ID, Name: r.Name,
					Plan: plan, Idle: idle, SourceSha: file.SHA,
				}); err != nil {
					return fmt.Errorf("save pool: %w", err)
				}
				keep = append(keep, r.Name)
			}
			if _, err := q.DeleteIdlePoolsExcept(ctx, db.DeleteIdlePoolsExceptParams{RepositoryID: &repo.ID, Keep: keep}); err != nil {
				return fmt.Errorf("delete pools: %w", err)
			}
		}
		var detail *string
		if problem != nil {
			detail = nilIfEmpty(problem.Error())
			if repo.ConfigError == nil || *repo.ConfigError != *detail {
				s.record(ctx, repo.InstallationID, "config.invalid", levelError, repo.FullName+": "+*detail, eventFields{repository: repo.FullName})
			}
		}

		return s.wrap("save config status", q.SetRepositoryConfig(ctx, db.SetRepositoryConfigParams{
			ID: repo.ID, ConfigSha: nilIfEmpty(file.SHA), ConfigError: detail,
		}))
	})
}

// HandlePush notes that a push to an activated repository's default branch
// may have changed its config, and that one to the organization's
// .cirun repository may have changed every repository's.
func (s *Service) HandlePush(ctx context.Context, event github.PushEvent) error {
	if event.Installation == nil {
		return nil
	}
	if repo, err := github.ParseRepo(event.Repository.FullName); err == nil && strings.EqualFold(repo.Name, runnerconfig.GlobalRepo) {
		s.ForgetConfig(event.Installation.ID, event.Repository.FullName)
		if event.Ref == "refs/heads/"+event.Repository.DefaultBranch {
			if err := s.q.MarkOrganizationConfigStale(ctx, event.Installation.ID); err != nil {
				return fmt.Errorf("mark organization config stale: %w", err)
			}
		}

		return nil
	}
	if event.Ref != "refs/heads/"+event.Repository.DefaultBranch {
		return nil
	}
	if _, err := s.q.MarkRepositoryConfigStale(ctx, event.Repository.ID); err != nil {
		return fmt.Errorf("mark config stale: %w", err)
	}
	s.ForgetConfig(event.Installation.ID, event.Repository.FullName)

	return nil
}

// HandleRepository keeps an activated repository's name, visibility and
// default branch current. A repository made public stops being served
// unless it was activated with public access allowed, because every job
// checks the stored row.
func (s *Service) HandleRepository(ctx context.Context, event github.RepositoryEvent) error {
	if event.Installation == nil {
		return nil
	}
	if event.Action == "deleted" || event.Action == "transferred" {
		_, err := s.q.DeactivateRepository(ctx, db.DeactivateRepositoryParams{InstallationID: event.Installation.ID, ID: event.Repository.ID})

		return s.wrap("deactivate repository", err)
	}
	err := s.q.UpdateRepositoryMetadata(ctx, db.UpdateRepositoryMetadataParams{
		ID: event.Repository.ID, FullName: event.Repository.FullName, Private: event.Repository.Private,
		DefaultBranch: event.Repository.DefaultBranch,
	})
	if err != nil {
		return fmt.Errorf("update repository: %w", err)
	}
	if event.Action == "edited" || event.Action == "renamed" {
		if _, err := s.q.MarkRepositoryConfigStale(ctx, event.Repository.ID); err != nil {
			return fmt.Errorf("mark config stale: %w", err)
		}
	}

	return nil
}
