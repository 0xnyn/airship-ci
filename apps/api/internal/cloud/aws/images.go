package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// canonicalOwner is Canonical's AWS account, which publishes Ubuntu AMIs.
const canonicalOwner = "099720109477"

// DefaultImage is used when a definition names no machine_image.
const DefaultImage = "ubuntu2204"

// ubuntuAliases map one-line image tokens to Canonical's AMI name
// patterns. The storage segment changed between releases, so it is
// wildcarded. The architecture segment is Debian's name for it (amd64,
// arm64), not EC2's (x86_64, arm64); see ubuntuArch.
var ubuntuAliases = map[string]string{
	"ubuntu2204": "ubuntu/images/hvm-ssd*/ubuntu-jammy-22.04-%s-server-*",
	"ubuntu2404": "ubuntu/images/hvm-ssd*/ubuntu-noble-24.04-%s-server-*",
}

// IsImageAlias reports whether ref is a known image alias.
func IsImageAlias(ref string) bool {
	_, ok := ubuntuAliases[ref]

	return ok
}

// resolvedImage is what a launch needs to know about its AMI.
type resolvedImage struct {
	id         string
	rootDevice string
	windows    bool
}

// resolveImage turns a machine's image reference into an AMI ID with its
// root device name (the disk size override needs it) and platform (the
// boot script depends on it). An alias resolves to Canonical's newest
// image for the architecture.
func (p *Provider) resolveImage(ctx context.Context, client EC2, scope string, m cloud.Machine) (resolvedImage, error) {
	ref := m.Image
	if ref == "" {
		ref = DefaultImage
	}
	key := scope + ":" + ref + ":" + m.Arch
	if image, ok := p.cacheGetImage(key); ok {
		return image, nil
	}

	var images []ec2types.Image
	if pattern, isAlias := ubuntuAliases[ref]; isAlias {
		out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
			Owners: []string{canonicalOwner},
			Filters: []ec2types.Filter{
				{Name: awssdk.String("name"), Values: []string{fmt.Sprintf(pattern, ubuntuArch(m.Arch))}},
				{Name: awssdk.String("state"), Values: []string{"available"}},
				{Name: awssdk.String("architecture"), Values: []string{string(ec2Arch(m.Arch))}},
			},
		})
		if err != nil {
			return resolvedImage{}, fmt.Errorf("find %s image: %w", ref, err)
		}
		images = out.Images
		// Newest first.
		slices.SortFunc(images, func(a, b ec2types.Image) int {
			return strings.Compare(awssdk.ToString(b.CreationDate), awssdk.ToString(a.CreationDate))
		})
	} else {
		out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{ref}})
		if err != nil {
			return resolvedImage{}, fmt.Errorf("describe image %s: %w", ref, err)
		}
		images = out.Images
	}
	if len(images) == 0 || images[0].RootDeviceName == nil {
		return resolvedImage{}, fmt.Errorf("image %s not found in this region", ref)
	}
	found := images[0]
	if found.Architecture != "" && found.Architecture != ec2Arch(m.Arch) {
		return resolvedImage{}, fmt.Errorf("image %s is %s but %s is %s", ref, found.Architecture, m.InstanceType, ec2Arch(m.Arch))
	}
	image := resolvedImage{
		id:         awssdk.ToString(found.ImageId),
		rootDevice: awssdk.ToString(found.RootDeviceName),
		windows:    found.Platform == ec2types.PlatformValuesWindows || strings.Contains(awssdk.ToString(found.PlatformDetails), "Windows"),
	}
	p.cachePutImage(key, image)

	return image, nil
}

// ubuntuArch is the architecture as Canonical spells it in AMI names.
func ubuntuArch(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}

	return "amd64"
}

func ec2Arch(arch string) ec2types.ArchitectureValues {
	if arch == "arm64" {
		return ec2types.ArchitectureValuesArm64
	}

	return ec2types.ArchitectureValuesX8664
}
