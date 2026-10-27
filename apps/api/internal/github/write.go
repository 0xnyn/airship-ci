package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
)

// The developer API edits the organization's access.yaml through a pull
// request, as Cirun's does, so changes are reviewed like any other code.
// These are the handful of write calls that takes.

// GetBranchSHA returns the commit a branch points at.
func (c *Client) GetBranchSHA(ctx context.Context, repo Repo, branch string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.do(ctx, http.MethodGet, repo.path()+"/git/ref/heads/"+url.PathEscape(branch), nil, &out)
	if err != nil {
		return "", err
	}

	return out.Object.SHA, nil
}

// CreateBranch creates a branch at sha.
func (c *Client) CreateBranch(ctx context.Context, repo Repo, branch, sha string) error {
	body := map[string]string{"ref": "refs/heads/" + branch, "sha": sha}

	return c.do(ctx, http.MethodPost, repo.path()+"/git/refs", body, nil)
}

// PutFile creates or updates a file on a branch. sha is the blob being
// replaced ("" for a new file); GitHub refuses the write if it changed.
func (c *Client) PutFile(ctx context.Context, repo Repo, path, branch, message string, content []byte, sha string) error {
	body := map[string]string{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  branch,
	}
	if sha != "" {
		body["sha"] = sha
	}

	return c.do(ctx, http.MethodPut, repo.path()+"/contents/"+escapePath(path), body, nil)
}

// PullRequest is an opened pull request.
type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

// CreatePullRequest opens a pull request from head into base.
func (c *Client) CreatePullRequest(ctx context.Context, repo Repo, title, body, head, base string) (PullRequest, error) {
	var out PullRequest
	err := c.do(ctx, http.MethodPost, repo.path()+"/pulls", map[string]string{"title": title, "body": body, "head": head, "base": base}, &out)
	if err != nil {
		return PullRequest{}, err
	}
	if out.Number == 0 {
		return PullRequest{}, fmt.Errorf("github returned no pull request number for %s", head)
	}

	return out, nil
}
