package fleet

import (
	"context"
	"fmt"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// registration is where a runner is registered with GitHub: a repository,
// or the organization (idle pool runners from .cirun.global.yml). Every
// GitHub call about a runner goes through it, so the two scopes cannot be
// mixed up.
type registration struct {
	s       *Service
	runner  db.Runner
	repo    github.Repo
	org     string
	groupID int64
}

// registrationFor resolves a runner's scope. For organization runners the
// runner group comes from the pool, which is only needed to register.
func (s *Service) registrationFor(ctx context.Context, runner db.Runner, forRegistration bool) (registration, error) {
	reg := registration{s: s, runner: runner}
	if runner.Scope == ScopeOrganization {
		reg.org = runner.RepositoryFullName
		if forRegistration {
			if runner.PoolID == nil {
				return registration{}, fmt.Errorf("organization runner %s has no pool", runner.Name)
			}
			pool, err := s.q.GetIdlePool(ctx, *runner.PoolID)
			if err != nil {
				return registration{}, fmt.Errorf("get pool: %w", err)
			}
			if pool.RunnerGroupID == nil {
				return registration{}, fmt.Errorf("organization pool %s has no runner group yet", pool.Name)
			}
			reg.groupID = *pool.RunnerGroupID
		}

		return reg, nil
	}
	repo, err := github.ParseRepo(runner.RepositoryFullName)
	if err != nil {
		return registration{}, err
	}
	reg.repo = repo

	return reg, nil
}

func (r registration) generate(ctx context.Context) (github.JITConfig, error) {
	if r.org != "" {
		return r.s.github.GenerateOrgJITConfig(ctx, r.runner.InstallationID, r.org, r.runner.Name, r.groupID, r.runner.Labels)
	}

	return r.s.github.GenerateJITConfig(ctx, r.runner.InstallationID, r.repo, r.runner.Name, r.runner.Labels)
}

func (r registration) get(ctx context.Context, id int64) (github.Runner, error) {
	if r.org != "" {
		return r.s.github.GetOrgRunner(ctx, r.runner.InstallationID, r.org, id)
	}

	return r.s.github.GetRunner(ctx, r.runner.InstallationID, r.repo, id)
}

func (r registration) find(ctx context.Context) (github.Runner, bool, error) {
	if r.org != "" {
		return r.s.github.FindOrgRunnerByName(ctx, r.runner.InstallationID, r.org, r.runner.Name)
	}

	return r.s.github.FindRunnerByName(ctx, r.runner.InstallationID, r.repo, r.runner.Name)
}

func (r registration) remove(ctx context.Context, id int64) error {
	if r.org != "" {
		return r.s.github.DeleteOrgRunner(ctx, r.runner.InstallationID, r.org, id)
	}

	return r.s.github.DeleteRunner(ctx, r.runner.InstallationID, r.repo, id)
}
