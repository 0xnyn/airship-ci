// Package runlooptest is a fake of Runloop's devbox API, enough of it for
// the provider: create, get, list by status, shut down and write a file.
// Devboxes start provisioning and run on the next read.
package runlooptest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Devbox is a fake devbox.
type Devbox struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Status         string            `json:"status"`
	Metadata       map[string]string `json:"metadata"`
	CreateTimeMs   int64             `json:"create_time_ms"`
	ShutdownReason string            `json:"shutdown_reason,omitempty"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	// Request is the create body the devbox was made from.
	Request map[string]any `json:"-"`
	// Files are the files written into it, by path.
	Files map[string]string `json:"-"`
}

// Server is the fake.
type Server struct {
	URL string
	// Key is the API key it accepts.
	Key string

	mu      sync.Mutex
	devbox  map[string]*Devbox
	order   []string
	next    int
	creates int
	// LoseCreateAnswer makes the next create make the devbox and answer
	// 502, as a proxy that lost the answer would.
	LoseCreateAnswer bool
	// FailCreate answers creates with this status (0: succeed).
	FailCreate int
	// FailWrites answers file writes with 500.
	FailWrites bool
	// StartAs is the status a devbox reaches on its first read (running by
	// default; failure to simulate a failed start).
	StartAs string
	// PageSize caps list pages, to exercise paging.
	PageSize int
}

// New starts a fake.
func New(t *testing.T, key string) *Server {
	t.Helper()
	s := &Server{Key: key, devbox: map[string]*Devbox{}, StartAs: "running"}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// Devboxes returns every devbox ever made, oldest first.
func (s *Server) Devboxes() []Devbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Devbox, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.devbox[id])
	}

	return out
}

// Creates is how many create calls succeeded at making a devbox.
func (s *Server) Creates() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.creates
}

// Add puts a devbox in the account directly.
func (s *Server) Add(d Devbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Files == nil {
		d.Files = map[string]string{}
	}
	s.devbox[d.ID] = &d
	s.order = append(s.order, d.ID)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.Key {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "invalid api key"})

		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/devboxes")
	switch {
	case path == "" && r.Method == http.MethodGet:
		s.list(w, r)
	case path == "" && r.Method == http.MethodPost:
		s.create(w, r)
	default:
		id, action, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		d, ok := s.devbox[id]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "devbox not found"})

			return
		}
		switch {
		case action == "" && r.Method == http.MethodGet:
			if d.Status == "provisioning" {
				d.Status = s.StartAs
				if d.Status == "failure" {
					d.FailureReason = "health_check_failed"
				}
			}
			writeJSON(w, http.StatusOK, d)
		case action == "shutdown" && r.Method == http.MethodPost:
			if d.Status == "shutdown" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": "devbox is already shut down"})

				return
			}
			d.Status, d.ShutdownReason = "shutdown", "api_shutdown"
			writeJSON(w, http.StatusOK, d)
		case action == "write_file_contents" && r.Method == http.MethodPost:
			if s.FailWrites {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "write failed"})

				return
			}
			if d.Status != "running" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": "devbox is not running"})

				return
			}
			var body struct {
				FilePath string `json:"file_path"`
				Contents string `json:"contents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FilePath == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": "bad request"})

				return
			}
			d.Files[body.FilePath] = body.Contents
			writeJSON(w, http.StatusOK, map[string]any{"exit_status": 0})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "no such route"})
		}
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if s.FailCreate != 0 {
		writeJSON(w, s.FailCreate, map[string]string{"message": "create refused"})

		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "bad json"})

		return
	}
	s.next++
	d := &Devbox{
		ID:           fmt.Sprintf("dbx_%04d", s.next),
		Status:       "provisioning",
		CreateTimeMs: time.Now().UnixMilli(),
		Metadata:     map[string]string{},
		Request:      body,
		Files:        map[string]string{},
	}
	d.Name, _ = body["name"].(string)
	if meta, ok := body["metadata"].(map[string]any); ok {
		for k, v := range meta {
			d.Metadata[k], _ = v.(string)
		}
	}
	s.devbox[d.ID] = d
	s.order = append(s.order, d.ID)
	s.creates++
	if s.LoseCreateAnswer {
		s.LoseCreateAnswer = false
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "bad gateway"})

		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if s.PageSize > 0 && (limit == 0 || limit > s.PageSize) {
		limit = s.PageSize
	}
	after := r.URL.Query().Get("starting_after")
	ids := slices.Clone(s.order)
	slices.Reverse(ids) // newest first
	var matched []*Devbox
	for _, id := range ids {
		d := s.devbox[id]
		if status == "" || d.Status == status {
			matched = append(matched, d)
		}
	}
	if after != "" {
		for i, d := range matched {
			if d.ID == after {
				matched = matched[i+1:]

				break
			}
		}
	}
	more := false
	if limit > 0 && len(matched) > limit {
		matched, more = matched[:limit], true
	}
	writeJSON(w, http.StatusOK, map[string]any{"devboxes": matched, "has_more": more})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
