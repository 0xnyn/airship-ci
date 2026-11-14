// Package runloop launches each runner as its own Runloop devbox in the
// customer's Runloop account, with the API key they connected. One devbox
// per job is the isolation boundary, as a VM is on AWS and GCP.
//
// A devbox is a sandbox, not a VM, and the lifecycle follows the Daytona
// provider's with the differences Runloop's API makes:
//
//   - Nothing of ours runs at boot, but a devbox has an entrypoint, and
//     Runloop shuts the devbox down when the entrypoint exits. The
//     entrypoint is a small waiter that holds no secret: it waits for the
//     boot script the control plane then writes into the devbox, and execs
//     it. So the boot script's exit is the devbox's shutdown (EC2's
//     shutdown-terminates), and a devbox whose script never arrives shuts
//     itself down after the waiter gives up.
//   - The JIT config travels in the written file, not in the entrypoint,
//     because the devbox record (and its entrypoint) can be read back from
//     the API for as long as Runloop keeps it.
//   - Devbox names are not unique and the list cannot filter by metadata,
//     so a repeated launch finds its devbox by listing the live devboxes
//     and matching the runner's metadata. The list is per status, live
//     statuses only, so it does not grow with history.
//   - The keep-alive (Runloop's maximum lifetime, 48 hours at most) is set
//     past the runner's own deadline as the backstop.
//   - Runloop has no regions: every devbox is in SandboxRegion.
package runloop

import (
	"context"
	"errors"
	"fmt"
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
	// MetaInstallation and MetaRunner mark every devbox the control plane
	// creates. Listing and deleting are scoped to MetaInstallation.
	MetaInstallation = "airship-installation"
	MetaRunner       = "airship-runner-id"
	// MetaRepository names the repository a runner was launched for.
	MetaRepository = "airship-repository"

	// MaxKeepAlive is Runloop's ceiling on keep_alive_time_seconds.
	MaxKeepAlive = 48 * time.Hour
	// DefaultDiskGB is the disk of a custom-size devbox when the definition
	// names none.
	DefaultDiskGB = 16

	// bootDir is where the boot script is written. It exists on every
	// image, so the write never races the entrypoint.
	bootDir = "/tmp"
	// waitSeconds is how long the entrypoint waits for the boot script.
	waitSeconds = 600

	requestTimeout = 30 * time.Second
	clientTimeout  = 2 * time.Minute
	// keepAliveMargin is added to the runner's maximum lifetime for the
	// devbox's keep-alive, so the script's deadline fires first.
	keepAliveMargin = 15 * time.Minute
	cleanupTimeout  = 20 * time.Second
	pageSize        = 1000
)

var (
	// startTimeout bounds waiting for a new devbox to run.
	startTimeout = 4 * time.Minute
	// pollInterval is how often a starting devbox is read.
	pollInterval = 2 * time.Second
	// listTimeout bounds one List call, all statuses and pages included.
	listTimeout = time.Minute

	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	// A blueprint name or ID.
	templatePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	// Sizes is every named size Runloop offers, as its API spells them.
	Sizes = []string{"X_SMALL", "SMALL", "MEDIUM", "LARGE", "X_LARGE", "XX_LARGE"}

	// liveStatuses are the statuses a devbox can be in before it ends.
	// shutdown and failure are final, and are never listed.
	liveStatuses = []string{"queued", "provisioning", "initializing", "running", "suspending", "suspended", "resuming"}
)

// Options configure the provider.
type Options struct {
	// Endpoint, when set, replaces the API URL in every credential: tests
	// point it at a fake.
	Endpoint string
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
}

// Provider is the Runloop provider.
type Provider struct {
	opts Options
	http *http.Client
}

// New returns a provider that talks to Runloop.
func New(opts Options) *Provider {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: clientTimeout}
	}

	return &Provider{opts: opts, http: client}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Runloop }

// devbox is Runloop's DevboxView, the fields Airship reads.
type devbox struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Status         string            `json:"status"`
	Metadata       map[string]string `json:"metadata"`
	CreateTimeMs   int64             `json:"create_time_ms"`
	FailureReason  string            `json:"failure_reason"`
	ShutdownReason string            `json:"shutdown_reason"`
}

type devboxList struct {
	Devboxes []devbox `json:"devboxes"`
	HasMore  bool     `json:"has_more"`
}

type client struct {
	api *restapi.Client
}

func (p *Provider) clientFor(raw []byte) (*client, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	base := secret.APIURL
	if p.opts.Endpoint != "" {
		base = p.opts.Endpoint
	}

	return &client{api: &restapi.Client{
		BaseURL: base,
		HTTP:    p.http,
		Header:  http.Header{"Authorization": {"Bearer " + secret.APIKey}},
	}}, nil
}

func (c *client) do(ctx context.Context, op, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	_, err := c.api.Do(ctx, op, method, path, in, out)

	return err //nolint:wrapcheck // restapi errors name the operation
}

// Verify implements cloud.Provider: listing one devbox proves the key
// works. Runloop has no regions, and reports no account for a key.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	if region != cloud.SandboxRegion {
		return cloud.Identity{}, fmt.Errorf("runloop has no regions; use %q", cloud.SandboxRegion)
	}
	err = c.do(ctx, "list devboxes", http.MethodGet, "/v1/devboxes?limit=1&include_total_count=false", nil, &devboxList{})
	if err != nil {
		if restapi.IsAuth(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, restapi.Message(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	secret, _ := ParseSecret(raw)

	return cloud.Identity{Principal: "API key " + MaskKey(secret.APIKey)}, nil
}

// listLive returns every live devbox labelled for the installation.
func (c *client) listLive(ctx context.Context, installationID int64) ([]devbox, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	want := strconv.FormatInt(installationID, 10)
	var out []devbox
	for _, status := range liveStatuses {
		after := ""
		for {
			q := url.Values{"status": {status}, "limit": {strconv.Itoa(pageSize)}, "include_total_count": {"false"}}
			if after != "" {
				q.Set("starting_after", after)
			}
			var page devboxList
			if err := c.do(ctx, "list devboxes", http.MethodGet, "/v1/devboxes?"+q.Encode(), nil, &page); err != nil {
				return nil, err
			}
			for _, d := range page.Devboxes {
				if d.Metadata[MetaInstallation] == want && d.Status == status {
					out = append(out, d)
				}
			}
			if !page.HasMore || len(page.Devboxes) == 0 {
				break
			}
			after = page.Devboxes[len(page.Devboxes)-1].ID
		}
	}

	return out, nil
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return nil, err
	}
	if region != cloud.SandboxRegion {
		return nil, fmt.Errorf("runloop has no region %q", region)
	}
	devboxes, err := c.listLive(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("list devboxes: %w", err)
	}
	out := make([]cloud.Instance, 0, len(devboxes))
	seen := map[string]bool{}
	for _, d := range devboxes {
		if seen[d.ID] {
			continue // a devbox that changed status between two pages
		}
		seen[d.ID] = true
		out = append(out, describe(d))
	}

	return out, nil
}

// describe maps a live devbox to what the control plane wants to know.
func describe(d devbox) cloud.Instance {
	instance := cloud.Instance{RunnerID: d.Metadata[MetaRunner], InstanceID: d.ID}
	switch d.Status {
	case "suspending", "suspended":
		// A suspended devbox keeps its disk and loses its processes: the
		// runner is gone, and the devbox still costs storage.
		instance.Stopped = true
	}

	return instance
}

// Terminate implements cloud.Provider. The devbox is read first and shut
// down only if its metadata says the installation launched it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, _, instanceID string, installationID int64) error {
	c, err := p.clientFor(raw)
	if err != nil {
		return err
	}
	d, err := c.get(ctx, instanceID)
	if restapi.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Metadata[MetaInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("devbox %s is not labelled for installation %d; not shutting it down", instanceID, installationID)
	}

	return c.shutdown(ctx, d)
}

func (c *client) get(ctx context.Context, id string) (*devbox, error) {
	var d devbox
	if err := c.do(ctx, "get devbox "+id, http.MethodGet, "/v1/devboxes/"+url.PathEscape(id), nil, &d); err != nil {
		return nil, err
	}

	return &d, nil
}

// shutdown ends a devbox; one already ended is not an error.
func (c *client) shutdown(ctx context.Context, d *devbox) error {
	if d.Status == "shutdown" || d.Status == "failure" {
		return nil
	}
	err := c.do(ctx, "shut down devbox "+d.ID, http.MethodPost, "/v1/devboxes/"+url.PathEscape(d.ID)+"/shutdown", nil, nil)
	if err == nil || restapi.IsNotFound(err) {
		return nil
	}
	// Runloop refuses to shut down a devbox that is already ending.
	if current, getErr := c.get(ctx, d.ID); getErr == nil && (current.Status == "shutdown" || current.Status == "failure") {
		return nil
	}

	return err
}

// createRequest is Runloop's DevboxCreateParameters, the fields Airship
// sets.
type createRequest struct {
	Name             string            `json:"name"`
	Metadata         map[string]string `json:"metadata"`
	BlueprintID      string            `json:"blueprint_id,omitempty"`
	BlueprintName    string            `json:"blueprint_name,omitempty"`
	Entrypoint       string            `json:"entrypoint"`
	LaunchParameters launchParameters  `json:"launch_parameters"`
}

type launchParameters struct {
	ResourceSizeRequest  string          `json:"resource_size_request,omitempty"`
	CustomCPUCores       int             `json:"custom_cpu_cores,omitempty"`
	CustomGBMemory       int             `json:"custom_gb_memory,omitempty"`
	CustomDiskSize       int             `json:"custom_disk_size,omitempty"`
	KeepAliveTimeSeconds int             `json:"keep_alive_time_seconds,omitempty"`
	Architecture         string          `json:"architecture,omitempty"`
	UserParameters       *userParameters `json:"user_parameters,omitempty"`
}

type userParameters struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
}

// scriptPath and readyPath are where a runner's boot script is written and
// where the marker that it is complete goes.
func scriptPath(runnerID string) string { return bootDir + "/airship-boot-" + runnerID + ".sh" }
func readyPath(runnerID string) string  { return bootDir + "/airship-boot-" + runnerID + ".ready" }

// Entrypoint is the devbox's main process: it waits for the boot script,
// moves it aside (so a repeated write is never read) and becomes it. It
// carries no secret. If no script arrives it exits, and Runloop shuts the
// devbox down.
func Entrypoint(runnerID string) string {
	script, ready := scriptPath(runnerID), readyPath(runnerID)
	running := strings.TrimSuffix(script, ".sh") + ".running.sh"
	log := strings.TrimSuffix(script, ".sh") + ".log"

	return "/bin/bash -c 'for _ in $(seq 1 " + strconv.Itoa(waitSeconds) + "); do " +
		"if [ -e " + ready + " ] && mv " + script + " " + running + "; then exec /bin/bash " + running + " > " + log + " 2>&1; fi; " +
		"sleep 1; done; echo \"airship: no boot script arrived\" >&2; exit 1'"
}

// buildRequest builds the devbox to create for a launch.
func buildRequest(launch cloud.Launch) (createRequest, error) {
	m := launch.Machine
	opts := m.Sandbox
	if opts == nil {
		opts = &cloud.SandboxOptions{}
	}
	body := createRequest{
		Name: launch.RunnerName,
		Metadata: map[string]string{
			MetaInstallation: strconv.FormatInt(launch.InstallationID, 10),
			MetaRunner:       launch.RunnerID,
			MetaRepository:   launch.Repository,
		},
		Entrypoint: Entrypoint(launch.RunnerID),
		LaunchParameters: launchParameters{
			Architecture: "x86_64",
			// Root, so the boot script installs what it needs without
			// relying on the image's sudo.
			UserParameters: &userParameters{Username: "root", UID: 0},
		},
	}
	if m.Arch == "arm64" {
		body.LaunchParameters.Architecture = "arm64"
	}
	if launch.Boot.MaxLifetimeSeconds > 0 {
		keepAlive := min(time.Duration(launch.Boot.MaxLifetimeSeconds)*time.Second+keepAliveMargin, MaxKeepAlive)
		body.LaunchParameters.KeepAliveTimeSeconds = int(keepAlive.Seconds())
	}
	switch t := opts.Template; {
	case t == "":
	case !templatePattern.MatchString(t):
		return body, fmt.Errorf("%q is not a Runloop blueprint name or ID", t)
	case strings.HasPrefix(t, "bpt_"):
		body.BlueprintID = t
	default:
		body.BlueprintName = t
	}
	switch {
	case opts.Size != "":
		if !isSize(opts.Size) {
			return body, fmt.Errorf("%q is not a Runloop size; one of %s", opts.Size, strings.Join(Sizes, ", "))
		}
		body.LaunchParameters.ResourceSizeRequest = opts.Size
	case opts.CPU > 0 && opts.MemoryGB > 0:
		disk := m.Disk.SizeGB
		if disk <= 0 {
			disk = DefaultDiskGB
		}
		body.LaunchParameters.ResourceSizeRequest = "CUSTOM_SIZE"
		body.LaunchParameters.CustomCPUCores = opts.CPU
		body.LaunchParameters.CustomGBMemory = opts.MemoryGB
		body.LaunchParameters.CustomDiskSize = disk
	default:
		return body, errors.New("a devbox needs a size (small, large) or a shape (4vcpu-16gb)")
	}

	return body, nil
}

func isSize(s string) bool {
	for _, size := range Sizes {
		if size == s {
			return true
		}
	}

	return false
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	c, err := p.clientFor(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if err := check(launch); err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	body, err := buildRequest(launch)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	docker := launch.Machine.Sandbox != nil && launch.Machine.Sandbox.Docker
	script, err := bootscript.Sandbox(launch.Boot, launch.Machine.Arch, docker, bootscript.StopEntrypoint)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	d, err := c.ensure(ctx, launch, body)
	if err != nil {
		return "", err
	}
	if err := c.boot(ctx, d, launch.RunnerID, script); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// The devbox is ours and has no runner, so nothing is lost by
		// ending it; a retry makes a new one. Whatever stays ends itself
		// when the entrypoint gives up waiting.
		if current, getErr := c.get(cleanupCtx, d.ID); getErr == nil {
			if stopErr := c.shutdown(cleanupCtx, current); stopErr != nil {
				err = fmt.Errorf("%w (and shutting the devbox down failed: %w)", err, stopErr)
			}
		}

		return "", classify(err)
	}

	return d.ID, nil
}

// check refuses launches Runloop cannot serve.
func check(launch cloud.Launch) error {
	switch {
	case !namePattern.MatchString(launch.RunnerName):
		return fmt.Errorf("runner name %q is not a valid devbox name", launch.RunnerName)
	case !runnerIDPattern.MatchString(launch.RunnerID):
		return fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)
	case launch.Region != cloud.SandboxRegion:
		return fmt.Errorf("runloop has no region %q", launch.Region)
	case launch.Boot.PullFromAPI:
		return errors.New("provision_via is not supported on runloop: a devbox runs nothing at boot")
	case launch.Boot.Windows.Interactive:
		return errors.New("windows runners are not supported on runloop")
	case launch.Machine.GPUType != "" || launch.Boot.GPU:
		return errors.New("GPU devboxes are not supported on runloop")
	case launch.Machine.Spot:
		return errors.New("runloop has no spot capacity")
	}

	return nil
}

// ensure returns the runner's devbox: the live one an earlier launch made,
// or a new one.
func (c *client) ensure(ctx context.Context, launch cloud.Launch, body createRequest) (*devbox, error) {
	existing, err := c.lookup(ctx, launch.InstallationID, launch.RunnerID)
	if err != nil {
		return nil, classify(err)
	}
	if existing != nil {
		return existing, nil
	}
	var created devbox
	err = c.do(ctx, "create devbox", http.MethodPost, "/v1/devboxes", body, &created)
	if err == nil {
		return &created, nil
	}
	if !restapi.IsTransport(err) && restapi.Status(err) < http.StatusInternalServerError {
		return nil, classify(err)
	}
	// The answer was lost: the devbox may exist.
	if found, lookupErr := c.lookup(context.WithoutCancel(ctx), launch.InstallationID, launch.RunnerID); lookupErr == nil && found != nil {
		return found, nil
	}

	return nil, &cloud.LaunchError{Err: err, Retryable: true, Ambiguous: true}
}

// lookup finds the live devbox made for a runner, or nil.
func (c *client) lookup(ctx context.Context, installationID int64, runnerID string) (*devbox, error) {
	devboxes, err := c.listLive(ctx, installationID)
	if err != nil {
		return nil, err
	}
	for i := range devboxes {
		if devboxes[i].Metadata[MetaRunner] == runnerID {
			return &devboxes[i], nil
		}
	}

	return nil, nil //nolint:nilnil // absence is the common answer
}

// boot waits for the devbox to run, then writes the script and the marker
// the entrypoint waits for. Repeating it is harmless: the entrypoint moved
// the first script aside, and the script's lock refuses a second runner.
func (c *client) boot(ctx context.Context, d *devbox, runnerID, script string) error {
	running, err := c.waitRunning(ctx, d)
	if err != nil {
		return err
	}
	path := "/v1/devboxes/" + url.PathEscape(running.ID) + "/write_file_contents"
	if err := c.do(ctx, "write boot script", http.MethodPost, path, map[string]string{"file_path": scriptPath(runnerID), "contents": script}, nil); err != nil {
		return err
	}

	return c.do(ctx, "mark boot script ready", http.MethodPost, path, map[string]string{"file_path": readyPath(runnerID), "contents": "ready\n"}, nil)
}

// waitRunning polls until the devbox runs.
func (c *client) waitRunning(ctx context.Context, d *devbox) (*devbox, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	current := d
	for {
		switch current.Status {
		case "running":
			return current, nil
		case "failure":
			return nil, &cloud.LaunchError{Err: fmt.Errorf("devbox failed to start: %s", orUnknown(current.FailureReason)), Retryable: current.FailureReason != "execution_failed"}
		case "shutdown":
			return nil, &cloud.LaunchError{Err: fmt.Errorf("devbox shut down before it ran: %s", orUnknown(current.ShutdownReason)), Retryable: true}
		case "suspending", "suspended":
			return nil, fmt.Errorf("devbox is %s instead of starting", current.Status)
		}
		select {
		case <-ctx.Done():
			return nil, &cloud.LaunchError{Err: fmt.Errorf("devbox did not start in time (last status %s): %w", current.Status, ctx.Err()), Retryable: true}
		case <-time.After(pollInterval):
		}
		next, err := c.get(ctx, d.ID)
		if err != nil {
			if restapi.IsNotFound(err) {
				return nil, &cloud.LaunchError{Err: errors.New("devbox disappeared while starting"), Retryable: true}
			}
			if !restapi.IsTransient(err) {
				return nil, err
			}

			continue // a lost poll is polled again
		}
		current = next
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "no reason given"
	}

	return s
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

	return &cloud.LaunchError{Err: err, Retryable: restapi.IsTransient(err)}
}
