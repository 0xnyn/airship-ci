// Package e2e_test runs the control plane over HTTP against Postgres, a
// fake GitHub and a real agent loop with an in-memory provider: every hop of
// a job's life except the machine itself.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agent/fake"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/server"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/pgtest"
)

const (
	webhookSecret = "whsec-test"
	origin        = "http://localhost:3000"
)

type env struct {
	t       *testing.T
	api     *httptest.Server
	gh      *fakegithub.Server
	browser *http.Client
	svc     *fleet.Service
	pool    *pgxpool.Pool
	app     *github.App
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	gh := fakegithub.New(t)
	gh.AddInstallation(github.Installation{ID: 1, Account: github.Account{ID: 1, Login: "acme", Type: "Organization"}})
	logger := slog.New(slog.DiscardHandler)
	queries := db.New(pool)
	svc := fleet.New(pool, fleet.AppGitHub{App: gh.App(t)}, fleet.DefaultConfig(), logger)

	app := gh.App(t)
	oauth := github.NewOAuth(github.OAuthConfig{ClientID: fakegithub.ClientID, ClientSecret: "secret", WebURL: gh.URL, APIURL: gh.URL})
	api := httptest.NewServer(server.NewHandler(server.Deps{
		DB: pool, Queries: queries, Fleet: svc, Sessions: auth.NewSessions(queries, false),
		OAuth: oauth, App: app,
		WebhookSecret: []byte(webhookSecret), Version: "test", Logger: logger,
		PublicURL: "http://api.test", WebURL: origin, DevLogin: true, CORSOrigins: []string{origin},
	}))
	t.Cleanup(api.Close)

	jar, _ := cookiejar.New(nil)

	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	return &env{t: t, api: api, gh: gh, browser: browser, svc: svc, pool: pool, app: app}
}

// call makes a dashboard request as the signed-in browser.
func (e *env) call(method, path string, body any, want int) map[string]any {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, e.api.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := e.browser.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)

	return out
}

// webhook sends a signed workflow_job event, as GitHub would.
func (e *env) webhook(action string, job github.WorkflowJob) {
	e.t.Helper()
	body, _ := json.Marshal(github.WorkflowJobEvent{
		Action: action, WorkflowJob: job,
		Repository:   github.Repository{ID: 5, FullName: "acme/api", Private: true},
		Installation: &github.InstallationRef{ID: 1},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.api.URL+"/v1/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("delivery-%d-%s", job.ID, action))
	req.Header.Set("X-Hub-Signature-256", github.Sign([]byte(webhookSecret), body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		e.t.Fatalf("webhook %s = %d", action, resp.StatusCode)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestJobLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// An admin signs in and sets up a data plane and a profile.
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	me := e.call(http.MethodGet, "/v1/me", nil, http.StatusOK)
	if orgs := me["organizations"].([]any); len(orgs) != 1 {
		t.Fatalf("organizations = %v", orgs)
	}
	created := e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "us-east-1"}, http.StatusCreated)
	token := created["token"].(string)
	dataPlaneID := created["dataPlane"].(map[string]any)["id"].(string)
	e.call(http.MethodPost, "/v1/orgs/acme/profiles", map[string]any{
		"label": "airship-2cpu", "dataPlaneId": dataPlaneID, "arch": "x64", "instanceType": "c7i.large", "maxRunners": 5,
	}, http.StatusCreated)

	// The customer starts the agent with the token.
	provider := fake.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	worker := agent.New(agent.NewClient(e.api.URL, token, nil), provider, agent.Config{Version: "test"}, slog.New(slog.DiscardHandler))
	go func() { _ = worker.Run(ctx) }()
	eventually(t, "agent online", func() bool {
		list := e.call(http.MethodGet, "/v1/orgs/acme/data-planes", nil, http.StatusOK)
		return list["items"].([]any)[0].(map[string]any)["status"] == "online"
	})

	// A workflow asks for the label.
	job := github.WorkflowJob{ID: 77, RunID: 1, RunAttempt: 1, Name: "test", Labels: []string{"self-hosted", "airship-2cpu"}, Status: "queued", CreatedAt: time.Now()}
	e.webhook("queued", job)

	var instance fake.Instance
	eventually(t, "runner launched", func() bool {
		instances := provider.Instances()
		if len(instances) == 1 {
			instance = instances[0]
		}
		return len(instances) == 1
	})
	registration, ok := e.gh.RunnerByJIT(instance.Launch.JITConfig)
	if !ok || registration.Repo != "acme/api" {
		t.Fatalf("instance carries no valid registration: %+v", instance.Launch)
	}
	if instance.DataPlaneID != dataPlaneID {
		t.Fatalf("instance tagged for %s, want %s", instance.DataPlaneID, dataPlaneID)
	}

	// The runner picks the job up and finishes it.
	started := time.Now()
	job.Status, job.StartedAt, job.RunnerName = "in_progress", &started, &instance.Launch.RunnerName
	e.webhook("in_progress", job)
	completed := time.Now()
	conclusion := "success"
	job.Status, job.CompletedAt, job.Conclusion = "completed", &completed, &conclusion
	e.webhook("completed", job)

	// The agent is told to tear the machine down.
	eventually(t, "instance terminated", func() bool { return len(provider.Instances()) == 0 })

	jobs := e.call(http.MethodGet, "/v1/orgs/acme/jobs", nil, http.StatusOK)["items"].([]any)
	got := jobs[0].(map[string]any)
	if got["onAirship"] != true || got["status"] != "completed" || got["conclusion"] != "success" {
		t.Fatalf("job = %v", got)
	}
	overview := e.call(http.MethodGet, "/v1/orgs/acme/overview", nil, http.StatusOK)
	if overview["airshipJobs"].(float64) != 1 {
		t.Fatalf("overview = %v", overview)
	}
	runners := e.call(http.MethodGet, "/v1/orgs/acme/runners", nil, http.StatusOK)["items"].([]any)
	if r := runners[0].(map[string]any); r["state"] != "finished" || r["finishReason"] != "completed" {
		t.Fatalf("runner = %v", r)
	}
}

func TestAgentAndWebhookAuth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// A bad agent token is refused.
	_, err := agent.NewClient(e.api.URL, "asd_not-a-real-token", nil).Sync(context.Background(), agentSyncRequest())
	if err == nil {
		t.Fatal("sync with a bad token succeeded")
	}

	// An unsigned or wrongly signed webhook is refused.
	for _, signature := range []string{"", "sha256=00", github.Sign([]byte("wrong"), []byte("{}"))} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.api.URL+"/v1/github/webhook", bytes.NewReader([]byte("{}")))
		req.Header.Set("X-GitHub-Event", "workflow_job")
		if signature != "" {
			req.Header.Set("X-Hub-Signature-256", signature)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("signature %q: status %d, want 401", signature, resp.StatusCode)
		}
	}

	// Dashboard routes need a session, and cross-origin writes are refused.
	resp, err := http.Get(e.api.URL + "/v1/me") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /v1/me = %d", resp.StatusCode)
	}
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.api.URL+"/v1/orgs/acme/data-planes", bytes.NewReader([]byte(`{"name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = e.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin create = %d, want 403", resp.StatusCode)
	}
	// Signing out from another site is refused too, and leaves the session.
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, e.api.URL+"/v1/auth/sign-out", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err = e.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin sign-out = %d, want 403", resp.StatusCode)
	}
	e.call(http.MethodGet, "/v1/me", nil, http.StatusOK)
	// Another organization's data is not visible.
	e.call(http.MethodGet, "/v1/orgs/globex/jobs", nil, http.StatusNotFound)
}

func agentSyncRequest() agentproto.SyncRequest {
	return agentproto.SyncRequest{Provider: "fake", Instances: []agentproto.Instance{}, Failures: []agentproto.LaunchFailure{}}
}
