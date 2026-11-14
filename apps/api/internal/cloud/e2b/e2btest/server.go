// Package e2btest is a fake of E2B: the sandbox API (create with an
// idempotency key, get, list by metadata, kill) and each sandbox's daemon
// (health, file upload, and the Connect process service's List and Start),
// served on one test server. A sandbox's daemon lives under
// /envd/<sandbox ID>.
package e2btest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Process is a process started through the daemon.
type Process struct {
	Cmd  string
	Args []string
	Tag  string
	PID  uint32
	User string
}

// Sandbox is a fake sandbox.
type Sandbox struct {
	SandboxID       string            `json:"sandboxID"`
	TemplateID      string            `json:"templateID"`
	State           string            `json:"state"`
	Metadata        map[string]string `json:"metadata"`
	EnvdAccessToken string            `json:"envdAccessToken"`
	StartedAt       string            `json:"startedAt"`
	// Request is the create body it was made from.
	Request map[string]any `json:"-"`
	// Files are the uploaded files, by path, and FileUsers who owns each.
	Files     map[string]string `json:"-"`
	FileUsers map[string]string `json:"-"`
	Processes []Process         `json:"-"`
}

// Server is the fake.
type Server struct {
	URL string
	Key string

	mu          sync.Mutex
	sandboxes   map[string]*Sandbox
	order       []string
	idempotency map[string]string
	next        int
	creates     int
	// LoseCreateAnswer makes the next create make the sandbox and answer
	// 502.
	LoseCreateAnswer bool
	// FailCreate answers creates with this status and error code.
	FailCreate     int
	FailCreateCode string
	// FailStart makes Process/Start end the stream with an error.
	FailStart bool
	// UnhealthyFor makes a new sandbox's daemon answer 502 to this many
	// health checks first.
	UnhealthyFor int
	unhealthy    map[string]int
	// PageSize caps list pages.
	PageSize int
	// CreatePaths records the path of every create request.
	CreatePaths []string
}

// New starts a fake.
func New(t *testing.T, key string) *Server {
	t.Helper()
	s := &Server{Key: key, sandboxes: map[string]*Sandbox{}, idempotency: map[string]string{}, unhealthy: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// EnvdURL is the daemon URL of a sandbox.
func (s *Server) EnvdURL(id string) string { return s.URL + "/envd/" + id }

// Sandboxes returns every sandbox still alive, oldest first.
func (s *Server) Sandboxes() []Sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Sandbox
	for _, id := range s.order {
		if sb, ok := s.sandboxes[id]; ok {
			out = append(out, *sb)
		}
	}

	return out
}

// Creates is how many sandboxes creation made.
func (s *Server) Creates() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.creates
}

// Add puts a sandbox in the team directly.
func (s *Server) Add(sb Sandbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.Files == nil {
		sb.Files, sb.FileUsers = map[string]string{}, map[string]string{}
	}
	s.sandboxes[sb.SandboxID] = &sb
	s.order = append(s.order, sb.SandboxID)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if rest, ok := strings.CutPrefix(r.URL.Path, "/envd/"); ok {
		id, path, _ := strings.Cut(rest, "/")
		s.envd(w, r, id, "/"+path)

		return
	}
	if r.Header.Get("X-Api-Key") != s.Key {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": "Invalid API key"})

		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case (r.URL.Path == "/v2/sandboxes" || r.URL.Path == "/sandboxes") && r.Method == http.MethodPost:
		s.CreatePaths = append(s.CreatePaths, r.URL.Path)
		s.create(w, r)
	case r.URL.Path == "/v2/sandboxes" && r.Method == http.MethodGet:
		s.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/"):
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		sb, ok := s.sandboxes[id]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "sandbox not found"})

			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, sb)
		case http.MethodDelete:
			delete(s.sandboxes, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "no such route"})
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if s.FailCreate != 0 {
		writeJSON(w, s.FailCreate, map[string]any{"code": s.FailCreate, "message": "create refused", "error_code": s.FailCreateCode})

		return
	}
	key := r.Header.Get("Idempotency-Key")
	if id, ok := s.idempotency[key]; ok && key != "" {
		writeJSON(w, http.StatusCreated, s.sandboxes[id])

		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "message": "bad json"})

		return
	}
	if timeout, _ := body["timeout"].(float64); timeout > 86400 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "message": "timeout cannot be greater than 24 hours"})

		return
	}
	s.next++
	sb := &Sandbox{
		SandboxID:       fmt.Sprintf("i%07d", s.next),
		State:           "running",
		Metadata:        map[string]string{},
		EnvdAccessToken: fmt.Sprintf("token-%d", s.next),
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		Request:         body,
		Files:           map[string]string{},
		FileUsers:       map[string]string{},
	}
	sb.TemplateID, _ = body["templateID"].(string)
	if meta, ok := body["metadata"].(map[string]any); ok {
		for k, v := range meta {
			sb.Metadata[k], _ = v.(string)
		}
	}
	s.sandboxes[sb.SandboxID] = sb
	s.order = append(s.order, sb.SandboxID)
	s.unhealthy[sb.SandboxID] = s.UnhealthyFor
	s.creates++
	if key != "" {
		s.idempotency[key] = sb.SandboxID
	}
	if s.LoseCreateAnswer {
		s.LoseCreateAnswer = false
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": 502, "message": "bad gateway"})

		return
	}
	writeJSON(w, http.StatusCreated, sb)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := map[string]string{}
	if raw := q.Get("metadata"); raw != "" {
		parsed, err := url.ParseQuery(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "message": "bad metadata"})

			return
		}
		for k := range parsed {
			filter[k] = parsed.Get(k)
		}
	}
	states := strings.Split(q.Get("state"), ",")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if s.PageSize > 0 && (limit == 0 || limit > s.PageSize) {
		limit = s.PageSize
	}
	offset, _ := strconv.Atoi(q.Get("nextToken"))
	var matched []*Sandbox
	for _, id := range s.order {
		sb, ok := s.sandboxes[id]
		if !ok || (q.Get("state") != "" && !slices.Contains(states, sb.State)) {
			continue
		}
		match := true
		for k, v := range filter {
			match = match && sb.Metadata[k] == v
		}
		if match {
			matched = append(matched, sb)
		}
	}
	matched = matched[min(offset, len(matched)):]
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
		w.Header().Set("X-Next-Token", strconv.Itoa(offset+limit))
	}
	if matched == nil {
		matched = []*Sandbox{}
	}
	writeJSON(w, http.StatusOK, matched)
}

func (s *Server) envd(w http.ResponseWriter, r *http.Request, id, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sandboxes[id]
	if !ok {
		http.Error(w, "sandbox not found", http.StatusBadGateway)

		return
	}
	if path == "/health" {
		if s.unhealthy[id] > 0 {
			s.unhealthy[id]--
			http.Error(w, "not ready", http.StatusBadGateway)

			return
		}
		w.WriteHeader(http.StatusNoContent)

		return
	}
	if r.Header.Get("X-Access-Token") != sb.EnvdAccessToken {
		http.Error(w, "invalid access token", http.StatusUnauthorized)

		return
	}
	user, _, _ := r.BasicAuth()
	switch path {
	case "/files":
		data, _ := io.ReadAll(r.Body)
		p := r.URL.Query().Get("path")
		sb.Files[p] = string(data)
		sb.FileUsers[p] = r.URL.Query().Get("username")
		writeJSON(w, http.StatusOK, []map[string]string{{"path": p}})
	case "/process.Process/List":
		var procs []map[string]any
		for _, proc := range sb.Processes {
			procs = append(procs, map[string]any{"pid": proc.PID, "tag": proc.Tag, "config": map[string]any{"cmd": proc.Cmd, "args": proc.Args}})
		}
		writeJSON(w, http.StatusOK, map[string]any{"processes": procs})
	case "/process.Process/Start":
		s.start(w, r, sb, user)
	default:
		http.Error(w, "no such route", http.StatusNotFound)
	}
}

func (s *Server) start(w http.ResponseWriter, r *http.Request, sb *Sandbox, user string) {
	if r.Header.Get("Content-Type") != "application/connect+json" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)

		return
	}
	var head [5]byte
	if _, err := io.ReadFull(r.Body, head[:]); err != nil {
		http.Error(w, "no envelope", http.StatusBadRequest)

		return
	}
	msg := make([]byte, binary.BigEndian.Uint32(head[1:]))
	if _, err := io.ReadFull(r.Body, msg); err != nil {
		http.Error(w, "short envelope", http.StatusBadRequest)

		return
	}
	var req struct {
		Process struct {
			Cmd  string   `json:"cmd"`
			Args []string `json:"args"`
		} `json:"process"`
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(msg, &req); err != nil {
		http.Error(w, "bad message", http.StatusBadRequest)

		return
	}
	w.Header().Set("Content-Type", "application/connect+json")
	w.WriteHeader(http.StatusOK)
	if s.FailStart {
		writeFrame(w, 0x02, map[string]any{"error": map[string]string{"code": "internal", "message": "failed to start process"}})

		return
	}
	pid := uint32(100 + len(sb.Processes)) //nolint:gosec // a handful
	sb.Processes = append(sb.Processes, Process{Cmd: req.Process.Cmd, Args: req.Process.Args, Tag: req.Tag, PID: pid, User: user})
	writeFrame(w, 0, map[string]any{"event": map[string]any{"start": map[string]any{"pid": pid}}})
	writeFrame(w, 0, map[string]any{"event": map[string]any{"keepalive": map[string]any{}}})
}

func writeFrame(w io.Writer, flags byte, v any) {
	data, _ := json.Marshal(v)
	head := []byte{flags, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(head[1:], uint32(len(data))) //nolint:gosec // small
	_, _ = w.Write(append(head, data...))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
