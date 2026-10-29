// Package gcp launches each runner as its own Compute Engine instance in
// the customer's project, with the service account key they connected in
// the dashboard. One VM per job is the isolation boundary, as on AWS.
//
// Compute Engine differs from EC2 in ways the lifecycle has to respect:
//
//   - Creating an instance is an asynchronous operation. The insert call
//     is accepted, and capacity or quota failures arrive when the operation
//     finishes, so the provider waits for it before reporting a launch.
//   - Instances are named, not numbered. The runner's name is the instance
//     name, which makes a repeated launch a lookup: if the instance exists
//     and carries the runner's label, it is the one.
//   - Requests carry a request ID. A retried insert with the same ID
//     returns the first operation instead of a second instance.
//   - A VM that powers itself off is not deleted; it stays TERMINATED and
//     is billed for its disk. The inventory scan treats such a VM as gone
//     and deletes it. Spot VMs are deleted on preemption
//     (instanceTerminationAction DELETE).
//   - The startup script runs on every boot, so the boot scripts refuse to
//     register a second time.
package gcp

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/google/uuid"
	"google.golang.org/api/option"
	htransport "google.golang.org/api/transport/http"
	"google.golang.org/protobuf/proto"
)

const (
	// LabelInstallation and LabelRunner mark every instance the control
	// plane creates. Listing and deleting are scoped to LabelInstallation,
	// so one customer's runners never touch another's even when two
	// installations share a project. Label values may not contain "/", so
	// the repository goes in metadata.
	LabelInstallation = "airship-installation"
	LabelRunner       = "airship-runner-id"
	// MetadataRepository names the repository a runner was launched for.
	MetadataRepository = "airship-repository"
	// NetworkTag is the network tag on every runner, for customers' own
	// firewall rules. Airship adds no rules of its own: the default
	// network's rules apply, and the runner needs no inbound traffic.
	NetworkTag = "airship-runner"

	// Compute Engine rejects a metadata value over 256 KiB.
	maxMetadataBytes = 256 << 10
	// DefaultDiskType is the boot disk type when a definition names none.
	DefaultDiskType = "pd-balanced"

	requestTimeout = 30 * time.Second
	clientTTL      = 15 * time.Minute
	cacheTTL       = 10 * time.Minute
	imageCacheTTL  = time.Hour
	cacheLimit     = 10000
)

// operationTimeout bounds waiting for an insert to finish. An instance is
// usually RUNNING within a minute; past this the launch is treated as
// ambiguous and the inventory scan settles it. Tests shorten it.
var operationTimeout = 3 * time.Minute

// listTimeout bounds one zone's instance list (all its pages).
var listTimeout = time.Minute

// clientTimeout is the HTTP client's bound on any one request. It is above
// the two minutes an operations.wait call may block on the server, so a
// wait is never cut short, and holds even for the SDK's list requests,
// which carry no context.
const clientTimeout = 3 * time.Minute

// requestIDNamespace derives request IDs from runner IDs. uuid.NewSHA1
// makes the same (runner, zone, attempt) always send the same ID, which is
// what makes a retried insert idempotent.
var requestIDNamespace = uuid.MustParse("3f3a6b8e-5a3e-4d7c-9c1a-2b8f1e6d4c21")

// Clients are the Compute Engine service clients for one credential.
type Clients struct {
	Instances        *compute.InstancesClient
	Images           *compute.ImagesClient
	MachineImages    *compute.MachineImagesClient
	Projects         *compute.ProjectsClient
	Regions          *compute.RegionsClient
	Routers          *compute.RoutersClient
	Subnetworks      *compute.SubnetworksClient
	ZoneOperations   *compute.ZoneOperationsClient
	RegionOperations *compute.RegionOperationsClient
}

// Close releases every client.
func (c *Clients) Close() {
	for _, closer := range []interface{ Close() error }{c.Instances, c.Images, c.MachineImages, c.Projects, c.Regions, c.Routers, c.Subnetworks, c.ZoneOperations, c.RegionOperations} {
		if closer != nil {
			_ = closer.Close()
		}
	}
}

// Options configure the provider.
type Options struct {
	// Endpoint points every client at a fake Compute Engine in tests.
	Endpoint string
	// NoAuth disables credentials, for tests against a fake.
	NoAuth bool
	// HTTPClient overrides the SDK's; it must do its own authentication.
	HTTPClient *http.Client
}

// Provider is the Compute Engine provider.
type Provider struct {
	opts Options

	mu      sync.Mutex
	clients map[string]*clientEntry
	images  map[string]cached[resolvedImage]
	zones   map[string]cached[[]string]
	nats    map[string]cached[struct{}]
}

type clientEntry struct {
	clients  *Clients
	lastUsed time.Time
	// borrowed counts calls holding the clients. An entry is evicted only
	// when nobody does: a closed client fails every request in flight.
	borrowed int
}

type cached[T any] struct {
	value T
	at    time.Time
}

// New returns a provider that talks to Compute Engine.
func New(opts Options) *Provider {
	return &Provider{
		opts:    opts,
		clients: map[string]*clientEntry{},
		images:  map[string]cached[resolvedImage]{},
		zones:   map[string]cached[[]string]{},
		nats:    map[string]cached[struct{}]{},
	}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.GCP }

// clientsFor returns clients authenticated with raw, building them on first
// use and keeping them for a while: each set holds a token the SDK
// refreshes, and a launch pass makes several calls with the same key. The
// caller holds the clients until it calls release; a set is closed only
// once it has gone unused for clientTTL and nobody holds it.
func (p *Provider) clientsFor(ctx context.Context, raw []byte) (*Clients, func(), error) {
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, entry := range p.clients {
		if entry.borrowed == 0 && now.Sub(entry.lastUsed) > clientTTL {
			entry.clients.Close()
			delete(p.clients, k)
		}
	}
	entry, ok := p.clients[key]
	if !ok {
		clients, err := p.newClients(ctx, raw)
		if err != nil {
			return nil, nil, err
		}
		entry = &clientEntry{clients: clients}
		p.clients[key] = entry
	}
	entry.lastUsed = now
	entry.borrowed++
	var once sync.Once
	release := func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			entry.borrowed--
			entry.lastUsed = time.Now()
		})
	}

	return entry.clients, release, nil
}

func (p *Provider) newClients(ctx context.Context, raw []byte) (*Clients, error) {
	// The clients are built without a deadline: ctx only scopes their
	// construction, and they outlive the call.
	ctx = context.WithoutCancel(ctx)
	httpClient := p.opts.HTTPClient
	if httpClient == nil {
		authOpts := []option.ClientOption{option.WithUserAgent("airship")}
		if p.opts.NoAuth {
			authOpts = append(authOpts, option.WithoutAuthentication())
		} else {
			// The key was validated by ParseSecret when it was connected;
			// the typed option refuses anything but a service account key
			// anyway, so a credential of another type can never reach the
			// SDK.
			authOpts = append(authOpts, option.WithAuthCredentialsJSON(option.ServiceAccount, raw))
		}
		var err error
		httpClient, _, err = htransport.NewClient(ctx, authOpts...)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the key file could not be used: "+err.Error())
		}
		// The SDK's generated list methods send their requests without
		// the caller's context, so a context deadline cannot end them; the
		// client's own timeout is the bound that holds for every call.
		httpClient.Timeout = clientTimeout
	}
	opts := []option.ClientOption{option.WithHTTPClient(httpClient)}
	if p.opts.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(p.opts.Endpoint))
	}
	c := &Clients{}
	var err error
	build := func(f func() error) {
		if err == nil {
			err = f()
		}
	}
	build(func() (e error) { c.Instances, e = compute.NewInstancesRESTClient(ctx, opts...); return })
	build(func() (e error) { c.Images, e = compute.NewImagesRESTClient(ctx, opts...); return })
	build(func() (e error) { c.MachineImages, e = compute.NewMachineImagesRESTClient(ctx, opts...); return })
	build(func() (e error) { c.Projects, e = compute.NewProjectsRESTClient(ctx, opts...); return })
	build(func() (e error) { c.Regions, e = compute.NewRegionsRESTClient(ctx, opts...); return })
	build(func() (e error) { c.Routers, e = compute.NewRoutersRESTClient(ctx, opts...); return })
	build(func() (e error) { c.Subnetworks, e = compute.NewSubnetworksRESTClient(ctx, opts...); return })
	build(func() (e error) { c.ZoneOperations, e = compute.NewZoneOperationsRESTClient(ctx, opts...); return })
	build(func() (e error) { c.RegionOperations, e = compute.NewRegionOperationsRESTClient(ctx, opts...); return })
	if err != nil {
		c.Close()

		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the key file could not be used: "+err.Error())
	}

	return c, nil
}

// Verify implements cloud.Provider. Reading the project proves the key
// works and that the Compute Engine API is enabled for it.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	if _, _, ok := SplitLocation(region); !ok {
		return cloud.Identity{}, fmt.Errorf("%q is not a Compute Engine zone or region", region)
	}
	c, release, err := p.clientsFor(ctx, raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.Projects.Get(ctx, &computepb.GetProjectRequest{Project: secret.ProjectID}); err != nil {
		if isAuthError(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}

	return cloud.Identity{AccountID: secret.ProjectID, Principal: secret.ClientEmail}, nil
}

// List implements cloud.Provider. The region's zones are listed one by
// one: the SDK offers no returnPartialSuccess on the aggregated list, where
// an unreachable zone anywhere in the world fails the call, while here only
// a zone of this region can, and then the whole call fails rather than
// hiding that zone's instances.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, err
	}
	scope, err := ParseScope(region, secret.ProjectID)
	if err != nil {
		return nil, err
	}
	c, release, err := p.clientsFor(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer release()
	zones, err := p.zonesFor(ctx, c, scope, "")
	if err != nil {
		return nil, fmt.Errorf("list instances in %s: %w", region, err)
	}
	out := []cloud.Instance{}
	for _, zone := range zones {
		instances, err := p.listZone(ctx, c, scope.Project, zone, installationID)
		if err != nil {
			return nil, err
		}
		out = append(out, instances...)
	}

	return out, nil
}

// listZone lists the installation's instances in one zone. The call is
// bounded like every other: the reconciler runs its steps in sequence, so
// a list that never answers would stop every runner's lifecycle, not just
// this scope's. The SDK's list requests ignore the context, so the
// deadline is enforced from outside: a list still running when it passes
// is abandoned (the HTTP client's own timeout ends it) and reported as a
// failed list, which decides nothing about the zone's instances.
func (p *Provider) listZone(ctx context.Context, c *Clients, project, zone string, installationID int64) ([]cloud.Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	type result struct {
		instances []cloud.Instance
		err       error
	}
	done := make(chan result, 1)
	go func() {
		it := c.Instances.List(ctx, &computepb.ListInstancesRequest{
			Project: project,
			Zone:    zone,
			Filter:  proto.String(fmt.Sprintf("labels.%s=%d", LabelInstallation, installationID)),
		})
		var out []cloud.Instance
		for instance, err := range it.All() {
			if err != nil {
				done <- result{err: fmt.Errorf("list instances in %s: %w", zone, err)}

				return
			}
			if instance.GetLabels()[LabelInstallation] != strconv.FormatInt(installationID, 10) {
				continue // the filter is a request to the API; the label is the proof
			}
			status := instance.GetStatus()
			out = append(out, cloud.Instance{
				RunnerID:   instance.GetLabels()[LabelRunner],
				InstanceID: InstanceID(zone, instance.GetName()),
				Stopping:   status == computepb.Instance_STOPPING.String(),
				Stopped: status == computepb.Instance_TERMINATED.String() || status == computepb.Instance_SUSPENDED.String() ||
					status == computepb.Instance_SUSPENDING.String(),
			})
		}
		done <- result{instances: out}
	}()
	select {
	case r := <-done:
		return r.instances, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("list instances in %s: %w", zone, ctx.Err())
	}
}

// Terminate implements cloud.Provider. The instance is read first and
// deleted only if its labels say the installation launched it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, region, instanceID string, installationID int64) error {
	secret, err := ParseSecret(raw)
	if err != nil {
		return err
	}
	scope, err := ParseScope(region, secret.ProjectID)
	if err != nil {
		return err
	}
	zone, name, err := ParseInstanceID(instanceID)
	if err != nil {
		return err
	}
	c, release, err := p.clientsFor(ctx, raw)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	instance, err := c.Instances.Get(ctx, &computepb.GetInstanceRequest{Project: scope.Project, Zone: zone, Instance: name})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s: %w", instanceID, err)
	}
	if instance.GetLabels()[LabelInstallation] != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("instance %s is not labeled for installation %d; not deleting it", instanceID, installationID)
	}
	_, err = c.Instances.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: scope.Project, Zone: zone, Instance: name,
		// Deleting is idempotent by nature; the ID only keeps a retried
		// call from being counted twice.
		RequestId: proto.String(uuid.NewSHA1(requestIDNamespace, []byte("delete/"+scope.Project+"/"+instanceID)).String()),
	})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete %s: %w", instanceID, err)
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	scope, err := ParseScope(launch.Region, secret.ProjectID)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if !namePattern.MatchString(launch.RunnerName) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("runner name %q is not a valid instance name", launch.RunnerName)}
	}
	c, release, err := p.clientsFor(ctx, raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	defer release()
	m := launch.Machine
	opts := m.GCP
	if opts == nil {
		opts = &cloud.GCPOptions{}
	}

	image, err := p.resolveImage(ctx, c, scope.Project, m)
	if err != nil {
		return "", classify(err)
	}
	zones, err := p.zonesFor(ctx, c, scope, m.Zone)
	if err != nil {
		return "", classify(err)
	}
	pinned := m.Zone != ""
	if opts.ExistingDisk != nil {
		disk, err := parseDiskSource(opts.ExistingDisk.Source, scope)
		if err != nil {
			return "", &cloud.LaunchError{Err: err}
		}
		if disk.zone != "" {
			// A zonal disk can only be attached in its zone.
			if m.Zone != "" && m.Zone != disk.zone {
				return "", &cloud.LaunchError{Err: fmt.Errorf("disk %s is in %s but the definition pins %s", disk.name, disk.zone, m.Zone)}
			}
			zones, pinned = []string{disk.zone}, true
		}
	}
	if opts.NoPublicIP {
		network, err := p.networkOf(ctx, c, scope, opts)
		if err != nil {
			return "", classify(err)
		}
		if err := p.ensureNAT(ctx, c, scope, network); err != nil {
			return "", classify(err)
		}
	}
	metadata, err := p.metadata(launch, image)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	// Spread launches over the region's zones, but deterministically per
	// runner: a repeated launch must try the same zones in the same order
	// with the same request IDs to find the instance an earlier call made.
	start := zoneOffset(launch.RunnerID)
	var lastErr error
	for attempt := range len(zones) {
		zone := zones[(start+uint64(attempt))%uint64(len(zones))] //nolint:gosec // attempt < len(zones)
		id, err := p.create(ctx, c, scope.Project, zone, launch, image, metadata, attempt)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if pinned || !isZoneSpecific(err, zone) {
			break
		}
	}

	return "", classify(lastErr)
}

// create makes the instance in one zone, or finds the one an earlier
// attempt made. It returns an ambiguous LaunchError when the insert may
// have gone through without an answer.
func (p *Provider) create(ctx context.Context, c *Clients, project, zone string, launch cloud.Launch, image resolvedImage, metadata *computepb.Metadata, attempt int) (string, error) {
	name := launch.RunnerName
	id := InstanceID(zone, name)
	runnerID := launch.RunnerID

	// Name-based idempotency: the runner's own instance, if an earlier
	// attempt created it, is the answer.
	existing, err := p.lookup(ctx, c, project, zone, name)
	switch {
	case err != nil:
		return "", err
	case existing != nil && existing.GetLabels()[LabelRunner] == runnerID:
		return id, nil
	case existing != nil:
		return "", fmt.Errorf("an instance named %s already exists in %s and is not this runner's", name, zone)
	}

	instance := p.instance(zone, Scope{Project: project, Region: RegionOf(zone)}, launch, image, metadata)
	req := &computepb.InsertInstanceRequest{
		Project:          project,
		Zone:             zone,
		InstanceResource: instance,
		RequestId:        proto.String(uuid.NewSHA1(requestIDNamespace, fmt.Appendf(nil, "%s/%s/%d", runnerID, zone, attempt)).String()),
	}
	if image.machineImage {
		req.SourceMachineImage = proto.String(image.selfLink)
	}
	insertCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	op, err := c.Instances.Insert(insertCtx, req)
	cancel()
	if err != nil {
		if isConflict(err) || isTransportError(err) || httpStatus(err) >= http.StatusInternalServerError {
			return p.settle(ctx, c, project, zone, name, runnerID, fmt.Errorf("insert %s in %s: %w", launch.Machine.InstanceType, zone, err))
		}

		return "", fmt.Errorf("insert %s in %s: %w", launch.Machine.InstanceType, zone, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	err = op.Wait(waitCtx)
	if codes := operationErrorCodes(op.Proto()); len(codes) > 0 {
		// Compute Engine accepted the insert and then failed it; the
		// instance it made, if any, is removed by the service itself.
		return "", &operationError{codes: codes, message: fmt.Sprintf("insert %s in %s: %s: %s", launch.Machine.InstanceType, zone, strings.Join(codes, ","), operationMessage(op.Proto()))}
	}
	if err != nil {
		// Polling failed or timed out: the instance may well exist.
		return p.settle(ctx, c, project, zone, name, runnerID, fmt.Errorf("wait for insert of %s in %s: %w", launch.Machine.InstanceType, zone, err))
	}

	return id, nil
}

// settle decides a launch whose answer was lost: the instance is there and
// ours, or the launch stays ambiguous for the inventory scan.
func (p *Provider) settle(ctx context.Context, c *Clients, project, zone, name, runnerID string, cause error) (string, error) {
	existing, err := p.lookup(context.WithoutCancel(ctx), c, project, zone, name)
	if err == nil && existing != nil && existing.GetLabels()[LabelRunner] == runnerID {
		return InstanceID(zone, name), nil
	}
	if err == nil && existing != nil {
		return "", fmt.Errorf("an instance named %s already exists in %s and is not this runner's", name, zone)
	}

	return "", &cloud.LaunchError{Err: cause, Retryable: true, Ambiguous: true}
}

// lookup returns the named instance, nil when there is none.
func (p *Provider) lookup(ctx context.Context, c *Clients, project, zone, name string) (*computepb.Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	instance, err := c.Instances.Get(ctx, &computepb.GetInstanceRequest{Project: project, Zone: zone, Instance: name})
	if isNotFound(err) {
		return nil, nil //nolint:nilnil // absence is the common answer
	}
	if err != nil {
		return nil, fmt.Errorf("get instance %s in %s: %w", name, zone, err)
	}

	return instance, nil
}

// builtinGPUFamilies carry their accelerators in the machine type; adding
// guestAccelerators to them is an error.
var builtinGPUFamilies = map[string]bool{"a2": true, "a3": true, "a4": true, "a4x": true, "g2": true, "g4": true}

// instance builds the resource to insert.
func (p *Provider) instance(zone string, scope Scope, launch cloud.Launch, image resolvedImage, metadata *computepb.Metadata) *computepb.Instance {
	m := launch.Machine
	opts := m.GCP
	if opts == nil {
		opts = &cloud.GCPOptions{}
	}
	labels := map[string]string{
		LabelInstallation: strconv.FormatInt(launch.InstallationID, 10),
		LabelRunner:       launch.RunnerID,
	}
	instance := &computepb.Instance{
		Name:        proto.String(launch.RunnerName),
		Description: proto.String("Airship GitHub Actions runner for " + launch.Repository),
		MachineType: proto.String("zones/" + zone + "/machineTypes/" + m.InstanceType),
		Labels:      labels,
		Metadata:    metadata,
		Tags:        &computepb.Tags{Items: []string{NetworkTag}},
		// A rebooted runner is of no use (its registration was single use),
		// so a host error must not bring it back.
		Scheduling: &computepb.Scheduling{AutomaticRestart: proto.Bool(false)},
	}
	if image.uefi {
		// Shielded VM features need a UEFI image; Compute Engine refuses
		// them on a BIOS one. Secure boot stays off: the GPU path loads
		// unsigned kernel modules.
		instance.ShieldedInstanceConfig = &computepb.ShieldedInstanceConfig{EnableVtpm: proto.Bool(true), EnableIntegrityMonitoring: proto.Bool(true)}
	}
	if !image.machineImage {
		instance.Disks = []*computepb.AttachedDisk{bootDisk(zone, m.Disk, image, labels)}
	}
	if opts.AdditionalDisk != nil {
		instance.Disks = append(instance.Disks, &computepb.AttachedDisk{
			AutoDelete: proto.Bool(true),
			Type:       proto.String(computepb.AttachedDisk_PERSISTENT.String()),
			Mode:       proto.String(computepb.AttachedDisk_READ_WRITE.String()),
			DeviceName: proto.String("airship-data"),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				DiskSizeGb: proto.Int64(int64(opts.AdditionalDisk.SizeGB)),
				DiskType:   proto.String(diskTypePath(zone, opts.AdditionalDisk.Type)),
				Labels:     labels,
			},
		})
	}
	if opts.ExistingDisk != nil {
		instance.Disks = append(instance.Disks, existingDisk(opts.ExistingDisk, scope))
	}
	instance.NetworkInterfaces = networkInterfaces(opts)

	family, _, _ := strings.Cut(m.InstanceType, "-")
	gpu := m.GPUType != "" && !builtinGPUFamilies[family]
	if gpu {
		instance.GuestAccelerators = []*computepb.AcceleratorConfig{{
			AcceleratorType:  proto.String("zones/" + zone + "/acceleratorTypes/" + m.GPUType),
			AcceleratorCount: proto.Int32(1),
		}}
	}
	hasGPU := gpu || builtinGPUFamilies[family]
	switch {
	case m.Spot:
		// Spot VMs cannot migrate or restart, and are deleted (not stopped)
		// when preempted, so no stopped VM lingers for the reconciler.
		instance.Scheduling.ProvisioningModel = proto.String(computepb.Scheduling_SPOT.String())
		instance.Scheduling.InstanceTerminationAction = proto.String(computepb.Scheduling_DELETE.String())
		instance.Scheduling.OnHostMaintenance = proto.String(computepb.Scheduling_TERMINATE.String())
	case opts.OnHostMaintenance != "":
		instance.Scheduling.ProvisioningModel = proto.String(computepb.Scheduling_STANDARD.String())
		instance.Scheduling.OnHostMaintenance = proto.String(opts.OnHostMaintenance)
	case hasGPU:
		instance.Scheduling.ProvisioningModel = proto.String(computepb.Scheduling_STANDARD.String())
		instance.Scheduling.OnHostMaintenance = proto.String(computepb.Scheduling_TERMINATE.String())
	default:
		instance.Scheduling.ProvisioningModel = proto.String(computepb.Scheduling_STANDARD.String())
		instance.Scheduling.OnHostMaintenance = proto.String(computepb.Scheduling_MIGRATE.String())
	}
	for _, sa := range opts.ServiceAccounts {
		instance.ServiceAccounts = append(instance.ServiceAccounts, &computepb.ServiceAccount{Email: proto.String(sa.Email), Scopes: sa.Scopes})
	}

	return instance
}

// bootDisk is the root disk: the image, the size and type the definition
// asked for, deleted with the instance.
func bootDisk(zone string, disk cloud.Disk, image resolvedImage, labels map[string]string) *computepb.AttachedDisk {
	params := &computepb.AttachedDiskInitializeParams{
		SourceImage: proto.String(image.selfLink),
		DiskType:    proto.String(diskTypePath(zone, disk.Type)),
		Labels:      labels,
	}
	if disk.SizeGB > 0 {
		params.DiskSizeGb = proto.Int64(int64(disk.SizeGB))
	}

	return &computepb.AttachedDisk{
		Boot:             proto.Bool(true),
		AutoDelete:       proto.Bool(true),
		Type:             proto.String(computepb.AttachedDisk_PERSISTENT.String()),
		Mode:             proto.String(computepb.AttachedDisk_READ_WRITE.String()),
		InitializeParams: params,
	}
}

// existingDisk attaches a customer's disk by reference. It is never
// auto-deleted: the machine is Airship's, the disk is not.
func existingDisk(disk *cloud.GCPExistingDisk, scope Scope) *computepb.AttachedDisk {
	parsed, _ := parseDiskSource(disk.Source, scope) // validated by Launch
	out := &computepb.AttachedDisk{
		Source:     proto.String(parsed.path),
		AutoDelete: proto.Bool(false),
		Boot:       proto.Bool(false),
		Type:       proto.String(computepb.AttachedDisk_PERSISTENT.String()),
		Mode:       proto.String(cmp.Or(disk.Mode, computepb.AttachedDisk_READ_WRITE.String())),
		DeviceName: proto.String(cmp.Or(disk.DeviceName, parsed.name)),
	}
	if disk.Interface != "" {
		out.Interface = proto.String(disk.Interface)
	}

	return out
}

// diskSource is a parsed disk reference.
type diskSource struct {
	// path is projects/P/(zones/Z|regions/R)/disks/N, always with a project.
	path    string
	project string
	zone    string // for a zonal disk
	region  string // for a regional disk
	name    string
}

var diskSourcePattern = regexp.MustCompile(`^(?:https://(?:www|compute)\.googleapis\.com/compute/(?:v1|beta|alpha)/)?(?:projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/)?(zones|regions)/([a-z]+-[a-z]+[0-9]+(?:-[a-z])?)/disks/([a-z]([-a-z0-9]{0,61}[a-z0-9])?)$`)

// parseDiskSource resolves a disk reference against the launch's scope and
// refuses a disk the launch could not attach: another region, or a zonal
// disk whose zone is not in the region.
func parseDiskSource(source string, scope Scope) (diskSource, error) {
	m := diskSourcePattern.FindStringSubmatch(source)
	if m == nil {
		return diskSource{}, fmt.Errorf("%q is not a disk reference (projects/P/zones/Z/disks/N)", source)
	}
	out := diskSource{project: cmp.Or(m[1], scope.Project), name: m[4]}
	switch m[2] {
	case "zones":
		if !IsZone(m[3]) || RegionOf(m[3]) != scope.Region {
			return diskSource{}, fmt.Errorf("disk %s is in zone %s, outside region %s", out.name, m[3], scope.Region)
		}
		out.zone = m[3]
	default:
		if m[3] != scope.Region {
			return diskSource{}, fmt.Errorf("disk %s is in region %s, not %s", out.name, m[3], scope.Region)
		}
		out.region = m[3]
	}
	out.path = "projects/" + out.project + "/" + m[2] + "/" + m[3] + "/disks/" + out.name

	return out, nil
}

// diskTypePath turns a disk type into the launch zone's resource path. A
// path from the config (the docs describe
// projects/P/zones/Z/diskTypes/T) names a zone of its own, which is not
// necessarily where this runner launches: disk types exist in every zone,
// so only the type's name is kept.
func diskTypePath(zone, diskType string) string {
	if diskType == "" {
		diskType = DefaultDiskType
	}
	if i := strings.LastIndex(diskType, "/"); i >= 0 {
		diskType = diskType[i+1:]
	}

	return "zones/" + zone + "/diskTypes/" + diskType
}

// networkInterfaces are the definition's, or one on the default network
// with an external address. Without a public IP no interface gets an
// access config; egress goes through the region's Cloud NAT instead.
func networkInterfaces(opts *cloud.GCPOptions) []*computepb.NetworkInterface {
	var out []*computepb.NetworkInterface
	if len(opts.NetworkInterfaces) == 0 {
		nic := &computepb.NetworkInterface{Network: proto.String(defaultNetwork)}
		if !opts.NoPublicIP {
			nic.AccessConfigs = []*computepb.AccessConfig{{
				Type:        proto.String(computepb.AccessConfig_ONE_TO_ONE_NAT.String()),
				Name:        proto.String("External NAT"),
				NetworkTier: proto.String(computepb.AccessConfig_PREMIUM.String()),
			}}
		}

		return []*computepb.NetworkInterface{nic}
	}
	for _, nic := range opts.NetworkInterfaces {
		pb := &computepb.NetworkInterface{}
		if nic.Network != "" {
			pb.Network = proto.String(nic.Network)
		}
		if nic.Subnetwork != "" {
			pb.Subnetwork = proto.String(nic.Subnetwork)
		}
		if nic.NicType != "" {
			pb.NicType = proto.String(nic.NicType)
		}
		if nic.StackType != "" {
			pb.StackType = proto.String(nic.StackType)
		}
		if !opts.NoPublicIP {
			for _, ac := range nic.AccessConfigs {
				pbAC := &computepb.AccessConfig{Type: proto.String(computepb.AccessConfig_ONE_TO_ONE_NAT.String())}
				if ac.Type != "" {
					pbAC.Type = proto.String(ac.Type)
				}
				if ac.Name != "" {
					pbAC.Name = proto.String(ac.Name)
				}
				if ac.NetworkTier != "" {
					pbAC.NetworkTier = proto.String(ac.NetworkTier)
				}
				pb.AccessConfigs = append(pb.AccessConfigs, pbAC)
			}
		}
		out = append(out, pb)
	}

	return out
}

// metadata carries the boot script and what the instance is for.
func (p *Provider) metadata(launch cloud.Launch, image resolvedImage) (*computepb.Metadata, error) {
	items := []*computepb.Items{
		{Key: proto.String(MetadataRepository), Value: proto.String(launch.Repository)},
	}
	if !launch.Boot.PullFromAPI {
		var script string
		var err error
		key := "startup-script"
		if image.windows {
			key = "windows-startup-script-ps1"
			password := ""
			if launch.Boot.Windows.Interactive {
				password, err = randomPassword()
				if err != nil {
					return nil, err
				}
			}
			script, err = bootscript.WindowsScript(launch.Boot, launch.Machine.Arch, password)
		} else {
			script, err = bootscript.Linux(launch.Boot, launch.Machine.Arch)
		}
		if err != nil {
			return nil, err
		}
		if len(script) > maxMetadataBytes {
			return nil, fmt.Errorf("boot script is %d bytes, over Compute Engine's %d byte metadata limit", len(script), maxMetadataBytes)
		}
		items = append(items, &computepb.Items{Key: proto.String(key), Value: proto.String(script)})
	}

	return &computepb.Metadata{Items: items}, nil
}

// randomPassword returns 24 alphanumeric characters for a Windows account.
func randomPassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 24)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("random password: %w", err)
		}
		out[i] = alphabet[n.Int64()]
	}

	return string(out), nil
}

func zoneOffset(runnerID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runnerID))

	return h.Sum64()
}

func operationMessage(op *computepb.Operation) string {
	if op == nil || op.GetError() == nil {
		return ""
	}
	var parts []string
	for _, e := range op.GetError().GetErrors() {
		parts = append(parts, e.GetMessage())
	}

	return strings.Join(parts, "; ")
}

// ErrNotSupported marks config the provider refuses rather than ignores.
var ErrNotSupported = errors.New("not supported")
