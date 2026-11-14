package e2b

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/e2b/e2btest"
)

const testKey = "e2b_0123456789abcdef0123456789"

func init() {
	pollInterval = time.Millisecond
}

func setup(t *testing.T) (*Provider, *e2btest.Server, []byte) {
	t.Helper()
	fake := e2btest.New(t, testKey)
	secret, err := EncodeSecret(Secret{APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}

	return New(Options{Endpoint: fake.URL, EnvdEndpoint: fake.EnvdURL}), fake, secret
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       "0190c0de-0000-7000-8000-000000000001",
		RunnerName:     "airship-acme-api-1",
		InstallationID: 42,
		Repository:     "acme/api",
		Region:         cloud.SandboxRegion,
		Machine: cloud.Machine{
			InstanceType: "ci-runner",
			Arch:         "x64",
			Sandbox:      &cloud.SandboxOptions{Template: "ci-runner", CPU: 4},
		},
		Boot: cloud.Boot{JITConfig: "aml0LXNlY3JldA==", RunnerVersion: "2.330.0", MaxLifetimeSeconds: 3600},
	}
}

// A launch creates one sandbox from the template, uploads the script as
// root and starts it as the tagged runner process; a repeated launch finds
// the sandbox and does not start the runner again.
func TestLaunchCreatesBootsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.UnhealthyFor = 2
	launch := testLaunch()

	id, err := p.Launch(context.Background(), secret, launch)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Launch(context.Background(), secret, launch)
	if err != nil || again != id {
		t.Fatalf("second launch = %q, %v; want %q", again, err, id)
	}
	sandboxes := fake.Sandboxes()
	if len(sandboxes) != 1 || fake.Creates() != 1 {
		t.Fatalf("sandboxes = %d, creates = %d", len(sandboxes), fake.Creates())
	}
	sb := sandboxes[0]
	if sb.TemplateID != "ci-runner" || sb.Metadata[MetaInstallation] != "42" || sb.Metadata[MetaRunner] != launch.RunnerID || sb.Metadata[MetaRepository] != "acme/api" {
		t.Errorf("sandbox = %+v", sb)
	}
	if sb.Request["timeout"] != 4500.0 || sb.Request["cpuCount"] != 4.0 || sb.Request["autoPause"] != false {
		t.Errorf("create request = %v", sb.Request)
	}
	path := scriptPath(launch.RunnerID)
	script := sb.Files[path]
	if !strings.Contains(script, "ACTIONS_RUNNER_INPUT_JITCONFIG='aml0LXNlY3JldA=='") || strings.Contains(script, "kill -TERM 1") || sb.FileUsers[path] != "root" {
		t.Errorf("boot script (as %q) = %q", sb.FileUsers[path], script[:min(len(script), 200)])
	}
	if len(sb.Processes) != 1 || sb.Processes[0].Tag != ProcessTag || sb.Processes[0].User != "root" || !strings.Contains(strings.Join(sb.Processes[0].Args, " "), path) {
		t.Errorf("processes = %+v", sb.Processes)
	}
}

// The timeout never asks for more than E2B allows, and a template is
// required.
func TestRequest(t *testing.T) {
	t.Parallel()
	launch := testLaunch()
	launch.Boot.MaxLifetimeSeconds = 100 * 3600
	body, err := buildRequest(FlavorE2B, launch)
	if err != nil || body.Timeout != int(MaxTimeout.Seconds()) {
		t.Fatalf("body = %+v, %v", body, err)
	}
	launch.Machine.Sandbox = &cloud.SandboxOptions{}
	if _, err := buildRequest(FlavorE2B, launch); err == nil {
		t.Error("a launch without a template was accepted")
	}
	launch.Machine.Sandbox = &cloud.SandboxOptions{Template: "base", MemoryGB: 8}
	if _, err := buildRequest(FlavorE2B, launch); err == nil {
		t.Error("memory was accepted")
	}
}

// A create whose answer is lost is retried with the same idempotency key
// on the next attempt, or found by its metadata now.
func TestLostCreateAnswerFindsTheSandbox(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.LoseCreateAnswer = true
	id, err := p.Launch(context.Background(), secret, testLaunch())
	if err != nil {
		t.Fatal(err)
	}
	if fake.Creates() != 1 || fake.Sandboxes()[0].SandboxID != id {
		t.Fatalf("creates = %d", fake.Creates())
	}
}

// A sandbox whose runner cannot be started is killed.
func TestFailedStartKillsTheSandbox(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.FailStart = true
	if _, err := p.Launch(context.Background(), secret, testLaunch()); err == nil {
		t.Fatal("launch succeeded")
	}
	if n := len(fake.Sandboxes()); n != 0 {
		t.Fatalf("%d sandboxes left", n)
	}
}

// Capacity is retryable, an idempotency outcome unknown is ambiguous, and
// a refused request is permanent.
func TestCreateErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status               int
		code                 string
		retryable, ambiguous bool
	}{
		{503, "sandbox_capacity_unavailable", true, true},
		{429, "", true, false},
		{422, "idempotency_outcome_unknown", true, true},
		{400, "", false, false},
	} {
		p, fake, secret := setup(t)
		fake.FailCreate, fake.FailCreateCode = tc.status, tc.code
		_, err := p.Launch(context.Background(), secret, testLaunch())
		if cloud.IsRetryable(err) != tc.retryable || cloud.IsAmbiguous(err) != tc.ambiguous {
			t.Errorf("%d %s: err = %v, retryable %v, ambiguous %v", tc.status, tc.code, err, cloud.IsRetryable(err), cloud.IsAmbiguous(err))
		}
	}
}

// List reports the installation's sandboxes across pages, paused ones as
// stopped; Terminate refuses another installation's.
func TestListAndTerminate(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.PageSize = 1
	for _, sb := range []e2btest.Sandbox{
		{SandboxID: "a", State: "running", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r1"}},
		{SandboxID: "b", State: "running", Metadata: map[string]string{MetaInstallation: "7", MetaRunner: "r2"}},
		{SandboxID: "c", State: "paused", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r3"}},
		{SandboxID: "d", State: "running", Metadata: map[string]string{MetaInstallation: "42", MetaRunner: "r4"}},
	} {
		fake.Add(sb)
	}
	instances, err := p.List(context.Background(), secret, cloud.SandboxRegion, 42)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]cloud.Instance{}
	for _, i := range instances {
		got[i.InstanceID] = i
	}
	if len(got) != 3 || got["a"].RunnerID != "r1" || !got["c"].Stopped || got["d"].Stopped {
		t.Fatalf("instances = %+v", instances)
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "b", 42); err == nil {
		t.Error("another installation's sandbox was killed")
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "a", 42); err != nil {
		t.Fatal(err)
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "a", 42); err != nil {
		t.Errorf("terminate again = %v", err)
	}
	if len(fake.Sandboxes()) != 3 {
		t.Errorf("sandboxes = %+v", fake.Sandboxes())
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	p, _, secret := setup(t)
	id, err := p.Verify(context.Background(), secret, cloud.SandboxRegion)
	if err != nil || id.Principal != "API key e2b_***6789" {
		t.Fatalf("verify = %+v, %v", id, err)
	}
	wrong, _ := EncodeSecret(Secret{APIKey: "e2b_wrongwrongwrongwrong"})
	if _, err := p.Verify(context.Background(), wrong, cloud.SandboxRegion); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("wrong key = %v", err)
	}
	if _, err := p.Verify(context.Background(), secret, "eu"); err == nil {
		t.Error("a region was accepted")
	}
	if _, err := EncodeSecret(Secret{APIKey: "sk_notan_e2b_key_at_all"}); err == nil {
		t.Error("a key without e2b_ was accepted")
	}
	if _, err := EncodeSecret(Secret{APIKey: testKey, Domain: "not a domain"}); err == nil {
		t.Error("a bad domain was accepted")
	}
}

// Novita and Lelantos speak E2B's protocol: the same provider serves them
// through the first create API, with Novita's long_running flag, and
// without E2B's idempotency key.
func TestFlavors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		flavor Flavor
		key    string
	}{
		{FlavorNovita, "sk_0123456789abcdef0123456789"},
		{FlavorLelantos, "lel_0123456789abcdef0123456789"},
	} {
		fake := e2btest.New(t, tc.key)
		secret, err := tc.flavor.EncodeSecret(Secret{APIKey: tc.key})
		if err != nil {
			t.Fatalf("%s: %v", tc.flavor.Cloud, err)
		}
		p := New(Options{Flavor: tc.flavor, Endpoint: fake.URL, EnvdEndpoint: fake.EnvdURL})
		if p.Name() != tc.flavor.Cloud {
			t.Errorf("name = %s", p.Name())
		}
		if _, err := p.Verify(context.Background(), secret, cloud.SandboxRegion); err != nil {
			t.Fatalf("%s verify: %v", tc.flavor.Cloud, err)
		}
		id, err := p.Launch(context.Background(), secret, testLaunch())
		if err != nil {
			t.Fatalf("%s launch: %v", tc.flavor.Cloud, err)
		}
		sb := fake.Sandboxes()[0]
		if sb.SandboxID != id || len(fake.CreatePaths) != 1 || fake.CreatePaths[0] != "/sandboxes" || sb.Request["secure"] != true || len(sb.Processes) != 1 {
			t.Errorf("%s: sandbox %+v, create paths %v", tc.flavor.Cloud, sb, fake.CreatePaths)
		}
		if long := sb.Metadata[MetaLongRunning]; (long == "true") != tc.flavor.LongRunning {
			t.Errorf("%s: long_running = %q", tc.flavor.Cloud, long)
		}
		if _, err := tc.flavor.EncodeSecret(Secret{APIKey: "short"}); err == nil {
			t.Errorf("%s accepted a short key", tc.flavor.Cloud)
		}
		parsed, _ := tc.flavor.ParseSecret(secret)
		if parsed.Domain != tc.flavor.DefaultDomain {
			t.Errorf("%s domain = %s", tc.flavor.Cloud, parsed.Domain)
		}
	}
	if f, ok := FlavorFor("novita"); !ok || f.Title != "Novita" {
		t.Error("FlavorFor(novita) failed")
	}
	if _, err := FlavorLelantos.EncodeSecret(Secret{APIKey: "e2b_0123456789abcdef"}); err != nil {
		t.Errorf("lelantos refused its e2b_ alias: %v", err)
	}
}
