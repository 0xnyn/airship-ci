package runnerconfig

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// GCP is what the GCP extra_config keys ask for: Compute Engine
// settings the provider applies to every variant of the runner.
type GCP struct {
	// Project overrides the credential's project (extra_config.project_id).
	Project string
	// AdditionalDisk is a second, empty disk attached at launch.
	AdditionalDisk *GCPDisk
	// NetworkInterfaces replace the default network and public address.
	NetworkInterfaces []GCPNetworkInterface
	// ServiceAccounts attach a service account to the machine.
	ServiceAccounts []GCPServiceAccount
	// NoPublicIP launches without an external address, behind a Cloud NAT
	// (enable_public_ip: false).
	NoPublicIP bool
	// OnHostMaintenance is MIGRATE or TERMINATE; empty means the provider's
	// default.
	OnHostMaintenance string
	// ExistingDisk is a disk of the customer's to attach; it is never
	// deleted with the machine.
	ExistingDisk *GCPExistingDisk
}

// GCPExistingDisk mirrors extra_config.existingDisk.
type GCPExistingDisk struct {
	// Source is projects/P/zones/Z/disks/N or projects/P/regions/R/disks/N.
	Source     string
	DeviceName string
	Mode       string
	Interface  string
	// Zone is the disk's zone when it is zonal; the launch is pinned to it.
	Zone string
	// Region is the disk's region, zonal or not.
	Region string
}

// GCPDisk is a disk size and type (pd-balanced, pd-ssd, a diskTypes path).
type GCPDisk struct {
	SizeGB int
	Type   string
}

// GCPNetworkInterface mirrors one entry of extra_config.networkInterfaces.
type GCPNetworkInterface struct {
	Network       string
	Subnetwork    string
	NicType       string
	StackType     string
	AccessConfigs []GCPAccessConfig
}

// GCPAccessConfig is an external address on a network interface.
type GCPAccessConfig struct {
	Type        string
	Name        string
	NetworkTier string
}

// GCPServiceAccount mirrors one entry of extra_config.serviceAccounts.
type GCPServiceAccount struct {
	Email  string
	Scopes []string
}

// Compute Engine resource names and references, as its API spells them.
var (
	// A region (us-central1) or a zone (us-central1-b).
	gcpLocationPattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+(-[a-z])?$`)
	gcpZonePattern     = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-[a-z]$`)
	// Machine types: n1-standard-1, e2-medium, a2-highgpu-1g, t2a-standard-4,
	// c3-standard-4-lssd, n2-custom-4-8192.
	gcpMachineTypePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)+$`)
	gcpProjectPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	gcpResourceName       = `[a-z]([-a-z0-9]{0,61}[a-z0-9])?`
	gcpNamePattern        = regexp.MustCompile(`^` + gcpResourceName + `$`)
	gcpSelfLinkPrefix     = `(https://(www|compute)\.googleapis\.com/compute/(v1|beta|alpha)/)?`
	// The image forms accepted: a name or family, project:name, a
	// projects/ path to an image or machine image, or a self link to one.
	gcpImagePattern = regexp.MustCompile(`^(` + gcpResourceName + `|[a-z][a-z0-9-]{4,28}[a-z0-9]:` + gcpResourceName +
		`|` + gcpSelfLinkPrefix + `projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/global/(images|machineImages)/` + gcpResourceName + `)$`)
	gcpGPUPattern        = regexp.MustCompile(`^nvidia-[a-z0-9]+(-[a-z0-9]+)*$`)
	gcpDiskTypePattern   = regexp.MustCompile(`^(` + gcpResourceName + `|` + gcpSelfLinkPrefix + `(projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/)?zones/[a-z]+-[a-z]+[0-9]+-[a-z]/diskTypes/` + gcpResourceName + `)$`)
	gcpNetworkPattern    = regexp.MustCompile(`^(` + gcpResourceName + `|` + gcpSelfLinkPrefix + `(projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/)?global/networks/` + gcpResourceName + `)$`)
	gcpSubnetworkPattern = regexp.MustCompile(`^` + gcpSelfLinkPrefix + `(projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/)?regions/[a-z]+-[a-z]+[0-9]+/subnetworks/` + gcpResourceName + `$`)
	gcpEmailPattern      = regexp.MustCompile(`^(default|[a-z0-9._-]+@[a-z0-9.-]+\.gserviceaccount\.com)$`)
	// A disk reference: the location kind, the zone or region, the name.
	gcpDiskSourcePattern = regexp.MustCompile(`^` + gcpSelfLinkPrefix + `(projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/)?(zones|regions)/([a-z]+-[a-z]+[0-9]+(-[a-z])?)/disks/` + gcpResourceName + `$`)
	gcpScopePattern      = regexp.MustCompile(`^https://www\.googleapis\.com/auth/[A-Za-z0-9._-]+$`)
	// Arm machine families: Axion (c4a, n4a) and Ampere Altra (t2a).
	gcpArmFamilies = map[string]bool{"t2a": true, "c4a": true, "n4a": true}
	// Families whose machine type includes the accelerators.
	gcpGPUFamilies = map[string]bool{"a2": true, "a3": true, "a4": true, "a4x": true, "g2": true, "g4": true}
)

const (
	maxGCPNetworkInterfaces = 8
	maxGCPScopes            = 20
	maxGCPDiskGB            = 65536
	minGCPBootDiskGB        = 10
)

// awsOnlyKeys and gcpOnlyKeys name the extra_config keys each cloud
// understands (azureOnlyKeys and daytonaOnlyKeys are in their files), so
// a key from another cloud is refused by name. enable_public_ip is GCP's
// and Azure's both.
var (
	awsOnlyKeys = []string{"BlockDeviceMappings", "SubnetId", "SecurityGroupIds", "IamInstanceProfile"}
	gcpOnlyKeys = []string{"baseDisk", "additionalDisk", "existingDisk", "project_id", "networkInterfaces", "serviceAccounts", "enable_public_ip", "scheduling"}
)

type rawGCPDisk struct {
	DiskSizeGb int    `yaml:"diskSizeGb"`
	DiskType   string `yaml:"diskType"`
}

type rawGCPExistingDisk struct {
	DeviceName string `yaml:"deviceName"`
	DiskSizeGb int    `yaml:"diskSizeGb"`
	Source     string `yaml:"source"`
	Type       string `yaml:"type"`
	Mode       string `yaml:"mode"`
	Interface  string `yaml:"interface"`
}

type rawGCPInterface struct {
	Network       string               `yaml:"network"`
	Subnetwork    string               `yaml:"subnetwork"`
	NicType       string               `yaml:"nicType"`
	StackType     string               `yaml:"stackType"`
	AccessConfigs []rawGCPAccessConfig `yaml:"accessConfigs"`
}

type rawGCPAccessConfig struct {
	Type        string `yaml:"type"`
	Name        string `yaml:"name"`
	NetworkTier string `yaml:"networkTier"`
}

type rawGCPServiceAccount struct {
	Email  string   `yaml:"email"`
	Scopes []string `yaml:"scopes"`
}

type rawGCPScheduling struct {
	OnHostMaintenance string `yaml:"onHostMaintenance"`
}

// keysSet lists which cloud-specific keys the file set.
func (e *rawExtra) keysSet() []string {
	var keys []string
	add := func(key string, set bool) {
		if set {
			keys = append(keys, key)
		}
	}
	add("BlockDeviceMappings", len(e.BlockDeviceMappings) > 0)
	add("SubnetId", e.SubnetID != "")
	add("SecurityGroupIds", len(e.SecurityGroupIDs) > 0)
	add("IamInstanceProfile", e.IamInstanceProfile != "")
	add("baseDisk", e.BaseDisk != nil)
	add("additionalDisk", e.AdditionalDisk != nil)
	add("existingDisk", e.ExistingDisk != nil)
	add("project_id", e.ProjectID != "")
	add("networkInterfaces", len(e.NetworkInterfaces) > 0)
	add("serviceAccounts", len(e.ServiceAccounts) > 0)
	add("enable_public_ip", e.EnablePublicIP != nil)
	add("scheduling", e.Scheduling != nil)
	add("storageProfile", e.StorageProfile != nil)
	add("disk", e.Disk != 0)
	add("volumes", len(e.Volumes) > 0)
	add("docker", e.Docker)
	add("network_allow_list", e.NetworkAllowList != "")
	add("domain_allow_list", e.DomainAllowList != "")

	return keys
}

// checkCloud refuses keys that belong to other clouds only.
func (e *rawExtra) checkCloud(cloud string) *fieldError {
	owners := []struct {
		cloud, name string
		keys        []string
	}{
		{CloudAWS, "an AWS", awsOnlyKeys},
		{CloudGCP, "a GCP", gcpOnlyKeys},
		{CloudAzure, "an Azure", append(slices.Clone(azureOnlyKeys), "enable_public_ip")},
		{CloudDaytona, "a Daytona", daytonaOnlyKeys},
		{CloudRunloop, "a Runloop", runloopOnlyKeys},
		{CloudE2B, "an E2B", dockerOnlyKeys},
		{CloudNovita, "a Novita", dockerOnlyKeys},
		{CloudLelantos, "a Lelantos", dockerOnlyKeys},
		{CloudNamespace, "a Namespace", dockerOnlyKeys},
		{CloudTenki, "a Tenki", tenkiOnlyKeys},
		{CloudModal, "a Modal", dockerOnlyKeys},
	}
	for _, key := range e.keysSet() {
		foreign := ""
		ours := false
		for _, owner := range owners {
			switch {
			case !slices.Contains(owner.keys, key):
			case owner.cloud == cloud:
				ours = true
			case foreign == "":
				foreign = owner.name
			}
		}
		if foreign != "" && !ours {
			return failf(".extra_config."+key, "%s is %s setting; this runner's cloud is %s", key, foreign, cloud)
		}
	}

	return nil
}

// applyGCP validates the GCP keys and fills out.GCP. It returns the boot
// disk.
func (e *rawExtra) applyGCP(out *Runner, disk Disk) (Disk, *fieldError) {
	gcp := &GCP{}
	if e.ExistingDisk != nil {
		disk, ferr := e.ExistingDisk.validate(".extra_config.existingDisk")
		if ferr != nil {
			return Disk{}, ferr
		}
		gcp.ExistingDisk = disk
	}
	if e.BaseDisk != nil {
		size, typ, ferr := e.BaseDisk.validate(".extra_config.baseDisk", minGCPBootDiskGB)
		if ferr != nil {
			return Disk{}, ferr
		}
		if size != 0 {
			disk.SizeGB = size
		}
		if typ != "" {
			disk.Type = typ
		}
	}
	if e.AdditionalDisk != nil {
		size, typ, ferr := e.AdditionalDisk.validate(".extra_config.additionalDisk", 1)
		if ferr != nil {
			return Disk{}, ferr
		}
		if size == 0 {
			return Disk{}, failf(".extra_config.additionalDisk.diskSizeGb", "diskSizeGb is required")
		}
		gcp.AdditionalDisk = &GCPDisk{SizeGB: size, Type: typ}
	}
	if e.ProjectID != "" {
		if !gcpProjectPattern.MatchString(e.ProjectID) {
			return Disk{}, failf(".extra_config.project_id", "%q is not a project ID", e.ProjectID)
		}
		gcp.Project = e.ProjectID
	}
	if len(e.NetworkInterfaces) > maxGCPNetworkInterfaces {
		return Disk{}, failf(".extra_config.networkInterfaces", "at most %d network interfaces", maxGCPNetworkInterfaces)
	}
	for i, raw := range e.NetworkInterfaces {
		nic, ferr := raw.validate(fmt.Sprintf(".extra_config.networkInterfaces[%d]", i))
		if ferr != nil {
			return Disk{}, ferr
		}
		gcp.NetworkInterfaces = append(gcp.NetworkInterfaces, nic)
	}
	if len(e.ServiceAccounts) > 1 {
		return Disk{}, failf(".extra_config.serviceAccounts", "a machine can have one service account")
	}
	for i, raw := range e.ServiceAccounts {
		sa, ferr := raw.validate(fmt.Sprintf(".extra_config.serviceAccounts[%d]", i))
		if ferr != nil {
			return Disk{}, ferr
		}
		gcp.ServiceAccounts = append(gcp.ServiceAccounts, sa)
	}
	if e.EnablePublicIP != nil && !*e.EnablePublicIP {
		gcp.NoPublicIP = true
		for i, nic := range gcp.NetworkInterfaces {
			if len(nic.AccessConfigs) > 0 {
				return Disk{}, failf(fmt.Sprintf(".extra_config.networkInterfaces[%d].accessConfigs", i), "an external address conflicts with enable_public_ip: false")
			}
		}
	}
	if e.Scheduling != nil {
		switch policy := strings.ToUpper(strings.TrimSpace(e.Scheduling.OnHostMaintenance)); policy {
		case "":
		case "MIGRATE", "TERMINATE":
			gcp.OnHostMaintenance = policy
		default:
			return Disk{}, failf(".extra_config.scheduling.onHostMaintenance", "onHostMaintenance must be MIGRATE or TERMINATE")
		}
	}
	out.GCP = gcp

	return disk, nil
}

func (d *rawGCPDisk) validate(path string, minGB int) (int, string, *fieldError) {
	if d.DiskSizeGb != 0 && (d.DiskSizeGb < minGB || d.DiskSizeGb > maxGCPDiskGB) {
		return 0, "", failf(path+".diskSizeGb", "diskSizeGb must be between %d and %d GB", minGB, maxGCPDiskGB)
	}
	if d.DiskType != "" && !gcpDiskTypePattern.MatchString(d.DiskType) {
		return 0, "", failf(path+".diskType", "diskType must be a disk type such as pd-balanced or a zones/.../diskTypes/ path")
	}

	return d.DiskSizeGb, d.DiskType, nil
}

func (d *rawGCPExistingDisk) validate(path string) (*GCPExistingDisk, *fieldError) {
	m := gcpDiskSourcePattern.FindStringSubmatch(strings.TrimSpace(d.Source))
	if m == nil {
		return nil, failf(path+".source", "source must be a disk reference such as projects/<project>/zones/<zone>/disks/<name>")
	}
	out := &GCPExistingDisk{Source: strings.TrimSpace(d.Source), DeviceName: strings.TrimSpace(d.DeviceName)}
	// Submatches: 1 self-link prefix, 2 scheme host, 3 version, 4 project,
	// 5 zones|regions, 6 location, 7 zone letter.
	if m[5] == "zones" {
		if !gcpZonePattern.MatchString(m[6]) {
			return nil, failf(path+".source", "%q is not a zone", m[6])
		}
		out.Zone = m[6]
		out.Region = m[6][:strings.LastIndex(m[6], "-")]
	} else {
		if gcpZonePattern.MatchString(m[6]) {
			return nil, failf(path+".source", "%q is a zone, not a region", m[6])
		}
		out.Region = m[6]
	}
	if out.DeviceName != "" && !gcpNamePattern.MatchString(out.DeviceName) {
		return nil, failf(path+".deviceName", "deviceName must be lowercase letters, digits and '-', up to 63 characters")
	}
	if d.DiskSizeGb != 0 && (d.DiskSizeGb < 1 || d.DiskSizeGb > maxGCPDiskGB) {
		return nil, failf(path+".diskSizeGb", "diskSizeGb must be between 1 and %d GB", maxGCPDiskGB)
	}
	switch typ := strings.ToUpper(d.Type); typ {
	case "", "PERSISTENT":
	default:
		return nil, failf(path+".type", "type must be PERSISTENT")
	}
	switch mode := strings.ToUpper(d.Mode); mode {
	case "":
	case "READ_WRITE", "READ_ONLY":
		out.Mode = mode
	default:
		return nil, failf(path+".mode", "mode must be READ_WRITE or READ_ONLY")
	}
	switch iface := strings.ToUpper(d.Interface); iface {
	case "":
	case "SCSI", "NVME":
		out.Interface = iface
	default:
		return nil, failf(path+".interface", "interface must be SCSI or NVME")
	}

	return out, nil
}

func (n *rawGCPInterface) validate(path string) (GCPNetworkInterface, *fieldError) {
	out := GCPNetworkInterface{Network: n.Network, Subnetwork: n.Subnetwork}
	if n.Network != "" && !gcpNetworkPattern.MatchString(n.Network) {
		return out, failf(path+".network", "network must be a network name or a global/networks/ path")
	}
	if gcpNamePattern.MatchString(n.Network) {
		out.Network = "global/networks/" + n.Network
	}
	if n.Subnetwork != "" && !gcpSubnetworkPattern.MatchString(n.Subnetwork) {
		return out, failf(path+".subnetwork", "subnetwork must be a regions/<region>/subnetworks/<name> path")
	}
	switch nic := strings.ToUpper(n.NicType); nic {
	case "":
	case "GVNIC", "VIRTIO_NET", "IDPF":
		out.NicType = nic
	default:
		return out, failf(path+".nicType", "nicType must be GVNIC, VIRTIO_NET or IDPF")
	}
	switch stack := strings.ToUpper(n.StackType); stack {
	case "":
	case "IPV4_ONLY", "IPV4_IPV6", "IPV6_ONLY":
		out.StackType = stack
	default:
		return out, failf(path+".stackType", "stackType must be IPV4_ONLY, IPV4_IPV6 or IPV6_ONLY")
	}
	if len(n.AccessConfigs) > 1 {
		return out, failf(path+".accessConfigs", "an interface has at most one access config")
	}
	for i, raw := range n.AccessConfigs {
		ac := GCPAccessConfig{Type: strings.ToUpper(raw.Type), Name: strings.TrimSpace(raw.Name), NetworkTier: strings.ToUpper(raw.NetworkTier)}
		acPath := fmt.Sprintf("%s.accessConfigs[%d]", path, i)
		switch ac.Type {
		case "", "ONE_TO_ONE_NAT":
			ac.Type = "ONE_TO_ONE_NAT"
		default:
			return out, failf(acPath+".type", "type must be ONE_TO_ONE_NAT")
		}
		if len(ac.Name) > 63 {
			return out, failf(acPath+".name", "name must be at most 63 characters")
		}
		switch ac.NetworkTier {
		case "", "PREMIUM", "STANDARD":
		default:
			return out, failf(acPath+".networkTier", "networkTier must be PREMIUM or STANDARD")
		}
		out.AccessConfigs = append(out.AccessConfigs, ac)
	}

	return out, nil
}

func (s *rawGCPServiceAccount) validate(path string) (GCPServiceAccount, *fieldError) {
	email := strings.TrimSpace(s.Email)
	if !gcpEmailPattern.MatchString(email) {
		return GCPServiceAccount{}, failf(path+".email", "email must be a service account address (...@<project>.iam.gserviceaccount.com) or default")
	}
	if len(s.Scopes) > maxGCPScopes {
		return GCPServiceAccount{}, failf(path+".scopes", "at most %d scopes", maxGCPScopes)
	}
	for i, scope := range s.Scopes {
		if !gcpScopePattern.MatchString(scope) {
			return GCPServiceAccount{}, failf(fmt.Sprintf("%s.scopes[%d]", path, i), "%q is not an OAuth scope URL (https://www.googleapis.com/auth/...)", scope)
		}
	}

	return GCPServiceAccount{Email: email, Scopes: slices.Clone(s.Scopes)}, nil
}

// checkGCP rejects combinations Compute Engine refuses at launch, so the
// file is wrong rather than every attempt failing.
func (g *GCP) checkGCP(gpu bool, variants []Variant) *fieldError {
	if g == nil {
		return nil
	}
	if g.ExistingDisk != nil {
		// The disk decides where the machine can be; a variant that says
		// otherwise could never launch.
		for i, v := range variants {
			index := ""
			if len(variants) > 1 {
				index = fmt.Sprintf("[%d]", i)
			}
			if v.Zone != "" && g.ExistingDisk.Zone != "" && v.Zone != g.ExistingDisk.Zone {
				return failf(".region"+index, "existingDisk is in zone %s; the runner cannot launch in %s", g.ExistingDisk.Zone, v.Zone)
			}
			if v.Region != "" && v.Region != g.ExistingDisk.Region {
				return failf(".region"+index, "existingDisk is in region %s; the runner cannot launch in %s", g.ExistingDisk.Region, v.Region)
			}
		}
	}
	if g.OnHostMaintenance != "MIGRATE" {
		return nil
	}
	if gpu {
		return failf(".extra_config.scheduling.onHostMaintenance", "MIGRATE is not allowed with a GPU; machines with accelerators stop for maintenance")
	}
	for i, v := range variants {
		if v.Spot {
			index := ""
			if len(variants) > 1 {
				index = fmt.Sprintf("[%d]", i)
			}

			return failf(".preemptible"+index, "MIGRATE is not allowed on preemptible machines")
		}
		if HasGPU(CloudGCP, v.InstanceType) {
			return failf(".extra_config.scheduling.onHostMaintenance", "MIGRATE is not allowed on %s, which has accelerators", v.InstanceType)
		}
	}

	return nil
}

// validateGCPVariant checks a GCP variant's location, machine type and
// image, and splits a zone into region and zone.
func validateGCPVariant(v *Variant, index string) *fieldError {
	if v.Region != "" {
		if !gcpLocationPattern.MatchString(v.Region) {
			return failf(".region"+index, "%q is not a GCP zone or region (such as us-central1-b)", v.Region)
		}
		if gcpZonePattern.MatchString(v.Region) {
			v.Zone = v.Region
			v.Region = v.Region[:strings.LastIndex(v.Region, "-")]
		}
	}
	if len(v.InstanceType) > 63 || !gcpMachineTypePattern.MatchString(v.InstanceType) {
		return failf(".instance_type"+index, "%q is not a Compute Engine machine type (such as n2-standard-4)", v.InstanceType)
	}
	if v.Image != "" && !imageAliasPattern.MatchString(v.Image) && !gcpImagePattern.MatchString(v.Image) {
		return failf(".machine_image"+index, "%q must be an image or family name, project:image, a projects/.../global/images/... path or its URL", v.Image)
	}

	return nil
}

// gcpFamily is the machine family of a machine type: n2 of n2-standard-4.
func gcpFamily(machineType string) string {
	family, _, _ := strings.Cut(machineType, "-")

	return family
}

// isWindowsImageName reports whether an image is named for Windows, as
// Google's public Windows families are (windows-2022, windows-cloud's
// windows-server-...).
func isWindowsImageName(image string) bool {
	name := image[strings.LastIndexAny(image, "/:")+1:]

	return strings.HasPrefix(name, "windows-")
}
