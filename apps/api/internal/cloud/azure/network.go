package azure

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v3"
)

const (
	// The address space of every per-region network. Runners never talk to
	// each other or to the customer's other networks, so overlap with them
	// does not matter.
	networkAddressSpace = "10.0.0.0/16"
	// natIdleTimeoutMinutes is the NAT gateway's TCP idle timeout, raised
	// from the 4-minute default for long quiet connections to GitHub.
	natIdleTimeoutMinutes = 10
	// orphanAge is how old an interface or address with no machine must be
	// before the sweep removes it: longer than any launch in flight.
	orphanAge = 15 * time.Minute
	// sweepInterval bounds how often one scope is swept.
	sweepInterval = 5 * time.Minute
)

// managedTags mark the shared infrastructure (resource group, networks,
// NAT) as the provider's. They carry no installation: two installations
// that share a subscription share the networks.
func managedTags() map[string]*string {
	return map[string]*string{TagManaged: to.Ptr("true")}
}

// ensureResourceGroup creates the provider's resource group in the
// subscription if it does not exist. A resource group has a location of
// its own (for its metadata); the first region launched in is as good as
// any, and resources of every region go in it.
func (p *Provider) ensureResourceGroup(ctx context.Context, c *Clients, subscription, region string) error {
	key := subscription
	if _, ok := p.cacheGetSetup(key); ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	_, err := c.ResourceGroups.Get(ctx, ResourceGroup, nil)
	switch {
	case err == nil:
		p.cachePutSetup(key)

		return nil
	case !isNotFound(err):
		return fmt.Errorf("get resource group %s: %w", ResourceGroup, err)
	}
	_, err = c.ResourceGroups.CreateOrUpdate(ctx, ResourceGroup, armresources.ResourceGroup{Location: to.Ptr(region), Tags: managedTags()}, nil)
	if err != nil {
		return fmt.Errorf("create resource group %s: %w", ResourceGroup, err)
	}
	p.cachePutSetup(key)

	return nil
}

// ensureNetwork makes sure the region has the provider's virtual network,
// with its subnet and security group, and a NAT gateway on the subnet when
// runners launch without a public address. It returns the subnet's ID.
//
// Creation is serialized within the process; across replicas ARM's PUTs
// are idempotent upserts, and a PUT that collides with another in flight
// fails as retryable (AnotherOperationInProgress), which the launch
// retries.
func (p *Provider) ensureNetwork(ctx context.Context, c *Clients, subscription, region string, nat bool) (string, error) {
	key := subscription + "/" + region + "/" + strconv.FormatBool(nat)
	if _, ok := p.cacheGetSetup(key); ok {
		return subnetID(subscription, region), nil
	}
	p.setupMu.Lock()
	defer p.setupMu.Unlock()
	if _, ok := p.cacheGetSetup(key); ok {
		return subnetID(subscription, region), nil
	}
	if err := p.ensureVirtualNetwork(ctx, c, region); err != nil {
		return "", err
	}
	if nat {
		if err := p.ensureNAT(ctx, c, subscription, region); err != nil {
			return "", err
		}
	}
	p.cachePutSetup(key)

	return subnetID(subscription, region), nil
}

func subnetID(subscription, region string) string {
	return resourceID(subscription, "Microsoft.Network", "virtualNetworks", networkName(region)) + "/subnets/" + subnetName
}

func (p *Provider) ensureVirtualNetwork(ctx context.Context, c *Clients, region string) error {
	name := networkName(region)
	getCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	vnet, err := c.VNets.Get(getCtx, ResourceGroup, name, nil)
	cancel()
	switch {
	case err == nil:
		return checkNetwork(vnet.VirtualNetwork, region)
	case !isNotFound(err):
		return fmt.Errorf("get virtual network %s: %w", name, err)
	}

	// The security group first: the subnet references it. No rules of
	// Airship's own: the default rules deny inbound traffic from the
	// internet and allow all outbound, which is what a runner needs. The
	// group is where customers add rules of their own.
	nsgPoller, err := c.NSGs.BeginCreateOrUpdate(ctx, ResourceGroup, name, armnetwork.SecurityGroup{
		Location: to.Ptr(region),
		Tags:     managedTags(),
	}, nil)
	if err != nil {
		return fmt.Errorf("create security group %s: %w", name, err)
	}
	nsg, err := pollUntilDone(ctx, nsgPoller, operationTimeout)
	if err != nil {
		return fmt.Errorf("create security group %s: %w", name, err)
	}
	poller, err := c.VNets.BeginCreateOrUpdate(ctx, ResourceGroup, name, armnetwork.VirtualNetwork{
		Location: to.Ptr(region),
		Tags:     managedTags(),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr(networkAddressSpace)}},
			Subnets: []*armnetwork.Subnet{{
				Name: to.Ptr(subnetName),
				Properties: &armnetwork.SubnetPropertiesFormat{
					AddressPrefix:        to.Ptr(networkAddressSpace),
					NetworkSecurityGroup: &armnetwork.SecurityGroup{ID: nsg.ID},
					// Egress is always explicit: a public address per
					// machine or the NAT gateway. Azure retired the implicit
					// default outbound access for new networks anyway.
					DefaultOutboundAccess: to.Ptr(false),
				},
			}},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("create virtual network %s: %w", name, err)
	}
	if _, err := pollUntilDone(ctx, poller, operationTimeout); err != nil {
		return fmt.Errorf("create virtual network %s: %w", name, err)
	}

	return nil
}

// checkNetwork accepts an existing network with Airship's name only if it
// is the provider's own, with the subnet runners attach to; anything else
// would leave runners on a network nobody meant for them.
func checkNetwork(vnet armnetwork.VirtualNetwork, region string) error {
	name := networkName(region)
	if vnet.Tags[TagManaged] == nil {
		return fmt.Errorf("virtual network %s in %s exists but was not created by Airship; rename it so Airship can make its own", name, ResourceGroup)
	}
	if vnet.Properties != nil {
		for _, subnet := range vnet.Properties.Subnets {
			if subnet != nil && subnet.Name != nil && *subnet.Name == subnetName {
				return nil
			}
		}
	}

	return fmt.Errorf("virtual network %s in %s has no %s subnet; delete the network so Airship can make it again", name, ResourceGroup, subnetName)
}

// ensureNAT gives the region's subnet a NAT gateway with a public address
// of its own, so runners without a public address still reach GitHub.
func (p *Provider) ensureNAT(ctx context.Context, c *Clients, subscription, region string) error {
	name := natName(region)
	getCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	_, err := c.NATs.Get(getCtx, ResourceGroup, name, nil)
	cancel()
	switch {
	case err == nil:
	case !isNotFound(err):
		return fmt.Errorf("get NAT gateway %s: %w", name, err)
	default:
		ipPoller, err := c.PublicIPs.BeginCreateOrUpdate(ctx, ResourceGroup, name+ipSuffix, armnetwork.PublicIPAddress{
			Location: to.Ptr(region),
			Tags:     managedTags(),
			SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
				PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
			},
		}, nil)
		if err != nil {
			return fmt.Errorf("create NAT address %s: %w", name+ipSuffix, err)
		}
		ip, err := pollUntilDone(ctx, ipPoller, operationTimeout)
		if err != nil {
			return fmt.Errorf("create NAT address %s: %w", name+ipSuffix, err)
		}
		natPoller, err := c.NATs.BeginCreateOrUpdate(ctx, ResourceGroup, name, armnetwork.NatGateway{
			Location: to.Ptr(region),
			Tags:     managedTags(),
			SKU:      &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
			Properties: &armnetwork.NatGatewayPropertiesFormat{
				PublicIPAddresses:    []*armnetwork.SubResource{{ID: ip.ID}},
				IdleTimeoutInMinutes: to.Ptr[int32](natIdleTimeoutMinutes),
			},
		}, nil)
		if err != nil {
			return fmt.Errorf("create NAT gateway %s: %w", name, err)
		}
		if _, err := pollUntilDone(ctx, natPoller, operationTimeout); err != nil {
			return fmt.Errorf("create NAT gateway %s: %w", name, err)
		}
	}

	// The subnet may predate the NAT (made by a launch with a public
	// address); attaching a NAT gateway to a subnet in use is allowed.
	getCtx, cancel = context.WithTimeout(ctx, requestTimeout)
	subnet, err := c.Subnets.Get(getCtx, ResourceGroup, networkName(region), subnetName, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("get subnet %s: %w", subnetName, err)
	}
	if subnet.Properties != nil && subnet.Properties.NatGateway != nil && subnet.Properties.NatGateway.ID != nil {
		return nil
	}
	if subnet.Properties == nil {
		subnet.Properties = &armnetwork.SubnetPropertiesFormat{}
	}
	subnet.Properties.NatGateway = &armnetwork.SubResource{ID: to.Ptr(resourceID(subscription, "Microsoft.Network", "natGateways", name))}
	poller, err := c.Subnets.BeginCreateOrUpdate(ctx, ResourceGroup, networkName(region), subnetName, subnet.Subnet, nil)
	if err != nil {
		return fmt.Errorf("attach NAT gateway %s to subnet: %w", name, err)
	}
	if _, err := pollUntilDone(ctx, poller, operationTimeout); err != nil {
		return fmt.Errorf("attach NAT gateway %s to subnet: %w", name, err)
	}

	return nil
}

// createAddress makes a machine's public address, deleted with the
// machine. The PUT is an upsert: a repeated launch finds it as it is.
func (p *Provider) createAddress(ctx context.Context, c *Clients, region, name string, tags map[string]*string) (string, error) {
	poller, err := c.PublicIPs.BeginCreateOrUpdate(ctx, ResourceGroup, name, armnetwork.PublicIPAddress{
		Location: to.Ptr(region),
		Tags:     tags,
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
			DeleteOption:             to.Ptr(armnetwork.DeleteOptionsDelete),
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create public address %s: %w", name, err)
	}
	ip, err := pollUntilDone(ctx, poller, operationTimeout)
	if err != nil {
		return "", fmt.Errorf("create public address %s: %w", name, err)
	}
	if ip.ID == nil {
		return "", fmt.Errorf("create public address %s: no ID in the answer", name)
	}

	return *ip.ID, nil
}

// createInterface makes a machine's network interface on the region's
// subnet, with its public address when it has one.
func (p *Provider) createInterface(ctx context.Context, c *Clients, region, name, subnet, publicIP string, tags map[string]*string) (string, error) {
	ipConfig := &armnetwork.InterfaceIPConfigurationPropertiesFormat{
		Primary:                   to.Ptr(true),
		PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
		PrivateIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
		Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnet)},
	}
	if publicIP != "" {
		ipConfig.PublicIPAddress = &armnetwork.PublicIPAddress{
			ID:         to.Ptr(publicIP),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{DeleteOption: to.Ptr(armnetwork.DeleteOptionsDelete)},
		}
	}
	poller, err := c.NICs.BeginCreateOrUpdate(ctx, ResourceGroup, name, armnetwork.Interface{
		Location: to.Ptr(region),
		Tags:     tags,
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{Name: to.Ptr("primary"), Properties: ipConfig}},
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create network interface %s: %w", name, err)
	}
	nic, err := pollUntilDone(ctx, poller, operationTimeout)
	if err != nil {
		return "", fmt.Errorf("create network interface %s: %w", name, err)
	}
	if nic.ID == nil {
		return "", fmt.Errorf("create network interface %s: no ID in the answer", name)
	}

	return *nic.ID, nil
}

// removeInterface and removeAddress delete a machine's interface and
// address after a launch that never made the machine, without waiting:
// the sweep catches what fails here.
func (p *Provider) removeInterface(ctx context.Context, c *Clients, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	if _, err := c.NICs.BeginDelete(ctx, ResourceGroup, name, nil); err != nil && !isNotFound(err) {
		p.log.WarnContext(ctx, "delete network interface of a failed launch", slog.String("interface", name), slog.Any("error", err))
	}
}

func (p *Provider) removeAddress(ctx context.Context, c *Clients, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	if _, err := c.PublicIPs.BeginDelete(ctx, ResourceGroup, name, nil); err != nil && !isNotFound(err) {
		p.log.WarnContext(ctx, "delete public address of a failed launch", slog.String("address", name), slog.Any("error", err))
	}
}

// Sweep implements cloud.Sweeper: it deletes the installation's network
// interfaces and public addresses in the region that no machine uses and
// that are older than any launch could still be. They are left behind
// when a launch made them and then could not make the machine (a crash,
// or a creation Azure refused outright); a machine's own deletion takes
// its interface and address with it.
func (p *Provider) Sweep(ctx context.Context, raw []byte, region string, installationID int64) error {
	secret, err := ParseSecret(raw)
	if err != nil {
		return err
	}
	if !IsRegion(region) {
		return fmt.Errorf("%q is not an Azure region", region)
	}
	key := secret.SubscriptionID + "/" + region + "/" + strconv.FormatInt(installationID, 10)
	if !p.sweepDue(key) {
		return nil
	}
	c, err := p.clientsFor(ctx, raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	installation := strconv.FormatInt(installationID, 10)
	cutoff := time.Now().Add(-orphanAge)
	orphaned := func(tags map[string]*string, location *string) bool {
		if location == nil || *location != region || tags[TagInstallation] == nil || *tags[TagInstallation] != installation {
			return false
		}
		created, err := time.Parse(time.RFC3339, deref(tags[TagCreated]))

		return err == nil && created.Before(cutoff)
	}

	// Interfaces first: an address attached to an interface cannot be
	// deleted until the interface is.
	nics := c.NICs.NewListPager(ResourceGroup, nil)
	for nics.More() {
		page, err := nics.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list network interfaces: %w", err)
		}
		for _, nic := range page.Value {
			if nic == nil || nic.Name == nil || !orphaned(nic.Tags, nic.Location) || nic.Properties == nil || nic.Properties.VirtualMachine != nil {
				continue
			}
			if _, err := c.NICs.BeginDelete(ctx, ResourceGroup, *nic.Name, nil); err != nil && !isNotFound(err) {
				p.log.WarnContext(ctx, "delete orphaned network interface", slog.String("interface", *nic.Name), slog.Any("error", err))

				continue
			}
			p.log.InfoContext(ctx, "deleted orphaned network interface", slog.String("interface", *nic.Name), slog.String("region", region))
		}
	}
	ips := c.PublicIPs.NewListPager(ResourceGroup, nil)
	for ips.More() {
		page, err := ips.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list public addresses: %w", err)
		}
		for _, ip := range page.Value {
			if ip == nil || ip.Name == nil || !orphaned(ip.Tags, ip.Location) || ip.Properties == nil || ip.Properties.IPConfiguration != nil || ip.Properties.NatGateway != nil {
				continue
			}
			if _, err := c.PublicIPs.BeginDelete(ctx, ResourceGroup, *ip.Name, nil); err != nil && !isNotFound(err) {
				p.log.WarnContext(ctx, "delete orphaned public address", slog.String("address", *ip.Name), slog.Any("error", err))

				continue
			}
			p.log.InfoContext(ctx, "deleted orphaned public address", slog.String("address", *ip.Name), slog.String("region", region))
		}
	}

	return nil
}

// sweepDue reports whether a scope's sweep is due, and marks it done.
func (p *Provider) sweepDue(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if last, ok := p.sweeps[key]; ok && time.Since(last) < sweepInterval {
		return false
	}
	if len(p.sweeps) >= cacheLimit {
		clear(p.sweeps)
	}
	p.sweeps[key] = time.Now()

	return true
}

// pollUntilDone waits for a long-running operation, for at most timeout
// or until ctx ends, and returns its result.
func pollUntilDone[T any](ctx context.Context, poller *runtime.Poller[T], timeout time.Duration) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: pollFrequency}) //nolint:wrapcheck // callers add the operation
}

func (p *Provider) cacheGetSetup(key string) (struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.setups[key]
	if !ok || time.Since(entry.at) > cacheTTL {
		return struct{}{}, false
	}

	return struct{}{}, true
}

func (p *Provider) cachePutSetup(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.setups) >= cacheLimit {
		clear(p.setups)
	}
	p.setups[key] = cached[struct{}]{at: time.Now()}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
