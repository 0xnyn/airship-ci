package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agent/fake"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

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

const e2eConfig = `
runners:
  - name: build
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [airship-build]
    idle:
      replicas: 1
`

// A repository's .airship.yml, from activation to a warm pool to a job and
// back to zero, driven through the dashboard API, webhooks and a real
// agent loop.
func TestRunnerConfigEndToEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"}
	e.gh.AddRepository(1, repo)
	e.gh.AddRepository(1, github.Repository{ID: 6, FullName: "acme/web", Private: true, DefaultBranch: "main"})
	e.gh.SetFile("acme/api", "main", runnerconfig.RepoFile, []byte(e2eConfig))

	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	created := e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "prod"}, http.StatusCreated)
	provider := fake.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	worker := agent.New(agent.NewClient(e.api.URL, created["token"].(string), nil), provider, agent.Config{Version: "test", Region: "us-east-1"}, slog.New(slog.DiscardHandler))
	go func() { _ = worker.Run(ctx) }()
	eventually(t, "agent online", func() bool {
		dp := e.call(http.MethodGet, "/v1/orgs/acme/data-planes", nil, http.StatusOK)["items"].([]any)[0].(map[string]any)
		return dp["status"] == "online" && dp["region"] == "us-east-1"
	})

	// The editor checks a file before it is committed.
	check := e.call(http.MethodPost, "/v1/orgs/acme/runner-config/validate", map[string]any{
		"file": ".airship.yml", "content": strings.ReplaceAll(e2eConfig, "c7i.large", "[c7i.large, m7i.large]\n    region: [us-east-1, us-west-2, eu-west-1]"),
	}, http.StatusOK)
	if check["valid"] != false || !strings.Contains(check["problem"].(map[string]any)["path"].(string), "instance_type") {
		t.Fatalf("mismatched fallback lists passed validation: %v", check)
	}
	check = e.call(http.MethodPost, "/v1/orgs/acme/runner-config/validate", map[string]any{"file": ".airship.yml", "content": e2eConfig}, http.StatusOK)
	if check["valid"] != true || check["runners"].([]any)[0].(map[string]any)["idle"] != "1 always idle" {
		t.Fatalf("validation = %v", check)
	}

	// Activation reads the config and starts the warm pool.
	repos := e.call(http.MethodGet, "/v1/orgs/acme/repositories", nil, http.StatusOK)["items"].([]any)
	if len(repos) != 2 || repos[0].(map[string]any)["activated"] != false {
		t.Fatalf("repositories = %v", repos)
	}
	e.call(http.MethodPut, "/v1/orgs/acme/repositories/999/activation", map[string]any{}, http.StatusNotFound)
	activated := e.call(http.MethodPut, "/v1/orgs/acme/repositories/5/activation", map[string]any{}, http.StatusOK)
	if activated["config"].(map[string]any)["sha"] == nil {
		t.Fatalf("activation did not read the config: %v", activated)
	}
	eventually(t, "warm runner launched", func() bool { return len(provider.Instances()) == 1 })
	warm := provider.Instances()[0]
	if warm.Launch.Profile.InstanceType != "c7i.large" {
		t.Fatalf("warm runner = %+v", warm.Launch.Profile)
	}
	pools := e.call(http.MethodGet, "/v1/orgs/acme/pools", nil, http.StatusOK)["items"].([]any)
	if len(pools) != 1 || pools[0].(map[string]any)["desired"].(float64) != 1 {
		t.Fatalf("pools = %v", pools)
	}

	// A suffixed job gets its own runner, registered with its suffix.
	e.gh.SetFile("acme/api", "abc123", runnerconfig.RepoFile, []byte(e2eConfig))
	e.gh.SetRunAttempt("acme/api", github.RunAttempt{ID: 3, RunAttempt: 1, Event: "push", Repository: repo, HeadRepository: &repo, Actor: &github.Actor{Login: "alice"}})
	job := github.WorkflowJob{ID: 90, RunID: 3, RunAttempt: 1, HeadSHA: "abc123", Name: "build", Labels: []string{"airship-build--3"}, Status: "queued", CreatedAt: time.Now()}
	e.send("workflow_job", github.WorkflowJobEvent{Action: "queued", WorkflowJob: job, Repository: repo, Installation: &github.InstallationRef{ID: 1}})
	eventually(t, "on-demand runner launched", func() bool { return len(provider.Instances()) == 2 })
	for _, instance := range provider.Instances() {
		registration, _ := e.gh.RunnerByJIT(instance.Launch.JITConfig)
		if instance.Launch.RunnerID != warm.Launch.RunnerID && !slices.Contains(registration.Labels, "airship-build--3") {
			t.Fatalf("on-demand runner labels = %v", registration.Labels)
		}
	}

	// A push that empties the pool trims the warm runner.
	e.gh.SetFile("acme/api", "main", runnerconfig.RepoFile, []byte(strings.ReplaceAll(e2eConfig, "replicas: 1", "replicas: 0")))
	e.send("push", github.PushEvent{Ref: "refs/heads/main", Repository: repo, Installation: &github.InstallationRef{ID: 1}})
	if err := e.svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "warm runner terminated", func() bool {
		for _, instance := range provider.Instances() {
			if instance.Launch.RunnerID == warm.Launch.RunnerID {
				return false
			}
		}

		return true
	})

	// Deactivation stops the config from serving new jobs.
	e.call(http.MethodDelete, "/v1/orgs/acme/repositories/5/activation", nil, http.StatusNoContent)
	job.ID = 91
	e.send("workflow_job", github.WorkflowJobEvent{Action: "queued", WorkflowJob: job, Repository: repo, Installation: &github.InstallationRef{ID: 1}})
	jobs := e.call(http.MethodGet, "/v1/orgs/acme/jobs", nil, http.StatusOK)["items"].([]any)
	if skipped := jobs[0].(map[string]any); skipped["skipReason"] != "repository_inactive" || skipped["skipDetail"] == nil {
		t.Fatalf("job after deactivation = %v", skipped)
	}
}
