package gcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// Public image projects, by the prefix of their family names, so a plain
// family such as debian-12 is found where Google publishes it. Cirun's
// default for a bare name is ubuntu-os-cloud.
var publicImageProjects = []struct{ prefix, project string }{
	{"ubuntu-", "ubuntu-os-cloud"},
	{"debian-", "debian-cloud"},
	{"windows-", "windows-cloud"},
	{"cos-", "cos-cloud"},
	{"rocky-", "rocky-linux-cloud"},
	{"centos-", "centos-cloud"},
	{"rhel-", "rhel-cloud"},
	{"fedora-", "fedora-cloud"},
	{"sles-", "suse-cloud"},
}

const (
	defaultImageProject = "ubuntu-os-cloud"
	// DefaultImage is Cirun's default Ubuntu release, as a family name.
	DefaultImage = "ubuntu-2204-lts"
)

// aliases map the one-line image tokens shared with AWS to Compute Engine
// families, per architecture. Ubuntu 22.04's arm64 family has a different
// suffix from 24.04's.
var aliases = map[string]map[string]string{
	"ubuntu2204": {"x64": "ubuntu-2204-lts", "arm64": "ubuntu-2204-lts-arm64"},
	"ubuntu2404": {"x64": "ubuntu-2404-lts-amd64", "arm64": "ubuntu-2404-lts-arm64"},
}

var (
	selfLinkPattern  = regexp.MustCompile(`^https://(www|compute)\.googleapis\.com/compute/(v1|beta|alpha)/(projects/.*)$`)
	imagePathPattern = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/global/(images|machineImages)/([a-z]([-a-z0-9]{0,61}[a-z0-9])?)$`)
	scopedPattern    = regexp.MustCompile(`^([a-z][a-z0-9-]{4,28}[a-z0-9]):([a-z]([-a-z0-9]{0,61}[a-z0-9])?)$`)
)

// IsImageRef reports whether ref is spelled like something the provider
// can resolve: an alias, a family or image name, project:name, a
// projects/ path or a self link to an image or machine image.
func IsImageRef(ref string) bool {
	if _, ok := aliases[ref]; ok {
		return true
	}
	if m := selfLinkPattern.FindStringSubmatch(ref); m != nil {
		ref = m[3]
	}

	return namePattern.MatchString(ref) || imagePathPattern.MatchString(ref) || scopedPattern.MatchString(ref)
}

// resolvedImage is what a launch needs to know about its image.
type resolvedImage struct {
	// selfLink is the image's or machine image's full resource link.
	selfLink     string
	machineImage bool
	windows      bool
	// uefi reports the UEFI_COMPATIBLE guest OS feature, which Shielded
	// VM options need.
	uefi bool
	arch string
}

// resolveImage turns a machine's image reference into an image to boot
// from. Families resolve to their newest image. An image whose
// architecture is known and differs from the machine's is refused, since
// the launch would fail anyway.
func (p *Provider) resolveImage(ctx context.Context, c *Clients, project string, m cloud.Machine) (resolvedImage, error) {
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
	key := project + ":" + ref + ":" + m.Arch
	if image, ok := p.cacheGetImage(key); ok {
		return image, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	image, err := p.findImage(ctx, c, project, ref)
	if err != nil {
		return resolvedImage{}, err
	}
	if image.arch != "" && image.arch != m.Arch {
		return resolvedImage{}, fmt.Errorf("image %s is %s but %s is %s", ref, image.arch, m.InstanceType, m.Arch)
	}
	p.cachePutImage(key, image)

	return image, nil
}

// findImage tries the forms Cirun documents, in order:
//
//	projects/P/global/images/N, projects/P/global/machineImages/N, or a
//	self link to either: exactly that resource;
//	P:N: an image, a machine image or a family in project P;
//	N: an image or family in the credential's project, then a family or
//	image in the public project its prefix suggests.
func (p *Provider) findImage(ctx context.Context, c *Clients, project, ref string) (resolvedImage, error) {
	if m := selfLinkPattern.FindStringSubmatch(ref); m != nil {
		ref = m[3]
	}
	if m := imagePathPattern.FindStringSubmatch(ref); m != nil {
		if m[2] == "machineImages" {
			return p.machineImage(ctx, c, m[1], m[3])
		}

		return p.image(ctx, c, m[1], m[3])
	}
	if m := scopedPattern.FindStringSubmatch(ref); m != nil {
		return p.firstOf(ref,
			func() (resolvedImage, error) { return p.image(ctx, c, m[1], m[2]) },
			func() (resolvedImage, error) { return p.machineImage(ctx, c, m[1], m[2]) },
			func() (resolvedImage, error) { return p.family(ctx, c, m[1], m[2]) },
		)
	}
	if !namePattern.MatchString(ref) {
		return resolvedImage{}, fmt.Errorf("%q is not an image name, project:image or projects/ path", ref)
	}
	public := defaultImageProject
	for _, candidate := range publicImageProjects {
		if strings.HasPrefix(ref, candidate.prefix) {
			public = candidate.project

			break
		}
	}

	return p.firstOf(ref,
		func() (resolvedImage, error) { return p.image(ctx, c, project, ref) },
		func() (resolvedImage, error) { return p.family(ctx, c, project, ref) },
		func() (resolvedImage, error) { return p.family(ctx, c, public, ref) },
		func() (resolvedImage, error) { return p.image(ctx, c, public, ref) },
	)
}

// firstOf returns the first lookup that finds something. Only "not found"
// moves on to the next; any other failure is reported as is, so a flaky
// API never looks like a missing image.
func (p *Provider) firstOf(ref string, lookups ...func() (resolvedImage, error)) (resolvedImage, error) {
	for _, lookup := range lookups {
		image, err := lookup()
		if err == nil {
			return image, nil
		}
		if !isNotFound(err) {
			return resolvedImage{}, err
		}
	}

	return resolvedImage{}, fmt.Errorf("image %s not found: %w", ref, notFoundErr)
}

// notFoundErr stands in for the API's 404 once every lookup has failed.
var notFoundErr = &notFound{}

type notFound struct{}

func (*notFound) Error() string { return "no such image, image family or machine image" }

func (p *Provider) image(ctx context.Context, c *Clients, project, name string) (resolvedImage, error) {
	img, err := c.Images.Get(ctx, &computepb.GetImageRequest{Project: project, Image: name})
	if err != nil {
		return resolvedImage{}, fmt.Errorf("get image %s/%s: %w", project, name, err)
	}

	return fromImage(project, img), nil
}

func (p *Provider) family(ctx context.Context, c *Clients, project, family string) (resolvedImage, error) {
	img, err := c.Images.GetFromFamily(ctx, &computepb.GetFromFamilyImageRequest{Project: project, Family: family})
	if err != nil {
		return resolvedImage{}, fmt.Errorf("get image family %s/%s: %w", project, family, err)
	}

	return fromImage(project, img), nil
}

func (p *Provider) machineImage(ctx context.Context, c *Clients, project, name string) (resolvedImage, error) {
	img, err := c.MachineImages.Get(ctx, &computepb.GetMachineImageRequest{Project: project, MachineImage: name})
	if err != nil {
		return resolvedImage{}, fmt.Errorf("get machine image %s/%s: %w", project, name, err)
	}
	out := resolvedImage{selfLink: img.GetSelfLink(), machineImage: true, windows: strings.Contains(name, "windows")}
	if out.selfLink == "" {
		out.selfLink = "projects/" + project + "/global/machineImages/" + name
	}
	for _, disk := range img.GetInstanceProperties().GetDisks() {
		if disk.GetBoot() {
			out.windows = out.windows || hasFeature(disk.GetGuestOsFeatures(), computepb.GuestOsFeature_WINDOWS)
			out.uefi = hasFeature(disk.GetGuestOsFeatures(), computepb.GuestOsFeature_UEFI_COMPATIBLE)
			out.arch = archOf(disk.GetInitializeParams().GetArchitecture())
		}
	}

	return out, nil
}

// fromImage reads an image found in project. The self link names the
// project the image lives in, which matters when it is a public one: a
// bare global/images/N would be looked up in the launch project.
func fromImage(project string, img *computepb.Image) resolvedImage {
	out := resolvedImage{
		selfLink: img.GetSelfLink(),
		windows:  hasFeature(img.GetGuestOsFeatures(), computepb.GuestOsFeature_WINDOWS),
		uefi:     hasFeature(img.GetGuestOsFeatures(), computepb.GuestOsFeature_UEFI_COMPATIBLE),
		arch:     archOf(img.GetArchitecture()),
	}
	if out.selfLink == "" {
		out.selfLink = "projects/" + project + "/global/images/" + img.GetName()
	}

	return out
}

func hasFeature(features []*computepb.GuestOsFeature, want computepb.GuestOsFeature_Type) bool {
	for _, f := range features {
		if f.GetType() == want.String() {
			return true
		}
	}

	return false
}

// archOf maps the API's architecture names to Airship's.
func archOf(arch string) string {
	switch arch {
	case computepb.Image_X86_64.String():
		return "x64"
	case computepb.Image_ARM64.String():
		return "arm64"
	default:
		return ""
	}
}

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
