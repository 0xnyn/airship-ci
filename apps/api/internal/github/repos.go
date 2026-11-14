package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// File is a file read from a repository.
type File struct {
	Content []byte
	// SHA is the blob SHA, which changes whenever the content does.
	SHA string
}

type contentsResponse struct {
	Type     string `json:"type"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
}

// ErrFileTooLarge is returned for files over GetFile's limit.
var ErrFileTooLarge = errors.New("file is too large")

// GetFile reads one file at ref (a commit SHA, branch or tag; empty means
// the default branch). found is false when there is no such file.
func (c *Client) GetFile(ctx context.Context, repo Repo, path, ref string, limit int) (File, bool, error) {
	query := url.Values{}
	if ref != "" {
		query.Set("ref", ref)
	}
	endpoint := repo.path() + "/contents/" + escapePath(path)
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var out contentsResponse
	err := c.do(ctx, http.MethodGet, endpoint, nil, &out)
	if IsNotFound(err) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	if out.Type != "file" {
		return File{}, false, nil
	}
	if out.Size > limit {
		return File{}, true, fmt.Errorf("%s is %d bytes: %w", path, out.Size, ErrFileTooLarge)
	}
	if out.Encoding != "base64" {
		return File{}, true, fmt.Errorf("%s: unexpected encoding %q", path, out.Encoding)
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return File{}, true, fmt.Errorf("decode %s: %w", path, err)
	}

	return File{Content: content, SHA: out.SHA}, true, nil
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}

	return strings.Join(parts, "/")
}

// Actor is a GitHub user as runs report them.
type Actor struct {
	Login string `json:"login"`
}

// RunAttempt is one attempt of a workflow run: who started it, for which
// event, and from which repository the code came.
type RunAttempt struct {
	ID              int64       `json:"id"`
	Event           string      `json:"event"`
	HeadSHA         string      `json:"head_sha"`
	HeadBranch      *string     `json:"head_branch"`
	HeadRepository  *Repository `json:"head_repository"`
	Repository      Repository  `json:"repository"`
	Actor           *Actor      `json:"actor"`
	TriggeringActor *Actor      `json:"triggering_actor"`
	RunAttempt      int32       `json:"run_attempt"`
}

// FromFork reports whether the run's code comes from another repository,
// as with pull requests from forks.
func (r RunAttempt) FromFork() bool {
	return r.HeadRepository == nil || r.HeadRepository.ID != r.Repository.ID
}

// PullRequest reports whether a pull request started the run.
func (r RunAttempt) PullRequest() bool {
	return strings.HasPrefix(r.Event, "pull_request")
}

// Starter is who started this attempt: whoever re-ran it, or the actor.
func (r RunAttempt) Starter() string {
	if r.TriggeringActor != nil && r.TriggeringActor.Login != "" {
		return r.TriggeringActor.Login
	}
	if r.Actor != nil {
		return r.Actor.Login
	}

	return ""
}

// GetRunAttempt reads one attempt of a workflow run.
func (c *Client) GetRunAttempt(ctx context.Context, repo Repo, runID int64, attempt int32) (RunAttempt, error) {
	var out RunAttempt
	path := fmt.Sprintf("%s/actions/runs/%d/attempts/%d?exclude_pull_requests=true", repo.path(), runID, max(attempt, 1))
	err := c.do(ctx, http.MethodGet, path, nil, &out)

	return out, err
}

// GetRepository reads a repository.
func (c *Client) GetRepository(ctx context.Context, repo Repo) (Repository, error) {
	var out Repository
	err := c.do(ctx, http.MethodGet, repo.path(), nil, &out)

	return out, err
}

// GetRepositoryByID reads a repository by its numeric ID, which survives
// renames and transfers. With an installation token it only finds
// repositories the installation can access.
func (c *Client) GetRepositoryByID(ctx context.Context, id int64) (Repository, error) {
	var out Repository
	err := c.do(ctx, http.MethodGet, "/repositories/"+strconv.FormatInt(id, 10), nil, &out)

	return out, err
}

// maxListedRepositories bounds ListRepositories; past it the dashboard
// asks people to search instead.
const maxListedRepositories = 1000

// ListRepositories lists repositories the installation can access.
func (c *Client) ListRepositories(ctx context.Context) ([]Repository, error) {
	var all []Repository
	for page := 1; len(all) < maxListedRepositories; page++ {
		var out struct {
			Repositories []Repository `json:"repositories"`
		}
		if err := c.do(ctx, http.MethodGet, "/installation/repositories?per_page=100&page="+strconv.Itoa(page), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		if len(out.Repositories) < 100 {
			break
		}
	}

	return all, nil
}

// TeamMember reports whether user is an active member of an organization
// team, child teams included.
func (c *Client) TeamMember(ctx context.Context, org, team, user string) (bool, error) {
	var out struct {
		State string `json:"state"`
	}
	path := "/orgs/" + url.PathEscape(org) + "/teams/" + url.PathEscape(team) + "/memberships/" + url.PathEscape(user)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return out.State == "active", nil
}

// RepoRoles returns the user's role on a repository and the base
// permission it implies (admin, write, read or none), both lowercase.
func (c *Client) RepoRoles(ctx context.Context, repo Repo, user string) ([]string, error) {
	var out struct {
		Permission string `json:"permission"`
		RoleName   string `json:"role_name"`
	}
	err := c.do(ctx, http.MethodGet, repo.path()+"/collaborators/"+url.PathEscape(user)+"/permission", nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return []string{strings.ToLower(out.RoleName), strings.ToLower(out.Permission)}, nil
}

// Entry is one item of a directory listing.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type is file, dir, symlink or submodule.
	Type string `json:"type"`
	Size int    `json:"size"`
	SHA  string `json:"sha"`
}

// ListDirectory lists a directory at ref (empty means the default branch).
// A missing directory, or a path that is a file, lists as empty.
func (c *Client) ListDirectory(ctx context.Context, repo Repo, path, ref string) ([]Entry, error) {
	endpoint := repo.path() + "/contents/" + escapePath(path)
	if ref != "" {
		endpoint += "?" + url.Values{"ref": {ref}}.Encode()
	}
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, endpoint, nil, &raw)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || raw[0] != '[' {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode %s listing: %w", path, err)
	}

	return entries, nil
}
