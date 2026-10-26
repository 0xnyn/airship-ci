package github

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Organization runners: idle pools from .cirun.global.yml register on the
// organization, in a runner group limited to the repositories that may
// use them. Needs the app's "Self-hosted runners: write" organization
// permission.

func orgRunnersPath(org string) string {
	return "/orgs/" + url.PathEscape(org) + "/actions/runners"
}

// GenerateOrgJITConfig registers an ephemeral runner on an organization,
// in a runner group.
func (c *Client) GenerateOrgJITConfig(ctx context.Context, org, name string, groupID int64, labels []string) (JITConfig, error) {
	body := map[string]any{
		"name":            name,
		"runner_group_id": groupID,
		"labels":          labels,
		"work_folder":     "_work",
	}
	var out JITConfig
	if err := c.do(ctx, http.MethodPost, orgRunnersPath(org)+"/generate-jitconfig", body, &out); err != nil {
		return JITConfig{}, err
	}
	if out.EncodedConfig == "" || out.Runner.ID == 0 {
		return JITConfig{}, errors.New("github returned an empty jit config")
	}

	return out, nil
}

// GetOrgRunner reads an organization runner registration.
func (c *Client) GetOrgRunner(ctx context.Context, org string, id int64) (Runner, error) {
	var out Runner
	err := c.do(ctx, http.MethodGet, orgRunnersPath(org)+"/"+strconv.FormatInt(id, 10), nil, &out)

	return out, err
}

// FindOrgRunnerByName looks an organization runner up by its exact name.
func (c *Client) FindOrgRunnerByName(ctx context.Context, org, name string) (Runner, bool, error) {
	var out struct {
		Runners []Runner `json:"runners"`
	}
	query := url.Values{"name": {name}}
	if err := c.do(ctx, http.MethodGet, orgRunnersPath(org)+"?"+query.Encode(), nil, &out); err != nil {
		return Runner{}, false, err
	}
	for _, runner := range out.Runners {
		if runner.Name == name {
			return runner, true, nil
		}
	}

	return Runner{}, false, nil
}

// DeleteOrgRunner removes an organization runner registration. One that is
// already gone counts as deleted.
func (c *Client) DeleteOrgRunner(ctx context.Context, org string, id int64) error {
	err := c.do(ctx, http.MethodDelete, orgRunnersPath(org)+"/"+strconv.FormatInt(id, 10), nil, nil)
	if IsNotFound(err) {
		return nil
	}

	return err
}

// RunnerGroup is an organization runner group.
type RunnerGroup struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

func runnerGroupsPath(org string) string {
	return "/orgs/" + url.PathEscape(org) + "/actions/runner-groups"
}

// EnsureRunnerGroup finds or creates a runner group visible to exactly
// repoIDs, and returns its ID. Public repositories are never allowed in.
func (c *Client) EnsureRunnerGroup(ctx context.Context, org, name string, repoIDs []int64) (int64, error) {
	if repoIDs == nil {
		repoIDs = []int64{}
	}
	var listed struct {
		Groups []RunnerGroup `json:"runner_groups"`
	}
	if err := c.do(ctx, http.MethodGet, runnerGroupsPath(org)+"?per_page=100", nil, &listed); err != nil {
		return 0, err
	}
	for _, group := range listed.Groups {
		if group.Name != name {
			continue
		}
		body := map[string]any{"selected_repository_ids": repoIDs}
		if err := c.do(ctx, http.MethodPut, runnerGroupsPath(org)+"/"+strconv.FormatInt(group.ID, 10)+"/repositories", body, nil); err != nil {
			return 0, err
		}

		return group.ID, nil
	}
	var created RunnerGroup
	body := map[string]any{
		"name":                       name,
		"visibility":                 "selected",
		"selected_repository_ids":    repoIDs,
		"allows_public_repositories": false,
	}
	if err := c.do(ctx, http.MethodPost, runnerGroupsPath(org), body, &created); err != nil {
		return 0, err
	}

	return created.ID, nil
}
