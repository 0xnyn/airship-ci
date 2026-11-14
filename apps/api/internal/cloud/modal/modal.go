// Package modal launches each runner as its own Modal Sandbox in the
// customer's Modal workspace, through Modal's official Go SDK
// (github.com/modal-labs/modal-client/go).
//
//   - Every Sandbox lives in one App, AppName, created on first use. A
//     Sandbox's name is unique among the App's running Sandboxes, so the
//     runner's name is the idempotency key: a repeated launch finds the
//     Sandbox by name.
//   - Tags (airship-installation, airship-runner-id, airship-repository)
//     mark every Sandbox; Modal filters its list by tag.
//   - The Sandbox's command is bootscript.Waiter, which holds no secret,
//     waits for the boot script and becomes it. A Sandbox ends when its
//     command exits, so the script's exit is the Sandbox's: EC2's
//     shutdown-terminates. The script is written through an exec, with the
//     script in the exec's environment.
//   - The timeout, past the runner's own deadline, is the backstop; Modal
//     allows 24 hours.
//   - docker: true runs the Sandbox on Modal's VM runtime, which Modal
//     recommends for Docker in Sandboxes; the boot script starts dockerd.
//   - gpu: names a Modal GPU (T4, L4, A10G, A100, A100-80GB, L40S, H100,
//     H200, B200), one per Sandbox.
//   - Modal has no regions here: every Sandbox is in SandboxRegion.
//
// The SDK is reached through the API interface, so the launch logic is
// tested against a fake of it.
package modal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
)

const (
	// AppName is the Modal App every runner Sandbox belongs to.
	AppName = "airship-runners"

	// TagInstallation, TagRunner and TagRepository are a Sandbox's tags.
	TagInstallation = "airship-installation"
	TagRunner       = "airship-runner-id"
	TagRepository   = "airship-repository"

	// DefaultImage is the registry image a definition without one runs.
	DefaultImage = "ubuntu:22.04"
	// MaxTimeout is Modal's longest Sandbox timeout.
	MaxTimeout = 24 * time.Hour

	timeoutMargin  = 15 * time.Minute
	callTimeout    = 30 * time.Second
	createTimeout  = 4 * time.Minute
	execTimeout    = 2 * time.Minute
	cleanupTimeout = 20 * time.Second
)

var (
	// GPUTypes are the GPUs Modal attaches, as its API spells them.
	GPUTypes = []string{"T4", "L4", "A10G", "A100", "A100-80GB", "L40S", "H100", "H200", "B200"}

	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
)

// Spec is a Sandbox to create.
type Spec struct {
	Name     string
	Image    string
	CPU      float64
	MemoryMB int
	GPU      string
	// VM runs the Sandbox on Modal's VM runtime (for Docker).
	VM      bool
	Command []string
	Tags    map[string]string
	Timeout time.Duration
}

// API is the part of Modal the provider uses.
type API interface {
	// App returns the ID of AppName, creating it if needed (and so proves
	// the credential works).
	App(ctx context.Context) (string, error)
	// Create makes a Sandbox and returns its ID; ErrAlreadyExists when a
	// running Sandbox has the name.
	Create(ctx context.Context, spec Spec) (string, error)
	// FromName finds the running Sandbox with a name in AppName.
	FromName(ctx context.Context, name string) (string, error)
	// Tags returns a Sandbox's tags.
	Tags(ctx context.Context, id string) (map[string]string, error)
	// List returns the IDs of the running Sandboxes in the App with all the
	// tags.
	List(ctx context.Context, appID string, tags map[string]string) ([]string, error)
	// Exec runs argv with env in a Sandbox and returns its exit code.
	Exec(ctx context.Context, id string, argv []string, env map[string]string) (int, error)
	Terminate(ctx context.Context, id string) error
	Close()
}

// Errors an API reports, so the provider can tell them apart.
var (
	ErrNotFound      = errors.New("modal: not found")
	ErrAlreadyExists = errors.New("modal: already exists")
	ErrUnauthorized  = errors.New("modal: the token was refused")
	// ErrTransient marks throttling and lost answers.
	ErrTransient = errors.New("modal: try again")
)

// Options configure the provider.
type Options struct {
	// Connect builds the API for a credential; nil is the real SDK.
	Connect func(Secret) (API, error)
}

// Provider is the Modal provider.
type Provider struct {
	connect func(Secret) (API, error)
}

// New returns a provider that talks to Modal.
func New(opts Options) *Provider {
	connect := opts.Connect
	if connect == nil {
		connect = connectSDK
	}

	return &Provider{connect: connect}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Modal }

func (p *Provider) apiFor(raw []byte) (API, Secret, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, Secret{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	api, err := p.connect(secret)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return nil, Secret{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
		}

		return nil, Secret{}, fmt.Errorf("modal client: %w", err)
	}

	return api, secret, nil
}

// GPUType resolves a gpu: name case-insensitively to Modal's spelling.
func GPUType(name string) (string, bool) {
	for _, g := range GPUTypes {
		if strings.EqualFold(g, name) {
			return g, true
		}
	}

	return "", false
}

// Verify implements cloud.Provider: looking up (or creating) the App
// proves the token works.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	api, secret, err := p.apiFor(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	defer api.Close()
	if region != cloud.SandboxRegion {
		return cloud.Identity{}, fmt.Errorf("modal has no regions here; use %q", cloud.SandboxRegion)
	}
	if _, err := api.App(ctx); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	environment := secret.Environment
	if environment == "" {
		environment = "default environment"
	}

	return cloud.Identity{AccountID: secret.Environment, Principal: "token " + secret.TokenID + " (" + environment + ")"}, nil
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	api, _, err := p.apiFor(raw)
	if err != nil {
		return nil, err
	}
	defer api.Close()
	if region != cloud.SandboxRegion {
		return nil, fmt.Errorf("modal has no region %q here", region)
	}
	appID, err := api.App(ctx)
	if err != nil {
		return nil, fmt.Errorf("modal app: %w", err)
	}
	want := strconv.FormatInt(installationID, 10)
	ids, err := api.List(ctx, appID, map[string]string{TagInstallation: want})
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	out := make([]cloud.Instance, 0, len(ids))
	for _, id := range ids {
		tags, err := api.Tags(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue // ended between the list and now
		}
		if err != nil {
			return nil, fmt.Errorf("sandbox %s tags: %w", id, err)
		}
		if tags[TagInstallation] != want {
			continue // the filter is a request; the tag is the proof
		}
		out = append(out, cloud.Instance{RunnerID: tags[TagRunner], InstanceID: id})
	}

	return out, nil
}

// Terminate implements cloud.Provider.
func (p *Provider) Terminate(ctx context.Context, raw []byte, _, instanceID string, installationID int64) error {
	api, _, err := p.apiFor(raw)
	if err != nil {
		return err
	}
	defer api.Close()
	tags, err := api.Tags(ctx, instanceID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if tags[TagInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("sandbox %s is not tagged for installation %d; not terminating it", instanceID, installationID)
	}
	if err := api.Terminate(ctx, instanceID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	return nil
}

// buildSpec is the Sandbox to create for a launch.
func buildSpec(launch cloud.Launch) (Spec, error) {
	m := launch.Machine
	opts := m.Sandbox
	if opts == nil || opts.CPU <= 0 || opts.MemoryGB <= 0 {
		return Spec{}, errors.New("a Modal Sandbox needs a shape such as 4vcpu-16gb")
	}
	image := m.Image
	if image == "" {
		image = DefaultImage
	}
	spec := Spec{
		Name:     launch.RunnerName,
		Image:    image,
		CPU:      float64(opts.CPU),
		MemoryMB: opts.MemoryGB * 1024,
		VM:       opts.Docker,
		Command:  []string{"/bin/sh", "-c", bootscript.Waiter(launch.RunnerID)},
		Tags: map[string]string{
			TagInstallation: strconv.FormatInt(launch.InstallationID, 10),
			TagRunner:       launch.RunnerID,
			TagRepository:   launch.Repository,
		},
		Timeout: min(time.Duration(max(launch.Boot.MaxLifetimeSeconds, 60))*time.Second+timeoutMargin, MaxTimeout),
	}
	if m.GPUType != "" {
		gpu, ok := GPUType(m.GPUType)
		if !ok {
			return Spec{}, fmt.Errorf("%q is not a Modal GPU; one of %s", m.GPUType, strings.Join(GPUTypes, ", "))
		}
		spec.GPU = gpu
	}

	return spec, nil
}

func check(launch cloud.Launch) error {
	switch {
	case !namePattern.MatchString(launch.RunnerName):
		return fmt.Errorf("runner name %q is not valid", launch.RunnerName)
	case !runnerIDPattern.MatchString(launch.RunnerID):
		return fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)
	case launch.Region != cloud.SandboxRegion:
		return fmt.Errorf("modal has no region %q here", launch.Region)
	case launch.Boot.PullFromAPI:
		return errors.New("provision_via is not supported on modal: a sandbox runs nothing of ours at boot")
	case launch.Boot.Windows.Interactive:
		return errors.New("windows runners are not supported on modal")
	case launch.Machine.Spot:
		return errors.New("modal has no spot capacity")
	case launch.Machine.Arch == "arm64":
		return errors.New("modal sandboxes are x64")
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	api, _, err := p.apiFor(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	defer api.Close()
	if err := check(launch); err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	spec, err := buildSpec(launch)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	docker := launch.Machine.Sandbox != nil && launch.Machine.Sandbox.Docker
	script, err := bootscript.Sandbox(launch.Boot, launch.Machine.Arch, docker, bootscript.StopEntrypoint)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	id, err := ensure(ctx, api, launch, spec)
	if err != nil {
		return "", err
	}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	code, err := api.Exec(execCtx, id, []string{"/bin/sh", "-c", bootscript.WriteCommand(launch.RunnerID)},
		map[string]string{bootscript.BootEnv: bootscript.EncodeScript(script)})
	if err == nil && code != 0 {
		err = fmt.Errorf("write boot script: exit %d", code)
	}
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// The Sandbox has no runner: nothing is lost by ending it, and one
		// that stays ends when its waiter gives up.
		if termErr := api.Terminate(cleanupCtx, id); termErr != nil && !errors.Is(termErr, ErrNotFound) {
			err = fmt.Errorf("%w (and terminating the sandbox failed: %w)", err, termErr)
		}

		return "", classify(err)
	}

	return id, nil
}

// ensure returns the runner's Sandbox: the running one an earlier launch
// made (found by name), or a new one.
func ensure(ctx context.Context, api API, launch cloud.Launch, spec Spec) (string, error) {
	if id, err := lookup(ctx, api, launch); err != nil || id != "" {
		if err != nil {
			return "", classify(err)
		}

		return id, nil
	}
	createCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	id, err := api.Create(createCtx, spec)
	switch {
	case err == nil:
		return id, nil
	case errors.Is(err, ErrAlreadyExists), errors.Is(err, ErrTransient):
		if found, lookupErr := lookup(context.WithoutCancel(ctx), api, launch); lookupErr == nil && found != "" {
			return found, nil
		} else if lookupErr == nil && errors.Is(err, ErrAlreadyExists) {
			return "", &cloud.LaunchError{Err: fmt.Errorf("a sandbox named %s exists and is not this runner's", launch.RunnerName)}
		}

		return "", &cloud.LaunchError{Err: err, Retryable: true, Ambiguous: errors.Is(err, ErrTransient)}
	default:
		return "", classify(err)
	}
}

// lookup finds the running Sandbox named for the runner, if it is this
// runner's.
func lookup(ctx context.Context, api API, launch cloud.Launch) (string, error) {
	id, err := api.FromName(ctx, launch.RunnerName)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	tags, err := api.Tags(ctx, id)
	if err != nil {
		return "", err
	}
	if tags[TagRunner] != launch.RunnerID {
		return "", nil
	}

	return id, nil
}

func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}
	if errors.Is(err, ErrUnauthorized) {
		return &cloud.LaunchError{Err: fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())}
	}

	return &cloud.LaunchError{Err: err, Retryable: errors.Is(err, ErrTransient)}
}
