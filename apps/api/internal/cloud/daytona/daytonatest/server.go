// Package daytonatest is a fake Daytona API for tests: enough of the
// sandbox API, the API key record, regions and a sandbox's toolbox for the
// provider to run against through the real generated clients, the way
// gcptest stands in for Compute Engine. It models the states a sandbox
// goes through after creation, name uniqueness, labels, the process
// session, and the ways a response can go missing.
package daytonatest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Sandbox is a fake sandbox, as the server holds it.
type Sandbox struct {
	ID        string
	Name      string
	Target    string
	State     string
	Labels    map[string]string
	CreatedAt time.Time
	// Body is the create request as received.
	Body map[string]any
	// Commands are the session commands started, in order; Executed the
	// synchronous commands run.
	Commands []string
	Executed []string
	// Script is the boot script the provider wrote, decoded.
	Script string
	reads  int
}

// HTTPError is a response to fail a request with.
type HTTPError struct {
	Status  int
	Message string
}

// Server is the fake API.
type Server struct {
	// URL is the endpoint to give the provider.
	URL string

	mu        sync.Mutex
	sandboxes map[string]*Sandbox // by ID
	nextID    int
	// Creates and Deletes count sandbox create and delete requests.
	Creates int
	Deletes int
	// Labels counts label replacements.
	Labels int

	// OrganizationID and Permissions are what the key record reports.
	OrganizationID string
	Permissions    []string
	// ExpiresAt, when set, is the key's expiry.
	ExpiresAt time.Time
	// Regions the organization can use.
	Regions []string
	// BadKeys are rejected with 401.
	BadKeys map[string]bool

	// StartAfterReads is how many reads a new sandbox takes to be
	// started (0: started at once).
	StartAfterReads int
	// FailBuild makes new sandboxes end in build_failed.
	FailBuild bool
	// HangStart keeps new sandboxes creating forever.
	HangStart bool
	// CreateFailure, when set, fails the next create.
	CreateFailure *HTTPError
	// DropNextCreate creates the sandbox but closes the connection
	// without answering.
	DropNextCreate bool
	// FailToolbox fails every toolbox request with a 500.
	FailToolbox bool
	// FailLabels fails label replacements with a 500.
	FailLabels bool
	// PageSize is how many sandboxes a list page holds (default 200).
	PageSize int

	server *httptest.Server
}

// New starts a fake with one organization and the given regions.
func New(t *testing.T, regions ...string) *Server {
	t.Helper()
	s := &Server{
		sandboxes:       map[string]*Sandbox{},
		OrganizationID:  "org-0000-1111",
		Permissions:     []string{"write:sandboxes", "delete:sandboxes", "read:volumes"},
		Regions:         regions,
		BadKeys:         map[string]bool{},
		StartAfterReads: 1,
		PageSize:        200,
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.server.URL
	t.Cleanup(s.server.Close)

	return s
}

// Sandbox returns a copy of the sandbox with that ID or name, or nil.
func (s *Server) Sandbox(idOrName string) *Sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.find(idOrName)
	if sb == nil {
		return nil
	}
	out := *sb
	out.Labels = copyMap(sb.Labels)
	out.Commands = append([]string(nil), sb.Commands...)
	out.Executed = append([]string(nil), sb.Executed...)

	return &out
}

// Sandboxes returns every sandbox that is not destroyed, oldest first.
func (s *Server) Sandboxes() []Sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Sandbox
	for _, sb := range s.sandboxes {
		if sb.State != "destroyed" {
			out = append(out, *sb)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}

// Add puts a sandbox in the server as another launch, or a hand, would
// have: a name, labels, a state and a creation time. It returns the ID.
func (s *Server) Add(name, target, state string, labels map[string]string, createdAt time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.create(name, target, labels, map[string]any{})
	sb.State = state
	sb.CreatedAt = createdAt
	sb.reads = s.StartAfterReads

	return sb.ID
}

// SetState changes a sandbox's state, as Daytona or a hand would.
func (s *Server) SetState(idOrName, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb := s.find(idOrName); sb != nil {
		sb.State = state
	}
}

func (s *Server) find(idOrName string) *Sandbox {
	if sb, ok := s.sandboxes[idOrName]; ok {
		return sb
	}
	for _, sb := range s.sandboxes {
		if sb.Name == idOrName && sb.State != "destroyed" {
			return sb
		}
	}

	return nil
}

func (s *Server) create(name, target string, labels map[string]string, body map[string]any) *Sandbox {
	s.nextID++
	id := fmt.Sprintf("sbx-%08d-0000-4000-8000-%012d", s.nextID, s.nextID)
	sb := &Sandbox{ID: id, Name: name, Target: target, State: "creating", Labels: copyMap(labels), CreatedAt: time.Now(), Body: body}
	if name == "" {
		sb.Name = id
	}
	s.sandboxes[id] = sb

	return sb
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	bad := auth == "" || s.BadKeys[auth]
	s.mu.Unlock()
	if bad {
		fail(w, http.StatusUnauthorized, "Unauthorized")

		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api-keys/current" && r.Method == http.MethodGet:
		s.apiKey(w, auth)
	case path == "/regions" && r.Method == http.MethodGet:
		s.regions(w)
	case path == "/sandbox" && r.Method == http.MethodPost:
		s.createSandbox(w, r)
	case path == "/sandbox" && r.Method == http.MethodGet:
		s.listSandboxes(w, r)
	case strings.HasPrefix(path, "/sandbox/"):
		s.sandbox(w, r, strings.TrimPrefix(path, "/sandbox/"))
	case strings.HasPrefix(path, "/toolbox/"):
		s.toolbox(w, r, strings.TrimPrefix(path, "/toolbox/"))
	default:
		fail(w, http.StatusNotFound, "Cannot "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) apiKey(w http.ResponseWriter, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	masked := key
	if len(masked) > 6 {
		masked = masked[:3] + "***" + masked[len(masked)-3:]
	}
	out := map[string]any{
		"name": "ci-key", "value": masked, "createdAt": time.Now().Add(-time.Hour).Format(time.RFC3339),
		"permissions": s.Permissions, "lastUsedAt": nil, "expiresAt": nil, "userId": "user_01", "organizationId": s.OrganizationID,
	}
	if !s.ExpiresAt.IsZero() {
		out["expiresAt"] = s.ExpiresAt.Format(time.RFC3339)
	}
	if s.OrganizationID == "" {
		delete(out, "organizationId")
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) regions(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, id := range s.Regions {
		out = append(out, map[string]any{"id": id, "name": id, "regionType": "shared", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid body")

		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Creates++
	if s.CreateFailure != nil {
		f := *s.CreateFailure
		s.CreateFailure = nil
		fail(w, f.Status, f.Message)

		return
	}
	name, _ := body["name"].(string)
	if name != "" && s.find(name) != nil {
		fail(w, http.StatusConflict, "Sandbox with name "+name+" already exists")

		return
	}
	if _, hasSnapshot := body["snapshot"]; hasSnapshot {
		for _, key := range []string{"cpu", "memory", "disk"} {
			if _, set := body[key]; set {
				fail(w, http.StatusBadRequest, "Cannot specify Sandbox resources when using a snapshot")

				return
			}
		}
	}
	if gpu, _ := body["gpu"].(float64); gpu == 0 {
		if spot, _ := body["spot"].(bool); spot {
			fail(w, http.StatusBadRequest, "Spot sandboxes require GPUs")

			return
		}
	}
	target, _ := body["target"].(string)
	labels := map[string]string{}
	if raw, ok := body["labels"].(map[string]any); ok {
		for k, v := range raw {
			labels[k] = fmt.Sprint(v)
		}
	}
	sb := s.create(name, target, labels, body)
	if s.HangStart {
		sb.reads = -1
	} else {
		sb.reads = s.StartAfterReads
	}
	if s.FailBuild {
		sb.State = "build_failed"
		sb.reads = -1
	}
	if s.DropNextCreate {
		s.DropNextCreate = false
		hijackClose(w)

		return
	}
	writeJSON(w, http.StatusOK, s.render(sb))
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]string{}
	if raw := r.URL.Query().Get("labels"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			fail(w, http.StatusBadRequest, "labels must be JSON")

			return
		}
	}
	var all []*Sandbox
	for _, sb := range s.sandboxes {
		if sb.State == "destroyed" {
			continue
		}
		match := true
		for k, v := range want {
			match = match && sb.Labels[k] == v
		}
		if match {
			all = append(all, sb)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		start, _ = strconv.Atoi(cursor)
	}
	size := s.PageSize
	if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && limit > 0 && limit < size {
		size = limit
	}
	end := min(start+size, len(all))
	items := []map[string]any{}
	for _, sb := range all[start:end] {
		items = append(items, s.render(sb))
	}
	var next any
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request, rest string) {
	idOrName, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	sb := s.find(idOrName)
	if sb == nil || (sb.State == "destroyed" && sub == "") {
		fail(w, http.StatusNotFound, "Sandbox with ID or name "+idOrName+" not found")

		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		s.advance(sb)
		writeJSON(w, http.StatusOK, s.render(sb))
	case sub == "" && r.Method == http.MethodDelete:
		s.Deletes++
		sb.State = "destroyed"
		writeJSON(w, http.StatusOK, s.render(sb))
	case sub == "labels" && r.Method == http.MethodPut:
		s.Labels++
		if s.FailLabels {
			fail(w, http.StatusInternalServerError, "Internal server error")

			return
		}
		var body struct {
			Labels map[string]string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid body")

			return
		}
		sb.Labels = copyMap(body.Labels)
		writeJSON(w, http.StatusOK, body)
	default:
		fail(w, http.StatusNotFound, "Cannot "+r.Method+" "+r.URL.Path)
	}
}

// advance moves a new sandbox towards started, one read at a time.
func (s *Server) advance(sb *Sandbox) {
	if sb.State != "creating" || sb.reads < 0 {
		return
	}
	if sb.reads == 0 {
		sb.State = "started"

		return
	}
	sb.reads--
}

func (s *Server) toolbox(w http.ResponseWriter, r *http.Request, rest string) {
	id, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailToolbox {
		fail(w, http.StatusInternalServerError, "toolbox unavailable")

		return
	}
	sb := s.sandboxes[id]
	if sb == nil || sb.State != "started" {
		fail(w, http.StatusBadGateway, "sandbox is not started")

		return
	}
	switch {
	case sub == "process/session" && r.Method == http.MethodPost:
		var body struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sb.Labels["_session:"+body.SessionID] != "" {
			fail(w, http.StatusConflict, "session already exists")

			return
		}
		sb.Labels["_session:"+body.SessionID] = "1"
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(sub, "process/session/") && strings.HasSuffix(sub, "/exec") && r.Method == http.MethodPost:
		var body struct {
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sb.Commands = append(sb.Commands, body.Command)
		writeJSON(w, http.StatusAccepted, map[string]any{"cmdId": fmt.Sprintf("cmd-%d", len(sb.Commands))})
	case strings.HasPrefix(sub, "process/session/") && r.Method == http.MethodGet:
		sessionID := strings.TrimPrefix(sub, "process/session/")
		if sb.Labels["_session:"+sessionID] == "" {
			fail(w, http.StatusNotFound, "session not found")

			return
		}
		commands := []map[string]any{}
		for i, c := range sb.Commands {
			commands = append(commands, map[string]any{"id": fmt.Sprintf("cmd-%d", i+1), "command": c, "exitCode": nil})
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "commands": commands})
	case sub == "process/execute" && r.Method == http.MethodPost:
		var body struct {
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sb.Executed = append(sb.Executed, body.Command)
		if script, ok := decodeScript(body.Command); ok {
			sb.Script = script
		}
		writeJSON(w, http.StatusOK, map[string]any{"exitCode": 0, "result": ""})
	default:
		fail(w, http.StatusNotFound, "Cannot "+r.Method+" "+r.URL.Path)
	}
}

// decodeScript recovers the boot script from the command that writes it.
func decodeScript(command string) (string, bool) {
	_, after, ok := strings.Cut(command, "printf '%s' '")
	if !ok {
		return "", false
	}
	encoded, _, ok := strings.Cut(after, "'")
	if !ok {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}

	return string(raw), true
}

func (s *Server) render(sb *Sandbox) map[string]any {
	labels := map[string]string{}
	for k, v := range sb.Labels {
		if !strings.HasPrefix(k, "_") {
			labels[k] = v
		}
	}
	out := map[string]any{
		"id": sb.ID, "organizationId": s.OrganizationID, "name": sb.Name, "user": "daytona", "target": sb.Target,
		"state": sb.State, "labels": labels, "createdAt": sb.CreatedAt.UTC().Format(time.RFC3339),
		"cpu": 2, "memory": 4, "disk": 10, "gpu": 0, "public": false, "networkBlockAll": false, "kvm": false,
		// As the real service reports it: the proxy without the sandbox.
		"env": map[string]string{}, "toolboxProxyUrl": s.URL + "/toolbox",
	}
	if sb.State == "build_failed" {
		out["errorReason"] = "image build failed: manifest unknown"
	}

	return out
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"statusCode": status, "message": message, "error": http.StatusText(status)})
}

// hijackClose drops the connection without a response.
func hijackClose(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()

			return
		}
	}
	w.WriteHeader(http.StatusInternalServerError)
}
