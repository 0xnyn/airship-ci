package runnerconfig

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Daytona is what a Daytona definition's fields ask for beyond the
// variants: sandbox settings the provider applies to every variant.
type Daytona struct {
	// Docker installs and starts a Docker daemon in the sandbox
	// (extra_config.docker).
	Docker bool
	// Volumes are Daytona volumes to mount (extra_config.volumes).
	Volumes []DaytonaVolume
	// NetworkAllowList and DomainAllowList restrict egress, as Daytona's
	// API takes them: comma-separated CIDRs and domains.
	NetworkAllowList string
	DomainAllowList  string
}

// DaytonaVolume mirrors one entry of extra_config.volumes.
type DaytonaVolume struct {
	Volume    string
	MountPath string
	Subpath   string
}

// Daytona has no machine types. A definition's instance_type is either a
// shape, "<n>vcpu-<m>gb", that the provider builds a sandbox of from the
// container image in machine_image, or the name of a snapshot the customer
// made, which fixes the image and the resources.
var (
	daytonaShapePattern    = regexp.MustCompile(`^([0-9]{1,3})vcpu-([0-9]{1,4})gb$`)
	daytonaSnapshotPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	daytonaRegionPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	// A container image reference with a tag or digest, as the Dockerfile
	// FROM line will carry it.
	daytonaImagePattern  = regexp.MustCompile(`^([a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?/)?[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[a-f0-9]{64})$`)
	daytonaVolumePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	daytonaDomainPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	// GPU types Daytona offers, as its API spells them. Matched without
	// regard to case.
	daytonaGPUTypes = []string{"H100", "H200", "B200", "B300", "RTX-PRO-6000", "RTX-4090", "RTX-5090", "MI355X"}
	// Image aliases shared with the other clouds, as container images.
	daytonaImageAliases = map[string]string{"ubuntu2204": "ubuntu:22.04", "ubuntu2404": "ubuntu:24.04"}
)

const (
	// DaytonaDefaultDiskGB is the disk of a sandbox built from an image
	// when extra_config.disk says nothing: the smallest tier's maximum.
	DaytonaDefaultDiskGB = 10
	maxDaytonaCPU        = 256
	maxDaytonaMemoryGB   = 2048
	maxDaytonaDiskGB     = 10240
	maxDaytonaVolumes    = 10
	maxDaytonaAllowList  = 2048
	// Daytona's own limits on the allow lists.
	maxDaytonaCIDRs   = 10
	maxDaytonaDomains = 100
)

// daytonaOnlyKeys name the extra_config keys only Daytona understands.
var daytonaOnlyKeys = []string{"disk", "volumes", "docker", "network_allow_list", "domain_allow_list"}

type rawDaytonaVolume struct {
	Volume    string `yaml:"volume"`
	MountPath string `yaml:"mount_path"`
	Subpath   string `yaml:"subpath"`
}

// applyDaytona validates the Daytona keys and fills out.Daytona. It
// returns the sandbox's disk.
func (e *rawExtra) applyDaytona(out *Runner, disk Disk) (Disk, *fieldError) {
	d := &Daytona{Docker: e.Docker}
	if e.Disk != 0 {
		if e.Disk < 1 || e.Disk > maxDaytonaDiskGB {
			return Disk{}, failf(".extra_config.disk", "disk must be between 1 and %d GB", maxDaytonaDiskGB)
		}
		disk.SizeGB = e.Disk
	}
	if len(e.Volumes) > maxDaytonaVolumes {
		return Disk{}, failf(".extra_config.volumes", "at most %d volumes", maxDaytonaVolumes)
	}
	for i, raw := range e.Volumes {
		path := fmt.Sprintf(".extra_config.volumes[%d]", i)
		v := DaytonaVolume{Volume: strings.TrimSpace(raw.Volume), MountPath: strings.TrimSpace(raw.MountPath), Subpath: strings.Trim(strings.TrimSpace(raw.Subpath), "/")}
		if !daytonaVolumePattern.MatchString(v.Volume) {
			return Disk{}, failf(path+".volume", "volume must be a Daytona volume name or ID")
		}
		if !strings.HasPrefix(v.MountPath, "/") || strings.ContainsAny(v.MountPath, " '\"\\\x00") || strings.Contains(v.MountPath, "..") || len(v.MountPath) > 255 {
			return Disk{}, failf(path+".mount_path", "mount_path must be an absolute path without spaces or quotes")
		}
		if strings.ContainsAny(v.Subpath, " '\"\\\x00") || strings.Contains(v.Subpath, "..") || len(v.Subpath) > 255 {
			return Disk{}, failf(path+".subpath", "subpath must be a path inside the volume")
		}
		for _, other := range d.Volumes {
			if other.MountPath == v.MountPath {
				return Disk{}, failf(path+".mount_path", "%s is mounted twice", v.MountPath)
			}
		}
		d.Volumes = append(d.Volumes, v)
	}
	// Daytona takes one egress policy per sandbox: CIDRs or domains, not
	// both (it answers 400 to a request with both). IPv4 CIDRs only.
	if e.NetworkAllowList != "" && e.DomainAllowList != "" {
		return Disk{}, failf(".extra_config.domain_allow_list", "set network_allow_list or domain_allow_list, not both; Daytona applies one egress policy per sandbox")
	}
	if e.NetworkAllowList != "" {
		list, ferr := splitList(".extra_config.network_allow_list", e.NetworkAllowList, maxDaytonaCIDRs, func(item string) bool {
			ip, _, err := net.ParseCIDR(item)

			return err == nil && ip.To4() != nil
		}, "an IPv4 CIDR such as 140.82.112.0/20")
		if ferr != nil {
			return Disk{}, ferr
		}
		d.NetworkAllowList = list
	}
	if e.DomainAllowList != "" {
		list, ferr := splitList(".extra_config.domain_allow_list", e.DomainAllowList, maxDaytonaDomains, daytonaDomainPattern.MatchString, "a domain such as github.com or *.githubusercontent.com")
		if ferr != nil {
			return Disk{}, ferr
		}
		d.DomainAllowList = list
	}
	if e.Docker || len(d.Volumes) > 0 || d.NetworkAllowList != "" || d.DomainAllowList != "" {
		out.Daytona = d
	}

	return disk, nil
}

// splitList validates a comma-separated list item by item and returns it
// trimmed and joined again, with at most limit items.
func splitList(path, raw string, limit int, valid func(string) bool, want string) (string, *fieldError) {
	if len(raw) > maxDaytonaAllowList {
		return "", failf(path, "at most %d characters", maxDaytonaAllowList)
	}
	var items []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if !valid(item) {
			return "", failf(path, "%q is not %s", item, want)
		}
		if !slices.Contains(items, item) {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return "", failf(path, "the list is empty")
	}
	if len(items) > limit {
		return "", failf(path, "at most %d entries", limit)
	}

	return strings.Join(items, ","), nil
}

// validateDaytonaVariant checks a Daytona variant's region, shape or
// snapshot, and image.
func validateDaytonaVariant(v *Variant, index string) *fieldError {
	if v.Region != "" && !daytonaRegionPattern.MatchString(v.Region) {
		return failf(".region"+index, "%q is not a Daytona region (such as us or eu)", v.Region)
	}
	if m := daytonaShapePattern.FindStringSubmatch(v.InstanceType); m != nil {
		cpu, _ := strconv.Atoi(m[1])
		memory, _ := strconv.Atoi(m[2])
		if cpu < 1 || cpu > maxDaytonaCPU || memory < 1 || memory > maxDaytonaMemoryGB {
			return failf(".instance_type"+index, "%q asks for more than %d vCPUs or %d GB", v.InstanceType, maxDaytonaCPU, maxDaytonaMemoryGB)
		}
		v.CPU, v.MemoryGB = cpu, memory
		if alias, ok := daytonaImageAliases[v.Image]; ok {
			v.Image = alias
		}
		if v.Image != "" && (!daytonaImagePattern.MatchString(v.Image) || strings.HasSuffix(v.Image, ":latest")) {
			return failf(".machine_image"+index, "%q must be a container image with a tag or digest (ubuntu:24.04, ghcr.io/org/ci@sha256:...), not latest", v.Image)
		}
		if isWindowsContainerImage(v.Image) {
			return failf(".machine_image"+index, "Windows sandboxes are not supported on daytona")
		}

		return nil
	}
	if !daytonaSnapshotPattern.MatchString(v.InstanceType) {
		return failf(".instance_type"+index, "%q is not a sandbox shape (such as 2vcpu-4gb) or a snapshot name", v.InstanceType)
	}
	if v.Image != "" {
		return failf(".machine_image"+index, "a snapshot (%s) fixes the image; leave machine_image empty or use a shape such as 2vcpu-4gb", v.InstanceType)
	}
	v.Snapshot = v.InstanceType

	return nil
}

// isWindowsContainerImage reports an image name Microsoft publishes
// Windows containers under.
func isWindowsContainerImage(image string) bool {
	return strings.HasPrefix(image, "mcr.microsoft.com/windows")
}

// daytonaGPU resolves a gpu: value to Daytona's spelling of the type.
func daytonaGPU(name string) (string, bool) {
	for _, g := range daytonaGPUTypes {
		if strings.EqualFold(g, name) {
			return g, true
		}
	}

	return "", false
}

// checkDaytona rejects combinations Daytona refuses at launch. diskSet
// says whether extra_config named a disk.
func checkDaytona(r *Runner, variants []Variant, diskSet bool) *fieldError {
	if r.ProvisionVia != "" {
		return failf(".extra_config.provision_via", "provision_via is not supported on daytona: a sandbox runs nothing at boot, Airship starts the runner itself")
	}
	if r.Windows.Interactive || r.Windows.User != "" || r.Windows.Path != "" {
		return failf(".extra_config.run_as", "Windows settings do not apply on daytona")
	}
	shapes := 0
	for i, v := range variants {
		index := ""
		if len(variants) > 1 {
			index = fmt.Sprintf("[%d]", i)
		}
		if v.Spot && !r.GPU {
			return failf(".preemptible"+index, "preemptible (spot) capacity on daytona is for GPU sandboxes only; set gpu")
		}
		if v.Snapshot == "" {
			shapes++
		}
	}
	// The disk applies to sandboxes built from an image; a snapshot fixes
	// its own. With no shape among the variants the key does nothing.
	if diskSet && shapes == 0 {
		return failf(".extra_config.disk", "a snapshot (%s) fixes the disk; remove disk", variants[0].Snapshot)
	}

	return nil
}
