package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/migration"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

// The dashboard's migration assistant reads a repository's workflows and
// proposes moving their jobs onto Airship; the planning is pure and lives
// in internal/migration. This file is its GitHub side.

// GitHubLister lists directories, which reading every workflow needs.
type GitHubLister interface {
	ListDirectory(ctx context.Context, installationID int64, repo github.Repo, path, ref string) ([]github.Entry, error)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) ListDirectory(ctx context.Context, inst int64, repo github.Repo, path, ref string) ([]github.Entry, error) {
	return a.App.Installation(inst).ListDirectory(ctx, repo, path, ref)
}

// MigrationFile is a file as the repository's default branch has it.
type MigrationFile struct {
	Path    string
	Content []byte
	// SHA is the blob SHA, which a write must name to replace it.
	SHA string
}

// MigrationSource is what migrating a repository's workflows starts from.
type MigrationSource struct {
	Repository github.Repository
	Workflows  []MigrationFile
	// Skipped names workflow files that were not read, and why.
	Skipped map[string]string
	// Config is the repository's airship.yaml; nil when it has none.
	Config *MigrationFile
	// SharedLabels are served by the organization's airship.global.yaml.
	SharedLabels []string
}

// ErrMigrationUnsupported is returned when this control plane's GitHub
// client cannot read directories or write files.
var ErrMigrationUnsupported = errors.New("this control plane cannot read or write repository files")

// ErrMigrationForbidden is returned when the GitHub App may not write the
// files a migration changes.
var ErrMigrationForbidden = errors.New("the GitHub App may not write to this repository")

// ErrMigrationConflict is returned when a file changed on GitHub after it
// was read.
var ErrMigrationConflict = errors.New("a file changed on GitHub while the pull request was being opened")

// ReadMigrationSource reads an accessible repository's workflows and
// airship.yaml from its default branch.
func (s *Service) ReadMigrationSource(ctx context.Context, installation db.Installation, repoID int64) (MigrationSource, error) {
	lister, ok := s.github.(GitHubLister)
	if !ok {
		return MigrationSource{}, ErrMigrationUnsupported
	}
	remote, err := s.github.GetRepositoryByID(ctx, installation.ID, repoID)
	if github.IsNotFound(err) {
		return MigrationSource{}, ErrRepositoryNotFound
	}
	if err != nil {
		return MigrationSource{}, fmt.Errorf("get repository: %w", err)
	}
	repo, err := github.ParseRepo(remote.FullName)
	if err != nil {
		return MigrationSource{}, err
	}
	branch := remote.DefaultBranch
	out := MigrationSource{Repository: remote, Workflows: []MigrationFile{}, Skipped: map[string]string{}}

	entries, err := lister.ListDirectory(ctx, installation.ID, repo, migration.WorkflowDir, branch)
	if err != nil {
		return MigrationSource{}, fmt.Errorf("list workflows: %w", err)
	}
	slices.SortFunc(entries, func(a, b github.Entry) int { return strings.Compare(a.Path, b.Path) })
	for _, entry := range entries {
		if entry.Type != "file" || !migration.IsWorkflowPath(entry.Path) {
			continue
		}
		if len(out.Workflows) >= migration.MaxWorkflows {
			out.Skipped[entry.Path] = fmt.Sprintf("only the first %d workflows are read", migration.MaxWorkflows)

			continue
		}
		file, found, err := s.github.GetFile(ctx, installation.ID, repo, entry.Path, branch, migration.MaxWorkflowBytes)
		switch {
		case errors.Is(err, github.ErrFileTooLarge):
			out.Skipped[entry.Path] = "the file is too large"
		case err != nil:
			return MigrationSource{}, fmt.Errorf("read %s: %w", entry.Path, err)
		case found:
			out.Workflows = append(out.Workflows, MigrationFile{Path: entry.Path, Content: file.Content, SHA: file.SHA})
		}
	}

	file, found, err := s.github.GetFile(ctx, installation.ID, repo, runnerconfig.RepoFile, branch, runnerconfig.MaxFileBytes)
	if err != nil && !errors.Is(err, github.ErrFileTooLarge) {
		return MigrationSource{}, fmt.Errorf("read %s: %w", runnerconfig.RepoFile, err)
	}
	if found {
		out.Config = &MigrationFile{Path: runnerconfig.RepoFile, Content: file.Content, SHA: file.SHA}
	}

	// Organization runners are optional: a broken or missing .airship
	// repository only means there are no shared labels to offer.
	if org, err := s.orgConfig(ctx, installation); err == nil && org.problem == "" {
		for _, r := range org.file.Runners {
			out.SharedLabels = append(out.SharedLabels, r.Labels...)
		}
	}

	return out, nil
}

// MigrationChange is a file a migration writes.
type MigrationChange struct {
	Path    string
	Content []byte
	// SHA is the blob being replaced; empty for a new file.
	SHA string
}

// OpenMigrationPullRequest commits changes to a new branch and opens a
// pull request into the default branch. Nothing runs differently until
// someone merges it.
func (s *Service) OpenMigrationPullRequest(ctx context.Context, installation db.Installation, remote github.Repository, changes []MigrationChange, summary string) (github.PullRequest, error) {
	writer, ok := s.github.(GitHubWriter)
	if !ok {
		return github.PullRequest{}, ErrMigrationUnsupported
	}
	repo, err := github.ParseRepo(remote.FullName)
	if err != nil {
		return github.PullRequest{}, err
	}
	base, err := writer.GetBranchSHA(ctx, installation.ID, repo, remote.DefaultBranch)
	if err != nil {
		return github.PullRequest{}, fmt.Errorf("read %s: %w", remote.DefaultBranch, err)
	}
	var nonce [3]byte
	_, _ = rand.Read(nonce[:])
	branch := "airship/migrate-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(nonce[:])
	if err := writer.CreateBranch(ctx, installation.ID, repo, branch, base); err != nil {
		return github.PullRequest{}, writeError("create branch", err)
	}
	for _, change := range changes {
		message := "Run " + change.Path + " on Airship"
		if change.Path == runnerconfig.RepoFile {
			message = "Define Airship runners in " + runnerconfig.RepoFile
		}
		if err := writer.PutFile(ctx, installation.ID, repo, change.Path, branch, message, change.Content, change.SHA); err != nil {
			return github.PullRequest{}, writeError("write "+change.Path, err)
		}
	}
	title := "Run GitHub Actions jobs on Airship"
	body := "Opened from the Airship dashboard.\n\n" + summary +
		"\n\nJobs move only once this is merged. The repository must be activated in Airship for them to run."
	pr, err := writer.CreatePullRequest(ctx, installation.ID, repo, title, body, branch, remote.DefaultBranch)
	if err != nil {
		return github.PullRequest{}, writeError("open pull request", err)
	}
	s.record(ctx, installation.ID, "migration.proposed", levelInfo, "Opened "+pr.HTMLURL+" to move workflows onto Airship", eventFields{repository: remote.FullName})

	return pr, nil
}

// writeError tells a missing permission and a stale file apart from other
// failures, so the dashboard can say what to do about them.
func writeError(step string, err error) error {
	switch github.StatusOf(err) {
	case 403, 404:
		return fmt.Errorf("%s: %w (%w)", step, ErrMigrationForbidden, err)
	case 409:
		return fmt.Errorf("%s: %w (%w)", step, ErrMigrationConflict, err)
	case 422:
		// GitHub answers 422 when a sha does not match, too.
		if strings.Contains(strings.ToLower(err.Error()), "sha") {
			return fmt.Errorf("%s: %w (%w)", step, ErrMigrationConflict, err)
		}
	}

	return fmt.Errorf("%s: %w", step, err)
}
