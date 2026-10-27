// Package daytona launches each runner as its own Daytona sandbox in the
// customer's Daytona organization, with the API key they connected in the
// dashboard. One sandbox per job is the isolation boundary, as a VM is on
// AWS and GCP.
//
// A sandbox is not a VM, and the lifecycle has to respect how it differs:
//
//   - Nothing runs at boot. A sandbox starts with Daytona's daemon as PID 1
//     and waits for commands, so the control plane itself writes the boot
//     script into the sandbox and starts it through the toolbox API, in a
//     process session that lives as long as the sandbox. A launch is thus a
//     sequence of idempotent steps (create, wait, write, start, label), and
//     a sandbox that exists without a started runner is worth nothing.
//   - There is no shutdown. The boot script ends every path by stopping
//     the daemon, which stops the sandbox; the sandbox is created with
//     autoDeleteInterval 0, so Daytona deletes it at once, as EC2 does on
//     shutdown-terminates. Auto-stop is disabled (it ignores running jobs)
//     and the sandbox carries a TTL past the runner's maximum lifetime, so
//     Daytona itself removes one whose script is gone.
//   - Sandboxes have names, and a name is unique in the organization. The
//     runner's name is the sandbox name, so a repeated launch is a lookup.
//   - A sandbox is created from a container image, built by Daytona from
//     a one-line Dockerfile and cached, or from a snapshot the customer
//     prepared, which fixes the resources.
//
// Because an unbooted sandbox is useless, this provider never reports an
// ambiguous launch: whatever it cannot finish it deletes. The inventory
// scan still has to meet sandboxes a crashed launch left behind, so List
// reports only sandboxes whose boot was started (a label set last), and
// counts an unlabelled one old enough that no launch can still own it as
// stopped, which has the scan delete it.
package daytona

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"

	apiclient "github.com/daytona/clients/api-client-go"
	toolbox "github.com/daytona/clients/toolbox-api-client-go"
)

const (
	// LabelInstallation and LabelRunner mark every sandbox the control
	// plane creates. Listing and deleting are scoped to LabelInstallation.
	LabelInstallation = "airship-installation"
	LabelRunner       = "airship-runner-id"
	// LabelRepository names the repository a runner was launched for.
	LabelRepository = "airship-repository"
	// LabelBoot is set once the boot script is running in the sandbox. It
	// is the last step of a launch; a sandbox without it has no runner.
	LabelBoot = "airship-boot"
	// BootStarted is LabelBoot's value.
	BootStarted = "started"

	// DefaultImage is the container image a definition without one builds
	// from.
	DefaultImage = "ubuntu:22.04"
	// DefaultDiskGB is the disk of a sandbox built from an image when the
	// definition names none.
	DefaultDiskGB = 10

	// sessionID is the toolbox process session the runner runs in. It is
	// never deleted: deleting a session kills every process it started.
	sessionID = "airship-runner"

	requestTimeout = 30 * time.Second
	// clientTimeout bounds any one HTTP request.
	clientTimeout = 2 * time.Minute
	// ttlMargin is added to the runner's maximum lifetime for the
	// sandbox's own TTL, so the script's deadline fires first.
	ttlMargin = 15 * time.Minute
	// cleanupTimeout bounds deleting a sandbox a failed launch made.
	cleanupTimeout = 20 * time.Second
	// pageSize is how many sandboxes a list request asks for.
	pageSize = 200
)

var (
	// startTimeout bounds waiting for a new sandbox to be started, image
	// build or snapshot pull included, when the caller's context does not.
	startTimeout = 4 * time.Minute
	// pollInterval is how often a starting sandbox is read.
	pollInterval = 2 * time.Second
	// BootGrace is how old an unlabelled sandbox must be before List
	// reports it as stopped (and so to be deleted). A launch is bounded by
	// the runner's lease, five minutes; nothing live is twice that old.
	BootGrace = 10 * time.Minute
	// listTimeout bounds one List call, all pages included.
	listTimeout = time.Minute

	namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// Runner IDs are UUIDs; they go into file names in the sandbox.
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	// A container image reference with a tag or digest; see
	// runnerconfig for the error messages. "latest" is refused there.
	imagePattern = regexp.MustCompile(`^([a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?/)?[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[a-f0-9]{64})$`)
	// GPU types Daytona offers, as its API spells them.
	gpuTypes = []apiclient.GpuType{
		apiclient.GPUTYPE_H100, apiclient.GPUTYPE_H200, apiclient.GPUTYPE_B200, apiclient.GPUTYPE_B300,
		apiclient.GPUTYPE_RTX_PRO_6000, apiclient.GPUTYPE_RTX_4090, apiclient.GPUTYPE_RTX_5090, apiclient.GPUTYPE_MI355X,
	}
)

// GPUTypes lists the GPU names Daytona accepts.
func GPUTypes() []string {
	out := make([]string, 0, len(gpuTypes))
	for _, g := range gpuTypes {
		out = append(out, string(g))
	}

	return out
}

// Options configure the provider.
type Options struct {
	// Endpoint, when set, replaces the API URL in every credential: tests
	// point it at a fake.
	Endpoint string
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
}

// Provider is the Daytona provider.
type Provider struct {
	opts Options
	http *http.Client
}

// New returns a provider that talks to Daytona.
func New(opts Options) *Provider {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: clientTimeout}
	}

	return &Provider{opts: opts, http: client}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Daytona }

// clients are the API client for one credential and the makings of a
// toolbox client for any of its sandboxes. A client is a configuration
// around the shared HTTP client, cheap to build per call; the key goes in
// a default header and nowhere else.
type clients struct {
	api    *apiclient.APIClient
	secret Secret
	http   *http.Client
}

func (p *Provider) clientsFor(raw []byte) (*clients, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	apiURL := secret.APIURL
	if p.opts.Endpoint != "" {
		apiURL = p.opts.Endpoint
	}
	cfg := apiclient.NewConfiguration()
	cfg.Servers = apiclient.ServerConfigurations{{URL: strings.TrimRight(apiURL, "/")}}
	cfg.HTTPClient = p.http
	cfg.UserAgent = "airship"
	cfg.AddDefaultHeader("Authorization", "Bearer "+secret.APIKey)

	return &clients{api: apiclient.NewAPIClient(cfg), secret: secret, http: p.http}, nil
}

// toolbox returns a client for one sandbox's toolbox API: the proxy the
// sandbox reports (https://proxy.app.daytona.io/toolbox), then the
// sandbox ID.
func (c *clients) toolbox(proxyURL, sandboxID string) (*toolbox.APIClient, error) {
	if proxyURL == "" {
		return nil, errors.New("sandbox has no toolbox URL")
	}
	cfg := toolbox.NewConfiguration()
	cfg.Servers = toolbox.ServerConfigurations{{URL: strings.TrimRight(proxyURL, "/") + "/" + sandboxID}}
	cfg.HTTPClient = c.http
	cfg.UserAgent = "airship"
	cfg.AddDefaultHeader("Authorization", "Bearer "+c.secret.APIKey)

	return toolbox.NewAPIClient(cfg), nil
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, requestTimeout)
}

// Verify implements cloud.Provider. Reading the key's own record proves it
// works and names the organization; the region must be one the
// organization can launch in.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	c, err := p.clientsFor(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	if !RegionPattern.MatchString(region) {
		return cloud.Identity{}, fmt.Errorf("%q is not a Daytona region (such as us or eu)", region)
	}
	keyCtx, cancel := withTimeout(ctx)
	defer cancel()
	key, resp, err := c.api.ApiKeysAPI.GetCurrentApiKey(keyCtx).Execute()
	if err = wrap("read api key", resp, err); err != nil {
		if isAuthError(err) || isNotFound(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	if key.OrganizationId == nil || *key.OrganizationId == "" {
		return cloud.Identity{}, fmt.Errorf("%w: the key belongs to no organization; create an organization API key", cloud.ErrBadCredentials)
	}
	if key.ExpiresAt.IsSet() && key.ExpiresAt.Get() != nil && key.ExpiresAt.Get().Before(time.Now()) {
		return cloud.Identity{}, fmt.Errorf("%w: the key expired on %s", cloud.ErrBadCredentials, key.ExpiresAt.Get().Format(time.RFC3339))
	}
	for _, want := range []string{"write:sandboxes", "delete:sandboxes"} {
		if !slices.Contains(key.Permissions, want) {
			return cloud.Identity{}, fmt.Errorf("%w: the key lacks the %s permission", cloud.ErrBadCredentials, want)
		}
	}
	regionCtx, cancel := withTimeout(ctx)
	defer cancel()
	regions, resp, err := c.api.OrganizationsAPI.ListAvailableRegions(regionCtx).Execute()
	if err = wrap("list regions", resp, err); err != nil {
		if isAuthError(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	ids := make([]string, 0, len(regions))
	found := false
	for _, r := range regions {
		found = found || r.Id == region
		ids = append(ids, r.Id)
	}
	slices.Sort(ids)
	if !found {
		return cloud.Identity{}, fmt.Errorf("region %q is not available to the organization; its regions are %s", region, strings.Join(ids, ", "))
	}
	if err := c.checkLinuxRegion(ctx, *key.OrganizationId, region, ids); err != nil {
		return cloud.Identity{}, err
	}

	return cloud.Identity{AccountID: *key.OrganizationId, Principal: key.Name}, nil
}

// checkLinuxRegion refuses a region that hosts no container sandboxes for
// the organization. A region can be in the organization's list and still
// serve only another class (a dedicated region of Windows hosts, say), in
// which case every launch there would fail with "No runners are configured
// in region ... for sandbox class 'container'"; better refused once, at
// connect time, with the regions that do work. Only regions from the
// organization's region list are offered: the class list can name a
// region that is not accepted as a target (ours lists a GPU region that
// way). A Daytona without the classes endpoint (an older self-hosted one)
// is not refused.
func (c *clients) checkLinuxRegion(ctx context.Context, organizationID, region string, regions []string) error {
	classCtx, cancel := withTimeout(ctx)
	defer cancel()
	classes, resp, err := c.api.OrganizationsAPI.ListAvailableSandboxClasses(classCtx, organizationID).Execute()
	if err = wrap("list sandbox classes", resp, err); err != nil {
		if isNotFound(err) {
			return nil
		}
		if isAuthError(err) {
			return fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return fmt.Errorf("verify credentials: %w", err)
	}
	var there []string
	linux := make([]string, 0, len(classes))
	for _, cl := range classes {
		if cl.SandboxClass != apiclient.SANDBOXCLASS_CONTAINER {
			if cl.RegionId == region {
				there = append(there, string(cl.SandboxClass))
			}

			continue
		}
		if cl.RegionId == region {
			return nil
		}
		if slices.Contains(regions, cl.RegionId) {
			linux = append(linux, cl.RegionId)
		}
	}
	slices.Sort(linux)
	slices.Sort(there)
	if len(there) == 0 {
		return fmt.Errorf("region %q hosts no Linux sandboxes for the organization; regions that do: %s", region, strings.Join(linux, ", "))
	}

	return fmt.Errorf("region %q hosts only %s sandboxes for the organization, and Airship runners are Linux containers; regions with Linux sandboxes: %s", region, strings.Join(there, " and "), strings.Join(linux, ", "))
}

// List implements cloud.Provider. Every sandbox labelled for the
// installation is read, page by page, and those in region reported.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	c, err := p.clientsFor(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	filter, _ := json.Marshal(map[string]string{LabelInstallation: strconv.FormatInt(installationID, 10)})
	out := []cloud.Instance{}
	cursor := ""
	now := time.Now()
	for {
		pageCtx, cancel := withTimeout(ctx)
		req := c.api.SandboxAPI.ListSandboxes(pageCtx).Labels(string(filter)).Limit(pageSize)
		if cursor != "" {
			req = req.Cursor(cursor)
		}
		page, resp, err := req.Execute()
		cancel()
		if err = wrap("list sandboxes", resp, err); err != nil {
			return nil, fmt.Errorf("list sandboxes in %s: %w", region, err)
		}
		for _, item := range page.Items {
			if item.Labels[LabelInstallation] != strconv.FormatInt(installationID, 10) {
				continue // the filter is a request to the API; the label is the proof
			}
			if item.Target != region {
				continue // another scope's
			}
			instance, ok := describe(item.Id, item.Labels, item.State, item.CreatedAt, now)
			if ok {
				out = append(out, instance)
			}
		}
		if !page.NextCursor.IsSet() || page.NextCursor.Get() == nil || *page.NextCursor.Get() == "" {
			break
		}
		cursor = *page.NextCursor.Get()
	}

	return out, nil
}

// describe maps a sandbox to what the control plane wants to know about
// it, or reports it as nothing to act on.
func describe(id string, labels map[string]string, state *apiclient.SandboxState, createdAt *string, now time.Time) (cloud.Instance, bool) {
	instance := cloud.Instance{RunnerID: labels[LabelRunner], InstanceID: id}
	s := apiclient.SANDBOXSTATE_UNKNOWN
	if state != nil {
		s = *state
	}
	switch s {
	case apiclient.SANDBOXSTATE_DESTROYED:
		return cloud.Instance{}, false
	case apiclient.SANDBOXSTATE_DESTROYING, apiclient.SANDBOXSTATE_STOPPING:
		instance.Stopping = true

		return instance, true
	case apiclient.SANDBOXSTATE_STOPPED, apiclient.SANDBOXSTATE_ARCHIVED, apiclient.SANDBOXSTATE_ARCHIVING,
		apiclient.SANDBOXSTATE_PAUSED, apiclient.SANDBOXSTATE_PAUSING, apiclient.SANDBOXSTATE_ERROR, apiclient.SANDBOXSTATE_BUILD_FAILED:
		// Nothing runs in it and nothing will: the runner's job, if it
		// had one, is over, and a failed build never gets a runner.
		instance.Stopped = true

		return instance, true
	case apiclient.SANDBOXSTATE_STARTED, apiclient.SANDBOXSTATE_CREATING, apiclient.SANDBOXSTATE_PENDING_BUILD,
		apiclient.SANDBOXSTATE_BUILDING_SNAPSHOT, apiclient.SANDBOXSTATE_PULLING_SNAPSHOT, apiclient.SANDBOXSTATE_STARTING,
		apiclient.SANDBOXSTATE_RESTORING, apiclient.SANDBOXSTATE_RESUMING, apiclient.SANDBOXSTATE_RESIZING,
		apiclient.SANDBOXSTATE_SNAPSHOTTING, apiclient.SANDBOXSTATE_FORKING,
		apiclient.SANDBOXSTATE_UNKNOWN, apiclient.SANDBOXSTATE_UNKNOWN_DEFAULT_OPEN_API:
		// Live, or on its way; what matters is whether a runner was started.
	}
	if labels[LabelBoot] == BootStarted {
		return instance, true
	}
	// No runner has been started in it. A launch may still be at work; past
	// the grace it cannot be, and the sandbox is a leak to remove.
	created, err := time.Parse(time.RFC3339, deref(createdAt))
	if err == nil && now.Sub(created) < BootGrace {
		return cloud.Instance{}, false
	}
	instance.Stopped = true

	return instance, true
}

// Terminate implements cloud.Provider. The sandbox is read first and
// deleted only if its labels say the installation launched it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, _, instanceID string, installationID int64) error {
	c, err := p.clientsFor(raw)
	if err != nil {
		return err
	}
	sandbox, err := c.get(ctx, instanceID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if sandbox.Labels[LabelInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("sandbox %s is not labelled for installation %d; not deleting it", instanceID, installationID)
	}
	if sandbox.State != nil && (*sandbox.State == apiclient.SANDBOXSTATE_DESTROYED || *sandbox.State == apiclient.SANDBOXSTATE_DESTROYING) {
		return nil
	}

	return c.delete(ctx, instanceID)
}

// get reads a sandbox by ID or name.
func (c *clients) get(ctx context.Context, idOrName string) (*apiclient.Sandbox, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	sandbox, resp, err := c.api.SandboxAPI.GetSandbox(ctx, idOrName).Execute()
	if err = wrap("get sandbox "+idOrName, resp, err); err != nil {
		return nil, err
	}

	return sandbox, nil
}

// delete destroys a sandbox; one already gone is not an error.
func (c *clients) delete(ctx context.Context, idOrName string) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, resp, err := c.api.SandboxAPI.DeleteSandbox(ctx, idOrName).Execute()
	if err = wrap("delete sandbox "+idOrName, resp, err); err != nil && !isNotFound(err) {
		return err
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	c, err := p.clientsFor(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if !namePattern.MatchString(launch.RunnerName) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("runner name %q is not a valid sandbox name", launch.RunnerName)}
	}
	if !runnerIDPattern.MatchString(launch.RunnerID) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)}
	}
	if !RegionPattern.MatchString(launch.Region) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("%q is not a Daytona region", launch.Region)}
	}
	if launch.Boot.PullFromAPI {
		return "", &cloud.LaunchError{Err: errors.New("provision_via is not supported on daytona: a sandbox runs nothing at boot")}
	}
	if launch.Boot.Windows.Interactive {
		return "", &cloud.LaunchError{Err: errors.New("windows runners are not supported on daytona")}
	}
	body, err := createRequest(launch)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	opts := launch.Machine.Daytona
	if opts == nil {
		opts = &cloud.DaytonaOptions{}
	}
	script, err := bootscript.Daytona(launch.Boot, launch.Machine.Arch, opts.Docker)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	sandbox, err := c.ensure(ctx, launch, body)
	if err != nil {
		return "", classify(err)
	}
	if sandbox.Labels[LabelBoot] == BootStarted {
		return sandbox.Id, nil // an earlier launch finished
	}
	if err := c.boot(ctx, sandbox, launch, script); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// Another launcher racing on the same runner may have finished the
		// boot meanwhile; its sandbox has a runner and must stay. This
		// launch then succeeded by proxy.
		if current, getErr := c.get(cleanupCtx, sandbox.Id); getErr == nil && current.Labels[LabelBoot] == BootStarted && current.Labels[LabelRunner] == launch.RunnerID {
			return sandbox.Id, nil
		}
		// Otherwise the sandbox is ours (ensure checked the label) and has
		// no runner, so nothing is lost by removing it; a retry makes a new
		// one. Best effort: whatever stays is removed by the scan once it
		// is older than BootGrace, and by its TTL in any case.
		if deleteErr := c.delete(cleanupCtx, sandbox.Id); deleteErr != nil {
			err = fmt.Errorf("%w (and deleting the sandbox failed: %w)", err, deleteErr)
		}

		return "", classify(err)
	}

	return sandbox.Id, nil
}

// ensure returns the runner's sandbox: the one an earlier launch made, or
// a new one. The name is the idempotency key.
func (c *clients) ensure(ctx context.Context, launch cloud.Launch, body apiclient.CreateSandbox) (*apiclient.Sandbox, error) {
	existing, err := c.lookup(ctx, launch.RunnerName, launch.RunnerID)
	if err != nil || existing != nil {
		return existing, err
	}
	createCtx, cancel := withTimeout(ctx)
	sandbox, resp, err := c.api.SandboxAPI.CreateSandbox(createCtx).CreateSandbox(body).Execute()
	cancel()
	err = wrap("create sandbox "+launch.Machine.InstanceType, resp, err)
	switch {
	case err == nil:
	case isConflict(err), isTransportError(err), status(err) >= http.StatusInternalServerError:
		// The name may be taken by the sandbox this very call made.
		existing, lookupErr := c.lookup(context.WithoutCancel(ctx), launch.RunnerName, launch.RunnerID)
		if lookupErr == nil && existing != nil {
			sandbox = existing
		} else {
			return nil, err
		}
	default:
		return nil, err
	}
	if sandbox.Target != launch.Region {
		// It would never be listed under its scope, so it cannot be
		// managed; better no runner than a stray one.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = c.delete(cleanupCtx, sandbox.Id)

		return nil, fmt.Errorf("daytona placed the sandbox in %q, not %q", sandbox.Target, launch.Region)
	}

	return sandbox, nil
}

// lookup returns the named sandbox if it is this runner's, nil if there is
// none, and an error if the name belongs to something else.
func (c *clients) lookup(ctx context.Context, name, runnerID string) (*apiclient.Sandbox, error) {
	sandbox, err := c.get(ctx, name)
	switch {
	case isNotFound(err):
		return nil, nil //nolint:nilnil // absence is the common answer
	case err != nil:
		return nil, err
	case sandbox.Labels[LabelRunner] != runnerID:
		return nil, fmt.Errorf("a sandbox named %s already exists and is not this runner's", name)
	}

	return sandbox, nil
}

// createRequest is the sandbox to make for a launch.
func createRequest(launch cloud.Launch) (apiclient.CreateSandbox, error) {
	m := launch.Machine
	opts := m.Daytona
	if opts == nil {
		opts = &cloud.DaytonaOptions{}
	}
	labels := map[string]string{
		LabelInstallation: strconv.FormatInt(launch.InstallationID, 10),
		LabelRunner:       launch.RunnerID,
		LabelRepository:   launch.Repository,
	}
	body := apiclient.CreateSandbox{
		Name:   ptr(launch.RunnerName),
		Labels: &labels,
		Target: ptr(launch.Region),
		// Auto-stop ignores running jobs; the script and the TTL end the
		// sandbox instead. Deleting on stop is the shutdown-terminates.
		AutoStopInterval:   ptr(int32(0)),
		AutoDeleteInterval: ptr(int32(0)),
	}
	if launch.Boot.MaxLifetimeSeconds > 0 {
		ttl := (time.Duration(launch.Boot.MaxLifetimeSeconds)*time.Second + ttlMargin).Minutes()
		body.TtlMinutes = ptr(int32(ttl)) //nolint:gosec // minutes of a bounded lifetime
	}
	switch {
	case opts.Snapshot != "":
		if m.Image != "" {
			return body, errors.New("a snapshot fixes the image; machine_image must be empty")
		}
		body.Snapshot = ptr(opts.Snapshot)
	default:
		image := m.Image
		if image == "" {
			image = DefaultImage
		}
		if !imagePattern.MatchString(image) || strings.HasSuffix(image, ":latest") {
			return body, fmt.Errorf("%q is not a container image reference with a tag or digest", image)
		}
		if opts.CPU <= 0 || opts.MemoryGB <= 0 {
			return body, errors.New("a sandbox built from an image needs cpu and memory")
		}
		disk := m.Disk.SizeGB
		if disk <= 0 {
			disk = DefaultDiskGB
		}
		body.BuildInfo = &apiclient.CreateBuildInfo{DockerfileContent: "FROM " + image + "\n"}
		body.Cpu = ptr(int32(opts.CPU))         //nolint:gosec // validated small
		body.Memory = ptr(int32(opts.MemoryGB)) //nolint:gosec // validated small
		body.Disk = ptr(int32(disk))            //nolint:gosec // validated small
	}
	if m.GPUType != "" {
		gpu, ok := gpuType(m.GPUType)
		if !ok {
			return body, fmt.Errorf("%q is not a Daytona GPU type; one of %s", m.GPUType, strings.Join(GPUTypes(), ", "))
		}
		body.Gpu = ptr(int32(1))
		body.GpuType = []apiclient.GpuType{gpu}
		if m.Spot {
			body.Spot = ptr(true)
		}
	} else if m.Spot {
		return body, errors.New("spot capacity on daytona is for GPU sandboxes only")
	}
	for _, v := range opts.Volumes {
		vol := apiclient.SandboxVolume{VolumeId: v.Volume, MountPath: v.MountPath}
		if v.Subpath != "" {
			vol.Subpath = ptr(v.Subpath)
		}
		body.Volumes = append(body.Volumes, vol)
	}
	if opts.NetworkAllowList != "" {
		body.NetworkAllowList = ptr(opts.NetworkAllowList)
	}
	if opts.DomainAllowList != "" {
		body.DomainAllowList = ptr(opts.DomainAllowList)
	}

	return body, nil
}

// gpuType resolves a GPU name case-insensitively to Daytona's spelling.
func gpuType(name string) (apiclient.GpuType, bool) {
	for _, g := range gpuTypes {
		if strings.EqualFold(string(g), name) {
			return g, true
		}
	}

	return "", false
}

// boot waits for the sandbox to be started, writes the script into it,
// starts it in the long-lived session and then labels the sandbox as
// booted. Every step may be repeated.
func (c *clients) boot(ctx context.Context, sandbox *apiclient.Sandbox, launch cloud.Launch, script string) error {
	started, err := c.waitStarted(ctx, sandbox)
	if err != nil {
		return err
	}
	tb, err := c.toolbox(started.ToolboxProxyUrl, started.Id)
	if err != nil {
		return err
	}
	if err := c.ensureSession(ctx, tb); err != nil {
		return err
	}
	path := "/tmp/airship-boot-" + launch.RunnerID + ".sh"
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	writeCtx, cancel := withTimeout(ctx)
	result, resp, err := tb.ProcessAPI.ExecuteCommand(writeCtx).Request(toolbox.ExecuteRequest{
		Command: "umask 077 && printf '%s' '" + encoded + "' | base64 -d > " + path + " && chmod 0700 " + path,
		Timeout: ptr(int32(60)),
	}).Execute()
	cancel()
	if err = wrap("write boot script", resp, err); err != nil {
		return err
	}
	if result.ExitCode != nil && *result.ExitCode != 0 {
		return fmt.Errorf("write boot script: exit %d: %s", *result.ExitCode, strings.TrimSpace(result.Result))
	}
	command := "nohup bash " + path + " > /tmp/airship-boot-" + launch.RunnerID + ".log 2>&1"
	if err := c.startOnce(ctx, tb, command); err != nil {
		return err
	}

	return c.markBooted(ctx, sandbox.Id, sandbox.Labels)
}

// waitStarted polls until the sandbox is started. Daytona builds the
// image or pulls the snapshot first; a build that fails never starts.
func (c *clients) waitStarted(ctx context.Context, sandbox *apiclient.Sandbox) (*apiclient.Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	current := sandbox
	for {
		state := apiclient.SANDBOXSTATE_UNKNOWN
		if current.State != nil {
			state = *current.State
		}
		switch state {
		case apiclient.SANDBOXSTATE_STARTED:
			return current, nil
		case apiclient.SANDBOXSTATE_BUILD_FAILED:
			return nil, fmt.Errorf("daytona could not build the image: %s", deref(current.ErrorReason))
		case apiclient.SANDBOXSTATE_ERROR:
			return nil, &cloud.LaunchError{Err: fmt.Errorf("sandbox failed to start: %s", deref(current.ErrorReason)), Retryable: true}
		case apiclient.SANDBOXSTATE_DESTROYED, apiclient.SANDBOXSTATE_DESTROYING:
			reason := "it was deleted"
			if current.QueueTimedOutAt != nil {
				reason = "no capacity was free in time"
			}

			return nil, &cloud.LaunchError{Err: errors.New("sandbox was destroyed before it started: " + reason), Retryable: true}
		case apiclient.SANDBOXSTATE_STOPPED, apiclient.SANDBOXSTATE_STOPPING, apiclient.SANDBOXSTATE_ARCHIVED, apiclient.SANDBOXSTATE_ARCHIVING,
			apiclient.SANDBOXSTATE_PAUSED, apiclient.SANDBOXSTATE_PAUSING:
			return nil, fmt.Errorf("sandbox is %s instead of starting", state)
		case apiclient.SANDBOXSTATE_CREATING, apiclient.SANDBOXSTATE_PENDING_BUILD, apiclient.SANDBOXSTATE_BUILDING_SNAPSHOT,
			apiclient.SANDBOXSTATE_PULLING_SNAPSHOT, apiclient.SANDBOXSTATE_STARTING, apiclient.SANDBOXSTATE_RESTORING,
			apiclient.SANDBOXSTATE_RESUMING, apiclient.SANDBOXSTATE_RESIZING, apiclient.SANDBOXSTATE_SNAPSHOTTING,
			apiclient.SANDBOXSTATE_FORKING, apiclient.SANDBOXSTATE_UNKNOWN, apiclient.SANDBOXSTATE_UNKNOWN_DEFAULT_OPEN_API:
			// Still on its way.
		}
		select {
		case <-ctx.Done():
			return nil, &cloud.LaunchError{Err: fmt.Errorf("sandbox did not start in time (last state %s): %w", state, ctx.Err()), Retryable: true}
		case <-time.After(pollInterval):
		}
		next, err := c.get(ctx, sandbox.Id)
		if err != nil {
			if isNotFound(err) {
				return nil, &cloud.LaunchError{Err: errors.New("sandbox disappeared while starting"), Retryable: true}
			}
			if !isTransportError(err) && status(err) < http.StatusInternalServerError && status(err) != http.StatusTooManyRequests {
				return nil, err
			}

			continue // a lost poll is polled again
		}
		current = next
	}
}

// ensureSession creates the runner's process session; one that exists is
// fine.
func (c *clients) ensureSession(ctx context.Context, tb *toolbox.APIClient) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	resp, err := tb.ProcessAPI.CreateSession(ctx).Request(toolbox.CreateSessionRequest{SessionId: sessionID}).Execute()
	if err = wrap("create session", resp, err); err != nil && !isConflict(err) {
		return err
	}

	return nil
}

// startOnce runs command in the session unless the session already runs
// it: a repeated launch must not start the runner twice.
func (c *clients) startOnce(ctx context.Context, tb *toolbox.APIClient, command string) error {
	getCtx, cancel := withTimeout(ctx)
	session, resp, err := tb.ProcessAPI.GetSession(getCtx, sessionID).Execute()
	cancel()
	if err = wrap("read session", resp, err); err != nil {
		return err
	}
	for _, existing := range session.Commands {
		if existing.Command == command {
			return nil
		}
	}
	execCtx, cancel := withTimeout(ctx)
	defer cancel()
	_, resp, err = tb.ProcessAPI.SessionExecuteCommand(execCtx, sessionID).Request(toolbox.SessionExecuteRequest{
		Command:  command,
		RunAsync: ptr(true),
	}).Execute()
	if err = wrap("start boot script", resp, err); err != nil {
		return err
	}

	return nil
}

// markBooted sets LabelBoot, keeping the other labels. It is the last step
// of a launch; a few tries are worth it, since failing here throws the
// booting sandbox away.
func (c *clients) markBooted(ctx context.Context, id string, labels map[string]string) error {
	merged := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		merged[k] = v
	}
	merged[LabelBoot] = BootStarted
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("label sandbox: %w", ctx.Err())
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		labelCtx, cancel := withTimeout(ctx)
		_, resp, execErr := c.api.SandboxAPI.ReplaceLabels(labelCtx, id).SandboxLabels(apiclient.SandboxLabels{Labels: merged}).Execute()
		cancel()
		if err = wrap("label sandbox", resp, execErr); err == nil {
			return nil
		}
		if !isTransportError(err) && status(err) < http.StatusInternalServerError && status(err) != http.StatusTooManyRequests {
			return err
		}
	}

	return err
}

func ptr[T any](v T) *T { return &v }

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
