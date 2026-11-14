package modal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/modal"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/modal/modaltest"
)

const (
	tokenID     = "ak-0123456789abcdef"
	tokenSecret = "as-0123456789abcdef"
)

func setup(t *testing.T) (*modal.Provider, *modaltest.Fake, []byte) {
	t.Helper()
	fake := modaltest.New(tokenID)
	secret, err := modal.EncodeSecret(modal.Secret{TokenID: tokenID, TokenSecret: tokenSecret})
	if err != nil {
		t.Fatal(err)
	}

	return modal.New(modal.Options{Connect: fake.Connect}), fake, secret
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       "0190c0de-0000-7000-8000-000000000001",
		RunnerName:     "airship-acme-api-1",
		InstallationID: 42,
		Repository:     "acme/api",
		Region:         cloud.SandboxRegion,
		Machine: cloud.Machine{
			InstanceType: "4vcpu-16gb",
			Arch:         "x64",
			GPUType:      "h100",
			Sandbox:      &cloud.SandboxOptions{CPU: 4, MemoryGB: 16, Docker: true},
		},
		Boot: cloud.Boot{JITConfig: "aml0LXNlY3JldA==", RunnerVersion: "2.330.0", MaxLifetimeSeconds: 3600, GPU: true},
	}
}

// A launch creates one tagged, named Sandbox whose command waits for the
// script, then writes it through an exec's environment; a repeated launch
// finds the Sandbox by name.
func TestLaunchCreatesBootsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
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
	if len(sandboxes) != 1 {
		t.Fatalf("sandboxes = %d", len(sandboxes))
	}
	sb := sandboxes[0]
	spec := sb.Spec
	if spec.Name != launch.RunnerName || spec.Image != modal.DefaultImage || spec.CPU != 4 || spec.MemoryMB != 16384 || spec.GPU != "H100" || !spec.VM ||
		spec.Timeout != 75*time.Minute || spec.Tags[modal.TagInstallation] != "42" || spec.Tags[modal.TagRunner] != launch.RunnerID {
		t.Errorf("spec = %+v", spec)
	}
	if len(spec.Command) != 3 || strings.Contains(spec.Command[2], "aml0LXNlY3JldA==") || !strings.Contains(spec.Command[2], bootscript.ReadyPath(launch.RunnerID)) {
		t.Errorf("command = %v", spec.Command)
	}
	if len(sb.Execs) != 2 {
		t.Fatalf("execs = %d", len(sb.Execs))
	}
	script, err := base64.StdEncoding.DecodeString(sb.Execs[0].Env[bootscript.BootEnv])
	if err != nil || !strings.Contains(string(script), "ACTIONS_RUNNER_INPUT_JITCONFIG='aml0LXNlY3JldA=='") || !strings.Contains(string(script), "nvidia-smi") ||
		strings.Contains(strings.Join(sb.Execs[0].Argv, " "), "aml0LXNlY3JldA==") {
		t.Error("the script did not arrive through the exec's environment")
	}
}

func TestLaunchFailures(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.LoseCreateAnswer = true
	if id, err := p.Launch(context.Background(), secret, testLaunch()); err != nil || fake.Sandboxes()[0].ID != id {
		t.Fatalf("lost answer: %q, %v", id, err)
	}

	// A running Sandbox with the runner's name that is someone else's.
	p2, fake2, secret2 := setup(t)
	fake2.Add("sb-other", modal.Spec{Name: "airship-acme-api-1", Tags: map[string]string{modal.TagRunner: "someone-else"}})
	if _, err := p2.Launch(context.Background(), secret2, testLaunch()); err == nil || cloud.IsRetryable(err) {
		t.Errorf("name clash = %v, want permanent", err)
	}

	p3, fake3, secret3 := setup(t)
	fake3.ExitCode = 1
	if _, err := p3.Launch(context.Background(), secret3, testLaunch()); err == nil {
		t.Fatal("a failed write succeeded")
	}
	if n := len(fake3.Sandboxes()); n != 0 {
		t.Errorf("%d sandboxes left", n)
	}
	bad := testLaunch()
	bad.Machine.GPUType = "tpu"
	if _, err := p3.Launch(context.Background(), secret3, bad); err == nil {
		t.Error("an unknown GPU was accepted")
	}
}

func TestListAndTerminate(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.Add("a", modal.Spec{Name: "a", Tags: map[string]string{modal.TagInstallation: "42", modal.TagRunner: "r1"}})
	fake.Add("b", modal.Spec{Name: "b", Tags: map[string]string{modal.TagInstallation: "7", modal.TagRunner: "r2"}})
	instances, err := p.List(context.Background(), secret, cloud.SandboxRegion, 42)
	if err != nil || len(instances) != 1 || instances[0].RunnerID != "r1" || instances[0].InstanceID != "a" {
		t.Fatalf("instances = %+v, %v", instances, err)
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "b", 42); err == nil {
		t.Error("another installation's sandbox was terminated")
	}
	for _, id := range []string{"a", "missing"} {
		if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, id, 42); err != nil {
			t.Errorf("terminate %s = %v", id, err)
		}
	}
	if len(fake.Sandboxes()) != 1 {
		t.Errorf("sandboxes = %+v", fake.Sandboxes())
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	if id, err := p.Verify(context.Background(), secret, cloud.SandboxRegion); err != nil || !strings.Contains(id.Principal, tokenID) || fake.Apps != 1 {
		t.Fatalf("verify = %+v, %v", id, err)
	}
	wrong, _ := modal.EncodeSecret(modal.Secret{TokenID: "ak-wrongwrongwrong", TokenSecret: tokenSecret})
	if _, err := p.Verify(context.Background(), wrong, cloud.SandboxRegion); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("wrong token = %v", err)
	}
	for _, bad := range []modal.Secret{{TokenID: "x", TokenSecret: tokenSecret}, {TokenID: tokenID, TokenSecret: "y"}, {TokenID: tokenID, TokenSecret: tokenSecret, Environment: "Bad Env"}} {
		if _, err := modal.EncodeSecret(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	if g, ok := modal.GPUType("a100-80gb"); !ok || g != "A100-80GB" {
		t.Errorf("GPUType = %q", g)
	}
}

// The real SDK client is built from a credential; with no time to call
// Modal, verification fails rather than hangs.
func TestRealClientBuilds(t *testing.T) {
	t.Parallel()
	p := modal.New(modal.Options{})
	secret, _ := modal.EncodeSecret(modal.Secret{TokenID: tokenID, TokenSecret: tokenSecret})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Verify(ctx, secret, cloud.SandboxRegion); err == nil {
		t.Error("verify with a cancelled context succeeded")
	}
}
