package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agent/fake"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// controlPlane records sync requests and answers with scripted responses.
type controlPlane struct {
	mu        sync.Mutex
	requests  []agentproto.SyncRequest
	responses []agentproto.SyncResponse
}

func (c *controlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer asd_test" {
		w.WriteHeader(http.StatusUnauthorized)

		return
	}
	var req agentproto.SyncRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	c.mu.Lock()
	c.requests = append(c.requests, req)
	resp := agentproto.SyncResponse{DataPlaneID: "dp-1"}
	if len(c.responses) > 0 {
		resp = c.responses[0]
		resp.DataPlaneID = "dp-1"
		c.responses = c.responses[1:]
	}
	c.mu.Unlock()
	_ = json.NewEncoder(w).Encode(resp)
}

func setup(t *testing.T, responses ...agentproto.SyncResponse) (*agent.Agent, *fake.Provider, *controlPlane) {
	t.Helper()
	cp := &controlPlane{responses: responses}
	server := httptest.NewServer(cp)
	t.Cleanup(server.Close)
	provider := fake.New()
	a := agent.New(agent.NewClient(server.URL, "asd_test", nil), provider, agent.Config{Version: "test"}, slog.New(slog.DiscardHandler))

	return a, provider, cp
}

func TestFirstSyncReportsUnknownInventory(t *testing.T) {
	t.Parallel()
	a, _, cp := setup(t)
	if _, err := a.Step(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if !cp.requests[0].InventoryUnknown || cp.requests[0].Capacity != 0 {
		t.Fatalf("first sync must not claim to know the inventory: %+v", cp.requests[0])
	}
}

func TestListFailureIsNeverReportedAsEmpty(t *testing.T) {
	t.Parallel()
	a, provider, cp := setup(t)
	ctx := context.Background()
	if _, err := a.Step(ctx, 0); err != nil { // learn the data plane
		t.Fatal(err)
	}
	provider.FailList = true
	if _, err := a.Step(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if req := cp.requests[1]; !req.InventoryUnknown || req.Capacity != 0 {
		t.Fatalf("a failed listing was reported as a complete one: %+v", req)
	}
}

func TestLaunchTerminateAndFailureReporting(t *testing.T) {
	t.Parallel()
	ok := agentproto.Launch{RunnerID: "r-ok", RunnerName: "airship-ok"}
	bad := agentproto.Launch{RunnerID: "r-bad", RunnerName: "airship-bad"}
	a, provider, cp := setup(t,
		agentproto.SyncResponse{}, // first contact
		agentproto.SyncResponse{Launch: []agentproto.Launch{ok, bad}},
		agentproto.SyncResponse{Terminate: []agentproto.Terminate{{RunnerID: "r-ok", InstanceID: "fake-1"}}},
	)
	provider.FailLaunch = func(l agentproto.Launch) error {
		if l.RunnerID == "r-bad" {
			return &agent.LaunchError{Err: errors.New("no capacity"), Retryable: true}
		}

		return nil
	}
	ctx := context.Background()
	for range 3 {
		if _, err := a.Step(ctx, 0); err != nil {
			t.Fatal(err)
		}
	}
	third := cp.requests[2]
	if len(third.Instances) != 1 || third.Instances[0].RunnerID != "r-ok" {
		t.Fatalf("launched instance not reported: %+v", third.Instances)
	}
	if len(third.Failures) != 1 || third.Failures[0].RunnerID != "r-bad" || !third.Failures[0].Retryable {
		t.Fatalf("failure not reported: %+v", third.Failures)
	}
	if len(provider.Instances()) != 0 {
		t.Fatal("terminate was not applied")
	}
	if _, err := a.Step(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if len(cp.requests[3].Failures) != 0 {
		t.Fatal("a delivered failure was reported twice")
	}
}

func TestBadTokenIsAnError(t *testing.T) {
	t.Parallel()
	cp := &controlPlane{}
	server := httptest.NewServer(cp)
	t.Cleanup(server.Close)
	_, err := agent.NewClient(server.URL, "asd_wrong", nil).Sync(context.Background(), agentproto.SyncRequest{})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestReceivedLaunchesAreAcknowledged(t *testing.T) {
	t.Parallel()
	launch := agentproto.Launch{RunnerID: "r-1", RunnerName: "airship-1"}
	a, _, cp := setup(t, agentproto.SyncResponse{}, agentproto.SyncResponse{Launch: []agentproto.Launch{launch}})
	ctx := context.Background()
	for range 3 {
		if _, err := a.Step(ctx, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := cp.requests[1].Received; got == nil || len(got) != 0 {
		t.Fatalf("before any launch Received = %#v, want an empty list (not omitted)", got)
	}
	if got := cp.requests[2].Received; len(got) != 1 || got[0] != "r-1" {
		t.Fatalf("Received = %v, want [r-1]", got)
	}
}
