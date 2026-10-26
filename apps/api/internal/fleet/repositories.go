package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/google/uuid"
)

// ErrRepositoryNotFound is returned for a repository the installation
// cannot access.
var ErrRepositoryNotFound = errors.New("repository not found")

// ListInstallationRepositories lists the repositories the app can access.
func (s *Service) ListInstallationRepositories(ctx context.Context, installationID int64) ([]github.Repository, error) {
	repos, err := s.github.ListRepositories(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}

	return repos, nil
}

// ActivateRepository lets a repository's .cirun.yml launch runners, and
// reads its config straight away so its pools start and its problems show.
// The repository is looked up on GitHub by ID, so only one the
// installation can access can be activated. by is the user who did it;
// nil for a developer API token.
func (s *Service) ActivateRepository(ctx context.Context, installationID, repoID int64, allowPublic bool, by *uuid.UUID) (db.Repository, error) {
	remote, err := s.github.GetRepositoryByID(ctx, installationID, repoID)
	if github.IsNotFound(err) {
		return db.Repository{}, ErrRepositoryNotFound
	}
	if err != nil {
		return db.Repository{}, fmt.Errorf("get repository: %w", err)
	}
	row, err := s.q.ActivateRepository(ctx, db.ActivateRepositoryParams{
		ID: remote.ID, InstallationID: installationID, FullName: remote.FullName, Private: remote.Private,
		DefaultBranch: remote.DefaultBranch, AllowPublic: allowPublic, ActivatedBy: by,
	})
	if isNoRows(err) {
		// The ID belongs to another installation's activated repository,
		// which can only happen after a transfer; it must be deactivated
		// there first.
		return db.Repository{}, ErrRepositoryNotFound
	}
	if err != nil {
		return db.Repository{}, fmt.Errorf("activate repository: %w", err)
	}
	s.record(ctx, installationID, "repository.activated", levelInfo, "Activated "+row.FullName, eventFields{repository: row.FullName})
	if err := s.RefreshRepositoryConfig(ctx, row); err != nil {
		s.log.WarnContext(ctx, "read config after activation", slog.String("repository", row.FullName), slog.Any("error", err))
	} else {
		s.fillRepositoryPools(ctx, row.ID)
	}

	return s.q.GetRepository(ctx, db.GetRepositoryParams{InstallationID: installationID, ID: row.ID}) //nolint:wrapcheck // a plain read
}

// DeactivateRepository stops a repository's config from launching runners.
// Its pools go with it; their idle runners lose their pool and are reaped.
func (s *Service) DeactivateRepository(ctx context.Context, installationID, repoID int64) error {
	repo, err := s.q.GetRepository(ctx, db.GetRepositoryParams{InstallationID: installationID, ID: repoID})
	if isNoRows(err) {
		return ErrRepositoryNotFound
	}
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}
	n, err := s.q.DeactivateRepository(ctx, db.DeactivateRepositoryParams{InstallationID: installationID, ID: repoID})
	if err != nil {
		return fmt.Errorf("deactivate repository: %w", err)
	}
	if n == 0 {
		return ErrRepositoryNotFound
	}
	s.record(ctx, installationID, "repository.deactivated", levelInfo, "Deactivated "+repo.FullName, eventFields{repository: repo.FullName})

	return nil
}

// OrganizationConfig is the organization's .cirun repository as jobs see
// it now.
type OrganizationConfig struct {
	// Found is false when there is no .cirun.global.yml (or the app
	// cannot read the .cirun repository).
	Found   bool
	SHA     string
	Runners []runnerconfig.Runner
	// Restricted is set when .access.yml governs the runners.
	Restricted bool
	Problem    string
}

// OrganizationConfig reads the organization's runner config. It shares
// the cache jobs use, so it may lag a push by up to a minute.
func (s *Service) OrganizationConfig(ctx context.Context, installation db.Installation) (OrganizationConfig, error) {
	org, err := s.orgConfig(ctx, installation)
	if err != nil {
		return OrganizationConfig{}, err
	}

	return OrganizationConfig{
		Found: org.sha != "" || org.problem != "", SHA: org.sha, Runners: org.file.Runners,
		Restricted: org.access != nil, Problem: org.problem,
	}, nil
}
