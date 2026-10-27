package daytona

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/daytona/daytonatest"

	apiclient "github.com/daytona/clients/api-client-go"
)

func stateOf(s string) apiclient.SandboxState { return apiclient.SandboxState(s) }

// The fake starts sandboxes on the next read, so polls can be quick and a
// sandbox that never starts gives up fast.
func TestMain(m *testing.M) {
	pollInterval = 20 * time.Millisecond
	startTimeout = 300 * time.Millisecond
	os.Exit(m.Run())
}

const (
	region   = "us"
	runnerID = "0192f3a4-5b6c-7d8e-9f00-112233445566"
	name     = "airship-0a1b2c3d4e5f"
	apiKey   = "dtn_0123456789abcdef0123456789abcdef"
)

type fixture struct {
	t        *testing.T
	server   *daytonatest.Server
	provider *Provider
	secret   []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	server := daytonatest.New(t, "us", "eu")
	secret, err := EncodeSecret(Secret{APIKey: apiKey, APIURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	return &fixture{t: t, server: server, provider: New(Options{}), secret: secret}
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       runnerID,
		RunnerName:     name,
		InstallationID: 42,
		Repository:     "acme/widgets",
		Region:         region,
		Machine: cloud.Machine{
			InstanceType: "2vcpu-4gb", Arch: "x64", Disk: cloud.Disk{SizeGB: 20},
			Daytona: &cloud.DaytonaOptions{CPU: 2, MemoryGB: 4},
		},
		Boot: cloud.Boot{
			JITConfig:          base64.StdEncoding.EncodeToString([]byte(`{"jit":"config"}`)),
			RunnerVersion:      "2.338.2",
			MaxLifetimeSeconds: 3600,
			Env:                map[string]string{"CI_TIER": "fast"},
		},
	}
}

func (f *fixture) launch(l cloud.Launch) (string, error) {
	return f.provider.Launch(context.Background(), f.secret, l)
}

func (f *fixture) mustLaunch(l cloud.Launch) *daytonatest.Sandbox {
	f.t.Helper()
	id, err := f.launch(l)
	if err != nil {
		f.t.Fatalf("launch: %v", err)
	}
	sb := f.server.Sandbox(id)
	if sb == nil {
		f.t.Fatalf("launch returned %s but no such sandbox exists", id)
	}

	return sb
}

func TestVerify(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	identity, err := f.provider.Verify(ctx, f.secret, region)
	if err != nil || identity.AccountID != f.server.OrganizationID || identity.Principal != "ci-key" {
		t.Fatalf("verify = %+v, %v", identity, err)
	}
	if _, err := f.provider.Verify(ctx, f.secret, "mars"); err == nil || !strings.Contains(err.Error(), "eu, us") {
		t.Errorf("unknown region: %v", err)
	}
	if _, err := f.provider.Verify(ctx, f.secret, "Not a region"); err == nil {
		t.Error("malformed region accepted")
	}
	f.server.BadKeys[apiKey] = true
	if _, err := f.provider.Verify(ctx, f.secret, region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("rejected key: %v", err)
	}
	f.server.BadKeys[apiKey] = false
	f.server.Permissions = []string{"write:sandboxes"}
	if _, err := f.provider.Verify(ctx, f.secret, region); !errors.Is(err, cloud.ErrBadCredentials) || !strings.Contains(err.Error(), "delete:sandboxes") {
		t.Errorf("key without delete permission: %v", err)
	}
	f.server.Permissions = []string{"write:sandboxes", "delete:sandboxes"}
	f.server.ExpiresAt = time.Now().Add(-time.Hour)
	if _, err := f.provider.Verify(ctx, f.secret, region); !errors.Is(err, cloud.ErrBadCredentials) || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired key: %v", err)
	}
	f.server.ExpiresAt = time.Time{}
	f.server.OrganizationID = ""
	if _, err := f.provider.Verify(ctx, f.secret, region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("key without organization: %v", err)
	}
	if _, err := f.provider.Verify(ctx, []byte("garbage"), region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Errorf("garbage secret: %v", err)
	}
}

// A launch creates the sandbox with the runner's name and labels, no
// auto-stop, delete-on-stop and a TTL past the runner's lifetime, waits
// for it to start, writes the boot script, starts it in the long-lived
// session and labels the sandbox as booted.
func TestLaunchCreatesBootsAndLabels(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sb := f.mustLaunch(testLaunch())
	if sb.Name != name || sb.Target != region || sb.State != "started" {
		t.Errorf("sandbox = %+v", sb)
	}
	body := sb.Body
	if body["autoStopInterval"] != 0.0 || body["autoDeleteInterval"] != 0.0 || body["ttlMinutes"] != 75.0 {
		t.Errorf("lifecycle settings = stop %v delete %v ttl %v", body["autoStopInterval"], body["autoDeleteInterval"], body["ttlMinutes"])
	}
	build, _ := body["buildInfo"].(map[string]any)
	if build["dockerfileContent"] != "FROM ubuntu:22.04\n" || body["cpu"] != 2.0 || body["memory"] != 4.0 || body["disk"] != 20.0 || body["snapshot"] != nil {
		t.Errorf("image and resources = %v %v %v %v", build, body["cpu"], body["memory"], body["disk"])
	}
	if sb.Labels[LabelInstallation] != "42" || sb.Labels[LabelRunner] != runnerID || sb.Labels[LabelRepository] != "acme/widgets" || sb.Labels[LabelBoot] != BootStarted {
		t.Errorf("labels = %v", sb.Labels)
	}
	if !strings.Contains(sb.Script, "ACTIONS_RUNNER_INPUT_JITCONFIG='"+testLaunch().Boot.JITConfig+"'") || !strings.Contains(sb.Script, "timeout --kill-after=60 \"$DEADLINE_SECONDS\"") ||
		!strings.Contains(sb.Script, "DEADLINE_SECONDS=3600") || !strings.Contains(sb.Script, "kill -TERM 1") || strings.Contains(sb.Script, "dockerd") {
		t.Errorf("script:\n%s", sb.Script)
	}
	if len(sb.Commands) != 1 || !strings.HasPrefix(sb.Commands[0], "nohup bash /tmp/airship-boot-"+runnerID+".sh") {
		t.Errorf("session commands = %v", sb.Commands)
	}
	if len(sb.Executed) != 1 || !strings.Contains(sb.Executed[0], "chmod 0700 /tmp/airship-boot-"+runnerID+".sh") {
		t.Errorf("executed = %v", sb.Executed)
	}
	// The sandbox's own TTL is omitted when the control plane sets no
	// lifetime.
	l := testLaunch()
	l.RunnerID, l.RunnerName, l.Boot.MaxLifetimeSeconds = "0192f3a4-5b6c-7d8e-9f00-112233445577", name+"b", 0
	if sb := f.mustLaunch(l); sb.Body["ttlMinutes"] != nil {
		t.Errorf("ttl without lifetime = %v", sb.Body["ttlMinutes"])
	}
}

// The same runner launched again is the same sandbox, whether the first
// launch finished or stopped anywhere along the way.
func TestRepeatedLaunchIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	first := f.mustLaunch(l)
	again, err := f.launch(l)
	if err != nil || again != first.ID {
		t.Fatalf("repeated launch = %s, %v; want %s", again, err, first.ID)
	}
	if f.server.Creates != 1 || len(f.server.Sandbox(first.ID).Commands) != 1 {
		t.Errorf("creates = %d, boot commands = %d", f.server.Creates, len(f.server.Sandbox(first.ID).Commands))
	}

	// A launch that died after starting the script but before labelling:
	// the next one finds the script running and only labels.
	g := newFixture(t)
	g.server.FailLabels = true
	if _, err := g.launch(l); err == nil {
		t.Fatal("launch succeeded without labelling")
	}
	if g.server.Deletes != 1 {
		t.Fatalf("the unlabelled sandbox should be deleted, deletes = %d", g.server.Deletes)
	}
	// ...unless the delete did not happen (a crash): put the sandbox back
	// as it was, running the script, and launch again.
	id := g.server.Add(name, region, "started", map[string]string{LabelInstallation: "42", LabelRunner: runnerID}, time.Now())
	g.server.FailLabels = false
	sbID, err := g.launch(l)
	if err != nil || sbID != id {
		t.Fatalf("launch onto an existing sandbox = %s, %v; want %s", sbID, err, id)
	}
	sb := g.server.Sandbox(id)
	if sb.Labels[LabelBoot] != BootStarted || len(sb.Commands) != 1 {
		t.Errorf("after relaunch: labels %v, commands %v", sb.Labels, sb.Commands)
	}
	if _, err := g.launch(l); err != nil || len(g.server.Sandbox(id).Commands) != 1 {
		t.Errorf("third launch: %v, commands %v", err, g.server.Sandbox(id).Commands)
	}

	// A name that belongs to something else is never adopted.
	k := newFixture(t)
	k.server.Add(name, region, "started", map[string]string{LabelInstallation: "42", LabelRunner: "another-runner"}, time.Now())
	if _, err := k.launch(l); err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "not this runner's") {
		t.Errorf("foreign sandbox: %v", err)
	}
}

// A create whose answer was lost is settled by name: the sandbox exists
// and carries the runner's label, so it is the one.
func TestLostCreateAnswerIsSettledByName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.DropNextCreate = true
	sb := f.mustLaunch(testLaunch())
	if f.server.Creates != 1 || sb.Labels[LabelBoot] != BootStarted || len(f.server.Sandboxes()) != 1 {
		t.Errorf("creates = %d, labels = %v, sandboxes = %d", f.server.Creates, sb.Labels, len(f.server.Sandboxes()))
	}
	// A 409 from a create that did go through earlier reads the same way.
	g := newFixture(t)
	g.server.Add(name, region, "started", map[string]string{LabelInstallation: "42", LabelRunner: runnerID}, time.Now())
	g.server.CreateFailure = &daytonatest.HTTPError{Status: http.StatusConflict, Message: "Sandbox with name " + name + " already exists"}
	if sb := g.mustLaunch(testLaunch()); sb.Labels[LabelBoot] != BootStarted {
		t.Errorf("after conflict: %v", sb.Labels)
	}
}

// Nothing from this provider is ambiguous: when a launch cannot be
// finished the sandbox is removed and the failure classified.
func TestFailedLaunchesCleanUpAndAreClassified(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		arrange   func(*daytonatest.Server)
		retryable bool
		deletes   int
		message   string
	}{
		{"build fails", func(s *daytonatest.Server) { s.FailBuild = true }, false, 1, "could not build the image"},
		{"never starts", func(s *daytonatest.Server) { s.HangStart = true }, true, 1, "did not start in time"},
		{"toolbox down", func(s *daytonatest.Server) { s.FailToolbox = true }, true, 1, "create session"},
		{"labels fail", func(s *daytonatest.Server) { s.FailLabels = true }, true, 1, "label sandbox"},
		{"quota", func(s *daytonatest.Server) {
			s.CreateFailure = &daytonatest.HTTPError{Status: http.StatusBadRequest, Message: "Sandbox CPU quota exceeded for the organization"}
		}, true, 0, "quota"},
		{"throttled", func(s *daytonatest.Server) {
			s.CreateFailure = &daytonatest.HTTPError{Status: http.StatusTooManyRequests, Message: "Too many requests"}
		}, true, 0, "Too many"},
		{"too large", func(s *daytonatest.Server) {
			s.CreateFailure = &daytonatest.HTTPError{Status: http.StatusBadRequest, Message: "Disk request 20GB exceeds maximum allowed per sandbox (10GB)."}
		}, false, 0, "per sandbox"},
		{"refused", func(s *daytonatest.Server) {
			s.CreateFailure = &daytonatest.HTTPError{Status: http.StatusBadRequest, Message: "Invalid target"}
		}, false, 0, "Invalid target"},
		{"forbidden", func(s *daytonatest.Server) {
			s.CreateFailure = &daytonatest.HTTPError{Status: http.StatusForbidden, Message: "Insufficient permissions"}
		}, true, 0, "Insufficient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.arrange(f.server)
			_, err := f.launch(testLaunch())
			if err == nil {
				t.Fatal("launch succeeded")
			}
			if cloud.IsRetryable(err) != tc.retryable || cloud.IsAmbiguous(err) || !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %v (retryable %v, ambiguous %v)", err, cloud.IsRetryable(err), cloud.IsAmbiguous(err))
			}
			if f.server.Deletes != tc.deletes || len(f.server.Sandboxes()) != 0 {
				t.Errorf("deletes = %d, remaining sandboxes = %d", f.server.Deletes, len(f.server.Sandboxes()))
			}
		})
	}
}

func TestSnapshotGPUAndOptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine = cloud.Machine{InstanceType: "my-snapshot", Arch: "x64", Daytona: &cloud.DaytonaOptions{
		Snapshot: "my-snapshot", Docker: true,
		Volumes: []cloud.DaytonaVolume{{Volume: "cache-vol", MountPath: "/mnt/cache", Subpath: "ci"}}, DomainAllowList: "github.com,*.githubusercontent.com",
	}}
	sb := f.mustLaunch(l)
	if sb.Body["snapshot"] != "my-snapshot" || sb.Body["cpu"] != nil || sb.Body["buildInfo"] != nil || sb.Body["domainAllowList"] != "github.com,*.githubusercontent.com" {
		t.Errorf("snapshot body = %v", sb.Body)
	}
	volumes, _ := sb.Body["volumes"].([]any)
	if len(volumes) != 1 {
		t.Errorf("volumes = %v", sb.Body["volumes"])
	} else if v, _ := volumes[0].(map[string]any); v["volumeId"] != "cache-vol" || v["mountPath"] != "/mnt/cache" || v["subpath"] != "ci" {
		t.Errorf("volume = %v", v)
	}
	if !strings.Contains(sb.Script, "dockerd") || !strings.Contains(sb.Script, "download.docker.com/linux/static/stable/x86_64/docker-"+"28.3.3") {
		t.Errorf("docker script:\n%s", sb.Script)
	}

	l = testLaunch()
	l.Machine.Image = "my-snapshot"
	l.Machine.Daytona.Snapshot = "my-snapshot"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) {
		t.Errorf("snapshot with image: %v", err)
	}

	l = testLaunch()
	l.RunnerID, l.RunnerName = "0192f3a4-5b6c-7d8e-9f00-112233445577", name+"g"
	l.Machine.GPUType, l.Machine.Spot, l.Boot.GPU = "h100", true, true
	sb = f.mustLaunch(l)
	if sb.Body["gpu"] != 1.0 || sb.Body["spot"] != true {
		t.Errorf("gpu body = %v", sb.Body)
	}
	if types, _ := sb.Body["gpuType"].([]any); len(types) != 1 || types[0] != "H100" {
		t.Errorf("gpuType = %v", sb.Body["gpuType"])
	}
	if !strings.Contains(sb.Script, "nvidia-smi") {
		t.Errorf("gpu script lacks the driver check")
	}

	l = testLaunch()
	l.Machine.Spot = true
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "GPU") {
		t.Errorf("spot without gpu: %v", err)
	}
	l = testLaunch()
	l.Machine.GPUType = "nvidia-tesla-t4"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) {
		t.Errorf("unknown gpu: %v", err)
	}
	l = testLaunch()
	l.Machine.Image = "ubuntu:latest"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) {
		t.Errorf("latest image: %v", err)
	}
	l = testLaunch()
	l.Boot.PullFromAPI = true
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) {
		t.Errorf("provision_via: %v", err)
	}
	if f.server.Creates != 2 {
		t.Errorf("refused launches reached the API: creates = %d", f.server.Creates)
	}
}

// List reports booted sandboxes by state, hides unbooted ones a launch may
// still own, and reports unbooted ones past the grace as stopped so the
// scan removes them.
func TestListReportsBootedOrAbandonedSandboxes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.PageSize = 2
	booted := map[string]string{LabelInstallation: "42", LabelRunner: "r-booted", LabelBoot: BootStarted}
	unbooted := map[string]string{LabelInstallation: "42", LabelRunner: "r-new"}
	live := f.server.Add("live", region, "started", booted, time.Now())
	f.server.Add("fresh", region, "started", unbooted, time.Now())
	old := f.server.Add("old", region, "started", map[string]string{LabelInstallation: "42", LabelRunner: "r-old"}, time.Now().Add(-BootGrace-time.Minute))
	stopped := f.server.Add("stopped", region, "stopped", booted, time.Now())
	stopping := f.server.Add("stopping", region, "destroying", booted, time.Now())
	failed := f.server.Add("failed", region, "build_failed", unbooted, time.Now())
	f.server.Add("elsewhere", "eu", "started", booted, time.Now())
	f.server.Add("theirs", region, "started", map[string]string{LabelInstallation: "7", LabelRunner: "r-x", LabelBoot: BootStarted}, time.Now())
	f.server.Add("destroyed", region, "destroyed", booted, time.Now())

	got, err := f.provider.List(context.Background(), f.secret, region, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]cloud.Instance{
		live:     {RunnerID: "r-booted", InstanceID: live},
		old:      {RunnerID: "r-old", InstanceID: old, Stopped: true},
		stopped:  {RunnerID: "r-booted", InstanceID: stopped, Stopped: true},
		stopping: {RunnerID: "r-booted", InstanceID: stopping, Stopping: true},
		failed:   {RunnerID: "r-new", InstanceID: failed, Stopped: true},
	}
	if len(got) != len(want) {
		t.Fatalf("listed %d instances, want %d: %+v", len(got), len(want), got)
	}
	for _, instance := range got {
		if want[instance.InstanceID] != instance {
			t.Errorf("instance %+v, want %+v", instance, want[instance.InstanceID])
		}
	}
	if _, err := f.provider.List(context.Background(), f.secret, "eu", 42); err != nil {
		t.Error(err)
	}
	f.server.BadKeys[apiKey] = true
	if _, err := f.provider.List(context.Background(), f.secret, region, 42); err == nil {
		t.Error("list with a rejected key succeeded")
	}
}

func TestTerminateChecksOwnership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sb := f.mustLaunch(testLaunch())
	ctx := context.Background()
	if err := f.provider.Terminate(ctx, f.secret, region, sb.ID, 43); err == nil || f.server.Deletes != 0 {
		t.Fatalf("terminate by another installation = %v, deletes %d", err, f.server.Deletes)
	}
	if err := f.provider.Terminate(ctx, f.secret, region, sb.ID, 42); err != nil || f.server.Deletes != 1 {
		t.Fatalf("terminate = %v, deletes %d", err, f.server.Deletes)
	}
	if err := f.provider.Terminate(ctx, f.secret, region, sb.ID, 42); err != nil || f.server.Deletes != 1 {
		t.Fatalf("terminate again = %v, deletes %d", err, f.server.Deletes)
	}
	if err := f.provider.Terminate(ctx, f.secret, region, "no-such-sandbox", 42); err != nil {
		t.Fatalf("terminate missing = %v", err)
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
	raw, err := EncodeSecret(Secret{APIKey: " " + apiKey + " "})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSecret(raw)
	if err != nil || s.APIKey != apiKey || s.APIURL != DefaultAPIURL {
		t.Errorf("parsed = %+v, %v", s, err)
	}
	if MaskKey(apiKey) != "dtn***def" {
		t.Errorf("mask = %s", MaskKey(apiKey))
	}
	for _, bad := range []Secret{{APIKey: ""}, {APIKey: "short"}, {APIKey: apiKey, APIURL: "ftp://x"}, {APIKey: apiKey, APIURL: "https://u:p@x.io"}, {APIKey: "has space " + apiKey}} {
		if _, err := EncodeSecret(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if _, err := ParseSecret([]byte(`{"apiKey":""}`)); err == nil {
		t.Error("empty key parsed")
	}
}

func TestDescribeStates(t *testing.T) {
	t.Parallel()
	now := time.Now()
	fresh := now.Add(-time.Minute).Format(time.RFC3339)
	booted := map[string]string{LabelRunner: "r", LabelBoot: BootStarted}
	bare := map[string]string{LabelRunner: "r"}
	for _, tc := range []struct {
		state   string
		labels  map[string]string
		created string
		ok      bool
		want    cloud.Instance
	}{
		{"started", booted, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x"}},
		{"creating", booted, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x"}},
		{"unknown", booted, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x"}},
		{"stopping", booted, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x", Stopping: true}},
		{"paused", booted, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x", Stopped: true}},
		{"error", bare, fresh, true, cloud.Instance{RunnerID: "r", InstanceID: "x", Stopped: true}},
		{"started", bare, fresh, false, cloud.Instance{}},
		{"started", bare, "garbage", true, cloud.Instance{RunnerID: "r", InstanceID: "x", Stopped: true}},
		{"destroyed", booted, fresh, false, cloud.Instance{}},
	} {
		got, ok := describe("x", tc.labels, ptr(stateOf(tc.state)), ptr(tc.created), now)
		if ok != tc.ok || got != tc.want {
			t.Errorf("describe(%s, %v) = %+v, %v; want %+v, %v", tc.state, tc.labels, got, ok, tc.want, tc.ok)
		}
	}
}

// Launchers on several replicas may launch the same runner at once (a lease
// handed on after a crash, a retried call racing its predecessor). Whatever
// the interleaving, there is one sandbox, every caller gets its ID, and the
// boot ends labelled; the script's own lock keeps a second start from
// registering twice.
func TestConcurrentLaunchesShareOneSandbox(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.StartAfterReads = 2
	l := testLaunch()
	const callers = 8
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	for range callers {
		go func() {
			id, err := f.launch(l)
			ids <- id
			errs <- err
		}()
	}
	var first string
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent launch: %v", err)
		}
		id := <-ids
		if first == "" {
			first = id
		}
		if id != first {
			t.Errorf("launches returned different sandboxes: %s and %s", first, id)
		}
	}
	all := f.server.Sandboxes()
	if len(all) != 1 || all[0].ID != first {
		t.Fatalf("sandboxes = %+v, want exactly %s", all, first)
	}
	sb := f.server.Sandbox(first)
	if sb.Labels[LabelBoot] != BootStarted || sb.Labels[LabelRunner] != runnerID || sb.State != "started" {
		t.Errorf("after racing launches: %+v", sb)
	}
	if f.server.Deletes != 0 {
		t.Errorf("a racing launch deleted the sandbox (%d deletes)", f.server.Deletes)
	}
	if len(sb.Commands) == 0 || len(sb.Commands) > callers {
		t.Errorf("boot commands = %d", len(sb.Commands))
	}
	for _, c := range sb.Commands {
		if c != sb.Commands[0] {
			t.Errorf("racing launches started different commands: %q vs %q", c, sb.Commands[0])
		}
	}
	// And once settled, a late launch is a pure lookup.
	creates := f.server.Creates
	if id, err := f.launch(l); err != nil || id != first || f.server.Creates != creates {
		t.Errorf("late launch = %s, %v, creates %d -> %d", id, err, creates, f.server.Creates)
	}
}

// A launch whose boot fails after another launcher of the same runner has
// finished theirs must not delete the sandbox with the runner in it: it is
// the launch's result.
func TestFailedBootDefersToTheLauncherThatFinished(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.FailLabels = true
	f.server.BeforeFailedLabel = func(sb *daytonatest.Sandbox) { sb.Labels[LabelBoot] = BootStarted }
	l := testLaunch()
	id, err := f.launch(l)
	if err != nil || id == "" {
		t.Fatalf("launch = %q, %v; want the sandbox the other launcher booted", id, err)
	}
	if f.server.Deletes != 0 || len(f.server.Sandboxes()) != 1 {
		t.Errorf("deletes = %d, sandboxes = %d", f.server.Deletes, len(f.server.Sandboxes()))
	}

	// Without that other launcher the failed boot still cleans up.
	g := newFixture(t)
	g.server.FailLabels = true
	if _, err := g.launch(l); err == nil || g.server.Deletes != 1 {
		t.Errorf("unlabelled failure: err %v, deletes %d", err, g.server.Deletes)
	}
}

// Daytona's own words, as answered to real requests on 2026-10-27, sort
// into waits for capacity and definitions that can never launch.
func TestRealDaytonaMessagesAreClassified(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status    int
		message   string
		retryable bool
	}{
		{400, "Total memory limit exceeded. Maximum allowed: 10GiB.\nTo increase concurrency limits, upgrade your organization's Tier by visiting https://app.daytona.io/dashboard/limits.", true},
		{400, "Disk request 20GB exceeds maximum allowed per sandbox (10GB)", false},
		{400, "Organization doesn't have GPU credits. Add more by visiting the Wallet page", false},
		{400, "Organization doesn't have spot GPU credits. Add more by visiting the Wallet page", false},
		{400, "Network access is restricted and cannot be overridden at the sandbox level. Remove domainAllowList from the request.", false},
		{403, "Declarative builds are not available to your organization in region us-west-4.", false},
		{400, "Snapshot daytona-small is not available in region us-west-4", false},
		{400, "No runners are configured in region 'us-west-4' for sandbox class 'container'. Try a different region or sandbox class.", false},
		{409, "Sandbox with name airship-x already exists", false},
		{429, "Too Many Requests", true},
		{503, "Service Unavailable", true},
	}
	for _, tc := range cases {
		err := classify(&apiError{op: "create sandbox", status: tc.status, message: tc.message})
		var le *cloud.LaunchError
		if !errors.As(err, &le) {
			t.Fatalf("%q: not a LaunchError: %v", tc.message, err)
		}
		if le.Retryable != tc.retryable || le.Ambiguous {
			t.Errorf("%d %q: retryable=%v ambiguous=%v, want retryable=%v", tc.status, tc.message, le.Retryable, le.Ambiguous, tc.retryable)
		}
	}
}
