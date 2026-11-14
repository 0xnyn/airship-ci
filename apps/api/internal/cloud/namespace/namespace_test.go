package namespace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/namespace/namespacetest"
)

const testToken = "nsct_0123456789abcdef0123456789"

func init() {
	pollInterval = time.Millisecond
}

func setup(t *testing.T) (*Provider, *namespacetest.Server, []byte) {
	t.Helper()
	fake := namespacetest.New(t, testToken)
	secret, err := EncodeSecret(Secret{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}

	return New(Options{Endpoint: fake.URL}), fake, secret
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       "0190c0de-0000-7000-8000-000000000001",
		RunnerName:     "airship-acme-api-1",
		InstallationID: 42,
		Repository:     "acme/api",
		Region:         "us",
		Machine: cloud.Machine{
			InstanceType: "4vcpu-8gb",
			Image:        "ghcr.io/acme/ci:2026.11",
			Arch:         "x64",
			Sandbox:      &cloud.SandboxOptions{CPU: 4, MemoryGB: 8, Docker: true},
		},
		Boot: cloud.Boot{JITConfig: "aml0LXNlY3JldA==", RunnerVersion: "2.330.0", MaxLifetimeSeconds: 3600},
	}
}

// A launch creates one labelled instance whose JOB container waits for
// the script, then writes the script through RunCommandSync's environment;
// a repeated launch finds the same instance.
func TestLaunchCreatesBootsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.FailCommands = 2
	launch := testLaunch()
	id, err := p.Launch(context.Background(), secret, launch)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Launch(context.Background(), secret, launch)
	if err != nil || again != id {
		t.Fatalf("second launch = %q, %v; want %q", again, err, id)
	}
	instances := fake.Instances()
	if len(instances) != 1 || fake.Creates() != 1 {
		t.Fatalf("instances = %d, creates = %d", len(instances), fake.Creates())
	}
	inst := instances[0]
	if inst.Labels[LabelInstallation] != "42" || inst.Labels[LabelRunner] != launch.RunnerID {
		t.Errorf("labels = %v", inst.Labels)
	}
	shape, _ := inst.Request["shape"].(map[string]any)
	if shape["virtualCpu"] != 4.0 || shape["memoryMegabytes"] != 8192.0 || shape["machineArch"] != "amd64" || shape["os"] != "linux" {
		t.Errorf("shape = %v", shape)
	}
	containers, _ := inst.Request["containers"].([]any)
	c, _ := containers[0].(map[string]any)
	entry, _ := c["entrypoint"].([]any)
	if c["imageRef"] != "ghcr.io/acme/ci:2026.11" || c["workloadType"] != "JOB" || c["dockerSockPath"] != DockerSocket || len(entry) != 3 ||
		strings.Contains(entry[2].(string), "aml0LXNlY3JldA==") || !strings.Contains(entry[2].(string), bootscript.ReadyPath(launch.RunnerID)) {
		t.Errorf("container = %v", c)
	}
	deadline, err := time.Parse(time.RFC3339, inst.Request["deadline"].(string))
	if err != nil || time.Until(deadline) < time.Hour || time.Until(deadline) > 2*time.Hour {
		t.Errorf("deadline = %v, %v", inst.Request["deadline"], err)
	}
	if !strings.Contains(inst.Files["boot"], "ACTIONS_RUNNER_INPUT_JITCONFIG='aml0LXNlY3JldA=='") || strings.Contains(inst.Files["boot"], "kill -TERM 1") {
		t.Error("the boot script did not arrive through the command's environment")
	}
	for _, argv := range inst.Commands {
		if strings.Contains(strings.Join(argv, " "), "aml0LXNlY3JldA==") {
			t.Error("the JIT config is in a command line")
		}
	}
}

func TestLostCreateAnswerAndFailures(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.LoseCreateAnswer = true
	id, err := p.Launch(context.Background(), secret, testLaunch())
	if err != nil || fake.Creates() != 1 || fake.Instances()[0].ID != id {
		t.Fatalf("lost answer: id %q, err %v, creates %d", id, err, fake.Creates())
	}

	p2, fake2, secret2 := setup(t)
	fake2.StartAs = "ERROR"
	if _, err := p2.Launch(context.Background(), secret2, testLaunch()); !cloud.IsRetryable(err) {
		t.Errorf("failed start = %v, want retryable", err)
	}
	if n := len(fake2.Instances()); n != 0 {
		t.Errorf("%d instances left after a failed start", n)
	}

	p3, fake3, secret3 := setup(t)
	fake3.FailCreate = "invalid_argument"
	if _, err := p3.Launch(context.Background(), secret3, testLaunch()); err == nil || cloud.IsRetryable(err) || cloud.IsAmbiguous(err) {
		t.Errorf("refused create = %v, want permanent", err)
	}
	fake3.FailCreate = "resource_exhausted"
	if _, err := p3.Launch(context.Background(), secret3, testLaunch()); !cloud.IsRetryable(err) {
		t.Errorf("exhausted = %v, want retryable", err)
	}
	bad := testLaunch()
	bad.Machine.Sandbox = &cloud.SandboxOptions{}
	if _, err := p3.Launch(context.Background(), secret3, bad); err == nil {
		t.Error("a launch without a shape was accepted")
	}
	bad = testLaunch()
	bad.Region = "ap"
	if _, err := p3.Launch(context.Background(), secret3, bad); err == nil {
		t.Error("an unknown region was accepted")
	}
}

func TestListAndTerminate(t *testing.T) {
	t.Parallel()
	p, fake, secret := setup(t)
	fake.PageSize = 1
	for _, inst := range []namespacetest.Instance{
		{ID: "a", Status: "RUNNING", Labels: map[string]string{LabelInstallation: "42", LabelRunner: "r1"}},
		{ID: "b", Status: "RUNNING", Labels: map[string]string{LabelInstallation: "7", LabelRunner: "r2"}},
		{ID: "c", Status: "SUSPENDED", Labels: map[string]string{LabelInstallation: "42", LabelRunner: "r3"}},
		{ID: "d", Status: "DESTROYING", Labels: map[string]string{LabelInstallation: "42", LabelRunner: "r4"}},
	} {
		fake.Add(inst)
	}
	instances, err := p.List(context.Background(), secret, "us", 42)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]cloud.Instance{}
	for _, i := range instances {
		got[i.InstanceID] = i
	}
	if len(got) != 3 || got["a"].RunnerID != "r1" || !got["c"].Stopped || !got["d"].Stopping {
		t.Fatalf("instances = %+v", instances)
	}
	if err := p.Terminate(context.Background(), secret, "us", "b", 42); err == nil {
		t.Error("another installation's instance was destroyed")
	}
	for _, id := range []string{"a", "a", "missing"} {
		if err := p.Terminate(context.Background(), secret, "us", id, 42); err != nil {
			t.Errorf("terminate %s = %v", id, err)
		}
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	p, _, secret := setup(t)
	if id, err := p.Verify(context.Background(), secret, "eu"); err != nil || id.Principal != "token nsct***6789" {
		t.Fatalf("verify = %+v, %v", id, err)
	}
	wrong, _ := EncodeSecret(Secret{Token: "nsct_wrongwrongwrongwrong"})
	if _, err := p.Verify(context.Background(), wrong, "us"); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("wrong token = %v", err)
	}
	if _, err := p.Verify(context.Background(), secret, "global"); err == nil {
		t.Error("a non-region was accepted")
	}
	if _, err := EncodeSecret(Secret{Token: "short"}); err == nil {
		t.Error("a short token was accepted")
	}
}
