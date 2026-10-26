package docker_test

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agent/docker"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// TestAgainstDocker drives a real Docker daemon. It runs only when
// AIRSHIP_DOCKER_TEST=1, because it pulls the runner image.
func TestAgainstDocker(t *testing.T) {
	if os.Getenv("AIRSHIP_DOCKER_TEST") != "1" {
		t.Skip("set AIRSHIP_DOCKER_TEST=1 to run against the local Docker daemon")
	}
	ctx := context.Background()
	provider := docker.New(docker.Config{Socket: os.Getenv("AIRSHIP_DOCKER_SOCKET")})
	dataPlane := "test-" + time.Now().Format("150405.000")
	launch := agentproto.Launch{
		RunnerID:   "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40",
		RunnerName: "airship-dockertest-" + time.Now().Format("150405"),
		// Not a real registration: the runner starts and retries, which is
		// all this test needs.
		JITConfig:          base64.StdEncoding.EncodeToString([]byte(`{".runner":"e30="}`)),
		RunnerVersion:      "2.338.2",
		MaxLifetimeSeconds: 120,
		Profile:            agentproto.LaunchProfile{Label: "airship-docker", Arch: "x64", InstanceType: "1cpu-1gb"},
	}

	id, err := provider.Launch(ctx, dataPlane, launch)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), id) })

	if _, err := provider.Launch(ctx, dataPlane, launch); err == nil {
		t.Fatal("launching the same runner twice started a second container")
	}

	// A broken entrypoint would exit at once and be auto-removed; the runner
	// keeps retrying its bogus registration instead.
	time.Sleep(3 * time.Second)
	list, err := provider.List(ctx, dataPlane)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 1 && list[0].Stopping {
		t.Fatal("runner container exited immediately")
	}
	if len(list) != 1 || list[0].InstanceID != id || list[0].RunnerID != launch.RunnerID {
		t.Fatalf("list = %+v", list)
	}
	other, err := provider.List(ctx, "some-other-data-plane")
	if err != nil || len(other) != 0 {
		t.Fatalf("another data plane sees this container: %+v %v", other, err)
	}

	if err := provider.Terminate(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := provider.Terminate(ctx, id); err != nil {
		t.Fatalf("terminating a gone container: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		list, err = provider.List(ctx, dataPlane)
		if err == nil && len(list) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("container still listed after terminate: %+v", list)
}
