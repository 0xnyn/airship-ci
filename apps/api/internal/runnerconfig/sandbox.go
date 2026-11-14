package runnerconfig

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The sandbox clouds beside Daytona. None has machine types:
//
//   - Runloop: instance_type is a named devbox size (small, x-large) or a
//     shape ("4vcpu-16gb"); machine_image a blueprint name or ID.
//   - E2B and the clouds that speak its protocol (Novita, Lelantos):
//     instance_type is the template the sandbox starts from, which fixes
//     memory and disk, optionally with more vCPUs ("ci-runner:4vcpu");
//     machine_image stays empty.
//   - Namespace, Tenki and Modal: instance_type is a shape ("4vcpu-8gb")
//     and machine_image a container image with a tag or digest (Namespace
//     and Modal default to ubuntu:22.04, Tenki to its own image).
//
// Only Namespace has regions (us, eu); the rest have the one region
// SandboxRegion. Only Modal attaches GPUs.
const (
	CloudRunloop   = "runloop"
	CloudE2B       = "e2b"
	CloudNovita    = "novita"
	CloudLelantos  = "lelantos"
	CloudNamespace = "namespace"
	CloudTenki     = "tenki"
	CloudModal     = "modal"
	// SandboxRegion is the one region the regionless clouds have.
	SandboxRegion = "global"

	maxRunloopCPU      = 16
	maxRunloopMemoryGB = 64
	minRunloopDiskGB   = 2
	maxRunloopDiskGB   = 64
	maxTemplateCPU     = 32
	maxShapeCPU        = 128
	maxShapeMemoryGB   = 512
	minTenkiDiskGB     = 5
	maxTenkiDiskGB     = 100
)

// Sandbox is what a sandbox cloud definition's extra_config asks for.
type Sandbox struct {
	// Docker installs and starts a Docker daemon before the runner (on
	// Namespace: mounts the VM's own; on Modal: runs on the VM runtime).
	Docker bool
}

var (
	// runloopSizes maps the sizes a config may name to Runloop's spelling.
	runloopSizes = map[string]string{
		"x-small": "X_SMALL", "small": "SMALL", "medium": "MEDIUM", "large": "LARGE", "x-large": "X_LARGE", "xx-large": "XX_LARGE",
	}
	runloopSizeNames = []string{"x-small", "small", "medium", "large", "x-large", "xx-large"}
	runloopShape     = regexp.MustCompile(`^([0-9]{1,2})vcpu-([0-9]{1,3})gb$`)
	shapePattern     = regexp.MustCompile(`^([0-9]{1,3})vcpu-([0-9]{1,4})gb$`)
	runloopBlueprint = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	e2bTemplate      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}(:[A-Za-z0-9._-]{1,64})?$`)
	e2bCPUSuffix     = regexp.MustCompile(`^(.+):([0-9]{1,2})vcpu$`)
	// GPUs Modal attaches, as its API spells them. Matched without regard
	// to case.
	modalGPUTypes    = []string{"T4", "L4", "A10G", "A100", "A100-80GB", "L40S", "H100", "H200", "B200"}
	namespaceRegions = []string{"us", "eu"}

	runloopOnlyKeys   = []string{"disk", "docker"}
	tenkiOnlyKeys     = []string{"disk", "docker"}
	dockerOnlyKeys    = []string{"docker"}
	sandboxCloudNames = map[string]string{
		CloudRunloop: "Runloop", CloudE2B: "E2B", CloudNovita: "Novita", CloudLelantos: "Lelantos",
		CloudNamespace: "Namespace", CloudTenki: "Tenki", CloudModal: "Modal",
	}
)

// IsSandboxCloud reports one of the sandbox clouds this file parses
// (Daytona has its own).
func IsSandboxCloud(cloud string) bool {
	_, ok := sandboxCloudNames[cloud]

	return ok
}

// isTemplateCloud reports a cloud whose instance_type is an E2B template.
func isTemplateCloud(cloud string) bool {
	return cloud == CloudE2B || cloud == CloudNovita || cloud == CloudLelantos
}

// isShapeCloud reports a cloud whose instance_type is a vCPU/memory shape
// and machine_image a container image.
func isShapeCloud(cloud string) bool {
	return cloud == CloudNamespace || cloud == CloudTenki || cloud == CloudModal
}

// HasRegions reports a sandbox cloud with regions of its own.
func HasRegions(cloud string) bool { return cloud == CloudNamespace }

// modalGPU resolves a gpu: value to Modal's spelling.
func modalGPU(name string) (string, bool) {
	for _, g := range modalGPUTypes {
		if strings.EqualFold(g, name) {
			return g, true
		}
	}

	return "", false
}

// applySandbox validates a sandbox cloud definition's extra_config.
func (e *rawExtra) applySandbox(out *Runner, disk Disk) (Disk, *fieldError) {
	if e.Disk != 0 {
		switch out.Cloud {
		case CloudRunloop:
			if e.Disk < minRunloopDiskGB || e.Disk > maxRunloopDiskGB || e.Disk%2 != 0 {
				return Disk{}, failf(".extra_config.disk", "disk must be an even number of GB between %d and %d", minRunloopDiskGB, maxRunloopDiskGB)
			}
		case CloudTenki:
			if e.Disk < minTenkiDiskGB || e.Disk > maxTenkiDiskGB {
				return Disk{}, failf(".extra_config.disk", "disk must be between %d and %d GB", minTenkiDiskGB, maxTenkiDiskGB)
			}
		default:
			return Disk{}, failf(".extra_config.disk", "the disk is not configurable on %s; remove disk", sandboxCloudNames[out.Cloud])
		}
		disk.SizeGB = e.Disk
	}
	if e.Docker {
		out.Sandbox = &Sandbox{Docker: true}
	}

	return disk, nil
}

// validateSandboxVariant checks a sandbox cloud's variant.
func validateSandboxVariant(cloud string, v *Variant, index string) *fieldError {
	name := sandboxCloudNames[cloud]
	switch {
	case HasRegions(cloud):
		if v.Region != "" && !slices.Contains(namespaceRegions, v.Region) {
			return failf(".region"+index, "%q is not a %s region (%s)", v.Region, name, strings.Join(namespaceRegions, ", "))
		}
	case v.Region != "" && v.Region != SandboxRegion:
		return failf(".region"+index, "%s has no regions; leave region out or use %q", name, SandboxRegion)
	}
	if v.Spot {
		return failf(".preemptible"+index, "%s has no preemptible capacity", name)
	}
	switch {
	case isTemplateCloud(cloud):
		return validateTemplateVariant(name, v, index)
	case isShapeCloud(cloud):
		return validateShapeVariant(cloud, v, index)
	}

	return validateRunloopVariant(v, index)
}

func validateRunloopVariant(v *Variant, index string) *fieldError {
	t := strings.ToLower(v.InstanceType)
	switch m := runloopShape.FindStringSubmatch(t); {
	case runloopSizes[t] != "":
		v.Size = runloopSizes[t]
	case m != nil:
		cpu, _ := strconv.Atoi(m[1])
		memory, _ := strconv.Atoi(m[2])
		if cpu < 1 || cpu > maxRunloopCPU || (cpu != 1 && cpu%2 != 0) {
			return failf(".instance_type"+index, "%q: vCPUs must be 1 or an even number up to %d", v.InstanceType, maxRunloopCPU)
		}
		if memory < 1 || memory > maxRunloopMemoryGB || (memory != 1 && memory%2 != 0) {
			return failf(".instance_type"+index, "%q: memory must be 1 GB or an even number of GB up to %d", v.InstanceType, maxRunloopMemoryGB)
		}
		if memory < 2*cpu || memory > 8*cpu {
			return failf(".instance_type"+index, "%q: memory must be between 2 and 8 GB per vCPU", v.InstanceType)
		}
		v.CPU, v.MemoryGB = cpu, memory
	default:
		return failf(".instance_type"+index, "%q is not a Runloop size (%s) or a shape such as 4vcpu-16gb", v.InstanceType, strings.Join(runloopSizeNames, ", "))
	}
	if v.Image != "" {
		if imageAliasPattern.MatchString(v.Image) || !runloopBlueprint.MatchString(v.Image) {
			return failf(".machine_image"+index, "%q must be a Runloop blueprint name or ID (bpt_...), or empty for Runloop's default image", v.Image)
		}
		v.Template = v.Image
	}

	return nil
}

func validateTemplateVariant(name string, v *Variant, index string) *fieldError {
	t := v.InstanceType
	if m := e2bCPUSuffix.FindStringSubmatch(t); m != nil {
		cpu, _ := strconv.Atoi(m[2])
		if cpu < 1 || cpu > maxTemplateCPU {
			return failf(".instance_type"+index, "%q asks for more than %d vCPUs", v.InstanceType, maxTemplateCPU)
		}
		t, v.CPU = m[1], cpu
	}
	if !e2bTemplate.MatchString(t) {
		return failf(".instance_type"+index, "%q is not a template name or ID on %s (such as base, or ci-runner:4vcpu for more vCPUs)", v.InstanceType, name)
	}
	if v.Image != "" {
		return failf(".machine_image"+index, "the template (%s) fixes the image on %s; leave machine_image empty", t, name)
	}
	v.Template = t

	return nil
}

func validateShapeVariant(cloud string, v *Variant, index string) *fieldError {
	name := sandboxCloudNames[cloud]
	m := shapePattern.FindStringSubmatch(strings.ToLower(v.InstanceType))
	if m == nil {
		return failf(".instance_type"+index, "%q is not a shape on %s (vCPUs and GB of memory, such as 4vcpu-8gb)", v.InstanceType, name)
	}
	cpu, _ := strconv.Atoi(m[1])
	memory, _ := strconv.Atoi(m[2])
	if cpu < 1 || cpu > maxShapeCPU || memory < 1 || memory > maxShapeMemoryGB {
		return failf(".instance_type"+index, "%q asks for more than %d vCPUs or %d GB", v.InstanceType, maxShapeCPU, maxShapeMemoryGB)
	}
	v.CPU, v.MemoryGB = cpu, memory
	if alias, ok := daytonaImageAliases[v.Image]; ok {
		v.Image = alias
	}
	if v.Image != "" && (!daytonaImagePattern.MatchString(v.Image) || strings.HasSuffix(v.Image, ":latest")) {
		return failf(".machine_image"+index, "%q must be a container image with a tag or digest (ubuntu:24.04, ghcr.io/org/ci@sha256:...), not latest", v.Image)
	}
	if isWindowsContainerImage(v.Image) {
		return failf(".machine_image"+index, "Windows sandboxes are not supported on %s", cloud)
	}

	return nil
}

// checkSandbox rejects what the sandbox clouds cannot do.
func checkSandbox(r *Runner, variants []Variant, diskSet bool) *fieldError {
	name := sandboxCloudNames[r.Cloud]
	if r.ProvisionVia != "" {
		return failf(".extra_config.provision_via", "provision_via is not supported on %s: a sandbox runs nothing at boot, Airship starts the runner itself", r.Cloud)
	}
	if r.Windows.Interactive || r.Windows.User != "" || r.Windows.Path != "" {
		return failf(".extra_config.run_as", "Windows settings do not apply on %s", r.Cloud)
	}
	if r.GPU && r.Cloud != CloudModal {
		return failf(".gpu", "%s sandboxes have no GPUs", name)
	}
	if diskSet && r.Cloud == CloudRunloop && !slices.ContainsFunc(variants, func(v Variant) bool { return v.CPU > 0 && v.Size == "" }) {
		return failf(".extra_config.disk", "a named Runloop size fixes its disk; use a shape such as 4vcpu-16gb, or remove disk")
	}

	return nil
}
