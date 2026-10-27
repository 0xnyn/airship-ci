// Package fakegithub is an in-memory GitHub for tests. It implements the
// endpoints Airship calls, checks app JWTs against the app's real key, and
// lets tests script failures.
package fakegithub

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// ClientID is the fake app's client ID.
const ClientID = "Iv1.fake"

// Server is a fake GitHub API.
type Server struct {
	*httptest.Server

	key *rsa.PrivateKey

	mu            sync.Mutex
	installations map[int64]github.Installation
	runners       map[int64]*Runner
	nextRunnerID  int64
	jobs          map[int64]github.WorkflowJob
	failures      map[string][]int
	beforeRoute   func(route string)
	deliveries    []github.HookDelivery
	redelivered   []int64
	calls         map[string]int
	users         map[string]userGrant
	orgRoles      map[string]string
	repos         map[string]repoEntry
	files         map[string][]byte
	runs          map[string]github.RunAttempt
	teams         map[string]bool
	repoRoles     map[string][2]string
	branches      map[string]string
	pulls         []PullRequest
	groups        []*RunnerGroup
}

// PullRequest is one opened through the fake.
type PullRequest struct {
	Number int
	Repo   string
	Title  string
	Body   string
	Head   string
	Base   string
}

type repoEntry struct {
	repo         github.Repository
	installation int64
}

// Runner is a registration created through generate-jitconfig. Repo is
// "owner/name", or "org:<login>" for an organization runner.
type Runner struct {
	ID      int64
	Repo    string
	Name    string
	Labels  []string
	Busy    bool
	GroupID int64
	// JITConfig is the opaque config handed out for this runner.
	JITConfig string
}

// RunnerGroup is an organization runner group created through the fake.
type RunnerGroup struct {
	ID    int64
	Org   string
	Name  string
	Repos []int64
}

type userGrant struct {
	user          github.User
	installations []int64
}

// New starts a fake GitHub.
func New(t *testing.T) *Server {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s := &Server{
		key:           key,
		installations: map[int64]github.Installation{},
		runners:       map[int64]*Runner{},
		nextRunnerID:  1000,
		jobs:          map[int64]github.WorkflowJob{},
		failures:      map[string][]int{},
		calls:         map[string]int{},
		users:         map[string]userGrant{},
		orgRoles:      map[string]string{},
		repos:         map[string]repoEntry{},
		files:         map[string][]byte{},
		runs:          map[string]github.RunAttempt{},
		teams:         map[string]bool{},
		repoRoles:     map[string][2]string{},
		branches:      map[string]string{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)

	return s
}

// PrivateKeyPEM is the app key Airship should be configured with.
func (s *Server) PrivateKeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(s.key)})
}

// App returns a real github.App client pointed at the fake.
func (s *Server) App(t *testing.T) *github.App {
	t.Helper()
	app, err := github.NewApp(github.AppConfig{ClientID: ClientID, PrivateKeyPEM: s.PrivateKeyPEM(), APIURL: s.URL})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	return app
}

// AddInstallation registers an installation of the app.
func (s *Server) AddInstallation(inst github.Installation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installations[inst.ID] = inst
}

// SetJob sets what GET /repos/.../actions/jobs/{id} returns.
func (s *Server) SetJob(job github.WorkflowJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
}

// AddRepository makes a repository visible to an installation.
func (s *Server) AddRepository(installationID int64, repo github.Repository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[strings.ToLower(repo.FullName)] = repoEntry{repo: repo, installation: installationID}
}

// SetFile sets a file's content at ref, a branch or commit SHA. Nil
// content removes it.
func (s *Server) SetFile(repo, ref, path string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fileKey(repo, ref, path)
	if content == nil {
		delete(s.files, key)

		return
	}
	s.files[key] = content
}

func fileKey(repo, ref, path string) string {
	return strings.ToLower(repo) + "@" + ref + ":" + path
}

// RunnerGroups lists organization runner groups.
func (s *Server) RunnerGroups() []RunnerGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunnerGroup, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, *g)
	}

	return out
}

// serveOrgActions answers organization runner and runner group calls.
// rest starts after /orgs/{org}/actions/.
func (s *Server) serveOrgActions(w http.ResponseWriter, r *http.Request, org string, rest []string) {
	switch rest[0] {
	case "runner-groups":
		s.serveRunnerGroups(w, r, org, rest[1:])
	case "runners":
		s.serveRepo(w, r, "org:"+strings.ToLower(org), rest)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) serveRunnerGroups(w http.ResponseWriter, r *http.Request, org string, rest []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	org = strings.ToLower(org)
	switch {
	case r.Method == http.MethodGet && len(rest) == 0:
		out := []map[string]any{}
		for _, g := range s.groups {
			if g.Org == org {
				out = append(out, map[string]any{"id": g.ID, "name": g.Name, "visibility": "selected"})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "runner_groups": out})
	case r.Method == http.MethodPost && len(rest) == 0:
		var body struct {
			Name  string  `json:"name"`
			Repos []int64 `json:"selected_repository_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return
		}
		g := &RunnerGroup{ID: int64(100 + len(s.groups)), Org: org, Name: body.Name, Repos: body.Repos}
		s.groups = append(s.groups, g)
		writeJSON(w, http.StatusCreated, map[string]any{"id": g.ID, "name": g.Name, "visibility": "selected"})
	case r.Method == http.MethodPut && len(rest) == 2 && rest[1] == "repositories":
		id, _ := strconv.ParseInt(rest[0], 10, 64)
		var body struct {
			Repos []int64 `json:"selected_repository_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return
		}
		for _, g := range s.groups {
			if g.ID == id && g.Org == org {
				g.Repos = body.Repos
				w.WriteHeader(http.StatusNoContent)

				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

// PullRequests lists pull requests opened through the fake.
func (s *Server) PullRequests() []PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]PullRequest(nil), s.pulls...)
}

// FileAt returns a file's content at ref, as written through the fake.
func (s *Server) FileAt(repo, ref, path string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.files[fileKey(repo, ref, path)]

	return content, ok
}

// SetRunAttempt sets what GET .../actions/runs/{id}/attempts/{n} returns.
func (s *Server) SetRunAttempt(repo string, run github.RunAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[fmt.Sprintf("%s/%d/%d", strings.ToLower(repo), run.ID, max(run.RunAttempt, 1))] = run
}

// SetTeamMember adds or removes an active team member.
func (s *Server) SetTeamMember(org, team, login string, member bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[strings.ToLower(org+"/"+team+"/"+login)] = member
}

// SetRepoRole sets a collaborator's role name and base permission.
func (s *Server) SetRepoRole(repo, login, role, permission string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repoRoles[strings.ToLower(repo+"/"+login)] = [2]string{role, permission}
}

// RetryAfterSeconds is what scripted 429 responses ask clients to wait.
const RetryAfterSeconds = 60

// BeforeRoute runs fn before every repository route is served, outside
// the server's lock, so a test can act (send a webhook, say) at a precise
// point of a call sequence. nil removes it.
func (s *Server) BeforeRoute(fn func(route string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeRoute = fn
}

// AddRunner registers a runner as generate-jitconfig would, for tests
// that place a registration without going through the launcher.
func (s *Server) AddRunner(repo, name string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRunnerID++
	s.runners[s.nextRunnerID] = &Runner{ID: s.nextRunnerID, Repo: repo, Name: name, Labels: []string{"self-hosted"}}

	return s.nextRunnerID
}

// FailNext makes the next calls to a route ("POST generate-jitconfig",
// "GET runner", "DELETE runner", "GET job", "GET membership") answer with
// these statuses.
func (s *Server) FailNext(route string, statuses ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[route] = append(s.failures[route], statuses...)
}

// Calls returns how many times a route was hit.
func (s *Server) Calls(route string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls[route]
}

// Runners returns a snapshot of registered runners.
func (s *Server) Runners() []Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Runner, 0, len(s.runners))
	for _, runner := range s.runners {
		out = append(out, *runner)
	}

	return out
}

// RunnerByJIT finds the registration a JIT config belongs to, as a real
// runner would when it connects.
func (s *Server) RunnerByJIT(config string) (Runner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, runner := range s.runners {
		if runner.JITConfig == config {
			return *runner, true
		}
	}

	return Runner{}, false
}

// SetRunnerBusy marks a runner busy, as when it accepts a job.
func (s *Server) SetRunnerBusy(id int64, busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if runner, ok := s.runners[id]; ok {
		runner.Busy = busy
	}
}

// RemoveRunner deletes a registration, as GitHub does after an ephemeral
// runner's job.
func (s *Server) RemoveRunner(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runners, id)
}

// AddDelivery appends to the app's webhook delivery log.
func (s *Server) AddDelivery(delivery github.HookDelivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveries = append([]github.HookDelivery{delivery}, s.deliveries...)
}

// Redelivered lists delivery ids Airship asked to resend.
func (s *Server) Redelivered() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]int64(nil), s.redelivered...)
}

// SetOrgRole sets a user's organization role ("admin" or "member"); ""
// removes them from the organization.
func (s *Server) SetOrgRole(org, login, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if role == "" {
		delete(s.orgRoles, org+"/"+login)

		return
	}
	s.orgRoles[org+"/"+login] = role
}

// AddUser lets an OAuth code sign in as user, with access to installations.
func (s *Server) AddUser(code string, user github.User, installations ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[code] = userGrant{user: user, installations: installations}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case r.Method == http.MethodPost && path == "/login/oauth/access_token":
		s.oauthToken(w, r)
	case r.Method == http.MethodGet && path == "/user":
		s.withUser(w, r, func(grant userGrant) { writeJSON(w, http.StatusOK, grant.user) })
	case r.Method == http.MethodGet && path == "/user/installations":
		s.withUser(w, r, func(grant userGrant) { s.userInstallations(w, grant) })
	case len(parts) >= 2 && parts[0] == "app":
		if !s.checkJWT(w, r) {
			return
		}
		s.serveApp(w, r, parts)
	case r.Method == http.MethodGet && len(parts) == 4 && parts[0] == "orgs" && parts[2] == "memberships":
		s.mu.Lock()
		s.calls["GET membership"]++
		if queue := s.failures["GET membership"]; len(queue) > 0 {
			s.failures["GET membership"] = queue[1:]
			s.mu.Unlock()
			writeJSON(w, queue[0], map[string]string{"message": "scripted failure"})

			return
		}
		role, ok := s.orgRoles[parts[1]+"/"+parts[3]]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"role": role, "state": "active"})
	case len(parts) >= 3 && (parts[0] == "repos" || parts[0] == "installation" || parts[0] == "orgs"):
		if !strings.HasPrefix(r.Header.Get("Authorization"), "token ghs_") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})

			return
		}
		if parts[0] == "orgs" && len(parts) >= 4 && parts[2] == "actions" {
			s.serveOrgActions(w, r, parts[1], parts[3:])

			return
		}
		if s.serveWrite(w, r, parts) {
			return
		}
		if !s.serveContent(w, r, parts) {
			s.serveRepo(w, r, parts[1]+"/"+parts[2], parts[4:])
		}
	case len(parts) == 2 && parts[0] == "installation" && parts[1] == "repositories":
		s.installationRepositories(w, r)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "repositories":
		s.repositoryByID(w, r, parts[1])
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) serveApp(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "installations" && parts[3] == "access_tokens":
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":      "ghs_" + parts[2],
			"expires_at": time.Now().Add(time.Hour),
		})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "installations":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		s.mu.Lock()
		inst, ok := s.installations[id]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

			return
		}
		writeJSON(w, http.StatusOK, inst)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "hook" && parts[2] == "deliveries":
		s.mu.Lock()
		out := append([]github.HookDelivery(nil), s.deliveries...)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case r.Method == http.MethodPost && len(parts) == 5 && parts[1] == "hook" && parts[4] == "attempts":
		id, _ := strconv.ParseInt(parts[3], 10, 64)
		s.mu.Lock()
		s.redelivered = append(s.redelivered, id)
		s.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

// serveWrite answers the git ref, file write and pull request calls the
// access-control API makes. It reports whether it handled the request.
func (s *Server) serveWrite(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if parts[0] != "repos" || len(parts) < 4 {
		return false
	}
	repo := strings.ToLower(parts[1] + "/" + parts[2])
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, known := s.repos[repo]
	switch {
	case r.Method == http.MethodGet && len(parts) == 7 && parts[3] == "git" && parts[4] == "ref" && parts[5] == "heads":
		branch := parts[6]
		sha, ok := s.branches[repo+"@"+branch]
		if !ok && known && branch == entry.repo.DefaultBranch {
			sha = "sha-" + branch
		}
		if sha == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"sha": sha, "type": "commit"}})
	case r.Method == http.MethodPost && len(parts) == 5 && parts[3] == "git" && parts[4] == "refs":
		var body struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.HasPrefix(body.Ref, "refs/heads/") || body.SHA == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return true
		}
		branch := strings.TrimPrefix(body.Ref, "refs/heads/")
		if _, exists := s.branches[repo+"@"+branch]; exists {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Reference already exists"})

			return true
		}
		s.branches[repo+"@"+branch] = body.SHA
		// The new branch starts with the default branch's files.
		if known {
			for key, content := range s.files {
				if prefix := repo + "@" + entry.repo.DefaultBranch + ":"; strings.HasPrefix(key, prefix) {
					s.files[repo+"@"+branch+":"+strings.TrimPrefix(key, prefix)] = content
				}
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ref": body.Ref, "object": map[string]string{"sha": body.SHA}})
	case r.Method == http.MethodPut && len(parts) >= 5 && parts[3] == "contents":
		var body struct {
			Message string `json:"message"`
			Content string `json:"content"`
			Branch  string `json:"branch"`
			SHA     string `json:"sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" || body.Branch == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return true
		}
		if _, exists := s.branches[repo+"@"+body.Branch]; !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Branch not found"})

			return true
		}
		path := strings.Join(parts[4:], "/")
		if existing, ok := s.files[fileKey(repo, body.Branch, path)]; ok {
			sum := sha256.Sum256(existing)
			if body.SHA != hex.EncodeToString(sum[:20]) {
				writeJSON(w, http.StatusConflict, map[string]string{"message": "sha does not match"})

				return true
			}
		}
		content, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "content is not base64"})

			return true
		}
		s.files[fileKey(repo, body.Branch, path)] = content
		s.branches[repo+"@"+body.Branch] = "sha-" + body.Branch + "-" + strconv.Itoa(len(content))
		writeJSON(w, http.StatusOK, map[string]any{"content": map[string]string{"path": path}})
	case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "pulls":
		var body struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Head  string `json:"head"`
			Base  string `json:"base"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" || body.Head == "" || body.Base == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return true
		}
		if _, exists := s.branches[repo+"@"+body.Head]; !exists {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "head branch not found"})

			return true
		}
		pr := PullRequest{Number: len(s.pulls) + 1, Repo: repo, Title: body.Title, Body: body.Body, Head: body.Head, Base: body.Base}
		s.pulls = append(s.pulls, pr)
		writeJSON(w, http.StatusCreated, map[string]any{"number": pr.Number, "html_url": "https://github.com/" + repo + "/pull/" + strconv.Itoa(pr.Number)})
	default:
		return false
	}

	return true
}

// serveContent answers the repository, file, run, team and role reads
// config files need. It reports whether it handled the request.
func (s *Server) serveContent(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if parts[0] == "repos" && len(parts) >= 5 && parts[3] == "actions" && (len(parts) != 8 || parts[4] != "runs") {
		return false
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

		return true
	}
	route := ""
	switch {
	case parts[0] == "orgs" && len(parts) == 6 && parts[2] == "teams" && parts[4] == "memberships":
		route = "GET team membership"
	case parts[0] == "repos" && len(parts) == 3:
		route = "GET repository"
	case parts[0] == "repos" && len(parts) >= 5 && parts[3] == "contents":
		route = "GET contents"
	case parts[0] == "repos" && len(parts) == 6 && parts[3] == "collaborators" && parts[5] == "permission":
		route = "GET permission"
	case parts[0] == "repos" && len(parts) == 8 && parts[3] == "actions" && parts[4] == "runs" && parts[6] == "attempts":
		route = "GET run attempt"
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[route]++
	if queue := s.failures[route]; len(queue) > 0 {
		s.failures[route] = queue[1:]
		writeJSON(w, queue[0], map[string]string{"message": "scripted failure"})

		return true
	}
	notFound := func() { writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"}) }
	repo := ""
	if parts[0] == "repos" {
		repo = parts[1] + "/" + parts[2]
	}
	entry, known := s.repos[strings.ToLower(repo)]

	switch route {
	case "GET team membership":
		if !s.teams[strings.ToLower(parts[1]+"/"+parts[3]+"/"+parts[5])] {
			notFound()

			return true
		}
		writeJSON(w, http.StatusOK, map[string]string{"state": "active", "role": "member"})
	case "GET repository":
		if !known {
			notFound()

			return true
		}
		writeJSON(w, http.StatusOK, entry.repo)
	case "GET contents":
		ref := r.URL.Query().Get("ref")
		if ref == "" && known {
			ref = entry.repo.DefaultBranch
		}
		content, ok := s.files[fileKey(repo, ref, strings.Join(parts[4:], "/"))]
		if !ok {
			notFound()

			return true
		}
		sum := sha256.Sum256(content)
		writeJSON(w, http.StatusOK, map[string]any{
			"type": "file", "size": len(content), "encoding": "base64",
			"content": base64.StdEncoding.EncodeToString(content), "sha": hex.EncodeToString(sum[:20]),
		})
	case "GET permission":
		role, ok := s.repoRoles[strings.ToLower(repo+"/"+parts[4])]
		if !ok {
			notFound()

			return true
		}
		writeJSON(w, http.StatusOK, map[string]string{"role_name": role[0], "permission": role[1]})
	case "GET run attempt":
		run, ok := s.runs[strings.ToLower(repo)+"/"+parts[5]+"/"+parts[7]]
		if !ok {
			notFound()

			return true
		}
		writeJSON(w, http.StatusOK, run)
	}

	return true
}

func (s *Server) repositoryByID(w http.ResponseWriter, r *http.Request, rawID string) {
	installation, _ := strconv.ParseInt(strings.TrimPrefix(r.Header.Get("Authorization"), "token ghs_"), 10, 64)
	id, _ := strconv.ParseInt(rawID, 10, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.repos {
		if entry.repo.ID == id && entry.installation == installation {
			writeJSON(w, http.StatusOK, entry.repo)

			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (s *Server) installationRepositories(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(strings.TrimPrefix(r.Header.Get("Authorization"), "token ghs_"), 10, 64)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	s.mu.Lock()
	out := []github.Repository{}
	for _, entry := range s.repos {
		if entry.installation == id {
			out = append(out, entry.repo)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b github.Repository) int { return strings.Compare(a.FullName, b.FullName) })
	if page > 1 {
		out = []github.Repository{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "repositories": out})
}

func (s *Server) serveRepo(w http.ResponseWriter, r *http.Request, repo string, rest []string) {
	route := ""
	switch {
	case r.Method == http.MethodPost && len(rest) == 2 && rest[0] == "runners" && rest[1] == "generate-jitconfig":
		route = "POST generate-jitconfig"
	case r.Method == http.MethodGet && len(rest) == 1 && rest[0] == "runners":
		route = "GET runners"
	case r.Method == http.MethodGet && len(rest) == 2 && rest[0] == "runners":
		route = "GET runner"
	case r.Method == http.MethodDelete && len(rest) == 2 && rest[0] == "runners":
		route = "DELETE runner"
	case r.Method == http.MethodGet && len(rest) == 2 && rest[0] == "jobs":
		route = "GET job"
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

		return
	}

	s.mu.Lock()
	before := s.beforeRoute
	s.mu.Unlock()
	if before != nil {
		before(route)
	}
	s.mu.Lock()
	s.calls[route]++
	if queue := s.failures[route]; len(queue) > 0 {
		status := queue[0]
		s.failures[route] = queue[1:]
		s.mu.Unlock()
		if status == http.StatusTooManyRequests {
			// GitHub's secondary rate limit asks clients to wait.
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds))
		}
		writeJSON(w, status, map[string]string{"message": "scripted failure"})

		return
	}
	defer s.mu.Unlock()

	switch route {
	case "POST generate-jitconfig":
		var body struct {
			Name    string   `json:"name"`
			Labels  []string `json:"labels"`
			GroupID int64    `json:"runner_group_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || len(body.Labels) == 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "invalid"})

			return
		}
		for _, existing := range s.runners {
			if existing.Repo == repo && existing.Name == body.Name {
				writeJSON(w, http.StatusConflict, map[string]string{"message": "Already exists"})

				return
			}
		}
		s.nextRunnerID++
		runner := &Runner{
			ID: s.nextRunnerID, Repo: repo, Name: body.Name, Labels: body.Labels, GroupID: body.GroupID,
			JITConfig: base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "jit:%s:%d", body.Name, s.nextRunnerID)),
		}
		s.runners[runner.ID] = runner
		writeJSON(w, http.StatusCreated, map[string]any{
			"runner":             map[string]any{"id": runner.ID, "name": runner.Name, "status": "offline", "busy": false},
			"encoded_jit_config": runner.JITConfig,
		})
	case "GET runners":
		name := r.URL.Query().Get("name")
		out := []map[string]any{}
		for _, runner := range s.runners {
			if runner.Repo == repo && (name == "" || runner.Name == name) {
				out = append(out, map[string]any{"id": runner.ID, "name": runner.Name, "busy": runner.Busy})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "runners": out})
	case "GET runner", "DELETE runner":
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		runner, ok := s.runners[id]
		if !ok || runner.Repo != repo {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

			return
		}
		if route == "GET runner" {
			writeJSON(w, http.StatusOK, map[string]any{"id": runner.ID, "name": runner.Name, "busy": runner.Busy})

			return
		}
		if runner.Busy {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Runner is still running a job"})

			return
		}
		delete(s.runners, id)
		w.WriteHeader(http.StatusNoContent)
	case "GET job":
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		job, ok := s.jobs[id]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})

			return
		}
		writeJSON(w, http.StatusOK, job)
	}
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})

		return
	}
	code := r.PostForm.Get("code")
	s.mu.Lock()
	_, ok := s.users[code]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]string{"error": "bad_verification_code", "error_description": "bad code"})

		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"access_token": "ghu_" + code, "token_type": "bearer"})
}

func (s *Server) withUser(w http.ResponseWriter, r *http.Request, fn func(userGrant)) {
	code, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ghu_")
	s.mu.Lock()
	grant, found := s.users[code]
	s.mu.Unlock()
	if !ok || !found {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})

		return
	}
	fn(grant)
}

func (s *Server) userInstallations(w http.ResponseWriter, grant userGrant) {
	s.mu.Lock()
	out := []github.Installation{}
	for _, id := range grant.installations {
		if inst, ok := s.installations[id]; ok {
			out = append(out, inst)
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(out), "installations": out})
}

// checkJWT verifies the app JWT's RS256 signature, issuer and lifetime, the
// way GitHub does.
func (s *Server) checkJWT(w http.ResponseWriter, r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(token, ".")
	if !ok || len(parts) != 3 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "A JSON web token could not be decoded"})

		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err != nil || rsa.VerifyPKCS1v15(&s.key.PublicKey, crypto.SHA256, digest[:], sig) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "bad signature"})

		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Iss != ClientID ||
		claims.Exp-claims.Iat > 600 || time.Now().Unix() > claims.Exp || claims.Iat > time.Now().Unix() {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "bad claims"})

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
