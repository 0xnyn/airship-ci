package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

// AccessControl is the organization's .access.yml as it is now.
type AccessControl struct {
	// Found is false when the .cirun repository has no .access.yml.
	Found    bool
	Document runnerconfig.AccessDocument
	SHA      string
}

// ErrNoOrganization is returned when access control is asked of a personal
// account, which has no .cirun repository.
var ErrNoOrganization = errors.New("access control needs an organization")

// GitHubWriter is what editing .access.yml needs beyond GitHub.
type GitHubWriter interface {
	GetRepository(ctx context.Context, installationID int64, repo github.Repo) (github.Repository, error)
	GetBranchSHA(ctx context.Context, installationID int64, repo github.Repo, branch string) (string, error)
	CreateBranch(ctx context.Context, installationID int64, repo github.Repo, branch, sha string) error
	PutFile(ctx context.Context, installationID int64, repo github.Repo, path, branch, message string, content []byte, sha string) error
	CreatePullRequest(ctx context.Context, installationID int64, repo github.Repo, title, body, head, base string) (github.PullRequest, error)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetRepository(ctx context.Context, inst int64, repo github.Repo) (github.Repository, error) {
	return a.App.Installation(inst).GetRepository(ctx, repo)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) GetBranchSHA(ctx context.Context, inst int64, repo github.Repo, branch string) (string, error) {
	return a.App.Installation(inst).GetBranchSHA(ctx, repo, branch)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) CreateBranch(ctx context.Context, inst int64, repo github.Repo, branch, sha string) error {
	return a.App.Installation(inst).CreateBranch(ctx, repo, branch, sha)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) PutFile(ctx context.Context, inst int64, repo github.Repo, path, branch, message string, content []byte, sha string) error {
	return a.App.Installation(inst).PutFile(ctx, repo, path, branch, message, content, sha)
}

//nolint:wrapcheck // see GenerateJITConfig
func (a AppGitHub) CreatePullRequest(ctx context.Context, inst int64, repo github.Repo, title, body, head, base string) (github.PullRequest, error) {
	return a.App.Installation(inst).CreatePullRequest(ctx, repo, title, body, head, base)
}

// ReadAccessControl reads the organization's .access.yml.
func (s *Service) ReadAccessControl(ctx context.Context, installation db.Installation) (AccessControl, error) {
	if installation.AccountType != "Organization" {
		return AccessControl{}, ErrNoOrganization
	}
	repo := github.Repo{Owner: installation.AccountLogin, Name: runnerconfig.GlobalRepo}
	file, found, err := s.github.GetFile(ctx, installation.ID, repo, runnerconfig.AccessFile, "", runnerconfig.MaxFileBytes)
	if err != nil {
		return AccessControl{}, fmt.Errorf("read %s: %w", runnerconfig.AccessFile, err)
	}
	if !found {
		return AccessControl{Document: runnerconfig.AccessDocument{Version: 1}}, nil
	}
	doc, err := runnerconfig.DecodeAccess(file.Content)
	if err != nil {
		return AccessControl{}, err //nolint:wrapcheck // a config error, worded for the customer
	}

	return AccessControl{Found: true, Document: doc, SHA: file.SHA}, nil
}

// AccessControlUpdate is the outcome of editing .access.yml.
type AccessControlUpdate struct {
	PullRequest github.PullRequest
	Branch      string
	Document    runnerconfig.AccessDocument
}

// UpdateAccessControl applies changes to .access.yml and opens a pull
// request against the .cirun repository with the result, as Cirun's API
// does. Nothing takes effect until the pull request is merged.
func (s *Service) UpdateAccessControl(ctx context.Context, installation db.Installation, changes []runnerconfig.AccessChange) (AccessControlUpdate, error) {
	writer, ok := s.github.(GitHubWriter)
	if !ok {
		return AccessControlUpdate{}, errors.New("this control plane cannot write to GitHub")
	}
	current, err := s.ReadAccessControl(ctx, installation)
	if err != nil {
		return AccessControlUpdate{}, err
	}
	doc := current.Document
	for _, change := range changes {
		if err := doc.Apply(change); err != nil {
			return AccessControlUpdate{}, err //nolint:wrapcheck // a config error, worded for the customer
		}
	}
	if _, err := doc.Compile(); err != nil {
		return AccessControlUpdate{}, err //nolint:wrapcheck // a config error, worded for the customer
	}
	content, err := doc.Encode()
	if err != nil {
		return AccessControlUpdate{}, err //nolint:wrapcheck // already descriptive
	}

	repo := github.Repo{Owner: installation.AccountLogin, Name: runnerconfig.GlobalRepo}
	remote, err := writer.GetRepository(ctx, installation.ID, repo)
	if err != nil {
		return AccessControlUpdate{}, fmt.Errorf("read %s repository: %w", runnerconfig.GlobalRepo, err)
	}
	base, err := writer.GetBranchSHA(ctx, installation.ID, repo, remote.DefaultBranch)
	if err != nil {
		return AccessControlUpdate{}, fmt.Errorf("read %s: %w", remote.DefaultBranch, err)
	}
	var nonce [3]byte
	_, _ = rand.Read(nonce[:])
	branch := "airship/access-control-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(nonce[:])
	if err := writer.CreateBranch(ctx, installation.ID, repo, branch, base); err != nil {
		return AccessControlUpdate{}, fmt.Errorf("create branch: %w", err)
	}
	message := "Update " + runnerconfig.AccessFile + " (" + describeChanges(changes) + ")"
	if err := writer.PutFile(ctx, installation.ID, repo, runnerconfig.AccessFile, branch, message, content, current.SHA); err != nil {
		return AccessControlUpdate{}, fmt.Errorf("write %s: %w", runnerconfig.AccessFile, err)
	}
	body := "Opened by the Airship developer API.\n\n" + describeChanges(changes) + "\n\nMerge to apply; nothing changes until then."
	pr, err := writer.CreatePullRequest(ctx, installation.ID, repo, message, body, branch, remote.DefaultBranch)
	if err != nil {
		return AccessControlUpdate{}, fmt.Errorf("open pull request: %w", err)
	}
	s.record(ctx, installation.ID, "access.updated", levelInfo, "Opened "+pr.HTMLURL+" to update "+runnerconfig.AccessFile, eventFields{repository: repo.FullName()})

	return AccessControlUpdate{PullRequest: pr, Branch: branch, Document: doc}, nil
}

func describeChanges(changes []runnerconfig.AccessChange) string {
	var parts []string
	for _, c := range changes {
		verb := "grant"
		if c.Remove {
			verb = "revoke"
		}
		parts = append(parts, fmt.Sprintf("%s %s on %s", verb, c.Repository, strings.Join(c.Resources, ", ")))
	}

	return strings.Join(parts, "; ")
}
