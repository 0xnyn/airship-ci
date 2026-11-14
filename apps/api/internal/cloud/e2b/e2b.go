// Package e2b launches each runner as its own E2B sandbox in the
// customer's E2B team, with the API key they connected. One sandbox per
// job is the isolation boundary, as a VM is on AWS and GCP.
//
// An E2B sandbox is a Firecracker microVM started from a template, with
// E2B's daemon (envd) inside it. The lifecycle follows the Daytona
// provider's with the differences E2B's API makes:
//
//   - Nothing of ours runs at boot. The control plane uploads the boot
//     script through envd's file API and starts it through envd's process
//     API as a tagged process, so a repeated launch sees the runner
//     already running and does not start it twice.
//   - A sandbox cannot end itself: nothing inside it can stop the VM, and
//     the script exiting leaves it idle. The control plane deletes the
//     sandbox when the job completes (as it terminates every cloud's
//     machine), and the sandbox's timeout, set past the runner's deadline,
//     is the backstop. E2B caps the timeout by plan (24 hours on Pro, one
//     hour on Hobby), and refuses a longer one.
//   - Creation takes an Idempotency-Key, the runner ID, so a repeated
//     create returns the first sandbox; the runner is found by its
//     metadata before every create too, for when the key has expired.
//   - The size is the template's; cpuCount may raise the vCPUs up to the
//     template's maximum. E2B has no regions: every sandbox is in
//     SandboxRegion.
package e2b

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/restapi"
)

const (
	// MetaInstallation and MetaRunner mark every sandbox the control plane
	// creates. Listing and deleting are scoped to MetaInstallation.
	MetaInstallation = "airship-installation"
	MetaRunner       = "airship-runner-id"
	// MetaRepository names the repository a runner was launched for.
	MetaRepository = "airship-repository"

	// ProcessTag marks the runner's process in envd.
	ProcessTag = "airship-runner"
	// EnvdPort is the port E2B exposes each sandbox's daemon on.
	EnvdPort = 49983
	// MaxTimeout is E2B's longest sandbox timeout (the Pro plan's).
	MaxTimeout = 24 * time.Hour
	// MetaLongRunning is Novita's flag for a sandbox past the plan's session
	// length.
	MetaLongRunning = "long_running"

	requestTimeout = 30 * time.Second
	clientTimeout  = 2 * time.Minute
	// timeoutMargin is added to the runner's maximum lifetime for the
	// sandbox's timeout, so the script's deadline fires first.
	timeoutMargin  = 15 * time.Minute
	cleanupTimeout = 20 * time.Second
	pageSize       = 100
	// maxFrame bounds one Connect message read from envd.
	maxFrame = 1 << 20
)

var (
	// listTimeout bounds one List call, all pages included.
	listTimeout = time.Minute
	// readyTimeout bounds waiting for a new sandbox's daemon to answer.
	readyTimeout = 2 * time.Minute
	// pollInterval is how often a daemon that is not answering yet is
	// tried again.
	pollInterval = time.Second

	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	// A template name, alias or ID, optionally with a tag (name:tag).
	templatePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}(:[A-Za-z0-9._-]{1,64})?$`)
)

// Options configure the provider.
type Options struct {
	// Flavor is the E2B-protocol cloud to talk to; the zero value is E2B.
	Flavor Flavor
	// Endpoint, when set, replaces the API URL every credential's domain
	// implies: tests point it at a fake.
	Endpoint string
	// EnvdEndpoint, when set, gives a sandbox's daemon URL in place of
	// https://49983-<id>.<domain> (tests).
	EnvdEndpoint func(sandboxID string) string
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
}

// Provider is the E2B provider.
type Provider struct {
	opts Options
	http *http.Client
}

// New returns a provider that talks to E2B.
func New(opts Options) *Provider {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: clientTimeout}
	}

	if opts.Flavor.Cloud == "" {
		opts.Flavor = FlavorE2B
	}

	return &Provider{opts: opts, http: client}
}

// Name implements cloud.Provider.
func (p *Provider) Name() string { return p.opts.Flavor.Cloud }

// flavor is the cloud this provider talks to.
func (p *Provider) flavor() Flavor { return p.opts.Flavor }

// sandbox is what E2B returns for a sandbox: the create answer, a
// SandboxDetail or a ListedSandbox (each has some of these fields).
type sandbox struct {
	SandboxID       string            `json:"sandboxID"`
	TemplateID      string            `json:"templateID"`
	State           string            `json:"state"`
	Metadata        map[string]string `json:"metadata"`
	EnvdAccessToken string            `json:"envdAccessToken"`
	Domain          *string           `json:"domain"`
	StartedAt       string            `json:"startedAt"`
}

type client struct {
	api    *restapi.Client
	secret Secret
	p      *Provider
}

func (p *Provider) clientFor(raw []byte) (*client, error) {
	secret, err := p.flavor().ParseSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	base := "https://api." + secret.Domain
	if p.opts.Endpoint != "" {
		base = p.opts.Endpoint
	}

	return &client{
		api:    &restapi.Client{BaseURL: base, HTTP: p.http, Header: http.Header{"X-Api-Key": {secret.APIKey}}},
		secret: secret,
		p:      p,
	}, nil
}

func (c *client) do(ctx context.Context, op, method, path string, in, out any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	return c.api.Do(ctx, op, method, path, in, out) //nolint:wrapcheck // restapi errors name the operation
}

// Verify implements cloud.Provider: listing one sandbox proves the key
// works. E2B has no regions, and reports no team for an API key.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	if region != cloud.SandboxRegion {
		return cloud.Identity{}, fmt.Errorf("%s has no regions; use %q", p.flavor().Cloud, cloud.SandboxRegion)
	}
	if _, err := c.do(ctx, "list sandboxes", http.MethodGet, "/v2/sandboxes?limit=1", nil, &[]sandbox{}); err != nil {
		if restapi.IsAuth(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, restapi.Message(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}

	return cloud.Identity{Principal: "API key " + MaskKey(c.secret.APIKey)}, nil
}

// metadataQuery is E2B's metadata filter: key=value, each URL encoded.
func metadataQuery(key, value string) string {
	return url.QueryEscape(key) + "=" + url.QueryEscape(value)
}

// list returns every running or paused sandbox whose metadata has key =
// value.
func (c *client) list(ctx context.Context, key, value string) ([]sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	var out []sandbox
	next := ""
	for {
		q := url.Values{"metadata": {metadataQuery(key, value)}, "state": {"running,paused"}, "limit": {strconv.Itoa(pageSize)}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var page []sandbox
		header, err := c.do(ctx, "list sandboxes", http.MethodGet, "/v2/sandboxes?"+q.Encode(), nil, &page)
		if err != nil {
			return nil, err
		}
		for _, s := range page {
			if s.Metadata[key] == value { // the filter is a request; the metadata is the proof
				out = append(out, s)
			}
		}
		next = header.Get("X-Next-Token")
		if next == "" || len(page) == 0 {
			return out, nil
		}
	}
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return nil, err
	}
	if region != cloud.SandboxRegion {
		return nil, fmt.Errorf("%s has no region %q", p.flavor().Cloud, region)
	}
	sandboxes, err := c.list(ctx, MetaInstallation, strconv.FormatInt(installationID, 10))
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	out := make([]cloud.Instance, 0, len(sandboxes))
	seen := map[string]bool{}
	for _, s := range sandboxes {
		if seen[s.SandboxID] {
			continue
		}
		seen[s.SandboxID] = true
		instance := cloud.Instance{RunnerID: s.Metadata[MetaRunner], InstanceID: s.SandboxID}
		// A paused sandbox runs nothing; its runner is gone.
		instance.Stopped = s.State == "paused"
		out = append(out, instance)
	}

	return out, nil
}

// Terminate implements cloud.Provider. The sandbox is read first and
// killed only if its metadata says the installation launched it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, _, instanceID string, installationID int64) error {
	c, err := p.clientFor(raw)
	if err != nil {
		return err
	}
	s, err := c.get(ctx, instanceID)
	if restapi.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Metadata[MetaInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("sandbox %s is not labelled for installation %d; not killing it", instanceID, installationID)
	}

	return c.kill(ctx, instanceID)
}

func (c *client) get(ctx context.Context, id string) (*sandbox, error) {
	var s sandbox
	if _, err := c.do(ctx, "get sandbox "+id, http.MethodGet, "/sandboxes/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// kill deletes a sandbox; one already gone is not an error.
func (c *client) kill(ctx context.Context, id string) error {
	_, err := c.do(ctx, "kill sandbox "+id, http.MethodDelete, "/sandboxes/"+url.PathEscape(id), nil, nil)
	if err == nil || restapi.IsNotFound(err) {
		return nil
	}

	return err
}

// createRequest is E2B's NewSandboxV2, the fields Airship sets.
type createRequest struct {
	TemplateID string            `json:"templateID"`
	Timeout    int               `json:"timeout"`
	CPUCount   int               `json:"cpuCount,omitempty"`
	AutoPause  bool              `json:"autoPause"`
	Metadata   map[string]string `json:"metadata"`
	// Secure asks the first API for an envd access token.
	Secure bool `json:"secure,omitempty"`
}

// buildRequest is the sandbox to create for a launch.
func buildRequest(f Flavor, launch cloud.Launch) (createRequest, error) {
	opts := launch.Machine.Sandbox
	if opts == nil {
		opts = &cloud.SandboxOptions{}
	}
	if !templatePattern.MatchString(opts.Template) {
		return createRequest{}, fmt.Errorf("%q is not a %s template name or ID", opts.Template, f.Title)
	}
	if opts.MemoryGB > 0 || opts.Size != "" {
		return createRequest{}, fmt.Errorf("a %s sandbox's memory is its template's; only cpu may be set", f.Title)
	}
	lifetime := time.Duration(max(launch.Boot.MaxLifetimeSeconds, 60))*time.Second + timeoutMargin

	body := createRequest{
		TemplateID: opts.Template,
		Timeout:    int(min(lifetime, f.MaxTimeout).Seconds()),
		Secure:     f.Secure,
		CPUCount:   opts.CPU,
		// A sandbox that times out is over; pausing it would keep it.
		AutoPause: false,
		Metadata: map[string]string{
			MetaInstallation: strconv.FormatInt(launch.InstallationID, 10),
			MetaRunner:       launch.RunnerID,
			MetaRepository:   launch.Repository,
		},
	}
	if f.LongRunning {
		body.Metadata[MetaLongRunning] = "true"
	}

	return body, nil
}

// check refuses launches an E2B-protocol cloud cannot serve.
func check(f Flavor, launch cloud.Launch) error {
	name := f.Cloud
	switch {
	case !namePattern.MatchString(launch.RunnerName):
		return fmt.Errorf("runner name %q is not valid", launch.RunnerName)
	case !runnerIDPattern.MatchString(launch.RunnerID):
		return fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)
	case launch.Region != cloud.SandboxRegion:
		return fmt.Errorf("%s has no region %q", name, launch.Region)
	case launch.Boot.PullFromAPI:
		return fmt.Errorf("provision_via is not supported on %s: a sandbox runs nothing at boot", name)
	case launch.Boot.Windows.Interactive:
		return fmt.Errorf("windows runners are not supported on %s", name)
	case launch.Machine.GPUType != "" || launch.Boot.GPU:
		return fmt.Errorf("GPU sandboxes are not supported on %s", name)
	case launch.Machine.Spot:
		return fmt.Errorf("%s has no spot capacity", name)
	case launch.Machine.Arch == "arm64":
		return fmt.Errorf("%s sandboxes are x64", name)
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if err := check(p.flavor(), launch); err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	body, err := buildRequest(p.flavor(), launch)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	docker := launch.Machine.Sandbox != nil && launch.Machine.Sandbox.Docker
	script, err := bootscript.Sandbox(launch.Boot, launch.Machine.Arch, docker, bootscript.StopByControlPlane)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	s, err := c.ensure(ctx, launch, body)
	if err != nil {
		return "", err
	}
	if err := c.boot(ctx, s, launch.RunnerID, script); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// The sandbox has no runner: nothing is lost by killing it, and a
		// retry makes a new one. Whatever stays ends at its timeout, and the
		// inventory scan kills it before that once its runner is finished.
		if killErr := c.kill(cleanupCtx, s.SandboxID); killErr != nil {
			err = fmt.Errorf("%w (and killing the sandbox failed: %w)", err, killErr)
		}

		return "", classify(err)
	}

	return s.SandboxID, nil
}

// ensure returns the runner's sandbox, with what reaching its daemon
// takes: the one an earlier launch made, or a new one.
func (c *client) ensure(ctx context.Context, launch cloud.Launch, body createRequest) (*sandbox, error) {
	existing, err := c.lookup(ctx, launch.RunnerID)
	if err != nil {
		return nil, classify(err)
	}
	if existing != nil {
		return existing, nil
	}
	createCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	data, err := json.Marshal(body)
	if err != nil {
		return nil, &cloud.LaunchError{Err: fmt.Errorf("encode request: %w", err)}
	}
	f := c.p.flavor()
	api := *c.api
	api.Header = c.api.Header.Clone()
	if f.Idempotent {
		api.Header.Set("Idempotency-Key", launch.RunnerID)
	}
	var created sandbox
	_, err = api.DoRaw(createCtx, "create sandbox", http.MethodPost, f.CreatePath, "application/json", bytes.NewReader(data), &created)
	if err == nil {
		return &created, nil
	}
	var apiErr *restapi.Error
	ambiguous := restapi.IsTransport(err) || restapi.Status(err) >= http.StatusInternalServerError ||
		(errors.As(err, &apiErr) && (apiErr.Code == "idempotency_outcome_unknown" || apiErr.Code == "idempotency_in_progress"))
	if !ambiguous {
		return nil, classify(err)
	}
	if found, lookupErr := c.lookup(context.WithoutCancel(ctx), launch.RunnerID); lookupErr == nil && found != nil {
		return found, nil
	}

	return nil, &cloud.LaunchError{Err: err, Retryable: true, Ambiguous: true}
}

// lookup finds the sandbox made for a runner, with its daemon token, or
// nil.
func (c *client) lookup(ctx context.Context, runnerID string) (*sandbox, error) {
	found, err := c.list(ctx, MetaRunner, runnerID)
	if err != nil || len(found) == 0 {
		return nil, err
	}

	return c.get(ctx, found[0].SandboxID)
}

// envd is a client for one sandbox's daemon, acting as root.
func (c *client) envd(s *sandbox) *restapi.Client {
	domain := c.secret.Domain
	if s.Domain != nil && *s.Domain != "" {
		domain = *s.Domain
	}
	base := "https://" + strconv.Itoa(EnvdPort) + "-" + s.SandboxID + "." + domain
	if c.p.opts.EnvdEndpoint != nil {
		base = c.p.opts.EnvdEndpoint(s.SandboxID)
	}
	header := http.Header{
		// envd picks the user a request acts as from Basic auth's name.
		"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("root:"))},
	}
	if s.EnvdAccessToken != "" {
		header.Set("X-Access-Token", s.EnvdAccessToken)
	}

	return &restapi.Client{BaseURL: base, HTTP: c.p.http, Header: header}
}

// scriptPath is where a runner's boot script is uploaded.
func scriptPath(runnerID string) string { return "/root/airship-boot-" + runnerID + ".sh" }

// processInfo is envd's ProcessInfo, the fields Airship reads.
type processInfo struct {
	PID uint32  `json:"pid"`
	Tag *string `json:"tag"`
}

// boot uploads the script and starts it as the tagged runner process,
// unless the process already runs. A repeated boot is harmless: a second
// start is refused by the script's own lock.
func (c *client) boot(ctx context.Context, s *sandbox, runnerID, script string) error {
	daemon := c.envd(s)
	if err := c.waitReady(ctx, daemon); err != nil {
		return err
	}
	var processes struct {
		Processes []processInfo `json:"processes"`
	}
	if err := callUnary(ctx, daemon, "list processes", "/process.Process/List", struct{}{}, &processes); err != nil {
		return err
	}
	for _, proc := range processes.Processes {
		if proc.Tag != nil && *proc.Tag == ProcessTag {
			return nil // an earlier launch started the runner
		}
	}
	path := scriptPath(runnerID)
	uploadCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	_, err := daemon.DoRaw(uploadCtx, "upload boot script", http.MethodPost, "/files?"+url.Values{"path": {path}, "username": {"root"}}.Encode(),
		"application/octet-stream", strings.NewReader(script), nil)
	cancel()
	if err != nil {
		return err
	}
	logPath := strings.TrimSuffix(path, ".sh") + ".log"

	return startProcess(ctx, daemon, startRequest{
		Process: processConfig{Cmd: "/bin/bash", Args: []string{"-c", "chmod 0700 " + path + " && exec /bin/bash " + path + " > " + logPath + " 2>&1"}},
		Tag:     ProcessTag,
		Stdin:   false,
	})
}

// waitReady waits for a new sandbox's daemon to answer.
func (c *client) waitReady(ctx context.Context, daemon *restapi.Client) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	for {
		healthCtx, healthCancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := daemon.Do(healthCtx, "envd health", http.MethodGet, "/health", nil, nil)
		healthCancel()
		if err == nil {
			return nil
		}
		if restapi.IsAuth(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return &cloud.LaunchError{Err: fmt.Errorf("the sandbox's daemon did not answer in time: %w", err), Retryable: true}
		case <-time.After(pollInterval):
		}
	}
}

type processConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args,omitempty"`
	Envs map[string]string `json:"envs,omitempty"`
}

// startRequest is envd's process.StartRequest in protobuf's JSON form.
type startRequest struct {
	Process processConfig `json:"process"`
	Tag     string        `json:"tag,omitempty"`
	Stdin   bool          `json:"stdin"`
}

// callUnary calls a Connect unary procedure with JSON.
func callUnary(ctx context.Context, daemon *restapi.Client, op, procedure string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	data, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	_, err = daemon.DoRaw(ctx, op, http.MethodPost, procedure, "application/json", bytes.NewReader(data), out)

	return err //nolint:wrapcheck // restapi errors name the operation
}

// startProcess calls envd's server-streaming Process/Start with the
// Connect protocol's JSON enveloping, reads the first event (the process
// started, or why not) and hangs up. envd keeps a process running after
// the client that started it has gone.
func startProcess(ctx context.Context, daemon *restapi.Client, in startRequest) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	payload, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("start boot script: %w", err)
	}
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload))) //nolint:gosec // a small JSON document
	frame = append(frame, payload...)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(daemon.BaseURL, "/")+"/process.Process/Start", bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("start boot script: %w", err)
	}
	for k, vs := range daemon.Header {
		req.Header[k] = vs
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", "airship")
	resp, err := daemon.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("start boot script: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

		return &restapi.Error{Op: "start boot script", Status: resp.StatusCode, Message: strings.TrimSpace(string(body)), Header: resp.Header}
	}
	flags, message, err := readFrame(bufio.NewReader(resp.Body))
	if err != nil {
		return fmt.Errorf("start boot script: %w", err)
	}
	if flags&0x02 != 0 {
		// End of stream before any event: the message is the error.
		var end struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(message, &end) == nil && end.Error != nil {
			return fmt.Errorf("start boot script: %s: %s", end.Error.Code, end.Error.Message)
		}

		return errors.New("start boot script: the daemon ended the stream without starting the process")
	}
	var event struct {
		Event struct {
			Start *struct {
				PID uint32 `json:"pid"`
			} `json:"start"`
		} `json:"event"`
	}
	if err := json.Unmarshal(message, &event); err != nil || event.Event.Start == nil {
		return fmt.Errorf("start boot script: unexpected first event %s", truncate(message))
	}

	return nil
}

// readFrame reads one Connect envelope: a flags byte, a big-endian length
// and the message.
func readFrame(r io.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, fmt.Errorf("read frame: %w", err)
	}
	size := binary.BigEndian.Uint32(head[1:])
	if size > maxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is too large", size)
	}
	message := make([]byte, size)
	if _, err := io.ReadFull(r, message); err != nil {
		return 0, nil, fmt.Errorf("read frame: %w", err)
	}

	return head[0], message, nil
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}

	return string(b)
}

// classify turns an error into a LaunchError the scheduler understands.
func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}
	if restapi.IsAuth(err) {
		return &cloud.LaunchError{Err: fmt.Errorf("%w: %s", cloud.ErrBadCredentials, restapi.Message(err))}
	}
	var apiErr *restapi.Error
	if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Code, "sandbox_capacity") || errors.As(err, &apiErr) && apiErr.Header.Get("Should-Retry") == "true" {
		return &cloud.LaunchError{Err: err, Retryable: true}
	}

	return &cloud.LaunchError{Err: err, Retryable: restapi.IsTransient(err)}
}
