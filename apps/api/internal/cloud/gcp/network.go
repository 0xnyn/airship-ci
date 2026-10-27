package gcp

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	// defaultNetwork is where runners launch when a definition names no
	// network interface: the project's default VPC.
	defaultNetwork = "global/networks/default"
	// NATName names the Cloud Router and NAT the provider creates in a
	// region for runners launched without a public address on the default
	// network, as Cirun does. Runners on another network get their own,
	// named after it (see natName): a NAT serves one network.
	NATName = "airship-nat"
)

// natName is the router and NAT name for a network in a region.
func natName(network string) string {
	short := network[strings.LastIndex(network, "/")+1:]
	if short == "default" {
		return NATName
	}
	name := NATName + "-" + short
	if len(name) > 63 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(short))
		name = fmt.Sprintf("%s-%08x", name[:54], h.Sum32())
	}

	return name
}

// zonesFor lists the zones a launch may use: the pinned zone, or every
// zone of the region as Compute Engine lists them.
func (p *Provider) zonesFor(ctx context.Context, c *Clients, scope Scope, pinned string) ([]string, error) {
	if pinned != "" {
		if RegionOf(pinned) != scope.Region {
			return nil, fmt.Errorf("zone %s is not in region %s", pinned, scope.Region)
		}

		return []string{pinned}, nil
	}
	key := scope.Project + "/" + scope.Region
	if zones, ok := p.cacheGetZones(key); ok {
		return zones, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	region, err := c.Regions.Get(ctx, &computepb.GetRegionRequest{Project: scope.Project, Region: scope.Region})
	if isNotFound(err) {
		return nil, fmt.Errorf("region %s does not exist in project %s", scope.Region, scope.Project)
	}
	if err != nil {
		return nil, fmt.Errorf("get region %s: %w", scope.Region, err)
	}
	var zones []string
	for _, link := range region.GetZones() {
		zone := link[strings.LastIndex(link, "/")+1:]
		if IsZone(zone) {
			zones = append(zones, zone)
		}
	}
	if len(zones) == 0 {
		return nil, fmt.Errorf("region %s lists no zones", scope.Region)
	}
	slices.Sort(zones)
	p.cachePutZones(key, zones)

	return zones, nil
}

// networkOf is the network runners launch on, which is where their NAT
// must be: the first interface's network, the network of its subnetwork
// when the definition names only that (Compute Engine infers the network
// from the subnetwork, so configs commonly leave it out), or the default.
func (p *Provider) networkOf(ctx context.Context, c *Clients, scope Scope, opts *cloud.GCPOptions) (string, error) {
	if len(opts.NetworkInterfaces) == 0 {
		return defaultNetwork, nil
	}
	nic := opts.NetworkInterfaces[0]
	if nic.Network != "" {
		return nic.Network, nil
	}
	if nic.Subnetwork == "" {
		return defaultNetwork, nil
	}
	project, region, name, err := parseSubnetwork(nic.Subnetwork, scope)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	subnet, err := c.Subnetworks.Get(ctx, &computepb.GetSubnetworkRequest{Project: project, Region: region, Subnetwork: name})
	if err != nil {
		return "", fmt.Errorf("get subnetwork %s: %w", nic.Subnetwork, err)
	}
	if subnet.GetNetwork() == "" {
		return "", fmt.Errorf("subnetwork %s names no network", nic.Subnetwork)
	}

	return subnet.GetNetwork(), nil
}

var subnetworkPattern = regexp.MustCompile(`^(?:https://(?:www|compute)\.googleapis\.com/compute/(?:v1|beta|alpha)/)?(?:projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/)?regions/([a-z]+-[a-z]+[0-9]+)/subnetworks/([a-z]([-a-z0-9]{0,61}[a-z0-9])?)$`)

// parseSubnetwork reads a subnetwork reference, a regions/R/subnetworks/N
// path with or without a project or self-link prefix.
func parseSubnetwork(ref string, scope Scope) (project, region, name string, err error) {
	m := subnetworkPattern.FindStringSubmatch(ref)
	if m == nil {
		return "", "", "", fmt.Errorf("%q is not a subnetwork reference (projects/P/regions/R/subnetworks/N)", ref)
	}

	return cmp.Or(m[1], scope.Project), m[2], m[3], nil
}

// ensureNAT makes sure the region has a Cloud Router with a NAT for
// network, so runners without a public address still reach GitHub. It is
// created once; a concurrent creation by another launch is fine.
func (p *Provider) ensureNAT(ctx context.Context, c *Clients, scope Scope, network string) error {
	key := scope.Project + "/" + scope.Region + "/" + network
	if _, ok := p.cacheGetNAT(key); ok {
		return nil
	}
	name := natName(network)
	getCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	router, err := c.Routers.Get(getCtx, &computepb.GetRouterRequest{Project: scope.Project, Region: scope.Region, Router: name})
	cancel()
	switch {
	case err == nil:
		if err := checkRouter(router, scope.Region, network); err != nil {
			return err
		}
		p.cachePutNAT(key)

		return nil
	case !isNotFound(err):
		return fmt.Errorf("get router %s in %s: %w", name, scope.Region, err)
	}

	insertCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	op, err := c.Routers.Insert(insertCtx, &computepb.InsertRouterRequest{
		Project: scope.Project,
		Region:  scope.Region,
		// The same request from a concurrent launch returns this operation.
		RequestId: proto.String(uuid.NewSHA1(requestIDNamespace, []byte("nat/"+key)).String()),
		RouterResource: &computepb.Router{
			Name:        proto.String(name),
			Description: proto.String("Airship: egress for GitHub Actions runners without a public address"),
			Network:     proto.String(network),
			Nats: []*computepb.RouterNat{{
				Name:                          proto.String(name),
				NatIpAllocateOption:           proto.String(computepb.RouterNat_AUTO_ONLY.String()),
				SourceSubnetworkIpRangesToNat: proto.String(computepb.RouterNat_ALL_SUBNETWORKS_ALL_IP_RANGES.String()),
			}},
		},
	})
	if isConflict(err) {
		// Something made a router by this name between the read and the
		// insert: another launch of ours, usually, but it is read back
		// rather than assumed, as in the first read.
		getCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		router, err := c.Routers.Get(getCtx, &computepb.GetRouterRequest{Project: scope.Project, Region: scope.Region, Router: name})
		cancel()
		if err != nil {
			return fmt.Errorf("get router %s in %s after a conflicting create: %w", name, scope.Region, err)
		}
		if err := checkRouter(router, scope.Region, network); err != nil {
			return err
		}
		p.cachePutNAT(key)

		return nil
	}
	if err != nil {
		return fmt.Errorf("create router %s in %s: %w", name, scope.Region, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := op.Wait(waitCtx); err != nil {
		if codes := operationErrorCodes(op.Proto()); len(codes) > 0 {
			return &operationError{codes: codes, message: fmt.Sprintf("create router %s in %s: %s: %s", name, scope.Region, strings.Join(codes, ","), operationMessage(op.Proto()))}
		}

		return fmt.Errorf("wait for router %s in %s: %w", name, scope.Region, err)
	}
	p.cachePutNAT(key)

	return nil
}

// checkRouter accepts an existing router with Airship's name only if it
// is on the runner's network and carries a NAT; anything else would leave
// runners without egress while looking configured.
func checkRouter(router *computepb.Router, region, network string) error {
	if !sameNetwork(router.GetNetwork(), network) {
		return fmt.Errorf("router %s in %s is on %s, not %s; move or rename it so Airship can make a NAT for this network", router.GetName(), region, router.GetNetwork(), network)
	}
	if len(router.GetNats()) == 0 {
		return fmt.Errorf("router %s in %s has no NAT; add one or delete the router so Airship can make it", router.GetName(), region)
	}

	return nil
}

// sameNetwork compares network references that may be spelled as a self
// link, a projects/ path or global/networks/NAME.
func sameNetwork(a, b string) bool {
	return a[strings.LastIndex(a, "/")+1:] == b[strings.LastIndex(b, "/")+1:]
}

func (p *Provider) cacheGetZones(key string) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.zones[key]
	if !ok || time.Since(entry.at) > imageCacheTTL {
		return nil, false
	}

	return entry.value, true
}

func (p *Provider) cachePutZones(key string, zones []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.zones) >= cacheLimit {
		clear(p.zones)
	}
	p.zones[key] = cached[[]string]{value: zones, at: time.Now()}
}

func (p *Provider) cacheGetNAT(key string) (struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.nats[key]
	if !ok || time.Since(entry.at) > cacheTTL {
		return struct{}{}, false
	}

	return struct{}{}, true
}

func (p *Provider) cachePutNAT(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.nats) >= cacheLimit {
		clear(p.nats)
	}
	p.nats[key] = cached[struct{}]{at: time.Now()}
}
