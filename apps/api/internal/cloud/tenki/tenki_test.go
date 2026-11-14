package tenki_test

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/tenki"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/tenki/tenkitest"
)

const testKey = "tk_0123456789abcdef0123456789"

func setup(t *testing.T) (*tenki.Provider, *tenkitest.Fake, []byte) {
	t.Helper()
	fake := tenkitest.New(testKey)
	secret, err := tenki.EncodeSecret(tenki.Secret{APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}

	return tenki.New(tenki.Options{Connect: fake.Connect}), fake, secret
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       "0190c0de-0000-7000-8000-000000000001",
		RunnerName:     "airship-acme-api-1",
		InstallationID: 42,
		Repository:     "acme/api",
		Region:         cloud.SandboxRegion,
		Machine: cloud.Machine{
			InstanceType: "4vcpu-8gb",
			Image:        "ghcr.io/acme/ci:2026.11",
			Arch:         "x64",
			Disk:         cloud.Disk{SizeGB: 20},
			Sandbox:      &cloud.SandboxOptions{CPU: 4, MemoryGB: 8, Docker: true},
		},
		Boot: cloud.Boot{JITConfig: "aml0LXNlY3JldA==", RunnerVersion: "2.330.0", MaxLifetimeSeconds: 3600},
	}
}

// A launch creates one tagged sandbox shaped as asked and starts the
// script detached, with the script in the exec's environment; a repeated
// launch finds the sandbox by its runner tag.
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
	s := sandboxes[0]
	spec := s.Spec
	if spec.Name != launch.RunnerName || spec.Image != "ghcr.io/acme/ci:2026.11" || spec.CPUCores != 4 || spec.MemoryMB != 8192 || spec.DiskGB != 20 ||
		spec.MaxDuration.Minutes() != 75 || spec.Metadata[tenki.MetaInstallation] != "42" || spec.Metadata[tenki.MetaRepository] != "acme/api" ||
		!slices.Contains(spec.Tags, "airship-installation-42") || !slices.Contains(spec.Tags, tenki.RunnerTag(launch.RunnerID)) {
		t.Errorf("spec = %+v", spec)
	}
	if len(s.Execs) != 2 {
		t.Fatalf("execs = %d", len(s.Execs))
	}
	exec := s.Execs[0]
	if strings.Contains(strings.Join(exec.Argv, " "), "aml0LXNlY3JldA==") || !strings.Contains(exec.Argv[2], "nohup") {
		t.Errorf("argv = %v", exec.Argv)
	}
	script, err := base64.StdEncoding.DecodeString(exec.Env[bootscript.BootEnv])
	if err != nil || !strings.Contains(string(script), "ACTIONS_RUNNER_INPUT_JITCONFIG='aml0LXNlY3JldA=='") || strings.Contains(string(script), "kill -TERM 1") {
		t.Error("the script is not in the exec's environment")
	}
	if fake.Closed == 0 {
		t.Error("the client was never closed")
	}
}

func TestLaunchFailures(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.LoseCreateAnswer = true
	if id, err := p.Launch(context.Background(), secret, testLaunch()); err != nil || fake.Sandboxes()[0].ID != id {
		t.Fatalf("lost answer: %q, %v", id, err)
	}

	p2, fake2, secret2 := setup(t)
	fake2.ExitCode = 1
	if _, err := p2.Launch(context.Background(), secret2, testLaunch()); err == nil {
		t.Fatal("a failed start succeeded")
	}
	if n := len(fake2.Sandboxes()); n != 0 {
		t.Errorf("%d sandboxes left after a failed start", n)
	}

	p3, fake3, secret3 := setup(t)
	fake3.FailCreate = errors.New("invalid image")
	if _, err := p3.Launch(context.Background(), secret3, testLaunch()); err == nil || cloud.IsRetryable(err) {
		t.Errorf("refused create = %v", err)
	}
	fake3.FailCreate = tenki.ErrTransient
	if _, err := p3.Launch(context.Background(), secret3, testLaunch()); !cloud.IsRetryable(err) || !cloud.IsAmbiguous(err) {
		t.Errorf("transient create = %v", err)
	}
	bad := testLaunch()
	bad.Machine.Disk.SizeGB = 500
	if _, err := p3.Launch(context.Background(), secret3, bad); err == nil {
		t.Error("a 500 GB disk was accepted")
	}
}

func TestListAndTerminate(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	for _, s := range []tenki.Session{
		{ID: "a", State: "RUNNING", Metadata: map[string]string{tenki.MetaInstallation: "42", tenki.MetaRunner: "r1"}, Tags: []string{"airship-installation-42"}},
		{ID: "b", State: "RUNNING", Metadata: map[string]string{tenki.MetaInstallation: "7", tenki.MetaRunner: "r2"}, Tags: []string{"airship-installation-7"}},
		{ID: "c", State: "PAUSED", Metadata: map[string]string{tenki.MetaInstallation: "42", tenki.MetaRunner: "r3"}, Tags: []string{"airship-installation-42"}},
		{ID: "d", State: "RUNNING", Metadata: map[string]string{tenki.MetaInstallation: "7"}, Tags: []string{"airship-installation-42"}},
	} {
		fake.Add(s)
	}
	instances, err := p.List(context.Background(), secret, cloud.SandboxRegion, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 || instances[0].RunnerID != "r1" || !instances[1].Stopped {
		t.Fatalf("instances = %+v", instances)
	}
	if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, "b", 42); err == nil {
		t.Error("another installation's sandbox was closed")
	}
	for _, id := range []string{"a", "a", "missing"} {
		if err := p.Terminate(context.Background(), secret, cloud.SandboxRegion, id, 42); err != nil {
			t.Errorf("terminate %s = %v", id, err)
		}
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	p, _, secret := setup(t)
	if id, err := p.Verify(context.Background(), secret, cloud.SandboxRegion); err != nil || id.Principal != "API key tk_***6789" {
		t.Fatalf("verify = %+v, %v", id, err)
	}
	wrong, _ := tenki.EncodeSecret(tenki.Secret{APIKey: "tk_wrongwrongwrongwrong"})
	if _, err := p.Verify(context.Background(), wrong, cloud.SandboxRegion); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("wrong key = %v", err)
	}
	if _, err := tenki.EncodeSecret(tenki.Secret{APIKey: "sk_notatenkikey123"}); err == nil {
		t.Error("a key without tk_ was accepted")
	}
	if _, err := tenki.EncodeSecret(tenki.Secret{APIKey: testKey, WorkspaceID: "bad id!"}); err == nil {
		t.Error("a bad workspace was accepted")
	}
}

// The real SDK client can be built from a credential without a network
// call.
func TestRealClientBuilds(t *testing.T) {
	t.Parallel()
	p := tenki.New(tenki.Options{})
	secret, _ := tenki.EncodeSecret(tenki.Secret{APIKey: testKey, APIURL: "http://127.0.0.1:1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Verify(ctx, secret, cloud.SandboxRegion); err == nil {
		t.Error("verify against nothing succeeded")
	}
}
