// Package e2e_test runs the control plane over HTTP against Postgres, a
// fake GitHub and a fake cloud: every hop of a job's life except the
// machine itself.
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

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/fake"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
	"github.com/wizenheimer/airship/apps/api/internal/secrets"
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
	aws     *fake.Provider
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
	key, _ := secrets.ParseKey(secrets.NewKey())
	keyring, _ := secrets.New(key)
	aws := fake.New(cloud.AWS)
	cfg := fleet.DefaultConfig()
	cfg.PublicURL = "http://api.test"
	clouds := cloud.Registry{cloud.AWS: aws}
	for _, name := range []string{cloud.GCP, cloud.Azure, cloud.Daytona, cloud.Runloop, cloud.E2B, cloud.Novita, cloud.Lelantos, cloud.Namespace, cloud.Tenki, cloud.Modal} {
		clouds[name] = fake.New(name)
	}
	svc := fleet.New(pool, fleet.AppGitHub{App: gh.App(t)}, clouds, keyring, cfg, logger)

	app := gh.App(t)
	oauth := github.NewOAuth(github.OAuthConfig{ClientID: fakegithub.ClientID, ClientSecret: "secret", WebURL: gh.URL, APIURL: gh.URL})
	api := httptest.NewServer(server.NewHandler(server.Deps{
		DB: pool, Queries: queries, Fleet: svc, Sessions: auth.NewSessions(queries, false),
		OAuth: oauth, App: app,
		WebhookSecret: []byte(webhookSecret), Version: "test", Logger: logger,
		PublicURL: "http://api.test", DashURL: origin, DevLogin: true, CORSOrigins: []string{origin},
	}))
	t.Cleanup(api.Close)

	// The launcher runs as it would in production.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.RunLauncher(ctx, 50*time.Millisecond)

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	return &env{t: t, api: api, gh: gh, aws: aws, browser: browser, svc: svc, pool: pool, app: app}
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

// connectAWS connects the fake cloud as the signed-in admin.
func (e *env) connectAWS() {
	e.t.Helper()
	got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/aws", map[string]any{
		"accessKeyId": "AKIAIOSFODNN7EXAMPLE", "secretAccessKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "defaultRegion": "us-east-1",
	}, http.StatusOK)
	if got["status"] != "ok" || got["identity"] != "AKIAIOSFODNN7EXAMPLE" || got["accountId"] != "123456789012" {
		e.t.Fatalf("connect = %v", got)
	}
}

// webhook sends a signed workflow_job event, as GitHub would.
func (e *env) webhook(action string, job github.WorkflowJob) {
	e.t.Helper()
	e.send("workflow_job", github.WorkflowJobEvent{
		Action: action, WorkflowJob: job,
		Repository:   github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"},
		Installation: &github.InstallationRef{ID: 1},
	})
}

// send delivers any signed webhook.
func (e *env) send(event string, payload any) {
	e.t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.api.URL+"/v1/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", event+"-"+time.Now().Format(time.RFC3339Nano))
	req.Header.Set("X-Hub-Signature-256", github.Sign([]byte(webhookSecret), body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		e.t.Fatalf("%s webhook = %d", event, resp.StatusCode)
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

const e2eConfig = `
runners:
  - name: build
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [airship-build]
`

func TestJobLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"}
	e.gh.AddRepository(1, repo)
	e.gh.SetFile("acme/api", "abc123", runnerconfig.RepoFile, []byte(e2eConfig))
	e.gh.SetRunAttempt("acme/api", github.RunAttempt{ID: 1, RunAttempt: 1, Event: "push", Repository: repo, HeadRepository: &repo, Actor: &github.Actor{Login: "alice"}})

	// An admin signs in, connects AWS and activates the repository.
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	me := e.call(http.MethodGet, "/v1/me", nil, http.StatusOK)
	if orgs := me["organizations"].([]any); len(orgs) != 1 {
		t.Fatalf("organizations = %v", orgs)
	}
	e.connectAWS()
	clouds := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any)
	if len(clouds) != 1 || clouds[0].(map[string]any)["defaultRegion"] != "us-east-1" {
		t.Fatalf("clouds = %v", clouds)
	}
	e.call(http.MethodPut, "/v1/orgs/acme/repositories/5/activation", map[string]any{}, http.StatusOK)

	// A workflow asks for the label.
	job := github.WorkflowJob{ID: 77, RunID: 1, RunAttempt: 1, HeadSHA: "abc123", Name: "test", Labels: []string{"self-hosted", "airship-build--1"}, Status: "queued", CreatedAt: time.Now()}
	e.webhook("queued", job)

	var instance fake.Instance
	eventually(t, "runner launched", func() bool {
		instances := e.aws.Instances()
		if len(instances) == 1 {
			instance = instances[0]
		}

		return len(instances) == 1
	})
	registration, ok := e.gh.RunnerByJIT(instance.Launch.Boot.JITConfig)
	if !ok || registration.Repo != "acme/api" {
		t.Fatalf("instance carries no valid registration: %+v", instance.Launch)
	}
	if instance.Launch.InstallationID != 1 || instance.Region != "us-east-1" || instance.Launch.Machine.InstanceType != "c7i.large" {
		t.Fatalf("instance = %+v", instance.Launch)
	}

	// The runner picks the job up and finishes it.
	started := time.Now()
	job.Status, job.StartedAt, job.RunnerName = "in_progress", &started, &instance.Launch.RunnerName
	e.webhook("in_progress", job)
	completed := time.Now()
	conclusion := "success"
	job.Status, job.CompletedAt, job.Conclusion = "completed", &completed, &conclusion
	e.webhook("completed", job)

	// The machine is torn down.
	eventually(t, "instance terminated", func() bool { return len(e.aws.Instances()) == 0 })

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
	if r := runners[0].(map[string]any); r["state"] != "finished" || r["finishReason"] != "completed" || r["region"] != "us-east-1" {
		t.Fatalf("runner = %v", r)
	}
	events := e.call(http.MethodGet, "/v1/orgs/acme/events", nil, http.StatusOK)["items"].([]any)
	if len(events) < 3 {
		t.Fatalf("events = %v", events)
	}
	analytics := e.call(http.MethodGet, "/v1/orgs/acme/analytics?days=7", nil, http.StatusOK)
	if daily := analytics["daily"].([]any); len(daily) != 1 || daily[0].(map[string]any)["airshipJobs"].(float64) != 1 {
		t.Fatalf("analytics = %v", analytics)
	}
}

func TestWebhookAndSessionAuth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

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
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch, e.api.URL+"/v1/orgs/acme/settings", bytes.NewReader([]byte(`{"maxRunners":5}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = e.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin update = %d, want 403", resp.StatusCode)
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

func TestCloudCredentialsAreVerifiedAndNeverEchoed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)

	// The cloud rejects the key: nothing is stored.
	bad, _ := json.Marshal(map[string]string{"accessKeyId": "AKIABADBADBADBADBAD1", "secretAccessKey": "nope-nope-nope-nope"})
	e.aws.BadSecret = string(bad)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/aws", map[string]any{"accessKeyId": "AKIABADBADBADBADBAD1", "secretAccessKey": "nope-nope-nope-nope"}, http.StatusUnprocessableEntity)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("rejected credentials were stored: %v", items)
	}

	e.connectAWS()
	raw := fmt.Sprint(e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK))
	if bytes.Contains([]byte(raw), []byte("wJalrXUtnFEMI")) {
		t.Fatal("the secret access key was echoed")
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), "select secret from cloud_credentials").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("wJalrXUtnFEMI")) || bytes.Contains(stored, []byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Fatal("the secret is stored in the clear")
	}

	// Members can look but not change anything; owners can disconnect.
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/aws", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/aws", nil, http.StatusNotFound)
}

// Google Cloud is connected with a service account key file pasted whole;
// the stored identity is the account's email and nothing of the key is
// echoed or stored in the clear.
func TestConnectGCP(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	const key = `{
  "type": "service_account",
  "project_id": "acme-ci-123456",
  "private_key_id": "k1",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----\n",
  "client_email": "airship@acme-ci-123456.iam.gserviceaccount.com",
  "client_id": "1234567890",
  "token_uri": "https://oauth2.googleapis.com/token",
  "universe_domain": "googleapis.com"
}`
	// Not a service account key, or a key with a bad location: nothing stored.
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/gcp", map[string]any{"serviceAccountKey": `{"type":"authorized_user","client_id":"x"}`}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/gcp", map[string]any{"serviceAccountKey": key, "defaultRegion": "us-east-1"}, http.StatusUnprocessableEntity)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("rejected credentials were stored: %v", items)
	}

	got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/gcp", map[string]any{"serviceAccountKey": key, "defaultRegion": "us-central1-b"}, http.StatusOK)
	if got["cloud"] != "gcp" || got["identity"] != "airship@acme-ci-123456.iam.gserviceaccount.com" || got["defaultRegion"] != "us-central1-b" || got["status"] != "ok" {
		t.Fatalf("connect gcp = %v", got)
	}
	raw := fmt.Sprint(e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK))
	if bytes.Contains([]byte(raw), []byte("MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7")) {
		t.Fatal("the private key was echoed")
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), "select secret from cloud_credentials where cloud = 'gcp'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7")) || bytes.Contains(stored, []byte("acme-ci-123456")) {
		t.Fatal("the key is stored in the clear")
	}

	// Both clouds can be connected at once; each is removed on its own.
	e.connectAWS()
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 2 {
		t.Fatalf("clouds = %v", items)
	}
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/gcp", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/gcp", nil, http.StatusNotFound)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("clouds after disconnecting gcp = %v", items)
	}
}

func TestConnectDaytona(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	const key = "dtn_8f3a9c2e7b1d4f6a8c0e2b4d6f8a1c3e5b7d9f1a"
	// Not a key, a bad URL, a bad region: nothing stored.
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/daytona", map[string]any{"apiKey": "short"}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/daytona", map[string]any{"apiKey": key, "apiUrl": "ftp://daytona.local"}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/daytona", map[string]any{"apiKey": key, "defaultRegion": "US East"}, http.StatusUnprocessableEntity)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("rejected credentials were stored: %v", items)
	}

	got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/daytona", map[string]any{"apiKey": key, "defaultRegion": "eu"}, http.StatusOK)
	if got["cloud"] != "daytona" || got["identity"] != "dtn***f1a" || got["defaultRegion"] != "eu" || got["status"] != "ok" {
		t.Fatalf("connect daytona = %v", got)
	}
	raw := fmt.Sprint(e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK))
	if bytes.Contains([]byte(raw), []byte(key)) {
		t.Fatal("the API key was echoed")
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), "select secret from cloud_credentials where cloud = 'daytona'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(key)) {
		t.Fatal("the key is stored in the clear")
	}
	// The default URL is the hosted service; a self-hosted one is kept.
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/daytona", map[string]any{"apiKey": key, "apiUrl": "https://daytona.internal.example/api"}, http.StatusOK)

	e.connectAWS()
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 2 {
		t.Fatalf("clouds = %v", items)
	}
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/daytona", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/daytona", nil, http.StatusNotFound)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("clouds after disconnecting daytona = %v", items)
	}
}

// Runloop and E2B connect with an API key alone, in their one region.
func TestConnectSandboxClouds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	const runloopKey = "ak_8f3a9c2e7b1d4f6a8c0e2b4d6f8a1c3e"
	const e2bKey = "e2b_8f3a9c2e7b1d4f6a8c0e2b4d6f8a1c3e"
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/runloop", map[string]any{"apiKey": "short"}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/runloop", map[string]any{"apiKey": runloopKey, "apiUrl": "ftp://runloop.local"}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/e2b", map[string]any{"apiKey": "sk_notane2bkey123"}, http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/e2b", map[string]any{"apiKey": e2bKey, "domain": "not a domain"}, http.StatusUnprocessableEntity)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("rejected credentials were stored: %v", items)
	}

	got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/runloop", map[string]any{"apiKey": runloopKey}, http.StatusOK)
	if got["cloud"] != "runloop" || got["identity"] != "ak_***c3e" || got["defaultRegion"] != "global" || got["status"] != "ok" {
		t.Fatalf("connect runloop = %v", got)
	}
	got = e.call(http.MethodPut, "/v1/orgs/acme/clouds/e2b", map[string]any{"apiKey": e2bKey}, http.StatusOK)
	if got["cloud"] != "e2b" || got["identity"] != "e2b_***1c3e" || got["defaultRegion"] != "global" {
		t.Fatalf("connect e2b = %v", got)
	}
	raw := fmt.Sprint(e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK))
	if bytes.Contains([]byte(raw), []byte(runloopKey)) || bytes.Contains([]byte(raw), []byte(e2bKey)) {
		t.Fatal("an API key was echoed")
	}
	rows, err := e.pool.Query(context.Background(), "select secret from cloud_credentials where cloud in ('runloop', 'e2b')")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var stored []byte
		_ = rows.Scan(&stored)
		if bytes.Contains(stored, []byte(runloopKey)) || bytes.Contains(stored, []byte(e2bKey)) {
			t.Fatal("a key is stored in the clear")
		}
	}
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/runloop", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/e2b", nil, http.StatusNoContent)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("clouds after disconnecting = %v", items)
	}
}

// Novita, Lelantos, Namespace, Tenki and Modal connect with their own
// credentials; Namespace keeps its region, the rest are global.
func TestConnectPhaseTwoClouds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	for _, tc := range []struct {
		path     string
		bad, ok  map[string]any
		identity string
		region   string
	}{
		{"novita", map[string]any{"apiKey": "short key!"}, map[string]any{"apiKey": "sk_8f3a9c2e7b1d4f6a8c0e2b4d"}, "sk_8***2b4d", "global"},
		{"lelantos", map[string]any{"apiKey": "e2c_8f3a9c2e7b1d4f6a"}, map[string]any{"apiKey": "lel_8f3a9c2e7b1d4f6a8c0e"}, "lel_***8c0e", "global"},
		{"namespace", map[string]any{"token": "nsct_8f3a9c2e7b1d4f6a", "defaultRegion": "ap"}, map[string]any{"token": "nsct_8f3a9c2e7b1d4f6a8c0e", "defaultRegion": "eu"}, "nsct***8c0e", "eu"},
		{"tenki", map[string]any{"apiKey": "sk_8f3a9c2e7b1d4f6a"}, map[string]any{"apiKey": "tk_8f3a9c2e7b1d4f6a8c0e"}, "tk_***8c0e", "global"},
		{"modal", map[string]any{"tokenId": "ak-8f3a9c2e", "tokenSecret": "nope-nope"}, map[string]any{"tokenId": "ak-8f3a9c2e7b1d", "tokenSecret": "as-8f3a9c2e7b1d"}, "ak-8f3a9c2e7b1d", "global"},
	} {
		e.call(http.MethodPut, "/v1/orgs/acme/clouds/"+tc.path, tc.bad, http.StatusUnprocessableEntity)
		got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/"+tc.path, tc.ok, http.StatusOK)
		if got["cloud"] != tc.path || got["identity"] != tc.identity || got["defaultRegion"] != tc.region || got["status"] != "ok" {
			t.Errorf("connect %s = %v", tc.path, got)
		}
	}
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 5 {
		t.Fatalf("clouds = %v", items)
	}
	for _, name := range []string{"novita", "lelantos", "namespace", "tenki", "modal"} {
		e.call(http.MethodDelete, "/v1/orgs/acme/clouds/"+name, nil, http.StatusNoContent)
	}
}

func TestConnectAzure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	const (
		subscription = "0f0e0d0c-0b0a-4908-8706-050403020100"
		tenant       = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		client       = "12345678-9abc-4def-8123-456789abcdef"
		secret       = "Q~8f3a9c2e7b1d4f6a8c0e2b4d6f8a1c3e5b7d9f1a"
	)
	body := func(overrides map[string]any) map[string]any {
		out := map[string]any{"subscriptionId": subscription, "tenantId": tenant, "clientId": client, "clientSecret": secret}
		for k, v := range overrides {
			out[k] = v
		}

		return out
	}
	// Not a GUID, no secret, a display name as the region: nothing stored.
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/azure", body(map[string]any{"subscriptionId": "my-subscription"}), http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/azure", body(map[string]any{"clientSecret": ""}), http.StatusUnprocessableEntity)
	e.call(http.MethodPut, "/v1/orgs/acme/clouds/azure", body(map[string]any{"defaultRegion": "East US"}), http.StatusUnprocessableEntity)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("rejected credentials were stored: %v", items)
	}

	got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/azure", body(map[string]any{"defaultRegion": "uksouth"}), http.StatusOK)
	if got["cloud"] != "azure" || got["identity"] != client || got["defaultRegion"] != "uksouth" || got["status"] != "ok" {
		t.Fatalf("connect azure = %v", got)
	}
	raw := fmt.Sprint(e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK))
	if bytes.Contains([]byte(raw), []byte(secret)) {
		t.Fatal("the client secret was echoed")
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), "select secret from cloud_credentials where cloud = 'azure'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(secret)) {
		t.Fatal("the secret is stored in the clear")
	}
	// The default region is eastus.
	if got := e.call(http.MethodPut, "/v1/orgs/acme/clouds/azure", body(nil), http.StatusOK); got["defaultRegion"] != "eastus" {
		t.Fatalf("default region = %v", got["defaultRegion"])
	}

	e.connectAWS()
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 2 {
		t.Fatalf("clouds = %v", items)
	}
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/azure", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/azure", nil, http.StatusNotFound)
	if items := e.call(http.MethodGet, "/v1/orgs/acme/clouds", nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("clouds after disconnecting azure = %v", items)
	}
}

func TestSettingsAndMembers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	if got := e.call(http.MethodGet, "/v1/orgs/acme/settings", nil, http.StatusOK); got["maxRunners"].(float64) != 50 {
		t.Fatalf("default settings = %v", got)
	}
	if got := e.call(http.MethodPatch, "/v1/orgs/acme/settings", map[string]any{"maxRunners": 7}, http.StatusOK); got["maxRunners"].(float64) != 7 {
		t.Fatalf("settings = %v", got)
	}
	e.call(http.MethodPatch, "/v1/orgs/acme/settings", map[string]any{"maxRunners": 0}, http.StatusUnprocessableEntity)
	members := e.call(http.MethodGet, "/v1/orgs/acme/members", nil, http.StatusOK)["items"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["role"] != "admin" {
		t.Fatalf("members = %v", members)
	}
}

// While GitHub has the app suspended nothing can run, so the console
// refuses changes (as the developer API does) and keeps reads open.
func TestSuspendedInstallationIsReadOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	e.send("installation", map[string]any{"action": "suspend", "installation": map[string]any{"id": 1, "account": map[string]any{"id": 1, "login": "acme", "type": "Organization"}}})
	e.call(http.MethodGet, "/v1/orgs/acme/settings", nil, http.StatusOK)
	e.call(http.MethodPatch, "/v1/orgs/acme/settings", map[string]any{"maxRunners": 7}, http.StatusConflict)
	e.send("installation", map[string]any{"action": "unsuspend", "installation": map[string]any{"id": 1, "account": map[string]any{"id": 1, "login": "acme", "type": "Organization"}}})
	e.call(http.MethodPatch, "/v1/orgs/acme/settings", map[string]any{"maxRunners": 7}, http.StatusOK)
}
