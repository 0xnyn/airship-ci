// Package azure launches each runner as its own virtual machine in the
// customer's Azure subscription, with the service principal they
// connected in the dashboard. One VM per job is the isolation boundary, as
// on AWS and GCP.
//
// Azure differs from the other clouds in ways the lifecycle has to
// respect:
//
//   - A machine is several resources: the virtual machine, its network
//     interface, its public address and its OS disk, each created by its
//     own call. They are named after the runner and tagged for it, and the
//     interface, address and disk are marked to be deleted with the VM, so
//     deleting the VM is the whole cleanup. An interface or address made
//     for a VM that was then never created is swept up by the inventory
//     scan once it is old enough not to belong to a launch in flight.
//   - Creating a VM is an asynchronous operation. The request is accepted,
//     and capacity or quota failures arrive when it finishes; a VM whose
//     creation failed stays as a Failed resource until it is deleted, so
//     the provider deletes it.
//   - Resources are named, not numbered, and a PUT of an existing VM with
//     boot data is refused. So a launch looks the VM up first: if it exists
//     and carries the runner's tag, it is the one, in whatever state its
//     creation left it.
//   - Linux images take the boot script as custom data, which cloud-init
//     runs on first boot. Windows images do not run custom data, and
//     specialized images take no provisioning at all; those boot through a
//     Run Command created after the VM, which runs the script once.
//   - A VM that powers itself off is "stopped" but not deallocated: it
//     runs nothing and is still billed. The inventory scan treats such a VM
//     as gone and deletes it. Spot VMs are deleted on eviction.
//   - New virtual networks have no implicit egress. Runners get a public
//     address each, or a NAT gateway on the region's subnet when the
//     definition asks for no public address.
package azure

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions/v2"
	"golang.org/x/crypto/ssh"
)

const (
	// TagInstallation and TagRunner mark every resource the control plane
	// creates for a runner. Listing and deleting are scoped to
	// TagInstallation, so one customer's runners never touch another's even
	// when two installations share a subscription.
	TagInstallation = "airship-installation"
	TagRunner       = "airship-runner-id"
	// TagRepository names the repository a runner was launched for.
	TagRepository = "airship-repository"
	// TagCreated is when a resource was made, so the sweep can tell an
	// orphan from a launch in flight.
	TagCreated = "airship-created"
	// TagManaged marks the shared infrastructure the provider creates.
	TagManaged = "airship-managed"

	// DefaultLinuxDiskGB is the OS disk of a Linux machine when a
	// definition names no size: the root volume default. Windows images
	// keep the image's own size (127 GB) unless a size is named, since a
	// disk smaller than the image is refused.
	DefaultLinuxDiskGB = 75
	// maxCustomDataBytes is Azure's limit on custom data, before encoding.
	maxCustomDataBytes = 65535

	requestTimeout = 30 * time.Second
	clientTTL      = 30 * time.Minute
	cacheTTL       = 10 * time.Minute
	imageCacheTTL  = time.Hour
	cacheLimit     = 10000
)

// Timeouts on the long-running operations. The launch's context, bounded
// by its lease, is the real limit; these keep one step from eating it all.
var (
	// operationTimeout bounds a network resource's creation.
	operationTimeout = 3 * time.Minute
	// vmOperationTimeout bounds a VM's creation. Linux is usually ready in
	// a minute or two, Windows in three to five.
	vmOperationTimeout = 10 * time.Minute
	// runCommandTimeout bounds the Run Command's start, which waits for the
	// VM agent to come up.
	runCommandTimeout = 5 * time.Minute
	// listTimeout bounds one inventory list (all its pages).
	listTimeout = 2 * time.Minute
	// pollFrequency is how often an operation is polled when Azure gives no
	// Retry-After. Tests shorten it.
	pollFrequency = 2 * time.Second
)

// Clients are the Resource Manager clients for one credential.
type Clients struct {
	VMs                  *armcompute.VirtualMachinesClient
	RunCommands          *armcompute.VirtualMachineRunCommandsClient
	VMImages             *armcompute.VirtualMachineImagesClient
	Images               *armcompute.ImagesClient
	GalleryImages        *armcompute.GalleryImagesClient
	GalleryImageVersions *armcompute.GalleryImageVersionsClient
	VNets                *armnetwork.VirtualNetworksClient
	Subnets              *armnetwork.SubnetsClient
	NSGs                 *armnetwork.SecurityGroupsClient
	PublicIPs            *armnetwork.PublicIPAddressesClient
	NICs                 *armnetwork.InterfacesClient
	NATs                 *armnetwork.NatGatewaysClient
	ResourceGroups       *armresources.ResourceGroupsClient
	Subscriptions        *armsubscriptions.Client
}

// Options configure the provider.
type Options struct {
	// Endpoint points every client at a fake Resource Manager in tests.
	Endpoint string
	// Credential replaces the service principal's, for tests against a
	// fake that checks no tokens.
	Credential azcore.TokenCredential
	// Transport overrides the SDK's HTTP client.
	Transport policy.Transporter
	// Logger is where cleanup that fails is reported; the default logger
	// when nil.
	Logger *slog.Logger
}

// Provider is the Azure provider.
type Provider struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	clients map[string]*clientEntry
	images  map[string]cached[resolvedImage]
	setups  map[string]cached[struct{}]
	sweeps  map[string]time.Time
	// setupMu serializes the creation of shared infrastructure.
	setupMu sync.Mutex

	sshOnce sync.Once
	sshKey  string
	sshErr  error
}

type clientEntry struct {
	clients  *Clients
	lastUsed time.Time
}

type cached[T any] struct {
	value T
	at    time.Time
}

// New returns a provider that talks to Azure Resource Manager.
func New(opts Options) *Provider {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Provider{
		opts:    opts,
		log:     log,
		clients: map[string]*clientEntry{},
		images:  map[string]cached[resolvedImage]{},
		setups:  map[string]cached[struct{}]{},
		sweeps:  map[string]time.Time{},
	}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Azure }

// clientsFor returns clients authenticated with raw, building them on
// first use and keeping them for a while: the credential caches the token
// Entra issued, and a launch makes a dozen calls with the same one. The
// clients hold no connections of their own, so an unused set is simply
// dropped.
func (p *Provider) clientsFor(ctx context.Context, raw []byte) (*Clients, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, entry := range p.clients {
		if now.Sub(entry.lastUsed) > clientTTL {
			delete(p.clients, k)
		}
	}
	entry, ok := p.clients[key]
	if !ok {
		clients, err := p.newClients(ctx, secret)
		if err != nil {
			return nil, err
		}
		entry = &clientEntry{clients: clients}
		p.clients[key] = entry
	}
	entry.lastUsed = now

	return entry.clients, nil
}

func (p *Provider) newClients(_ context.Context, secret Secret) (*Clients, error) {
	cfg := azcloud.AzurePublic
	if p.opts.Endpoint != "" {
		cfg = azcloud.Configuration{
			ActiveDirectoryAuthorityHost: azcloud.AzurePublic.ActiveDirectoryAuthorityHost,
			Services: map[azcloud.ServiceName]azcloud.ServiceConfiguration{
				azcloud.ResourceManager: {Audience: azcloud.AzurePublic.Services[azcloud.ResourceManager].Audience, Endpoint: p.opts.Endpoint},
			},
		}
	}
	cred := p.opts.Credential
	if cred == nil {
		var err error
		cred, err = azidentity.NewClientSecretCredential(secret.TenantID, secret.ClientID, secret.ClientSecret, &azidentity.ClientSecretCredentialOptions{
			ClientOptions: azcore.ClientOptions{Cloud: cfg, Transport: p.opts.Transport, Telemetry: policy.TelemetryOptions{ApplicationID: "airship"}},
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the credential could not be used: "+err.Error())
		}
	}
	opts := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Cloud:     cfg,
		Transport: p.opts.Transport,
		Telemetry: policy.TelemetryOptions{ApplicationID: "airship"},
		// Every call carries a context deadline; the SDK's own retries stay
		// short so a deadline is spent on the call, not on waiting.
		Retry:                           policy.RetryOptions{MaxRetries: 2, RetryDelay: 500 * time.Millisecond, MaxRetryDelay: 4 * time.Second},
		InsecureAllowCredentialWithHTTP: strings.HasPrefix(p.opts.Endpoint, "http://"),
	}}
	compute, err := armcompute.NewClientFactory(secret.SubscriptionID, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the credential could not be used: "+err.Error())
	}
	network, err := armnetwork.NewClientFactory(secret.SubscriptionID, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the credential could not be used: "+err.Error())
	}
	groups, err := armresources.NewResourceGroupsClient(secret.SubscriptionID, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the credential could not be used: "+err.Error())
	}
	subscriptions, err := armsubscriptions.NewClient(cred, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, "the credential could not be used: "+err.Error())
	}

	return &Clients{
		VMs:                  compute.NewVirtualMachinesClient(),
		RunCommands:          compute.NewVirtualMachineRunCommandsClient(),
		VMImages:             compute.NewVirtualMachineImagesClient(),
		Images:               compute.NewImagesClient(),
		GalleryImages:        compute.NewGalleryImagesClient(),
		GalleryImageVersions: compute.NewGalleryImageVersionsClient(),
		VNets:                network.NewVirtualNetworksClient(),
		Subnets:              network.NewSubnetsClient(),
		NSGs:                 network.NewSecurityGroupsClient(),
		PublicIPs:            network.NewPublicIPAddressesClient(),
		NICs:                 network.NewInterfacesClient(),
		NATs:                 network.NewNatGatewaysClient(),
		ResourceGroups:       groups,
		Subscriptions:        subscriptions,
	}, nil
}

// Verify implements cloud.Provider. Reading the subscription proves the
// credential works and names a subscription the application can see; the
// region must be one the subscription offers.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	if !IsRegion(region) {
		return cloud.Identity{}, fmt.Errorf("%q is not an Azure region (such as eastus)", region)
	}
	c, err := p.clientsFor(ctx, raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	sub, err := c.Subscriptions.Get(ctx, secret.SubscriptionID, nil)
	if err != nil {
		if isAuthError(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}
	if sub.State != nil && (*sub.State == armsubscriptions.SubscriptionStateDisabled || *sub.State == armsubscriptions.SubscriptionStateDeleted) {
		return cloud.Identity{}, fmt.Errorf("%w: subscription %s is %s", cloud.ErrBadCredentials, secret.SubscriptionID, string(*sub.State))
	}
	found := false
	pager := c.Subscriptions.NewListLocationsPager(secret.SubscriptionID, nil)
	for pager.More() && !found {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return cloud.Identity{}, fmt.Errorf("list regions: %w", err)
		}
		for _, loc := range page.Value {
			if loc != nil && loc.Name != nil && *loc.Name == region {
				found = true

				break
			}
		}
	}
	if !found {
		return cloud.Identity{}, fmt.Errorf("region %s is not available to subscription %s", region, secret.SubscriptionID)
	}

	return cloud.Identity{AccountID: secret.SubscriptionID, Principal: secret.ClientID}, nil
}

// List implements cloud.Provider. The subscription's VMs are listed with
// their power state (which a resource group listing cannot carry) and
// filtered to the provider's resource group, the region and the
// installation. A page that fails fails the whole list: an instance
// missing from a successful list is presumed gone.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	if !IsRegion(region) {
		return nil, fmt.Errorf("%q is not an Azure region", region)
	}
	c, err := p.clientsFor(ctx, raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	installation := strconv.FormatInt(installationID, 10)
	out := []cloud.Instance{}
	pager := c.VMs.NewListAllPager(&armcompute.VirtualMachinesClientListAllOptions{StatusOnly: to.Ptr("true")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list virtual machines in %s: %w", region, err)
		}
		for _, vm := range page.Value {
			if vm == nil || vm.ID == nil || vm.Name == nil || !inResourceGroup(*vm.ID) || !strings.EqualFold(deref(vm.Location), region) {
				continue
			}
			if deref(vm.Tags[TagInstallation]) != installation {
				continue
			}
			state := provisioningState(vm)
			power := powerState(vm)
			out = append(out, cloud.Instance{
				RunnerID:   deref(vm.Tags[TagRunner]),
				InstanceID: *vm.Name,
				Stopping:   state == "Deleting",
				// A VM that was never allocated has no power state at all.
				Stopped: stoppedPowerStates[power] || (power == "" && state == "Failed"),
			})
		}
	}

	return out, nil
}

// stoppedPowerStates are the power states of a VM that runs nothing: shut
// down from inside (stopped, still billed) or deallocated by hand.
var stoppedPowerStates = map[string]bool{"stopped": true, "stopping": true, "deallocated": true, "deallocating": true}

func provisioningState(vm *armcompute.VirtualMachine) string {
	if vm == nil || vm.Properties == nil {
		return ""
	}

	return deref(vm.Properties.ProvisioningState)
}

// powerState reads the PowerState/<state> status of a VM's instance view.
func powerState(vm *armcompute.VirtualMachine) string {
	if vm == nil || vm.Properties == nil || vm.Properties.InstanceView == nil {
		return ""
	}
	for _, status := range vm.Properties.InstanceView.Statuses {
		if status != nil && status.Code != nil {
			if state, ok := strings.CutPrefix(*status.Code, "PowerState/"); ok {
				return strings.ToLower(state)
			}
		}
	}

	return ""
}

// Terminate implements cloud.Provider. The VM is read first and deleted
// only if its tags say the installation launched it. Its interface,
// address and disk go with it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, region, instanceID string, installationID int64) error {
	if !IsRegion(region) {
		return fmt.Errorf("%q is not an Azure region", region)
	}
	name, err := ParseInstanceID(instanceID)
	if err != nil {
		return err
	}
	c, err := p.clientsFor(ctx, raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	vm, err := c.VMs.Get(ctx, ResourceGroup, name, nil)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s: %w", name, err)
	}
	if deref(vm.Tags[TagInstallation]) != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("virtual machine %s is not tagged for installation %d; not deleting it", name, installationID)
	}
	if provisioningState(&vm.VirtualMachine) == "Deleting" {
		return nil
	}
	// The deletion is not waited for: the scan lists the VM as stopping
	// until it is gone.
	_, err = c.VMs.BeginDelete(ctx, ResourceGroup, name, &armcompute.VirtualMachinesClientBeginDeleteOptions{ForceDeletion: to.Ptr(true)})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if !IsRegion(launch.Region) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("%q is not an Azure region", launch.Region)}
	}
	if !namePattern.MatchString(launch.RunnerName) {
		return "", &cloud.LaunchError{Err: fmt.Errorf("runner name %q is not a valid virtual machine name", launch.RunnerName)}
	}
	c, err := p.clientsFor(ctx, raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	opts := launch.Machine.Azure
	if opts == nil {
		opts = &cloud.AzureOptions{}
	}
	region := launch.Region

	if err := p.ensureResourceGroup(ctx, c, secret.SubscriptionID, region); err != nil {
		return "", classify(err)
	}
	image, err := p.resolveImage(ctx, c, secret.SubscriptionID, region, launch.Machine)
	if err != nil {
		return "", classify(err)
	}
	subnet, err := p.ensureNetwork(ctx, c, secret.SubscriptionID, region, opts.NoPublicIP)
	if err != nil {
		return "", classify(err)
	}

	// Name-based idempotency: the runner's own VM, if an earlier attempt
	// created it, is the answer, in whatever state its creation is.
	name := launch.RunnerName
	existing, err := p.lookup(ctx, c, name)
	switch {
	case err != nil:
		return "", classify(err)
	case existing != nil && deref(existing.Tags[TagRunner]) == launch.RunnerID:
		return p.adopt(ctx, c, launch, image, existing)
	case existing != nil:
		return "", &cloud.LaunchError{Err: fmt.Errorf("a virtual machine named %s already exists in %s and is not this runner's", name, ResourceGroup)}
	}

	return p.create(ctx, c, launch, image, subnet, !opts.NoPublicIP)
}

// create makes the VM and what it needs. It returns an ambiguous
// LaunchError when the creation may have gone through without an answer.
func (p *Provider) create(ctx context.Context, c *Clients, launch cloud.Launch, image resolvedImage, subnet string, publicIP bool) (string, error) {
	name := launch.RunnerName
	region := launch.Region
	tags := map[string]*string{
		TagInstallation: to.Ptr(strconv.FormatInt(launch.InstallationID, 10)),
		TagRunner:       to.Ptr(launch.RunnerID),
		TagRepository:   to.Ptr(launch.Repository),
		TagCreated:      to.Ptr(time.Now().UTC().Format(time.RFC3339)),
	}
	vm, err := p.virtualMachine(launch, image, tags)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	addressID := ""
	if publicIP {
		addressID, err = p.createAddress(ctx, c, region, name+ipSuffix, tags)
		if err != nil {
			return "", classify(err)
		}
	}
	nicID, err := p.createInterface(ctx, c, region, name+nicSuffix, subnet, addressID, tags)
	if err != nil {
		if publicIP {
			p.removeAddress(ctx, c, name+ipSuffix)
		}

		return "", classify(err)
	}
	vm.Properties.NetworkProfile = &armcompute.NetworkProfile{NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{
		ID:         to.Ptr(nicID),
		Properties: &armcompute.NetworkInterfaceReferenceProperties{Primary: to.Ptr(true), DeleteOption: to.Ptr(armcompute.DeleteOptionsDelete)},
	}}}

	poller, err := c.VMs.BeginCreateOrUpdate(ctx, ResourceGroup, name, vm, nil)
	if err != nil {
		if lostAnswer(err) {
			return p.settle(ctx, c, launch, image, fmt.Errorf("create %s in %s: %w", launch.Machine.InstanceType, region, err))
		}
		// Refused outright: nothing of the VM exists, but its interface
		// and address do.
		p.removeInterface(ctx, c, name+nicSuffix)
		if publicIP {
			p.removeAddress(ctx, c, name+ipSuffix)
		}

		return "", refused(err, "create %s in %s", launch.Machine.InstanceType, region)
	}
	if _, err := pollUntilDone(ctx, poller, vmOperationTimeout); err != nil {
		if lostAnswer(err) {
			// Polling failed or timed out: the VM may well be there.
			return p.settle(ctx, c, launch, image, fmt.Errorf("wait for creation of %s in %s: %w", launch.Machine.InstanceType, region, err))
		}
		// Azure accepted the creation and then failed it (no capacity,
		// quota, a refused image). The VM stays as a Failed resource, and
		// takes its interface, address and disk with it when deleted.
		p.removeMachine(ctx, c, name)

		return "", refused(err, "create %s in %s", launch.Machine.InstanceType, region)
	}

	return p.boot(ctx, c, launch, image)
}

// lostAnswer reports a call whose outcome is unknown: no answer at all, or
// a status with no verdict in it (the API itself failing, throttling).
func lostAnswer(err error) bool {
	if isTransportError(err) {
		return true
	}
	status := httpStatus(err)
	if status == 0 || len(codes(err)) > 0 && !hasCode(err, "ServiceUnavailable", "GatewayTimeout", "TooManyRequests", "InternalServerError", "RequestTimeout") {
		return false
	}

	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= http.StatusInternalServerError
}

// settle decides a launch whose answer was lost: the VM is there and ours,
// or the launch stays ambiguous for the inventory scan.
func (p *Provider) settle(ctx context.Context, c *Clients, launch cloud.Launch, image resolvedImage, cause error) (string, error) {
	existing, err := p.lookup(context.WithoutCancel(ctx), c, launch.RunnerName)
	switch {
	case err == nil && existing != nil && deref(existing.Tags[TagRunner]) == launch.RunnerID:
		return p.adopt(ctx, c, launch, image, existing)
	case err == nil && existing != nil:
		return "", &cloud.LaunchError{Err: fmt.Errorf("a virtual machine named %s already exists in %s and is not this runner's", launch.RunnerName, ResourceGroup)}
	}

	return "", &cloud.LaunchError{Err: cause, Retryable: true, Ambiguous: true}
}

// adopt finishes a launch whose VM exists: it waits out a creation still in
// progress, boots a VM that is ready, and removes one whose creation
// failed so the next attempt can start over.
func (p *Provider) adopt(ctx context.Context, c *Clients, launch cloud.Launch, image resolvedImage, vm *armcompute.VirtualMachine) (string, error) {
	name := launch.RunnerName
	state := provisioningState(vm)
	// The wait is bounded as a creation of its own would be, whatever the
	// launch's context says.
	waitCtx, cancel := context.WithTimeout(ctx, vmOperationTimeout)
	defer cancel()
	for state == "Creating" || state == "Updating" || state == "" {
		select {
		case <-waitCtx.Done():
			// Still being created when the launch ran out of time: the VM
			// is real, and the scan lists it.
			return "", &cloud.LaunchError{Err: fmt.Errorf("virtual machine %s is still %s", name, strings.ToLower(state)), Retryable: true, Ambiguous: true}
		case <-time.After(pollFrequency):
		}
		current, err := p.lookup(waitCtx, c, name)
		if err != nil {
			// The VM is known to exist; not knowing how it is doing is
			// the ambiguous case, however the lookup failed.
			return "", &cloud.LaunchError{Err: fmt.Errorf("virtual machine %s is being created: %w", name, err), Retryable: true, Ambiguous: true}
		}
		if current == nil {
			return "", &cloud.LaunchError{Err: fmt.Errorf("virtual machine %s disappeared while being created", name), Retryable: true}
		}
		state = provisioningState(current)
	}
	switch state {
	case "Succeeded":
		return p.boot(ctx, c, launch, image)
	case "Deleting":
		return "", &cloud.LaunchError{Err: fmt.Errorf("virtual machine %s is still being deleted", name), Retryable: true}
	default:
		// Failed: a creation an earlier attempt could not clean up.
		p.removeMachine(ctx, c, name)

		return "", &cloud.LaunchError{Err: fmt.Errorf("virtual machine %s failed to provision; removed it", name), Retryable: true}
	}
}

// boot gives a Windows or specialized VM its boot script through a Run
// Command, once; Linux images already ran theirs from custom data. A VM
// that cannot be given its script is removed: it would never register.
func (p *Provider) boot(ctx context.Context, c *Clients, launch cloud.Launch, image resolvedImage) (string, error) {
	name := launch.RunnerName
	if launch.Boot.PullFromAPI || (!image.windows && !image.specialized) {
		return name, nil
	}
	getCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	_, err := c.RunCommands.GetByVirtualMachine(getCtx, ResourceGroup, name, runCommandName, nil)
	cancel()
	switch {
	case err == nil:
		return name, nil // created by an earlier attempt; it runs once
	case !isNotFound(err):
		return "", classify(fmt.Errorf("get run command on %s: %w", name, err))
	}
	script, err := p.bootScript(launch, image)
	if err != nil {
		p.removeMachine(ctx, c, name)

		return "", &cloud.LaunchError{Err: err}
	}
	poller, err := c.RunCommands.BeginCreateOrUpdate(ctx, ResourceGroup, name, runCommandName, armcompute.VirtualMachineRunCommand{
		Location: to.Ptr(launch.Region),
		Properties: &armcompute.VirtualMachineRunCommandProperties{
			// The script runs the job and then shuts the machine down; the
			// command only has to start it.
			AsyncExecution: to.Ptr(true),
			Source:         &armcompute.VirtualMachineRunCommandScriptSource{Script: to.Ptr(script)},
		},
	}, nil)
	if err == nil {
		_, err = pollUntilDone(ctx, poller, runCommandTimeout)
	}
	if err == nil {
		return name, nil
	}
	if lostAnswer(err) {
		// The command may have been created: if it is there, it runs.
		getCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
		_, getErr := c.RunCommands.GetByVirtualMachine(getCtx, ResourceGroup, name, runCommandName, nil)
		cancel()
		if getErr == nil {
			return name, nil
		}
		p.removeMachine(ctx, c, name)

		return "", &cloud.LaunchError{Err: fmt.Errorf("start the boot script on %s: %w", name, err), Retryable: true}
	}
	p.removeMachine(ctx, c, name)

	return "", refused(err, "start the boot script on %s", name)
}

// bootScript renders the script a Run Command starts: PowerShell on
// Windows, bash on a specialized Linux image.
func (p *Provider) bootScript(launch cloud.Launch, image resolvedImage) (string, error) {
	if image.windows {
		password := ""
		if launch.Boot.Windows.Interactive {
			var err error
			password, err = randomPassword()
			if err != nil {
				return "", err
			}
		}

		return bootscript.WindowsScript(launch.Boot, launch.Machine.Arch, password) //nolint:wrapcheck // the script's own error names the field
	}

	return bootscript.Linux(launch.Boot, launch.Machine.Arch) //nolint:wrapcheck // the script's own error names the field
}

// removeMachine deletes a VM whose launch is being given up on, without
// waiting. The inventory scan terminates it anyway if this fails: its
// runner is finished.
func (p *Provider) removeMachine(ctx context.Context, c *Clients, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	_, err := c.VMs.BeginDelete(ctx, ResourceGroup, name, &armcompute.VirtualMachinesClientBeginDeleteOptions{ForceDeletion: to.Ptr(true)})
	if err != nil && !isNotFound(err) {
		p.log.WarnContext(ctx, "delete virtual machine of a failed launch", slog.String("vm", name), slog.Any("error", err))
	}
}

// lookup returns the named VM, nil when there is none.
func (p *Provider) lookup(ctx context.Context, c *Clients, name string) (*armcompute.VirtualMachine, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	vm, err := c.VMs.Get(ctx, ResourceGroup, name, nil)
	if isNotFound(err) {
		return nil, nil //nolint:nilnil // absence is the common answer
	}
	if err != nil {
		return nil, fmt.Errorf("get virtual machine %s: %w", name, err)
	}

	return &vm.VirtualMachine, nil
}

// virtualMachine builds the resource to create, without its network
// profile, which the interface decides.
func (p *Provider) virtualMachine(launch cloud.Launch, image resolvedImage, tags map[string]*string) (armcompute.VirtualMachine, error) {
	m := launch.Machine
	name := launch.RunnerName
	osDisk := &armcompute.OSDisk{
		Name:         to.Ptr(name + osDiskSuffix),
		CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
		DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDelete),
		Caching:      to.Ptr(armcompute.CachingTypesReadWrite),
	}
	size := m.Disk.SizeGB
	if size == 0 && !image.windows {
		size = DefaultLinuxDiskGB
	}
	if size > 0 {
		osDisk.DiskSizeGB = to.Ptr(int32(size)) //nolint:gosec // validated by the config parser
	}
	if m.Disk.Type != "" {
		osDisk.ManagedDisk = &armcompute.ManagedDiskParameters{StorageAccountType: to.Ptr(armcompute.StorageAccountTypes(m.Disk.Type))}
	}
	vm := armcompute.VirtualMachine{
		Location: to.Ptr(launch.Region),
		Tags:     tags,
		Plan:     image.plan,
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(m.InstanceType))},
			StorageProfile:  &armcompute.StorageProfile{ImageReference: &image.ref, OSDisk: osDisk},
		},
	}
	if image.trustedLaunch {
		// Secure boot stays off: the GPU path loads unsigned kernel
		// modules, and Windows needs only the vTPM.
		vm.Properties.SecurityProfile = &armcompute.SecurityProfile{
			SecurityType: to.Ptr(armcompute.SecurityTypesTrustedLaunch),
			UefiSettings: &armcompute.UefiSettings{SecureBootEnabled: to.Ptr(false), VTpmEnabled: to.Ptr(true)},
		}
	}
	if m.Spot {
		// Spot VMs are deleted (not deallocated) on eviction, so no stopped
		// VM lingers for the reconciler, and priced up to on-demand.
		vm.Properties.Priority = to.Ptr(armcompute.VirtualMachinePriorityTypesSpot)
		vm.Properties.EvictionPolicy = to.Ptr(armcompute.VirtualMachineEvictionPolicyTypesDelete)
		vm.Properties.BillingProfile = &armcompute.BillingProfile{MaxPrice: to.Ptr(-1.0)}
	}
	if image.specialized {
		return vm, nil // the image carries its own accounts and host name
	}
	profile := &armcompute.OSProfile{AdminUsername: to.Ptr(adminUsername)}
	if image.windows {
		password, err := randomPassword()
		if err != nil {
			return armcompute.VirtualMachine{}, err
		}
		profile.ComputerName = to.Ptr(computerName(name))
		profile.AdminPassword = to.Ptr(password)
		profile.WindowsConfiguration = &armcompute.WindowsConfiguration{
			ProvisionVMAgent: to.Ptr(true),
			// Updates would restart a machine mid-job.
			EnableAutomaticUpdates: to.Ptr(false),
		}
	} else {
		key, err := p.sshPublicKey()
		if err != nil {
			return armcompute.VirtualMachine{}, err
		}
		profile.ComputerName = to.Ptr(name)
		profile.LinuxConfiguration = &armcompute.LinuxConfiguration{
			DisablePasswordAuthentication: to.Ptr(true),
			ProvisionVMAgent:              to.Ptr(true),
			SSH: &armcompute.SSHConfiguration{PublicKeys: []*armcompute.SSHPublicKey{{
				Path:    to.Ptr("/home/" + adminUsername + "/.ssh/authorized_keys"),
				KeyData: to.Ptr(key),
			}}},
		}
		if !launch.Boot.PullFromAPI {
			script, err := bootscript.Linux(launch.Boot, m.Arch)
			if err != nil {
				return armcompute.VirtualMachine{}, err
			}
			if len(script) > maxCustomDataBytes {
				return armcompute.VirtualMachine{}, fmt.Errorf("boot script is %d bytes, over Azure's %d byte custom data limit", len(script), maxCustomDataBytes)
			}
			profile.CustomData = to.Ptr(base64.StdEncoding.EncodeToString([]byte(script)))
		}
	}
	vm.Properties.OSProfile = profile

	return vm, nil
}

// sshPublicKey is the key Linux images are provisioned with, which Azure
// requires. Its private half is generated once per process and never
// written anywhere: nobody logs in to a runner.
func (p *Provider) sshPublicKey() (string, error) {
	p.sshOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			p.sshErr = fmt.Errorf("generate ssh key: %w", err)

			return
		}
		public, err := ssh.NewPublicKey(&key.PublicKey)
		if err != nil {
			p.sshErr = fmt.Errorf("encode ssh key: %w", err)

			return
		}
		p.sshKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
	})

	return p.sshKey, p.sshErr
}

// randomPassword returns 24 alphanumeric characters with the upper and
// lower case letters and digits Windows requires.
func randomPassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for {
		out := make([]byte, 24)
		var upper, lower, digit bool
		for i := range out {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return "", fmt.Errorf("random password: %w", err)
			}
			out[i] = alphabet[n.Int64()]
			switch {
			case out[i] >= 'A' && out[i] <= 'Z':
				upper = true
			case out[i] >= 'a' && out[i] <= 'z':
				lower = true
			default:
				digit = true
			}
		}
		if upper && lower && digit {
			return string(out), nil
		}
	}
}

// ErrNotSupported marks config the provider refuses rather than ignores.
var ErrNotSupported = errors.New("not supported")
