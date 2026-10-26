package main

import (
	"context"
	"errors"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

var errNoGitHubApp = errors.New("no GitHub App is configured")

// unconfiguredGitHub lets the control plane run in development without a
// GitHub App. Every call fails, so runners stay pending.
type unconfiguredGitHub struct{}

func (unconfiguredGitHub) GenerateJITConfig(context.Context, int64, github.Repo, string, []string) (github.JITConfig, error) {
	return github.JITConfig{}, errNoGitHubApp
}

func (unconfiguredGitHub) GetRunner(context.Context, int64, github.Repo, int64) (github.Runner, error) {
	return github.Runner{}, errNoGitHubApp
}

func (unconfiguredGitHub) FindRunnerByName(context.Context, int64, github.Repo, string) (github.Runner, bool, error) {
	return github.Runner{}, false, errNoGitHubApp
}

func (unconfiguredGitHub) DeleteRunner(context.Context, int64, github.Repo, int64) error {
	return errNoGitHubApp
}

func (unconfiguredGitHub) GetJob(context.Context, int64, github.Repo, int64) (github.WorkflowJob, error) {
	return github.WorkflowJob{}, errNoGitHubApp
}

func (unconfiguredGitHub) GetInstallation(context.Context, int64) (github.Installation, error) {
	return github.Installation{}, errNoGitHubApp
}

func (unconfiguredGitHub) GetFile(context.Context, int64, github.Repo, string, string, int) (github.File, bool, error) {
	return github.File{}, false, errNoGitHubApp
}

func (unconfiguredGitHub) GetRunAttempt(context.Context, int64, github.Repo, int64, int32) (github.RunAttempt, error) {
	return github.RunAttempt{}, errNoGitHubApp
}

func (unconfiguredGitHub) TeamMember(context.Context, int64, string, string, string) (bool, error) {
	return false, errNoGitHubApp
}

func (unconfiguredGitHub) RepoRoles(context.Context, int64, github.Repo, string) ([]string, error) {
	return nil, errNoGitHubApp
}

func (unconfiguredGitHub) ListRepositories(context.Context, int64) ([]github.Repository, error) {
	return nil, errNoGitHubApp
}

func (unconfiguredGitHub) GetRepositoryByID(context.Context, int64, int64) (github.Repository, error) {
	return github.Repository{}, errNoGitHubApp
}

func (unconfiguredGitHub) GenerateOrgJITConfig(context.Context, int64, string, string, int64, []string) (github.JITConfig, error) {
	return github.JITConfig{}, errNoGitHubApp
}

func (unconfiguredGitHub) GetOrgRunner(context.Context, int64, string, int64) (github.Runner, error) {
	return github.Runner{}, errNoGitHubApp
}

func (unconfiguredGitHub) FindOrgRunnerByName(context.Context, int64, string, string) (github.Runner, bool, error) {
	return github.Runner{}, false, errNoGitHubApp
}

func (unconfiguredGitHub) DeleteOrgRunner(context.Context, int64, string, int64) error {
	return errNoGitHubApp
}

func (unconfiguredGitHub) EnsureRunnerGroup(context.Context, int64, string, string, []int64) (int64, error) {
	return 0, errNoGitHubApp
}
