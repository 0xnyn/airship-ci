// Package docker launches runners as containers on one Docker host, through
// the Engine API on its unix socket.
//
// A container shares the host kernel, so this provider is for trusted
// workloads, development and evaluation. Use the aws provider, one VM per
// job, for untrusted code.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

const (
	labelDataPlane = "dev.airship.data-plane"
	labelRunner    = "dev.airship.runner-id"
	apiVersion     = "v1.43"
	// killGrace lets the runner report its job's result after SIGTERM.
	killGrace = 60
)

// Config configures the Docker provider.
type Config struct {
	// Socket is the Docker Engine socket path.
	Socket string
	// DefaultImage is used when a profile names no image. "%s" is replaced
	// with the runner version.
	DefaultImage string
	// Network is the Docker network for runner containers ("" = default).
	Network string
	// AllowPrivileged lets runner configs ask for privileged containers.
	// A privileged container is root on the host, so the host's operator
	// has to opt in; without it such launches fail.
	AllowPrivileged bool
}

// Provider is the Docker provider.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a Docker provider.
func New(cfg Config) *Provider {
	if cfg.Socket == "" {
		cfg.Socket = "/var/run/docker.sock"
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "ghcr.io/actions/actions-runner:%s"
	}
	socket := cfg.Socket
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, "unix", socket)
		},
	}

	return &Provider{cfg: cfg, client: &http.Client{Transport: transport, Timeout: 10 * time.Minute}}
}

// Name implements agent.Provider.
func (*Provider) Name() string { return "docker" }

type containerSummary struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// List implements agent.Provider.
func (p *Provider) List(ctx context.Context, dataPlaneID string) ([]agent.Instance, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {labelDataPlane + "=" + dataPlaneID}})
	var containers []containerSummary
	if err := p.do(ctx, http.MethodGet, "/containers/json?all=true&filters="+url.QueryEscape(string(filters)), nil, &containers); err != nil {
		return nil, err
	}
	out := make([]agent.Instance, 0, len(containers))
	for _, c := range containers {
		// "dead" and "exited" containers are gone in every way that matters;
		// "removing" is on its way.
		stopping := c.State == "removing" || c.State == "exited" || c.State == "dead"
		out = append(out, agent.Instance{RunnerID: c.Labels[labelRunner], InstanceID: c.ID, Stopping: stopping})
		if c.State == "exited" || c.State == "dead" {
			_ = p.Terminate(ctx, c.ID)
		}
	}

	return out, nil
}

// shapePattern matches profile instance types such as "4cpu-16gb".
var shapePattern = regexp.MustCompile(`^(\d+)cpu-(\d+)gb$`)

// Launch implements agent.Provider. The container name is derived from the
// runner, so a repeated launch fails with a conflict instead of starting a
// second container.
func (p *Provider) Launch(ctx context.Context, dataPlaneID string, launch agentproto.Launch) (string, error) {
	if launch.Profile.Privileged && !p.cfg.AllowPrivileged {
		return "", &agent.LaunchError{Err: errors.New("the runner asks for a privileged container; set AIRSHIP_DOCKER_ALLOW_PRIVILEGED=true on the agent to allow it")}
	}
	if err := agentproto.CheckEnv(launch.Profile.Env); err != nil {
		return "", &agent.LaunchError{Err: err}
	}
	image := launch.Profile.Image
	if image == "" {
		image = fmt.Sprintf(p.cfg.DefaultImage, launch.RunnerVersion)
	}
	if err := p.pull(ctx, image); err != nil {
		return "", &agent.LaunchError{Err: fmt.Errorf("pull %s: %w", image, err), Retryable: true}
	}

	hostConfig := map[string]any{
		"AutoRemove":  true,
		"Init":        true,
		"NetworkMode": p.cfg.Network,
		"SecurityOpt": []string{"no-new-privileges"},
	}
	if launch.Profile.Privileged {
		hostConfig["Privileged"] = true
		delete(hostConfig, "SecurityOpt")
	}
	env := []string{"ACTIONS_RUNNER_INPUT_JITCONFIG=" + launch.JITConfig}
	for _, name := range slices.Sorted(maps.Keys(launch.Profile.Env)) {
		env = append(env, name+"="+launch.Profile.Env[name])
	}
	if m := shapePattern.FindStringSubmatch(launch.Profile.InstanceType); m != nil {
		cpus, _ := strconv.ParseInt(m[1], 10, 64)
		gb, _ := strconv.ParseInt(m[2], 10, 64)
		hostConfig["NanoCpus"] = cpus * 1e9
		hostConfig["Memory"] = gb << 30
	}

	body := map[string]any{
		"Image": image,
		// timeout enforces the max lifetime even if the agent is gone.
		"Entrypoint": []string{
			"timeout", "--signal=TERM", "--kill-after=" + strconv.Itoa(killGrace),
			strconv.Itoa(launch.MaxLifetimeSeconds), "/home/runner/run.sh",
		},
		// The runner reads ACTIONS_RUNNER_INPUT_* as arguments and removes
		// them from its environment, keeping the JIT config out of ps output
		// and out of the job's environment.
		"Env":        env,
		"Labels":     map[string]string{labelDataPlane: dataPlaneID, labelRunner: launch.RunnerID},
		"HostConfig": hostConfig,
	}
	var created struct {
		ID string `json:"Id"`
	}
	name := url.QueryEscape(launch.RunnerName)
	err := p.do(ctx, http.MethodPost, "/containers/create?name="+name, body, &created)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusConflict {
		return "", &agent.LaunchError{Err: errors.New("a container for this runner already exists")}
	}
	if err != nil {
		return "", &agent.LaunchError{Err: fmt.Errorf("create container: %w", err), Retryable: true}
	}
	if err := p.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil); err != nil {
		_ = p.Terminate(context.WithoutCancel(ctx), created.ID)

		return "", &agent.LaunchError{Err: fmt.Errorf("start container: %w", err), Retryable: true}
	}

	return created.ID, nil
}

// Terminate implements agent.Provider.
func (p *Provider) Terminate(ctx context.Context, instanceID string) error {
	err := p.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(instanceID)+"?force=true", nil, nil)
	var apiErr *apiError
	if errors.As(err, &apiErr) && (apiErr.status == http.StatusNotFound || apiErr.status == http.StatusConflict) {
		return nil // gone, or already being removed
	}

	return err
}

func (p *Provider) pull(ctx context.Context, image string) error {
	if err := p.do(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, nil); err == nil {
		return nil
	}
	ref, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		ref, tag = image[:i], image[i+1:]
	}
	query := url.Values{"fromImage": {ref}, "tag": {tag}}

	return p.do(ctx, http.MethodPost, "/images/create?"+query.Encode(), nil, nil)
}

type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: %d %s", e.status, e.message) }

func (p *Provider) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body) // image pulls stream progress; drain it
	if err != nil {
		return fmt.Errorf("read docker response: %w", err)
	}
	if resp.StatusCode >= 300 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &msg)

		return &apiError{status: resp.StatusCode, message: msg.Message}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode docker response: %w", err)
		}
	}

	return nil
}
