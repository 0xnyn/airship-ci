package runnerconfig

import (
	"regexp"
	"strings"
)

// Azure is what an Azure definition's extra_config asks for beyond the
// variants: virtual machine settings the provider applies to every
// variant.
type Azure struct {
	// NoPublicIP launches without a public address, behind a NAT gateway
	// (enable_public_ip: false).
	NoPublicIP bool
}

// Azure names, as its API spells them.
var (
	// A region: eastus, westeurope, germanywestcentral.
	azureRegionPattern = regexp.MustCompile(`^[a-z][a-z0-9]{2,39}$`)
	// A VM size: Standard_D4s_v5, Standard_NC6s_v3, Standard_D2pls_v5,
	// Basic_A1.
	azureSizePattern = regexp.MustCompile(`^(Standard|Basic)_[A-Z]{1,3}[A-Z0-9]*[0-9]+(-[0-9]+)?[a-z]*(_[A-Za-z0-9]+)*$`)
	// The vCPU count and the feature letters after it (D2pls: p, l, s);
	// "p" marks an Arm (Ampere, Cobalt) processor.
	azureFeaturesPattern = regexp.MustCompile(`^(?:Standard|Basic)_[A-Z]+[0-9]+(?:-[0-9]+)?([a-z]*)`)
	// Sizes with accelerators: the NC, ND, NV and NG families.
	azureGPUPattern = regexp.MustCompile(`^Standard_N[CDVG]`)
	azureImageSeg   = `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`
	// An image: a marketplace URN (publisher:offer:sku:version), a
	// managed image ID or a gallery image (version) ID, as Cirun takes
	// them, or an alias.
	azureURNPattern     = regexp.MustCompile(`^` + azureImageSeg + `:` + azureImageSeg + `:` + azureImageSeg + `:(latest|[0-9]+\.[0-9]+\.[0-9]+)$`)
	azureManagedPattern = regexp.MustCompile(`(?i)^/subscriptions/[0-9a-f-]{36}/resourceGroups/[A-Za-z0-9._()-]{1,90}/providers/Microsoft\.Compute/images/` + azureImageSeg + `$`)
	azureGalleryPattern = regexp.MustCompile(`(?i)^/subscriptions/[0-9a-f-]{36}/resourceGroups/[A-Za-z0-9._()-]{1,90}/providers/Microsoft\.Compute/galleries/` + azureImageSeg + `/images/` + azureImageSeg + `(/versions/[0-9]+\.[0-9]+\.[0-9]+)?$`)
	// Managed disk kinds.
	azureDiskTypes = []string{"Standard_LRS", "StandardSSD_LRS", "Premium_LRS", "StandardSSD_ZRS", "Premium_ZRS", "PremiumV2_LRS", "UltraSSD_LRS"}
)

const (
	minAzureDiskGB = 30
	maxAzureDiskGB = 4095
)

// azureOnlyKeys name the extra_config keys only Azure understands;
// enable_public_ip is shared with GCP.
var azureOnlyKeys = []string{"storageProfile"}

// rawAzureStorageProfile mirrors Cirun's extra_config.storageProfile: the
// OS disk's size and kind, as the API spells them.
type rawAzureStorageProfile struct {
	OSDisk *rawAzureOSDisk `yaml:"osDisk"`
}

type rawAzureOSDisk struct {
	DiskSizeGB  int                  `yaml:"diskSizeGB"`
	ManagedDisk *rawAzureManagedDisk `yaml:"managedDisk"`
}

type rawAzureManagedDisk struct {
	StorageAccountType string `yaml:"storageAccountType"`
}

// applyAzure validates the Azure keys and fills out.Azure. It returns the
// OS disk; zero means the image's own size (Windows) or the provider's
// default (Linux).
func (e *rawExtra) applyAzure(out *Runner, disk Disk) (Disk, *fieldError) {
	azure := &Azure{}
	if e.EnablePublicIP != nil && !*e.EnablePublicIP {
		azure.NoPublicIP = true
	}
	if e.StorageProfile != nil && e.StorageProfile.OSDisk != nil {
		osDisk := e.StorageProfile.OSDisk
		if osDisk.DiskSizeGB != 0 && (osDisk.DiskSizeGB < minAzureDiskGB || osDisk.DiskSizeGB > maxAzureDiskGB) {
			return Disk{}, failf(".extra_config.storageProfile.osDisk.diskSizeGB", "diskSizeGB must be between %d and %d GB", minAzureDiskGB, maxAzureDiskGB)
		}
		disk.SizeGB = osDisk.DiskSizeGB
		if osDisk.ManagedDisk != nil && osDisk.ManagedDisk.StorageAccountType != "" {
			typ, ok := azureDiskType(osDisk.ManagedDisk.StorageAccountType)
			if !ok {
				return Disk{}, failf(".extra_config.storageProfile.osDisk.managedDisk.storageAccountType", "storageAccountType must be one of %s", strings.Join(azureDiskTypes, ", "))
			}
			disk.Type = typ
		}
	}
	if azure.NoPublicIP {
		out.Azure = azure
	}

	return disk, nil
}

// azureDiskType matches a storage account type without regard to case.
func azureDiskType(s string) (string, bool) {
	for _, typ := range azureDiskTypes {
		if strings.EqualFold(strings.TrimSpace(s), typ) {
			return typ, true
		}
	}

	return "", false
}

// validateAzureVariant checks an Azure variant's region, size and image.
func validateAzureVariant(v *Variant, index string) *fieldError {
	if v.Region != "" && !azureRegionPattern.MatchString(v.Region) {
		return failf(".region"+index, "%q is not an Azure region (such as eastus)", v.Region)
	}
	if len(v.InstanceType) > 64 || !azureSizePattern.MatchString(v.InstanceType) {
		return failf(".instance_type"+index, "%q is not an Azure VM size (such as Standard_D4s_v5)", v.InstanceType)
	}
	if v.Image != "" && !imageAliasPattern.MatchString(v.Image) && !IsAzureImage(v.Image) {
		return failf(".machine_image"+index, "%q must be an image URN (publisher:offer:sku:version), a managed image ID, a gallery image ID, ubuntu2204 or ubuntu2404", v.Image)
	}

	return nil
}

// IsAzureImage reports whether image is an Azure image reference: a
// marketplace URN, a managed image ID or a gallery image ID.
func IsAzureImage(image string) bool {
	return azureURNPattern.MatchString(image) || azureManagedPattern.MatchString(image) || azureGalleryPattern.MatchString(image)
}

// azureArch is x64 or arm64 by the size's feature letters.
func azureArch(size string) string {
	if m := azureFeaturesPattern.FindStringSubmatch(size); m != nil && strings.Contains(m[1], "p") {
		return "arm64"
	}

	return "x64"
}

// azureImageURN spells the mapping form of machine_image Cirun takes
// ({publisher, offer, sku, version}) as a URN; version defaults to latest.
func azureImageURN(fields map[string]string) (string, bool) {
	publisher, offer, sku, version := fields["publisher"], fields["offer"], fields["sku"], fields["version"]
	if publisher == "" || offer == "" || sku == "" {
		return "", false
	}
	if version == "" {
		version = "latest"
	}

	return publisher + ":" + offer + ":" + sku + ":" + version, true
}
