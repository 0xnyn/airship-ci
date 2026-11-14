// Package namespacetest is a fake of Namespace's Compute API, enough of it
// for the provider: ComputeService's CreateInstance, DescribeInstance,
// ListInstances (label filters, cursor paging) and DestroyInstance, and
// CommandService's RunCommandSync, which runs the command's shell against
// the fake container's files (only the write the provider sends). Connect
// unary calls are JSON POSTs; errors are Connect error bodies.
package namespacetest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Instance is a fake instance.
type Instance struct {
	ID     string
	Status string
	Labels map[string]string
	// Request is the create body it was made from.
	Request map[string]any
	// Files are the files written into its container, by path, decoded from
	// the command's AIRSHIP_BOOT.
	Files map[string]string
	// Commands are the argv of every RunCommandSync.
	Commands [][]string
	reads    int
}

// Server is the fake.
type Server struct {
	URL   string
	Token string

	mu        sync.Mutex
	instances map[string]*Instance
	order     []string
	next      int
	creates   int
	// LoseCreateAnswer makes the next create make the instance and answer
	// 503.
	LoseCreateAnswer bool
	// FailCreate answers creates with this Connect code.
	FailCreate string
	// StartAs is the status an instance reaches on its second read.
	StartAs string
	// FailCommands answers this many RunCommandSync calls with an error
	// first, as a container still starting.
	FailCommands int
	// PageSize caps list pages.
	PageSize int
}

// New starts a fake.
func New(t *testing.T, token string) *Server {
	t.Helper()
	s := &Server{Token: token, instances: map[string]*Instance{}, StartAs: "RUNNING"}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// Instances returns every instance not destroyed, oldest first.
func (s *Server) Instances() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Instance
	for _, id := range s.order {
		if inst := s.instances[id]; inst.Status != "DESTROYED" {
			out = append(out, *inst)
		}
	}

	return out
}

// Creates is how many instances creation made.
func (s *Server) Creates() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.creates
}

// Add puts an instance in the workspace directly.
func (s *Server) Add(inst Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst.Files == nil {
		inst.Files = map[string]string{}
	}
	s.instances[inst.ID] = &inst
	s.order = append(s.order, inst.ID)
}

func connectError(w http.ResponseWriter, code string, message string) {
	status := map[string]int{
		"not_found": 404, "unauthenticated": 401, "permission_denied": 403, "invalid_argument": 400,
		"unavailable": 503, "resource_exhausted": 429, "failed_precondition": 400, "internal": 500,
	}[code]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Connect-Protocol-Version") != "1" {
		connectError(w, "invalid_argument", "not a Connect unary JSON call")

		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		connectError(w, "unauthenticated", "invalid token")

		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		connectError(w, "invalid_argument", "bad json")

		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch {
	case strings.HasSuffix(r.URL.Path, "ComputeService/"+method):
		s.compute(w, method, body)
	case r.URL.Path == "/namespace.cloud.compute.v1beta.CommandService/RunCommandSync":
		s.runCommand(w, body)
	default:
		connectError(w, "not_found", "no such procedure")
	}
}

func (s *Server) compute(w http.ResponseWriter, method string, body map[string]any) {
	switch method {
	case "CreateInstance":
		if s.FailCreate != "" {
			connectError(w, s.FailCreate, "create refused")

			return
		}
		s.next++
		inst := &Instance{ID: fmt.Sprintf("ns%05d", s.next), Status: "CREATING", Labels: map[string]string{}, Request: body, Files: map[string]string{}}
		labels, _ := body["labels"].([]any)
		for _, raw := range labels {
			l, _ := raw.(map[string]any)
			name, _ := l["name"].(string)
			inst.Labels[name], _ = l["value"].(string)
		}
		s.instances[inst.ID] = inst
		s.order = append(s.order, inst.ID)
		s.creates++
		if s.LoseCreateAnswer {
			s.LoseCreateAnswer = false
			connectError(w, "unavailable", "upstream reset")

			return
		}
		writeJSON(w, s.describe(inst))
	case "DescribeInstance":
		id, _ := body["instanceId"].(string)
		inst, ok := s.instances[id]
		if !ok {
			connectError(w, "not_found", "instance not found")

			return
		}
		inst.reads++
		if inst.Status == "CREATING" && inst.reads >= 2 {
			inst.Status = s.StartAs
		}
		writeJSON(w, s.describe(inst))
	case "ListInstances":
		s.list(w, body)
	case "DestroyInstance":
		id, _ := body["instanceId"].(string)
		inst, ok := s.instances[id]
		if !ok || inst.Status == "DESTROYED" {
			connectError(w, "not_found", "instance not found")

			return
		}
		inst.Status = "DESTROYED"
		writeJSON(w, map[string]any{})
	default:
		connectError(w, "not_found", "no such procedure")
	}
}

func (s *Server) describe(inst *Instance) map[string]any {
	return map[string]any{
		"metadata":         s.metadata(inst),
		"extendedMetadata": map[string]any{"commandServiceEndpoint": s.URL},
	}
}

func (s *Server) metadata(inst *Instance) map[string]any {
	labels := make([]map[string]string, 0, len(inst.Labels))
	for k, v := range inst.Labels {
		labels = append(labels, map[string]string{"name": k, "value": v})
	}

	return map[string]any{"instanceId": inst.ID, "status": inst.Status, "labels": labels}
}

func (s *Server) list(w http.ResponseWriter, body map[string]any) {
	filters, _ := body["labelFilter"].([]any)
	offset := 0
	if raw, _ := body["paginationCursor"].(string); raw != "" {
		decoded, _ := base64.StdEncoding.DecodeString(raw)
		offset, _ = strconv.Atoi(string(decoded))
	}
	limit := s.PageSize
	var matched []map[string]any
	for _, id := range s.order {
		inst := s.instances[id]
		if inst.Status == "DESTROYED" {
			continue
		}
		ok := true
		for _, raw := range filters {
			f, _ := raw.(map[string]any)
			name, _ := f["name"].(string)
			value, _ := f["value"].(string)
			ok = ok && f["op"] == "EQUAL" && inst.Labels[name] == value
		}
		if ok {
			matched = append(matched, s.metadata(inst))
		}
	}
	matched = matched[min(offset, len(matched)):]
	out := map[string]any{}
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
		out["paginationCursor"] = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
	}
	out["instances"] = matched
	writeJSON(w, out)
}

func (s *Server) runCommand(w http.ResponseWriter, body map[string]any) {
	id, _ := body["instanceId"].(string)
	inst, ok := s.instances[id]
	if !ok {
		connectError(w, "not_found", "instance not found")

		return
	}
	if s.FailCommands > 0 {
		s.FailCommands--
		connectError(w, "failed_precondition", "container is not running yet")

		return
	}
	cmd, _ := body["command"].(map[string]any)
	var argv []string
	rawArgv, _ := cmd["command"].([]any)
	for _, a := range rawArgv {
		str, _ := a.(string)
		argv = append(argv, str)
	}
	inst.Commands = append(inst.Commands, argv)
	envVars, _ := cmd["envVars"].([]any)
	for _, raw := range envVars {
		e, _ := raw.(map[string]any)
		if e["name"] == "AIRSHIP_BOOT" {
			value, _ := e["value"].(string)
			decoded, _ := base64.StdEncoding.DecodeString(value)
			inst.Files["boot"] = string(decoded)
		}
	}
	writeJSON(w, map[string]any{"exitCode": 0})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
