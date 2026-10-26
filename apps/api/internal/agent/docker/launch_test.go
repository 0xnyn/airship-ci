package docker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agent/docker"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

type createBody struct {
	Env        []string
	HostConfig map[string]any
}

// fakeEngine serves just enough of the Docker Engine API on a unix socket
// to capture container create requests.
func fakeEngine(t *testing.T) (string, func() []createBody) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk") // unix socket paths are short on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var created []createBody
	server := &http.Server{ReadHeaderTimeout: 0, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			var body createBody
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			created = append(created, body)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"c1"}`))
		case strings.HasSuffix(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK) // image pulls
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	return socket, func() []createBody {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(created)
	}
}

func unitLaunch() agentproto.Launch {
	return agentproto.Launch{
		RunnerID: "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40", RunnerName: "airship-unit", JITConfig: "ZXlK",
		RunnerVersion: "2.338.2", MaxLifetimeSeconds: 60,
		Profile: agentproto.LaunchProfile{Label: "cpu", Arch: "x64", InstanceType: "2vcpu-4gb", Env: map[string]string{"B": "2", "A": "1 $(x)"}},
	}
}

func TestLaunchPassesEnv(t *testing.T) {
	t.Parallel()
	socket, created := fakeEngine(t)
	if _, err := docker.New(docker.Config{Socket: socket}).Launch(context.Background(), "dp", unitLaunch()); err != nil {
		t.Fatal(err)
	}
	body := created()[0]
	want := []string{"ACTIONS_RUNNER_INPUT_JITCONFIG=ZXlK", "A=1 $(x)", "B=2"}
	if !slices.Equal(body.Env, want) {
		t.Fatalf("env = %v, want %v", body.Env, want)
	}
	if body.HostConfig["Privileged"] != nil {
		t.Fatal("container is privileged without asking")
	}
}

func TestPrivilegedNeedsOptIn(t *testing.T) {
	t.Parallel()
	socket, created := fakeEngine(t)
	launch := unitLaunch()
	launch.Profile.Privileged = true

	_, err := docker.New(docker.Config{Socket: socket}).Launch(context.Background(), "dp", launch)
	var launchErr *agent.LaunchError
	if !errors.As(err, &launchErr) || launchErr.Retryable || !strings.Contains(err.Error(), "AIRSHIP_DOCKER_ALLOW_PRIVILEGED") {
		t.Fatalf("privileged launch without opt-in = %v", err)
	}
	if len(created()) != 0 {
		t.Fatal("a container was created anyway")
	}

	if _, err := docker.New(docker.Config{Socket: socket, AllowPrivileged: true}).Launch(context.Background(), "dp", launch); err != nil {
		t.Fatal(err)
	}
	if body := created()[0]; body.HostConfig["Privileged"] != true {
		t.Fatalf("host config = %v, want privileged", body.HostConfig)
	}
}

func TestLaunchRejectsReservedEnv(t *testing.T) {
	t.Parallel()
	socket, created := fakeEngine(t)
	launch := unitLaunch()
	launch.Profile.Env = map[string]string{"ACTIONS_RUNNER_INPUT_JITCONFIG": "evil"}
	if _, err := docker.New(docker.Config{Socket: socket}).Launch(context.Background(), "dp", launch); err == nil || len(created()) != 0 {
		t.Fatalf("reserved variable accepted: %v", err)
	}
}
