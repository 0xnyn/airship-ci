package azure

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v8"
)

// DefaultImage is Cirun's default Ubuntu release, as an alias.
const DefaultImage = "ubuntu2204"

// aliases map the one-line image tokens shared with the other clouds to
// marketplace image URNs, per architecture. Canonical publishes 22.04's
// arm64 build as its own SKU of the jammy offer, and 24.04's as a SKU of
// the ubuntu-24_04-lts offer.
var aliases = map[string]map[string]string{
	"ubuntu2204": {"x64": "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest", "arm64": "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-arm64:latest"},
	"ubuntu2404": {"x64": "Canonical:ubuntu-24_04-lts:server:latest", "arm64": "Canonical:ubuntu-24_04-lts:server-arm64:latest"},
}

const imageSegment = `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`

// The image forms Cirun documents: a marketplace URN, a managed image's
// resource ID and a compute gallery image's, with or without a version.
// Resource IDs compare without regard to case, as ARM does.
var (
	urnPattern          = regexp.MustCompile(`^(` + imageSegment + `):(` + imageSegment + `):(` + imageSegment + `):(latest|[0-9]+\.[0-9]+\.[0-9]+)$`)
	managedImagePattern = regexp.MustCompile(`(?i)^/subscriptions/([0-9a-f-]{36})/resourceGroups/([A-Za-z0-9._()-]{1,90})/providers/Microsoft\.Compute/images/(` + imageSegment + `)$`)
	galleryImagePattern = regexp.MustCompile(`(?i)^/subscriptions/([0-9a-f-]{36})/resourceGroups/([A-Za-z0-9._()-]{1,90})/providers/Microsoft\.Compute/galleries/(` + imageSegment + `)/images/(` + imageSegment + `)(?:/versions/([0-9]+\.[0-9]+\.[0-9]+))?$`)
)

// IsImageRef reports whether ref is spelled like something the provider
// can resolve.
func IsImageRef(ref string) bool {
	if _, ok := aliases[ref]; ok {
		return true
	}

	return urnPattern.MatchString(ref) || managedImagePattern.MatchString(ref) || galleryImagePattern.MatchString(ref)
}

// IsWindowsImageRef reports an image reference that names Windows, as the
// marketplace URNs for Windows do (MicrosoftWindowsServer:WindowsServer,
// MicrosoftWindowsDesktop:windows-11); the provider reads the OS from the
// image itself at launch.
func IsWindowsImageRef(ref string) bool {
	return strings.Contains(strings.ToLower(ref), "windows")
}

// resolvedImage is what a launch needs to know about its image.
type resolvedImage struct {
	// ref is the storage profile's image reference: a URN's parts or a
	// resource ID.
	ref armcompute.ImageReference
	// plan is the marketplace purchase plan of a third-party image, which
	// the machine must repeat.
	plan *armcompute.Plan
	// windows images boot through a Run Command; so do specialized ones,
	// which take no OS profile at all.
	windows     bool
	specialized bool
	// arch is x64 or arm64 when the image says, else empty.
	arch string
	// trustedLaunch asks for the Trusted Launch security type: required by
	// some images (Windows 11), supported by most generation 2 ones.
	trustedLaunch bool
}

// resolveImage turns a machine's image reference into an image to boot
// from. An image whose architecture is known and differs from the
// machine's is refused, since the launch would fail anyway.
func (p *Provider) resolveImage(ctx context.Context, c *Clients, subscription, region string, m cloud.Machine) (resolvedImage, error) {
	ref := m.Image
	if ref == "" {
		ref = DefaultImage
	}
	if byArch, ok := aliases[ref]; ok {
		ref = byArch[m.Arch]
		if ref == "" {
			ref = byArch["x64"]
		}
	}
	key := subscription + ":" + region + ":" + strings.ToLower(ref) + ":" + m.Arch
	if image, ok := p.cacheGetImage(key); ok {
		return image, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	image, err := p.findImage(ctx, c, subscription, region, ref)
	if err != nil {
		return resolvedImage{}, err
	}
	if image.arch != "" && image.arch != m.Arch {
		return resolvedImage{}, fmt.Errorf("image %s is %s but %s is %s", ref, image.arch, m.InstanceType, m.Arch)
	}
	p.cachePutImage(key, image)

	return image, nil
}

func (p *Provider) findImage(ctx context.Context, c *Clients, subscription, region, ref string) (resolvedImage, error) {
	if m := urnPattern.FindStringSubmatch(ref); m != nil {
		return p.platformImage(ctx, c, region, m[1], m[2], m[3], m[4])
	}
	if m := managedImagePattern.FindStringSubmatch(ref); m != nil {
		if !strings.EqualFold(m[1], subscription) {
			return resolvedImage{}, fmt.Errorf("image %s is in another subscription than the connected one", ref)
		}

		return p.managedImage(ctx, c, m[2], m[3])
	}
	if m := galleryImagePattern.FindStringSubmatch(ref); m != nil {
		if !strings.EqualFold(m[1], subscription) {
			return resolvedImage{}, fmt.Errorf("gallery image %s is in another subscription than the connected one", ref)
		}

		return p.galleryImage(ctx, c, m[2], m[3], m[4], m[5])
	}

	return resolvedImage{}, fmt.Errorf("%q is not an image URN (publisher:offer:sku:version), a managed image ID or a gallery image ID", ref)
}

// platformImage reads a marketplace image. "latest" is left for Azure to
// resolve at launch; the newest version is read here for the image's
// properties, which do not change between versions of one SKU.
func (p *Provider) platformImage(ctx context.Context, c *Clients, region, publisher, offer, sku, version string) (resolvedImage, error) {
	urn := publisher + ":" + offer + ":" + sku + ":" + version
	lookup := version
	if version == "latest" {
		list, err := c.VMImages.List(ctx, region, publisher, offer, sku, nil)
		if err != nil {
			if isNotFound(err) {
				return resolvedImage{}, fmt.Errorf("image %s not found in %s: %w", urn, region, errNotFound)
			}

			return resolvedImage{}, fmt.Errorf("list versions of %s in %s: %w", urn, region, err)
		}
		lookup = newestVersion(list.VirtualMachineImageResourceArray)
		if lookup == "" {
			return resolvedImage{}, fmt.Errorf("image %s has no versions in %s: %w", urn, region, errNotFound)
		}
	}
	img, err := c.VMImages.Get(ctx, region, publisher, offer, sku, lookup, nil)
	if err != nil {
		if isNotFound(err) {
			return resolvedImage{}, fmt.Errorf("image %s not found in %s: %w", urn, region, errNotFound)
		}

		return resolvedImage{}, fmt.Errorf("get image %s in %s: %w", urn, region, err)
	}
	props := img.Properties
	if props == nil {
		return resolvedImage{}, fmt.Errorf("image %s has no properties", urn)
	}
	if props.ImageDeprecationStatus != nil && props.ImageDeprecationStatus.ImageState != nil && *props.ImageDeprecationStatus.ImageState == armcompute.ImageStateDeprecated {
		return resolvedImage{}, fmt.Errorf("image %s is deprecated by its publisher", urn)
	}
	out := resolvedImage{
		ref: armcompute.ImageReference{Publisher: to.Ptr(publisher), Offer: to.Ptr(offer), SKU: to.Ptr(sku), Version: to.Ptr(version)},
	}
	if props.OSDiskImage != nil && props.OSDiskImage.OperatingSystem != nil {
		out.windows = *props.OSDiskImage.OperatingSystem == armcompute.OperatingSystemTypesWindows
	}
	if props.Architecture != nil {
		out.arch = archOf(string(*props.Architecture))
	}
	if props.Plan != nil {
		out.plan = &armcompute.Plan{Name: props.Plan.Name, Product: props.Plan.Product, Publisher: props.Plan.Publisher}
	}
	for _, f := range props.Features {
		if f != nil && f.Name != nil && f.Value != nil {
			out.trustedLaunch = out.trustedLaunch || wantsTrustedLaunch(*f.Name, *f.Value, out.windows)
		}
	}

	return out, nil
}

// managedImage reads an image the customer captured from a VM.
func (p *Provider) managedImage(ctx context.Context, c *Clients, resourceGroup, name string) (resolvedImage, error) {
	img, err := c.Images.Get(ctx, resourceGroup, name, nil)
	if err != nil {
		if isNotFound(err) {
			return resolvedImage{}, fmt.Errorf("managed image %s/%s not found: %w", resourceGroup, name, errNotFound)
		}

		return resolvedImage{}, fmt.Errorf("get managed image %s/%s: %w", resourceGroup, name, err)
	}
	out := resolvedImage{ref: armcompute.ImageReference{ID: img.ID}}
	if out.ref.ID == nil {
		return resolvedImage{}, fmt.Errorf("managed image %s/%s has no ID", resourceGroup, name)
	}
	if img.Properties != nil && img.Properties.StorageProfile != nil && img.Properties.StorageProfile.OSDisk != nil {
		disk := img.Properties.StorageProfile.OSDisk
		out.windows = disk.OSType != nil && *disk.OSType == armcompute.OperatingSystemTypesWindows
		out.specialized = disk.OSState != nil && *disk.OSState == armcompute.OperatingSystemStateTypesSpecialized
	}

	return out, nil
}

// galleryImage reads a compute gallery image definition, and the version
// when one is named; without one Azure launches the newest version.
func (p *Provider) galleryImage(ctx context.Context, c *Clients, resourceGroup, gallery, name, version string) (resolvedImage, error) {
	path := resourceGroup + "/" + gallery + "/" + name
	img, err := c.GalleryImages.Get(ctx, resourceGroup, gallery, name, nil)
	if err != nil {
		if isNotFound(err) {
			return resolvedImage{}, fmt.Errorf("gallery image %s not found: %w", path, errNotFound)
		}

		return resolvedImage{}, fmt.Errorf("get gallery image %s: %w", path, err)
	}
	out := resolvedImage{ref: armcompute.ImageReference{ID: img.ID}}
	if img.ID == nil {
		return resolvedImage{}, fmt.Errorf("gallery image %s has no ID", path)
	}
	if props := img.Properties; props != nil {
		out.windows = props.OSType != nil && *props.OSType == armcompute.OperatingSystemTypesWindows
		out.specialized = props.OSState != nil && *props.OSState == armcompute.OperatingSystemStateTypesSpecialized
		if props.Architecture != nil {
			out.arch = archOf(string(*props.Architecture))
		}
		if props.PurchasePlan != nil {
			out.plan = &armcompute.Plan{Name: props.PurchasePlan.Name, Product: props.PurchasePlan.Product, Publisher: props.PurchasePlan.Publisher}
		}
		for _, f := range props.Features {
			if f != nil && f.Name != nil && f.Value != nil {
				out.trustedLaunch = out.trustedLaunch || wantsTrustedLaunch(*f.Name, *f.Value, out.windows)
			}
		}
	}
	if version != "" {
		v, err := c.GalleryImageVersions.Get(ctx, resourceGroup, gallery, name, version, nil)
		if err != nil {
			if isNotFound(err) {
				return resolvedImage{}, fmt.Errorf("gallery image version %s/%s not found: %w", path, version, errNotFound)
			}

			return resolvedImage{}, fmt.Errorf("get gallery image version %s/%s: %w", path, version, err)
		}
		if v.ID == nil {
			return resolvedImage{}, fmt.Errorf("gallery image version %s/%s has no ID", path, version)
		}
		out.ref.ID = v.ID
	}

	return out, nil
}

// wantsTrustedLaunch decides the security type from an image's
// SecurityType feature. Images that require it (Windows 11) get it; so do
// Windows images that merely support it, since Windows is where the
// difference shows. Linux images stay on the standard type, which every VM
// size takes.
func wantsTrustedLaunch(feature, value string, windows bool) bool {
	if feature != "SecurityType" {
		return false
	}
	if value == "TrustedLaunch" {
		return true
	}

	return windows && strings.HasPrefix(value, "TrustedLaunch")
}

// newestVersion picks the highest Major.Minor.Build among image versions.
func newestVersion(versions []*armcompute.VirtualMachineImageResource) string {
	best, bestParts := "", []int(nil)
	for _, v := range versions {
		if v == nil || v.Name == nil {
			continue
		}
		parts := versionParts(*v.Name)
		if parts == nil {
			continue
		}
		if best == "" || compareVersions(parts, bestParts) > 0 {
			best, bestParts = *v.Name, parts
		}
	}

	return best
}

func versionParts(v string) []int {
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}

	return out
}

func compareVersions(a, b []int) int {
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x > y {
				return 1
			}

			return -1
		}
	}

	return 0
}

// archOf maps the API's architecture names to Airship's.
func archOf(arch string) string {
	switch strings.ToLower(arch) {
	case "x64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return ""
	}
}

// errNotFound stands in for the API's 404 once a lookup has failed.
var errNotFound = errors.New("no such image")

func (p *Provider) cacheGetImage(key string) (resolvedImage, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.images[key]
	if !ok || time.Since(entry.at) > imageCacheTTL {
		return resolvedImage{}, false
	}

	return entry.value, true
}

func (p *Provider) cachePutImage(key string, value resolvedImage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.images) >= cacheLimit {
		clear(p.images)
	}
	p.images[key] = cached[resolvedImage]{value: value, at: time.Now()}
}
