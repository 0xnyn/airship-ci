package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultRunnerGroupID is the group runners join. Repository runners have no
// groups of their own and GitHub expects 1; for an organization, 1 is the
// Default group.
const defaultRunnerGroupID = 1

// Runner is a self-hosted runner registration.
type Runner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

// JITConfig is a single-use runner registration. EncodedConfig is a
// credential: it must reach the runner and nothing else.
type JITConfig struct {
	Runner        Runner `json:"runner"`
	EncodedConfig string `json:"encoded_jit_config"`
}

// Repo is an "owner/name" repository reference.
type Repo struct {
	Owner string
	Name  string
}

// ParseRepo splits "owner/name".
func ParseRepo(fullName string) (Repo, error) {
	owner, name, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("invalid repository name %q", fullName)
	}

	return Repo{Owner: owner, Name: name}, nil
}

func (r Repo) path() string {
	return "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
}

// FullName is "owner/name".
func (r Repo) FullName() string {
	return r.Owner + "/" + r.Name
}

func (r Repo) runnersPath() string {
	return r.path() + "/actions/runners"
}

// GenerateJITConfig registers an ephemeral runner on a repository. GitHub
// hands the runner exactly one job and removes it afterwards.
func (c *Client) GenerateJITConfig(ctx context.Context, repo Repo, name string, labels []string) (JITConfig, error) {
	body := map[string]any{
		"name":            name,
		"runner_group_id": defaultRunnerGroupID,
		"labels":          labels,
		"work_folder":     "_work",
	}
	var out JITConfig
	if err := c.do(ctx, http.MethodPost, repo.runnersPath()+"/generate-jitconfig", body, &out); err != nil {
		return JITConfig{}, err
	}
	if out.EncodedConfig == "" || out.Runner.ID == 0 {
		return JITConfig{}, errors.New("github returned an empty jit config")
	}

	return out, nil
}

// GetRunner reads a runner registration.
func (c *Client) GetRunner(ctx context.Context, repo Repo, id int64) (Runner, error) {
	var out Runner
	err := c.do(ctx, http.MethodGet, repo.runnersPath()+"/"+strconv.FormatInt(id, 10), nil, &out)

	return out, err
}

// FindRunnerByName looks a runner up by its exact name.
func (c *Client) FindRunnerByName(ctx context.Context, repo Repo, name string) (Runner, bool, error) {
	var out struct {
		Runners []Runner `json:"runners"`
	}
	query := url.Values{"name": {name}}
	if err := c.do(ctx, http.MethodGet, repo.runnersPath()+"?"+query.Encode(), nil, &out); err != nil {
		return Runner{}, false, err
	}
	for _, runner := range out.Runners {
		if runner.Name == name {
			return runner, true, nil
		}
	}

	return Runner{}, false, nil
}

// DeleteRunner removes a runner registration. A runner that is already gone
// counts as deleted.
func (c *Client) DeleteRunner(ctx context.Context, repo Repo, id int64) error {
	err := c.do(ctx, http.MethodDelete, repo.runnersPath()+"/"+strconv.FormatInt(id, 10), nil, nil)
	if IsNotFound(err) {
		return nil
	}

	return err
}

// WorkflowJob is the subset of a GitHub Actions job Airship tracks. It is
// shared by the REST API and the workflow_job webhook.
type WorkflowJob struct {
	ID           int64      `json:"id"`
	RunID        int64      `json:"run_id"`
	RunAttempt   int32      `json:"run_attempt"`
	WorkflowName *string    `json:"workflow_name"`
	Name         string     `json:"name"`
	HeadBranch   *string    `json:"head_branch"`
	HeadSHA      string     `json:"head_sha"`
	HTMLURL      *string    `json:"html_url"`
	Labels       []string   `json:"labels"`
	Status       string     `json:"status"`
	Conclusion   *string    `json:"conclusion"`
	RunnerID     *int64     `json:"runner_id"`
	RunnerName   *string    `json:"runner_name"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
}

// GetJob reads one workflow job.
func (c *Client) GetJob(ctx context.Context, repo Repo, id int64) (WorkflowJob, error) {
	var out WorkflowJob
	err := c.do(ctx, http.MethodGet, repo.path()+"/actions/jobs/"+strconv.FormatInt(id, 10), nil, &out)

	return out, err
}
