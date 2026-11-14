// Package tenki launches each runner as its own Tenki sandbox (a
// Firecracker microVM) in the customer's Tenki workspace, through Tenki's
// official Go SDK (go.luxor.ai/sandbox, gRPC/Connect to the control plane
// and a per-sandbox data plane for exec).
//
// The lifecycle follows the other sandbox clouds':
//
//   - Tags mark every sandbox: airship-installation-<id> and
//     airship-runner-<runner ID>. Tenki filters its list by tag, so List and
//     a repeated launch's lookup ask the server for exactly those; metadata
//     carries the same values as the proof.
//   - Create waits until the sandbox runs with its data plane ready; the
//     control plane then writes the boot script and starts it detached in
//     one exec, with the script in the exec's environment, not its argv.
//   - Nothing inside a Tenki sandbox ends it, so the control plane closes
//     the sandbox when the job completes (as it terminates every cloud's
//     machine), and the sandbox's max duration, set past the runner's
//     deadline, is the backstop.
//   - Tenki has no regions: every sandbox is in SandboxRegion.
//
// The SDK is reached through the api interface, so the launch logic is
// tested against a fake of it.
package tenki

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"

	"go.luxor.ai/sandbox"
)

const (
	// MetaInstallation, MetaRunner and MetaRepository are the sandbox's
	// metadata.
	MetaInstallation = "airship-installation"
	MetaRunner       = "airship-runner-id"
	MetaRepository   = "airship-repository"

	// MinDiskGB and MaxDiskGB are Tenki's limits on the root disk.
	MinDiskGB = 5
	MaxDiskGB = 100

	callTimeout    = 30 * time.Second
	createTimeout  = 4 * time.Minute
	execTimeout    = 2 * time.Minute
	durationMargin = 15 * time.Minute
	cleanupTimeout = 20 * time.Second
)

var (
	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
)

// InstallationTag and RunnerTag are the tags a runner's sandbox carries.
func InstallationTag(installationID int64) string {
	return "airship-installation-" + strconv.FormatInt(installationID, 10)
}

// RunnerTag is the tag naming a runner.
func RunnerTag(runnerID string) string { return "airship-runner-" + runnerID }

// Session is what the provider reads of a Tenki sandbox.
type Session struct {
	ID       string
	State    string
	Metadata map[string]string
	Tags     []string
}

// Spec is a sandbox to create.
type Spec struct {
	Name        string
	Image       string
	CPUCores    int
	MemoryMB    int
	DiskGB      int
	Metadata    map[string]string
	Tags        []string
	MaxDuration time.Duration
}

// API is the part of Tenki the provider uses.
type API interface {
	// Ping proves the credential works.
	Ping(ctx context.Context) error
	Create(ctx context.Context, spec Spec) (Session, error)
	Get(ctx context.Context, id string) (Session, error)
	// ListByTag returns the live sandboxes with the tag.
	ListByTag(ctx context.Context, tag string) ([]Session, error)
	// Exec runs argv with env and returns its exit code and stderr.
	Exec(ctx context.Context, id string, argv []string, env map[string]string) (int, string, error)
	// Terminate ends a sandbox.
	Terminate(ctx context.Context, id string) error
	Close() error
}

// Errors an API reports, so the provider can tell them apart.
var (
	ErrNotFound     = errors.New("tenki: sandbox not found")
	ErrUnauthorized = errors.New("tenki: the key was refused")
	// ErrTransient marks capacity, throttling and lost answers.
	ErrTransient = errors.New("tenki: try again")
)

// Options configure the provider.
type Options struct {
	// Connect builds the API for a credential; nil is the real SDK.
	Connect func(Secret) (API, error)
}

// Provider is the Tenki provider.
type Provider struct {
	connect func(Secret) (API, error)
}

// New returns a provider that talks to Tenki.
func New(opts Options) *Provider {
	connect := opts.Connect
	if connect == nil {
		connect = connectSDK
	}

	return &Provider{connect: connect}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Tenki }

func (p *Provider) apiFor(raw []byte) (API, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	api, err := p.connect(secret)
	if err != nil {
		return nil, fmt.Errorf("tenki client: %w", err)
	}

	return api, nil
}

// Verify implements cloud.Provider.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	api, err := p.apiFor(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	defer api.Close()
	if region != cloud.SandboxRegion {
		return cloud.Identity{}, fmt.Errorf("tenki has no regions; use %q", cloud.SandboxRegion)
	}
	if err := api.Ping(ctx); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	secret, _ := ParseSecret(raw)

	return cloud.Identity{AccountID: secret.WorkspaceID, Principal: "API key " + MaskKey(secret.APIKey)}, nil
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	api, err := p.apiFor(raw)
	if err != nil {
		return nil, err
	}
	defer api.Close()
	if region != cloud.SandboxRegion {
		return nil, fmt.Errorf("tenki has no region %q", region)
	}
	sessions, err := api.ListByTag(ctx, InstallationTag(installationID))
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	want := strconv.FormatInt(installationID, 10)
	out := make([]cloud.Instance, 0, len(sessions))
	for _, s := range sessions {
		if s.Metadata[MetaInstallation] != want {
			continue // the tag is a request; the metadata is the proof
		}
		instance := cloud.Instance{RunnerID: s.Metadata[MetaRunner], InstanceID: s.ID}
		switch s.State {
		case "TERMINATED":
			continue
		case "TERMINATING":
			instance.Stopping = true
		case "PAUSED", "PAUSING", "USER_SHUTDOWN":
			// Nothing runs in it; its runner is gone.
			instance.Stopped = true
		}
		out = append(out, instance)
	}

	return out, nil
}

// Terminate implements cloud.Provider.
func (p *Provider) Terminate(ctx context.Context, raw []byte, _, instanceID string, installationID int64) error {
	api, err := p.apiFor(raw)
	if err != nil {
		return err
	}
	defer api.Close()
	s, err := api.Get(ctx, instanceID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Metadata[MetaInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("sandbox %s is not labelled for installation %d; not closing it", instanceID, installationID)
	}
	if s.State == "TERMINATED" || s.State == "TERMINATING" {
		return nil
	}
	if err := api.Terminate(ctx, instanceID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	return nil
}

// buildSpec is the sandbox to create for a launch.
func buildSpec(launch cloud.Launch) (Spec, error) {
	m := launch.Machine
	opts := m.Sandbox
	if opts == nil || opts.CPU <= 0 || opts.MemoryGB <= 0 {
		return Spec{}, errors.New("a Tenki sandbox needs a shape such as 4vcpu-8gb")
	}
	if m.Disk.SizeGB != 0 && (m.Disk.SizeGB < MinDiskGB || m.Disk.SizeGB > MaxDiskGB) {
		return Spec{}, fmt.Errorf("a Tenki disk is %d to %d GB", MinDiskGB, MaxDiskGB)
	}
	lifetime := time.Duration(max(launch.Boot.MaxLifetimeSeconds, 60))*time.Second + durationMargin

	return Spec{
		Name:     launch.RunnerName,
		Image:    m.Image,
		CPUCores: opts.CPU,
		MemoryMB: opts.MemoryGB * 1024,
		DiskGB:   m.Disk.SizeGB,
		Metadata: map[string]string{
			MetaInstallation: strconv.FormatInt(launch.InstallationID, 10),
			MetaRunner:       launch.RunnerID,
			MetaRepository:   launch.Repository,
		},
		Tags:        []string{InstallationTag(launch.InstallationID), RunnerTag(launch.RunnerID)},
		MaxDuration: lifetime,
	}, nil
}

func check(launch cloud.Launch) error {
	switch {
	case !namePattern.MatchString(launch.RunnerName):
		return fmt.Errorf("runner name %q is not valid", launch.RunnerName)
	case !runnerIDPattern.MatchString(launch.RunnerID):
		return fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)
	case launch.Region != cloud.SandboxRegion:
		return fmt.Errorf("tenki has no region %q", launch.Region)
	case launch.Boot.PullFromAPI:
		return errors.New("provision_via is not supported on tenki: a sandbox runs nothing of ours at boot")
	case launch.Boot.Windows.Interactive:
		return errors.New("windows runners are not supported on tenki")
	case launch.Machine.GPUType != "" || launch.Boot.GPU:
		return errors.New("GPU sandboxes are not supported on tenki")
	case launch.Machine.Spot:
		return errors.New("tenki has no spot capacity")
	case launch.Machine.Arch == "arm64":
		return errors.New("tenki sandboxes are x64")
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	api, err := p.apiFor(raw)
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
	script, err := bootscript.Sandbox(launch.Boot, launch.Machine.Arch, docker, bootscript.StopByControlPlane)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	id, err := ensure(ctx, api, launch, spec)
	if err != nil {
		return "", err
	}
	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	code, stderr, err := api.Exec(execCtx, id, []string{"/bin/sh", "-c", bootscript.StartDetachedCommand(launch.RunnerID)},
		map[string]string{bootscript.BootEnv: bootscript.EncodeScript(script)})
	if err == nil && code != 0 {
		err = fmt.Errorf("start boot script: exit %d: %s", code, strings.TrimSpace(stderr))
	}
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// The sandbox has no runner: nothing is lost by closing it.
		if termErr := api.Terminate(cleanupCtx, id); termErr != nil && !errors.Is(termErr, ErrNotFound) {
			err = fmt.Errorf("%w (and closing the sandbox failed: %w)", err, termErr)
		}

		return "", classify(err)
	}

	return id, nil
}

// ensure returns the runner's sandbox: the live one an earlier launch made,
// or a new one.
func ensure(ctx context.Context, api API, launch cloud.Launch, spec Spec) (string, error) {
	if found, err := lookup(ctx, api, launch); err != nil || found != "" {
		if err != nil {
			return "", classify(err)
		}

		return found, nil
	}
	createCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	s, err := api.Create(createCtx, spec)
	if err == nil {
		return s.ID, nil
	}
	if !errors.Is(err, ErrTransient) {
		return "", classify(err)
	}
	if found, lookupErr := lookup(context.WithoutCancel(ctx), api, launch); lookupErr == nil && found != "" {
		return found, nil
	}

	return "", &cloud.LaunchError{Err: err, Retryable: true, Ambiguous: true}
}

func lookup(ctx context.Context, api API, launch cloud.Launch) (string, error) {
	sessions, err := api.ListByTag(ctx, RunnerTag(launch.RunnerID))
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.Metadata[MetaRunner] == launch.RunnerID && s.State != "TERMINATED" && s.State != "TERMINATING" {
			return s.ID, nil
		}
	}

	return "", nil
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

// sdk is API over Tenki's Go SDK.
type sdk struct {
	client *sandbox.Client
}

func connectSDK(secret Secret) (API, error) {
	opts := []sandbox.Option{
		sandbox.WithAuthToken(secret.APIKey),
		sandbox.WithBaseURL(secret.APIURL),
		sandbox.WithHTTPTimeout(callTimeout),
		sandbox.WithWarningHandler(func(sandbox.SandboxWarning) {}),
	}
	client, err := sandbox.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}

	return &sdk{client: client}, nil
}

// mapErr sorts the SDK's errors into the API's.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sandbox.ErrSessionNotFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, sandbox.ErrUnauthorized), errors.Is(err, sandbox.ErrPermissionDenied):
		return fmt.Errorf("%w: %w", ErrUnauthorized, err)
	case errors.Is(err, sandbox.ErrCapacityUnavailable), errors.Is(err, sandbox.ErrRateLimited), sandbox.IsDataPlaneNotReady(err),
		errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrTransient, err)
	}

	return err
}

func toSession(s *sandbox.Session) Session {
	return Session{ID: s.ID, State: string(s.State), Metadata: maps.Clone(s.Metadata), Tags: slices.Clone(s.Tags)}
}

func (a *sdk) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	_, err := a.client.List(ctx, sandbox.WithTagFilter("airship-verify"))

	return mapErr(err)
}

func (a *sdk) Create(ctx context.Context, spec Spec) (Session, error) {
	opts := []sandbox.CreateOption{
		sandbox.WithName(spec.Name),
		sandbox.WithCPUCores(int32(spec.CPUCores)), //nolint:gosec // validated small
		sandbox.WithMemoryMB(int32(spec.MemoryMB)), //nolint:gosec // validated small
		sandbox.WithMetadata(spec.Metadata),
		sandbox.WithTags(spec.Tags...),
		sandbox.WithMaxDuration(spec.MaxDuration),
		sandbox.WithAllowOutbound(true),
		sandbox.WithWaitTimeout(createTimeout),
	}
	if spec.Image != "" {
		opts = append(opts, sandbox.WithImage(spec.Image))
	}
	if spec.DiskGB > 0 {
		opts = append(opts, sandbox.WithDiskSizeGB(spec.DiskGB))
	}
	s, err := a.client.Create(ctx, opts...)
	if err != nil {
		return Session{}, mapErr(err)
	}
	defer s.Release()

	return toSession(s), nil
}

func (a *sdk) Get(ctx context.Context, id string) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	s, err := a.client.Get(ctx, id)
	if err != nil {
		return Session{}, mapErr(err)
	}
	defer s.Release()

	return toSession(s), nil
}

func (a *sdk) ListByTag(ctx context.Context, tag string) ([]Session, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	sessions, err := a.client.List(ctx, sandbox.WithTagFilter(tag))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, toSession(s))
		s.Release()
	}

	return out, nil
}

func (a *sdk) Exec(ctx context.Context, id string, argv []string, env map[string]string) (int, string, error) {
	s, err := a.client.Get(ctx, id)
	if err != nil {
		return 0, "", mapErr(err)
	}
	defer s.Release()
	timeout := execTimeout
	// Privileged runs as root, so the boot script needs no sudo in the image.
	result, err := s.Command(argv, sandbox.RunOptions{Env: env, Timeout: &timeout, Privileged: true}).Exec(ctx)
	if err != nil {
		return 0, "", mapErr(err)
	}

	return int(result.ExitCode), string(result.Stderr), nil
}

func (a *sdk) Terminate(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	s, err := a.client.Get(ctx, id)
	if err != nil {
		return mapErr(err)
	}

	return mapErr(s.Close(ctx))
}

func (a *sdk) Close() error { return a.client.Close() } //nolint:wrapcheck // closing a local client
