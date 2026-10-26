package e2e_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cacheproxy"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

const poolConfig = `
runners:
  - name: build
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [cirun-build]
    idle:
      replicas: 1
`

// A repository's .cirun.yml, from activation to an idle pool to a job and
// back to zero, driven through the dashboard API, webhooks and the real
// launcher.
func TestRunnerConfigEndToEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"}
	e.gh.AddRepository(1, repo)
	e.gh.AddRepository(1, github.Repository{ID: 6, FullName: "acme/web", Private: true, DefaultBranch: "main"})
	e.gh.SetFile("acme/api", "main", runnerconfig.RepoFile, []byte(poolConfig))

	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	e.connectAWS()

	// The editor checks a file before it is committed.
	check := e.call(http.MethodPost, "/v1/orgs/acme/runner-config/validate", map[string]any{
		"file": ".cirun.yml", "content": strings.ReplaceAll(poolConfig, "c7i.large", "[c7i.large, m7i.large]\n    region: [us-east-1, us-west-2, eu-west-1]"),
	}, http.StatusOK)
	if check["valid"] != false || !strings.Contains(check["problem"].(map[string]any)["path"].(string), "instance_type") {
		t.Fatalf("mismatched fallback lists passed validation: %v", check)
	}
	check = e.call(http.MethodPost, "/v1/orgs/acme/runner-config/validate", map[string]any{"file": ".cirun.yml", "content": poolConfig}, http.StatusOK)
	if check["valid"] != true || check["runners"].([]any)[0].(map[string]any)["idle"] != "1 always idle" {
		t.Fatalf("validation = %v", check)
	}

	// Activation reads the config and starts the idle pool.
	repos := e.call(http.MethodGet, "/v1/orgs/acme/repositories", nil, http.StatusOK)["items"].([]any)
	if len(repos) != 2 || repos[0].(map[string]any)["activated"] != false {
		t.Fatalf("repositories = %v", repos)
	}
	e.call(http.MethodPut, "/v1/orgs/acme/repositories/999/activation", map[string]any{}, http.StatusNotFound)
	activated := e.call(http.MethodPut, "/v1/orgs/acme/repositories/5/activation", map[string]any{}, http.StatusOK)
	if activated["config"].(map[string]any)["sha"] == nil {
		t.Fatalf("activation did not read the config: %v", activated)
	}
	eventually(t, "idle runner launched", func() bool { return len(e.aws.Instances()) == 1 })
	warm := e.aws.Instances()[0]
	if warm.Launch.Machine.InstanceType != "c7i.large" {
		t.Fatalf("idle runner = %+v", warm.Launch.Machine)
	}
	pools := e.call(http.MethodGet, "/v1/orgs/acme/pools", nil, http.StatusOK)["items"].([]any)
	if len(pools) != 1 || pools[0].(map[string]any)["desired"].(float64) != 1 {
		t.Fatalf("pools = %v", pools)
	}

	// A suffixed job gets its own runner, registered with its suffix.
	e.gh.SetFile("acme/api", "abc123", runnerconfig.RepoFile, []byte(poolConfig))
	e.gh.SetRunAttempt("acme/api", github.RunAttempt{ID: 3, RunAttempt: 1, Event: "push", Repository: repo, HeadRepository: &repo, Actor: &github.Actor{Login: "alice"}})
	job := github.WorkflowJob{ID: 90, RunID: 3, RunAttempt: 1, HeadSHA: "abc123", Name: "build", Labels: []string{"cirun-build--3"}, Status: "queued", CreatedAt: time.Now()}
	e.webhook("queued", job)
	eventually(t, "on-demand runner launched", func() bool { return len(e.aws.Instances()) == 2 })
	for _, instance := range e.aws.Instances() {
		registration, _ := e.gh.RunnerByJIT(instance.Launch.Boot.JITConfig)
		if instance.Launch.RunnerID != warm.Launch.RunnerID && !slices.Contains(registration.Labels, "cirun-build--3") {
			t.Fatalf("on-demand runner labels = %v", registration.Labels)
		}
	}

	// A push that empties the pool trims the idle runner.
	e.gh.SetFile("acme/api", "main", runnerconfig.RepoFile, []byte(strings.ReplaceAll(poolConfig, "replicas: 1", "replicas: 0")))
	e.send("push", github.PushEvent{Ref: "refs/heads/main", Repository: repo, Installation: &github.InstallationRef{ID: 1}})
	if err := e.svc.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "idle runner terminated", func() bool {
		for _, instance := range e.aws.Instances() {
			if instance.Launch.RunnerID == warm.Launch.RunnerID {
				return false
			}
		}

		return true
	})

	// Deactivation stops the config from serving new jobs.
	e.call(http.MethodDelete, "/v1/orgs/acme/repositories/5/activation", nil, http.StatusNoContent)
	job.ID = 91
	e.webhook("queued", job)
	jobs := e.call(http.MethodGet, "/v1/orgs/acme/jobs", nil, http.StatusOK)["items"].([]any)
	if skipped := jobs[0].(map[string]any); skipped["skipReason"] != "repository_inactive" || skipped["skipDetail"] == nil {
		t.Fatalf("job after deactivation = %v", skipped)
	}
}

// provision_via: pull_from_api machines get no user data; the image
// fetches the script from the developer API with an API token, once.
func TestProvisionViaAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"}
	e.gh.AddRepository(1, repo)
	config := strings.ReplaceAll(poolConfig, "    idle:\n      replicas: 1\n", "    extra_config:\n      provision_via: pull_from_api\n")
	e.gh.SetFile("acme/api", "abc123", runnerconfig.RepoFile, []byte(config))
	e.gh.SetRunAttempt("acme/api", github.RunAttempt{ID: 3, RunAttempt: 1, Event: "push", Repository: repo, HeadRepository: &repo, Actor: &github.Actor{Login: "alice"}})

	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	e.connectAWS()
	e.call(http.MethodPut, "/v1/orgs/acme/repositories/5/activation", map[string]any{}, http.StatusOK)
	token := e.call(http.MethodPost, "/v1/orgs/acme/api-tokens", map[string]any{"name": "image"}, http.StatusCreated)["token"].(string)

	job := github.WorkflowJob{ID: 90, RunID: 3, RunAttempt: 1, HeadSHA: "abc123", Name: "build", Labels: []string{"cirun-build"}, Status: "queued", CreatedAt: time.Now()}
	e.webhook("queued", job)
	eventually(t, "runner launched", func() bool { return len(e.aws.Instances()) == 1 })
	instance := e.aws.Instances()[0]
	if !instance.Launch.Boot.PullFromAPI {
		t.Fatalf("launch = %+v, want no user data", instance.Launch.Boot)
	}

	script := e.scriptText(token, "/api/runner-provision?runner_name="+instance.Launch.RunnerName, http.StatusOK)
	if !strings.HasPrefix(script, "#!/bin/bash") || !strings.Contains(script, instance.Launch.Boot.JITConfig) {
		t.Fatalf("script = %.80s", script)
	}
	// Once only, and never for someone else's token or an unknown runner.
	e.scriptText(token, "/api/runner-provision?runner_name="+instance.Launch.RunnerName, http.StatusNotFound)
	e.scriptText(token, "/api/runner-provision?runner_name=airship-unknown", http.StatusNotFound)
	e.scriptText("asa_wrong", "/api/runner-provision?runner_name="+instance.Launch.RunnerName, http.StatusUnauthorized)
}

// The cache proxy on a machine fetches its bucket credentials with the
// token from its boot script; nothing else can.
func TestCacheGrantEndpoint(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	repo := github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"}
	e.gh.AddRepository(1, repo)
	config := strings.ReplaceAll(poolConfig, "    idle:\n      replicas: 1\n", "    extra_config:\n      cache: true\n")
	e.gh.SetFile("acme/api", "abc123", runnerconfig.RepoFile, []byte(config))
	e.gh.SetRunAttempt("acme/api", github.RunAttempt{ID: 3, RunAttempt: 1, Event: "push", Repository: repo, HeadRepository: &repo, Actor: &github.Actor{Login: "alice"}})
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	e.connectAWS()
	e.call(http.MethodPut, "/v1/orgs/acme/repositories/5/activation", map[string]any{}, http.StatusOK)

	job := github.WorkflowJob{ID: 90, RunID: 3, RunAttempt: 1, HeadSHA: "abc123", Name: "build", Labels: []string{"cirun-build"}, Status: "queued", CreatedAt: time.Now()}
	e.webhook("queued", job)
	eventually(t, "runner launched", func() bool { return len(e.aws.Instances()) == 1 })
	boot := e.aws.Instances()[0].Launch.Boot.Cache
	if boot == nil {
		t.Fatal("no cache boot")
	}
	grant := e.script(boot.Token, http.MethodGet, cacheproxy.GrantPath, nil, http.StatusOK)
	if grant["prefix"] != "acme/api" || grant["region"] != "us-east-1" || grant["accessKeyId"] == "" || !strings.HasPrefix(grant["bucket"].(string), "airship-cache-") {
		t.Fatalf("grant = %v", grant)
	}
	e.script("asr_nope", http.MethodGet, cacheproxy.GrantPath, nil, http.StatusUnauthorized)
	e.script("", http.MethodGet, cacheproxy.GrantPath, nil, http.StatusUnauthorized)
	// The proxy binary is served when this build embeds it (pnpm build),
	// and refused with a clear message otherwise.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, e.api.URL+"/v1/assets/cacheproxy/linux/x64", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("asset = %d", resp.StatusCode)
	}
	e.scriptText("", "/v1/assets/cacheproxy/windows/x64", http.StatusUnprocessableEntity)
}
