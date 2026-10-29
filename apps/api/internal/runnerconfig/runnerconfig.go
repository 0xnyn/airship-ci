// Package runnerconfig parses runners-as-code: the airship.yaml file in a
// repository, and the organization-wide airship.global.yaml and access.yaml
// files in the organization's .airship repository.
//
// The format is a list of runners, each with a cloud, machine
// shape and labels. List values for instance_type, machine_image, region
// and preemptible describe fallbacks, tried in order when a launch fails.
//
// Everything here is pure: parsing, validation, label matching and idle
// schedules. Fetching files and checking access with GitHub happen in fleet.
package runnerconfig

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// File names and conventions.
const (
	// RepoFile is read from the repository a job runs in.
	RepoFile = "airship.yaml"
	// GlobalRepo is the organization repository holding shared files.
	GlobalRepo = ".airship"
	// GlobalFile defines runners every activated repository may use.
	GlobalFile = "airship.global.yaml"
	// AccessFile restricts who may launch the global runners.
	AccessFile = "access.yaml"
	// LabelPrefix starts every label a config file defines. Only jobs that
	// ask for such a label cause config files to be read, so ordinary jobs
	// cost no GitHub API calls.
	LabelPrefix = "airship-"
	// SuffixSeparator splits a runs-on label into the label a config file
	// defines and a suffix, such as the run ID, that makes the runner unique
	// to one workflow run: airship-gpu--${{ github.run_id }}.
	SuffixSeparator = "--"
	// ProductLabel is registered on every runner, so a job can ask for
	// any Airship runner alongside its
	// config label.
	ProductLabel = "airship"
	// MaxFileBytes bounds every config file.
	MaxFileBytes = 64 << 10
)

// Clouds a runner can name. The other clouds (do, openstack, oracle,
// vast_ai, on_prem) are recognized so the error says they are not
// supported here rather than "unknown cloud".
const (
	CloudAWS     = "aws"
	CloudGCP     = "gcp"
	CloudAzure   = "azure"
	CloudDaytona = "daytona"
)

// Clouds lists the supported clouds, for messages.
var Clouds = []string{CloudAWS, CloudGCP, CloudAzure, CloudDaytona}

// cloudList is the supported clouds as a message spells them.
const cloudList = "aws, gcp, azure or daytona"

var otherClouds = []string{"do", "digitalocean", "openstack", "oracle", "vast_ai", "on_prem"}

// Provisioning modes.
const (
	// ProvisionPullFromAPI leaves the machine without a boot script; its
	// image fetches one from the developer API (provision_via: pull_from_api).
	ProvisionPullFromAPI = "pull_from_api"
)

// Limits that keep one file from asking for unbounded work.
const (
	maxRunners      = 100
	maxVariants     = 20
	maxLabels       = 20
	maxEnvVars      = 50
	maxEnvValue     = 4096
	maxIdleReplicas = 100
	maxIdleMinutes  = 24 * 60
	maxLabelLength  = 100
	// DefaultDiskGB and DefaultDiskType are the root volume defaults.
	DefaultDiskGB   = 75
	DefaultDiskType = "gp2"
)

var (
	namePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	labelPattern       = regexp.MustCompile(`^airship-[a-z0-9][a-z0-9._-]{0,62}$`)
	jobLabelPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	instancePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)
	amiPattern         = regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)
	regionPattern      = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
	envNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	subnetPattern      = regexp.MustCompile(`^subnet-[0-9a-f]{8,17}$`)
	sgPattern          = regexp.MustCompile(`^sg-[0-9a-f]{8,17}$`)
	profilePattern     = regexp.MustCompile(`^(arn:aws[a-z-]*:iam::[0-9]{12}:instance-profile/)?[A-Za-z0-9+=,.@_/-]{1,128}$`)
	windowsPathPattern = regexp.MustCompile(`^[A-Za-z]:\\[A-Za-z0-9_.\\-]{1,200}$`)
	windowsUserPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)
	imageAliasPattern  = regexp.MustCompile(`^ubuntu(2204|2404)$`)
	// An EC2 instance type: a family that starts with a letter, a dot, and
	// a size (c7i.large, u-6tb1.112xlarge, m7i.metal-24xl).
	instanceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z0-9-]*[a-z][a-z0-9-]*$`)
	maxSecurityGroupIDs = 5
	// Graviton families have a "g" after the generation number: c7g, m6gd,
	// t4g, c7gn, x2gd, is4gen. GPU families such as g5 and g4dn do not.
	// Apple silicon Macs (mac2, mac2-m2, mac2-m1ultra) are arm64 too.
	gravitonPattern = regexp.MustCompile(`^(a1|mac2(-[a-z0-9]+)?|[a-z]+[0-9]+[a-z]*g[a-z]*)$`)
	// GPU families: g*, p*, gr*, inf*, trn*, dl*.
	gpuFamilyPattern = regexp.MustCompile(`^(g[0-9]|gr[0-9]|p[0-9]|inf[0-9]|trn[0-9]|dl[0-9])`)
)

// File is a parsed airship.yaml or airship.global.yaml.
type File struct {
	Runners []Runner
}

// Runner is one runner definition.
type Runner struct {
	Name  string
	Cloud string
	// Labels are the runs-on labels this runner serves, all prefixed.
	Labels []string
	// Variants are the machine shapes to try, in order.
	Variants []Variant
	// Env is set in the runner's environment for every job.
	Env map[string]string
	// GPU asks for NVIDIA drivers on the machine (the gpu: field).
	GPU bool
	// GPUType is the accelerator the gpu: field named, for clouds that
	// attach accelerators by name (GCP: nvidia-tesla-t4). Empty on AWS,
	// where the instance type decides.
	GPUType string
	// Cache turns on transparent actions/cache acceleration.
	Cache bool
	// ProvisionVia is "" (boot script in user data) or ProvisionPullFromAPI.
	ProvisionVia string
	// Windows tunes Windows images: run_as, runner_user, runner_path.
	Windows Windows
	// Network overrides for AWS: SubnetId, SecurityGroupIds, IamInstanceProfile.
	SubnetID         string
	SecurityGroupIDs []string
	InstanceProfile  string
	// GCP holds Compute Engine settings; nil on other clouds.
	GCP *GCP
	// Azure holds virtual machine settings; nil on other clouds and when
	// nothing is set.
	Azure *Azure
	// Daytona holds sandbox settings; nil on other clouds.
	Daytona *Daytona
	Idle    *Idle
}

// Windows settings from extra_config.
type Windows struct {
	Interactive bool
	User        string
	Path        string
}

// Variant is one machine shape a runner may launch as.
type Variant struct {
	InstanceType string
	// Image is an AMI ID, Compute Engine image reference or Azure image
	// URN or ID, an alias such as ubuntu2204, or empty for the cloud's
	// default Ubuntu.
	Image  string
	Region string
	// Zone pins a GCP launch to one zone (region: us-central1-b); Region is
	// then that zone's region. Empty lets the provider pick a zone.
	Zone string
	Spot bool
	Arch string
	Disk Disk
	// Daytona: a sandbox built from Image has CPU vCPUs and MemoryGB of
	// memory (instance_type: 2vcpu-4gb); one from a snapshot names it.
	CPU      int
	MemoryGB int
	Snapshot string
}

// Disk is the root volume. Zero means the cloud's default.
type Disk struct {
	SizeGB     int
	Type       string
	IOPS       int
	Throughput int
	InitRate   int
}

// Idle keeps runners booted and registered ahead of demand. Exactly one of
// Always, Schedule or Usage is set.
type Idle struct {
	// Always keeps this many runners idle at all times.
	Always   int
	Schedule []Window
	Usage    *Usage
}

// Window keeps Replicas runners idle on the given days between StartHour
// (inclusive) and EndHour (exclusive), in Location.
type Window struct {
	Days      [7]bool // indexed by time.Weekday
	StartHour int
	EndHour   int
	Location  *time.Location
	Replicas  int
}

// Usage keeps Replicas runners idle for Duration after one of the runner's
// runners picks up a job.
type Usage struct {
	Replicas int
	Duration time.Duration
}

// Error is a config file problem, worded for the customer.
type Error struct {
	File   string
	Path   string
	Detail string
}

func (e *Error) Error() string {
	where := e.File
	if e.Path != "" {
		where += ": " + e.Path
	}

	return where + ": " + e.Detail
}

// Parse reads and validates a runner file. name is used in errors.
func Parse(name string, data []byte) (File, error) {
	if len(data) > MaxFileBytes {
		return File{}, &Error{File: name, Detail: fmt.Sprintf("file is %d bytes; the limit is %d", len(data), MaxFileBytes)}
	}
	var raw rawFile
	if err := decodeStrict(data, &raw); err != nil {
		return File{}, &Error{File: name, Detail: err.Error()}
	}
	if len(raw.Runners) == 0 {
		return File{}, &Error{File: name, Path: "runners", Detail: "define at least one runner"}
	}
	if len(raw.Runners) > maxRunners {
		return File{}, &Error{File: name, Path: "runners", Detail: fmt.Sprintf("at most %d runners", maxRunners)}
	}

	out := File{Runners: make([]Runner, 0, len(raw.Runners))}
	seen := map[string]bool{}
	for i, r := range raw.Runners {
		path := fmt.Sprintf("runners[%d]", i)
		runner, err := r.validate()
		if err != nil {
			return File{}, &Error{File: name, Path: path + err.path, Detail: err.detail}
		}
		key := strings.ToLower(runner.Name)
		if seen[key] {
			return File{}, &Error{File: name, Path: path + ".name", Detail: fmt.Sprintf("runner %q is defined twice", runner.Name)}
		}
		seen[key] = true
		out.Runners = append(out.Runners, runner)
	}

	return out, nil
}

func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, errEOF) {
			return errors.New("file is empty")
		}

		return fmt.Errorf("invalid YAML: %w", cleanYAMLError(err))
	}

	return nil
}

// fieldError locates a problem inside one runner.
type fieldError struct {
	path   string
	detail string
}

func failf(path, format string, args ...any) *fieldError {
	return &fieldError{path: path, detail: fmt.Sprintf(format, args...)}
}

type rawFile struct {
	Runners []rawRunner `yaml:"runners"`
}

type rawRunner struct {
	Name         string    `yaml:"name"`
	Cloud        string    `yaml:"cloud"`
	InstanceType listOf    `yaml:"instance_type"`
	MachineImage listOf    `yaml:"machine_image"`
	Region       listOf    `yaml:"region"`
	Preemptible  listOf    `yaml:"preemptible"`
	Labels       []string  `yaml:"labels"`
	ExtraConfig  *rawExtra `yaml:"extra_config"`
	Idle         *rawIdle  `yaml:"idle"`
	GPU          yaml.Node `yaml:"gpu"`
}

// rawExtra is extra_config for AWS, GCP and Azure, plus the
// keys for Daytona. A key of another cloud is refused by name; keys from another
// other clouds are rejected as unknown fields, which names the key in the
// error.
type rawExtra struct {
	EnvVars      map[string]string `yaml:"env_vars"`
	Cache        bool              `yaml:"cache"`
	ProvisionVia string            `yaml:"provision_via"`
	RunAs        string            `yaml:"run_as"`
	RunnerUser   string            `yaml:"runner_user"`
	RunnerPath   string            `yaml:"runner_path"`
	// AWS
	BlockDeviceMappings []rawDisk `yaml:"BlockDeviceMappings"`
	SubnetID            string    `yaml:"SubnetId"`
	SecurityGroupIDs    []string  `yaml:"SecurityGroupIds"`
	IamInstanceProfile  string    `yaml:"IamInstanceProfile"`
	// GCP
	BaseDisk          *rawGCPDisk            `yaml:"baseDisk"`
	AdditionalDisk    *rawGCPDisk            `yaml:"additionalDisk"`
	ExistingDisk      *rawGCPExistingDisk    `yaml:"existingDisk"`
	ProjectID         string                 `yaml:"project_id"`
	NetworkInterfaces []rawGCPInterface      `yaml:"networkInterfaces"`
	ServiceAccounts   []rawGCPServiceAccount `yaml:"serviceAccounts"`
	EnablePublicIP    *bool                  `yaml:"enable_public_ip"`
	Scheduling        *rawGCPScheduling      `yaml:"scheduling"`
	// Azure
	StorageProfile *rawAzureStorageProfile `yaml:"storageProfile"`
	// Daytona
	Disk             int                `yaml:"disk"`
	Volumes          []rawDaytonaVolume `yaml:"volumes"`
	Docker           bool               `yaml:"docker"`
	NetworkAllowList string             `yaml:"network_allow_list"`
	DomainAllowList  string             `yaml:"domain_allow_list"`
}

type rawDisk struct {
	DeviceName               string `yaml:"DeviceName"`
	VolumeSize               int    `yaml:"VolumeSize"`
	VolumeType               string `yaml:"VolumeType"`
	Iops                     int    `yaml:"Iops"`
	Throughput               int    `yaml:"Throughput"`
	VolumeInitializationRate int    `yaml:"VolumeInitializationRate"`
}

type rawIdle struct {
	Replicas  *int          `yaml:"replicas"`
	Schedule  []rawWindow   `yaml:"schedule"`
	UsageBase *rawUsageIdle `yaml:"usage_based"`
}

type rawWindow struct {
	Day      string `yaml:"day"`
	Time     string `yaml:"time"`
	Timezone string `yaml:"timezone"`
	Replicas *int   `yaml:"replicas"`
}

type rawUsageIdle struct {
	Trigger  string `yaml:"trigger"`
	Replicas int    `yaml:"replicas"`
	Duration int    `yaml:"duration"`
}

// listOf accepts a scalar or a list of scalars, for fallbacks.
type listOf []string

// A value may also be a mapping of an Azure image's parts ({publisher,
// offer, sku, version}), accepted for machine_image; it is read
// as the URN it spells.
func (l *listOf) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		value, err := listValue(node)
		if err != nil {
			return err
		}
		*l = listOf{value}

		return nil
	}
	out := make(listOf, 0, len(node.Content))
	for _, item := range node.Content {
		value, err := listValue(item)
		if err != nil {
			return err
		}
		out = append(out, value)
	}
	*l = out

	return nil
}

// listValue reads one entry of a list field: a scalar, or an image
// mapping.
func listValue(node *yaml.Node) (string, error) {
	switch node.Kind { //nolint:exhaustive // anything else is refused below
	case yaml.ScalarNode:
		return node.Value, nil
	case yaml.MappingNode:
		fields := map[string]string{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
				return "", fmt.Errorf("line %d: an image mapping has publisher, offer, sku and version values", value.Line)
			}
			fields[strings.ToLower(key.Value)] = strings.TrimSpace(value.Value)
		}
		urn, ok := azureImageURN(fields)
		if !ok {
			return "", fmt.Errorf("line %d: an image mapping needs publisher, offer and sku", node.Line)
		}

		return urn, nil
	default:
		return "", fmt.Errorf("line %d: expected a value, not a %s", node.Line, kindName(node.Kind))
	}
}

func kindName(kind yaml.Kind) string {
	names := map[yaml.Kind]string{yaml.MappingNode: "mapping", yaml.SequenceNode: "list"}
	if name, ok := names[kind]; ok {
		return name
	}

	return "value"
}

func (r rawRunner) validate() (Runner, *fieldError) {
	out := Runner{Name: strings.TrimSpace(r.Name)}
	if !namePattern.MatchString(out.Name) {
		return Runner{}, failf(".name", "name is required: letters, digits, '.', '_' or '-', up to 63 characters")
	}

	switch cloud := strings.ToLower(strings.TrimSpace(r.Cloud)); {
	case slices.Contains(Clouds, cloud):
		out.Cloud = cloud
	case cloud == "":
		return Runner{}, failf(".cloud", "cloud is required: %s", cloudList)
	case slices.Contains(otherClouds, cloud):
		return Runner{}, failf(".cloud", "cloud %q is not supported yet; only %s are", r.Cloud, cloudList)
	default:
		return Runner{}, failf(".cloud", "cloud %q is unknown; use %s", r.Cloud, cloudList)
	}
	if r.GPU.Kind != 0 {
		// The value is a flag: any GPU name turns it on, and
		// what Python (YAML 1.1) reads as false or null turns it off.
		if r.GPU.Kind != yaml.ScalarNode {
			return Runner{}, failf(".gpu", "gpu must be a GPU name, such as nvidia-tesla-t4")
		}
		switch name := strings.ToLower(strings.TrimSpace(r.GPU.Value)); name {
		case "":
			if r.GPU.Tag != "!!null" {
				return Runner{}, failf(".gpu", "gpu must be a GPU name, such as nvidia-tesla-t4")
			}
		case "false", "no", "off", "0", "null", "~", "none":
		default:
			out.GPU = true
			switch out.Cloud {
			case CloudGCP:
				// Compute Engine attaches the accelerator by this name.
				if !gcpGPUPattern.MatchString(name) {
					return Runner{}, failf(".gpu", "%q is not a Compute Engine GPU name, such as nvidia-tesla-t4 or nvidia-l4", r.GPU.Value)
				}
				out.GPUType = name
			case CloudDaytona:
				// Daytona offers a few GPU types by name.
				gpu, ok := daytonaGPU(name)
				if !ok {
					return Runner{}, failf(".gpu", "%q is not a Daytona GPU type; one of %s", r.GPU.Value, strings.Join(daytonaGPUTypes, ", "))
				}
				out.GPUType = gpu
			}
		}
	}

	labels, ferr := validateLabels(r.Labels)
	if ferr != nil {
		return Runner{}, ferr
	}
	out.Labels = labels

	disk, ferr := r.ExtraConfig.apply(&out)
	if ferr != nil {
		return Runner{}, ferr
	}

	variants, ferr := expandVariants(out.Cloud, r, disk)
	if ferr != nil {
		return Runner{}, ferr
	}
	out.Variants = variants
	if ferr := out.GCP.checkGCP(out.GPU, variants); ferr != nil {
		return Runner{}, ferr
	}
	if out.Cloud == CloudDaytona {
		if ferr := checkDaytona(&out, variants, r.ExtraConfig != nil && r.ExtraConfig.Disk != 0); ferr != nil {
			return Runner{}, ferr
		}
	}

	if r.Idle != nil {
		idle, ferr := r.Idle.validate()
		if ferr != nil {
			return Runner{}, ferr
		}
		out.Idle = idle
	}

	return out, nil
}

func validateLabels(labels []string) ([]string, *fieldError) {
	if len(labels) == 0 {
		return nil, failf(".labels", "add at least one label, starting with %q", LabelPrefix)
	}
	if len(labels) > maxLabels {
		return nil, failf(".labels", "at most %d labels", maxLabels)
	}
	out := make([]string, 0, len(labels))
	for i, label := range labels {
		lower := strings.ToLower(strings.TrimSpace(label))
		if !strings.HasPrefix(lower, LabelPrefix) {
			return nil, failf(fmt.Sprintf(".labels[%d]", i), "label %q must start with %q", label, LabelPrefix)
		}
		if strings.Contains(lower, SuffixSeparator) {
			return nil, failf(fmt.Sprintf(".labels[%d]", i), "label %q may not contain %q; that separates the per-run suffix in runs-on", label, SuffixSeparator)
		}
		if !labelPattern.MatchString(lower) {
			return nil, failf(fmt.Sprintf(".labels[%d]", i), "label %q may only use lowercase letters, digits, '.', '_' and '-'", label)
		}
		if !slices.Contains(out, lower) {
			out = append(out, lower)
		}
	}

	return out, nil
}

// apply validates extra_config and fills the runner's extra fields. It
// returns the root disk, which every variant shares.
func (e *rawExtra) apply(out *Runner) (Disk, *fieldError) {
	disk := Disk{SizeGB: DefaultDiskGB, Type: DefaultDiskType}
	switch out.Cloud {
	case CloudGCP:
		disk.Type = "" // the provider's default, pd-balanced
	case CloudAzure:
		disk = Disk{} // the image's size (Windows) or the provider's default (Linux), on the default managed disk kind
	case CloudDaytona:
		disk = Disk{SizeGB: DaytonaDefaultDiskGB} // a sandbox has one disk, of one kind
	}
	if e == nil {
		return disk, nil
	}
	if ferr := e.checkCloud(out.Cloud); ferr != nil {
		return Disk{}, ferr
	}
	var ferr *fieldError
	switch out.Cloud {
	case CloudGCP:
		if disk, ferr = e.applyGCP(out, disk); ferr != nil {
			return Disk{}, ferr
		}
	case CloudAzure:
		if disk, ferr = e.applyAzure(out, disk); ferr != nil {
			return Disk{}, ferr
		}
	case CloudDaytona:
		if disk, ferr = e.applyDaytona(out, disk); ferr != nil {
			return Disk{}, ferr
		}
	}

	if len(e.BlockDeviceMappings) > 1 {
		return Disk{}, failf(".extra_config.BlockDeviceMappings", "only the root volume can be configured")
	}
	if len(e.BlockDeviceMappings) == 1 {
		d := e.BlockDeviceMappings[0]
		if d.VolumeSize != 0 {
			if d.VolumeSize < 8 || d.VolumeSize > 16384 {
				return Disk{}, failf(".extra_config.BlockDeviceMappings[0].VolumeSize", "VolumeSize must be between 8 and 16384 GiB")
			}
			disk.SizeGB = d.VolumeSize
		}
		switch d.VolumeType {
		case "":
		case "gp3", "gp2", "io1", "io2":
			disk.Type = d.VolumeType
		default:
			return Disk{}, failf(".extra_config.BlockDeviceMappings[0].VolumeType", "VolumeType must be gp3, gp2, io1 or io2")
		}
		if ferr := validateDiskPerformance(disk.Type, d.Iops, d.Throughput); ferr != nil {
			return Disk{}, ferr
		}
		if d.VolumeInitializationRate != 0 && (d.VolumeInitializationRate < 100 || d.VolumeInitializationRate > 300) {
			return Disk{}, failf(".extra_config.BlockDeviceMappings[0].VolumeInitializationRate", "VolumeInitializationRate must be between 100 and 300 MiB/s")
		}
		disk.IOPS, disk.Throughput, disk.InitRate = d.Iops, d.Throughput, d.VolumeInitializationRate
	}

	env, ferr := validateEnv(e.EnvVars)
	if ferr != nil {
		return Disk{}, ferr
	}
	out.Env = env
	out.Cache = e.Cache

	switch strings.ToLower(strings.TrimSpace(e.ProvisionVia)) {
	case "":
	case ProvisionPullFromAPI:
		out.ProvisionVia = ProvisionPullFromAPI
	default:
		return Disk{}, failf(".extra_config.provision_via", "provision_via must be pull_from_api")
	}

	switch strings.ToLower(strings.TrimSpace(e.RunAs)) {
	case "":
	case "interactive":
		out.Windows.Interactive = true
	default:
		return Disk{}, failf(".extra_config.run_as", "run_as must be interactive")
	}
	if e.RunnerUser != "" {
		if !windowsUserPattern.MatchString(e.RunnerUser) {
			return Disk{}, failf(".extra_config.runner_user", "runner_user must be a Windows account name: letters, digits, '_' or '-'")
		}
		out.Windows.User = e.RunnerUser
	}
	if e.RunnerPath != "" {
		if !windowsPathPattern.MatchString(e.RunnerPath) {
			return Disk{}, failf(".extra_config.runner_path", `runner_path must be a Windows path such as "C:\\r", without spaces or quotes`)
		}
		out.Windows.Path = e.RunnerPath
	}

	if e.SubnetID != "" {
		if !subnetPattern.MatchString(e.SubnetID) {
			return Disk{}, failf(".extra_config.SubnetId", "SubnetId must be a subnet ID (subnet-...)")
		}
		out.SubnetID = e.SubnetID
	}
	if len(e.SecurityGroupIDs) > maxSecurityGroupIDs {
		return Disk{}, failf(".extra_config.SecurityGroupIds", "at most %d security groups", maxSecurityGroupIDs)
	}
	for i, id := range e.SecurityGroupIDs {
		if !sgPattern.MatchString(id) {
			return Disk{}, failf(fmt.Sprintf(".extra_config.SecurityGroupIds[%d]", i), "%q is not a security group ID (sg-...)", id)
		}
	}
	out.SecurityGroupIDs = e.SecurityGroupIDs
	if e.IamInstanceProfile != "" {
		if !profilePattern.MatchString(e.IamInstanceProfile) {
			return Disk{}, failf(".extra_config.IamInstanceProfile", "IamInstanceProfile must be an instance profile name or ARN")
		}
		out.InstanceProfile = e.IamInstanceProfile
	}

	return disk, nil
}

// Variables the runner itself reads; letting config set them would let a
// file change how the runner talks to GitHub.
var reservedEnvPrefixes = []string{"GITHUB_", "RUNNER_", "ACTIONS_"}

func validateEnv(vars map[string]string) (map[string]string, *fieldError) {
	if len(vars) == 0 {
		return nil, nil
	}
	if len(vars) > maxEnvVars {
		return nil, failf(".extra_config.env_vars", "at most %d variables", maxEnvVars)
	}
	for name, value := range vars {
		path := ".extra_config.env_vars." + name
		if !envNamePattern.MatchString(name) {
			return nil, failf(path, "%q is not a valid variable name", name)
		}
		for _, prefix := range reservedEnvPrefixes {
			if strings.HasPrefix(strings.ToUpper(name), prefix) {
				return nil, failf(path, "variables starting with %s are reserved for the runner", prefix)
			}
		}
		if len(value) > maxEnvValue {
			return nil, failf(path, "values are at most %d bytes", maxEnvValue)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, failf(path, "values may not contain line breaks")
		}
	}

	return vars, nil
}

// expandVariants zips the list fields into machine shapes. Every list must
// be as long as the longest one, or a single value used for every shape.
func expandVariants(cloud string, r rawRunner, disk Disk) ([]Variant, *fieldError) {
	if len(r.InstanceType) == 0 {
		return nil, failf(".instance_type", "instance_type is required")
	}
	fields := map[string]listOf{
		"instance_type": r.InstanceType,
		"machine_image": r.MachineImage,
		"region":        r.Region,
		"preemptible":   r.Preemptible,
	}
	n := 1
	for _, values := range fields {
		n = max(n, len(values))
	}
	if n > maxVariants {
		return nil, failf("", "at most %d fallbacks", maxVariants)
	}
	for _, name := range []string{"instance_type", "machine_image", "region", "preemptible"} {
		if l := len(fields[name]); l > 1 && l != n {
			return nil, failf("."+name, "%s lists %d values but other fields list %d; fallback lists must be the same length", name, l, n)
		}
	}
	pick := func(values listOf, i int) string {
		switch len(values) {
		case 0:
			return ""
		case 1:
			return values[0]
		default:
			return values[i]
		}
	}

	out := make([]Variant, 0, n)
	for i := range n {
		v := Variant{
			InstanceType: strings.TrimSpace(pick(r.InstanceType, i)),
			Image:        strings.TrimSpace(pick(r.MachineImage, i)),
			Region:       strings.TrimSpace(pick(r.Region, i)),
			Disk:         disk,
		}
		index := ""
		if n > 1 {
			index = fmt.Sprintf("[%d]", i)
		}
		switch strings.ToLower(pick(r.Preemptible, i)) {
		case "", "false", "no":
		case "true", "yes":
			v.Spot = true
		default:
			return nil, failf(".preemptible"+index, "preemptible must be true or false")
		}
		switch cloud {
		case CloudGCP:
			if ferr := validateGCPVariant(&v, index); ferr != nil {
				return nil, ferr
			}
		case CloudAzure:
			if ferr := validateAzureVariant(&v, index); ferr != nil {
				return nil, ferr
			}
		case CloudDaytona:
			if ferr := validateDaytonaVariant(&v, index); ferr != nil {
				return nil, ferr
			}
		default:
			if v.Region != "" && !regionPattern.MatchString(v.Region) {
				return nil, failf(".region"+index, "%q is not an AWS region (such as us-east-1)", v.Region)
			}
			if !instancePattern.MatchString(v.InstanceType) || !instanceTypePattern.MatchString(v.InstanceType) {
				return nil, failf(".instance_type"+index, "%q is not an EC2 instance type (such as c7i.xlarge)", v.InstanceType)
			}
			if v.Image != "" && !amiPattern.MatchString(v.Image) && !imageAliasPattern.MatchString(v.Image) {
				return nil, failf(".machine_image"+index, "%q must be an AMI ID (ami-...), ubuntu2204 or ubuntu2404", v.Image)
			}
		}
		v.Arch = ArchForInstanceType(cloud, v.InstanceType)
		out = append(out, v)
	}

	return out, nil
}

// ArchForInstanceType infers the CPU architecture of an instance type:
// Graviton and Apple silicon families on AWS, Axion and Altra on GCP, the
// "p" sizes (Ampere, Cobalt) on Azure. Daytona sandboxes are x64.
func ArchForInstanceType(cloud, instanceType string) string {
	switch cloud {
	case CloudGCP:
		if gcpArmFamilies[gcpFamily(instanceType)] {
			return "arm64"
		}

		return "x64"
	case CloudAzure:
		return azureArch(instanceType)
	case CloudDaytona:
		return "x64"
	}
	family, _, _ := strings.Cut(instanceType, ".")
	if gravitonPattern.MatchString(family) {
		return "arm64"
	}

	return "x64"
}

// HasGPU reports whether an instance family carries an accelerator. On
// Daytona only the gpu: field does.
func HasGPU(cloud, instanceType string) bool {
	switch cloud {
	case CloudGCP:
		return gcpGPUFamilies[gcpFamily(instanceType)]
	case CloudAzure:
		return azureGPUPattern.MatchString(instanceType)
	case CloudDaytona:
		return false
	}
	family, _, _ := strings.Cut(instanceType, ".")

	return gpuFamilyPattern.MatchString(family)
}

func (r *rawIdle) validate() (*Idle, *fieldError) {
	forms := 0
	if r.Replicas != nil {
		forms++
	}
	if len(r.Schedule) > 0 {
		forms++
	}
	if r.UsageBase != nil {
		forms++
	}
	if forms != 1 {
		return nil, failf(".idle", "set exactly one of replicas, schedule or usage_based")
	}

	switch {
	case r.Replicas != nil:
		if *r.Replicas < 0 || *r.Replicas > maxIdleReplicas {
			return nil, failf(".idle.replicas", "replicas must be between 0 and %d", maxIdleReplicas)
		}

		return &Idle{Always: *r.Replicas}, nil
	case r.UsageBase != nil:
		u := r.UsageBase
		if u.Trigger != "runner_allocated" {
			return nil, failf(".idle.usage_based.trigger", "trigger must be runner_allocated")
		}
		if u.Replicas < 1 || u.Replicas > maxIdleReplicas {
			return nil, failf(".idle.usage_based.replicas", "replicas must be between 1 and %d", maxIdleReplicas)
		}
		if u.Duration < 1 || u.Duration > maxIdleMinutes {
			return nil, failf(".idle.usage_based.duration", "duration must be between 1 and %d minutes", maxIdleMinutes)
		}

		return &Idle{Usage: &Usage{Replicas: u.Replicas, Duration: time.Duration(u.Duration) * time.Minute}}, nil
	default:
		windows := make([]Window, 0, len(r.Schedule))
		for i, w := range r.Schedule {
			window, ferr := w.validate()
			if ferr != nil {
				ferr.path = fmt.Sprintf(".idle.schedule[%d]%s", i, ferr.path)

				return nil, ferr
			}
			windows = append(windows, window)
		}

		return &Idle{Schedule: windows}, nil
	}
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// iopsRange is what EC2 accepts for each volume type that takes Iops.
var iopsRange = map[string][2]int{"gp3": {3000, 16000}, "io1": {100, 64000}, "io2": {100, 256000}}

const (
	gp3DefaultIOPS = 3000
	// gp3 throughput may not exceed a quarter MiB/s per provisioned IOPS.
	gp3ThroughputPerIOPS = 0.25
	gp3MinThroughput     = 125
	gp3MaxThroughput     = 1000
)

// validateDiskPerformance checks Iops and Throughput against what EC2
// accepts for the volume type, so a bad combination is a config error
// rather than a failed launch per fallback attempt.
func validateDiskPerformance(volumeType string, iops, throughput int) *fieldError {
	const iopsPath, throughputPath = ".extra_config.BlockDeviceMappings[0].Iops", ".extra_config.BlockDeviceMappings[0].Throughput"
	if iops < 0 {
		return failf(iopsPath, "Iops must be positive")
	}
	if throughput < 0 {
		return failf(throughputPath, "Throughput must be positive")
	}
	allowed, takesIOPS := iopsRange[volumeType]
	switch {
	case iops > 0 && !takesIOPS:
		return failf(iopsPath, "Iops only applies to gp3, io1 and io2 volumes")
	case iops == 0 && (volumeType == "io1" || volumeType == "io2"):
		return failf(iopsPath, "%s volumes need Iops", volumeType)
	case iops > 0 && (iops < allowed[0] || iops > allowed[1]):
		return failf(iopsPath, "Iops for %s must be between %d and %d", volumeType, allowed[0], allowed[1])
	}
	if throughput > 0 {
		if volumeType != "gp3" {
			return failf(throughputPath, "Throughput only applies to gp3 volumes")
		}
		if throughput < gp3MinThroughput || throughput > gp3MaxThroughput {
			return failf(throughputPath, "Throughput must be between %d and %d MiB/s", gp3MinThroughput, gp3MaxThroughput)
		}
		if iops == 0 {
			iops = gp3DefaultIOPS
		}
		if float64(throughput) > float64(iops)*gp3ThroughputPerIOPS {
			return failf(throughputPath, "Throughput of %d MiB/s needs Iops of at least %d (a quarter MiB/s per IOPS)", throughput, int(float64(throughput)/gp3ThroughputPerIOPS))
		}
	}

	return nil
}

var timeRangePattern = regexp.MustCompile(`^([0-9]{1,2})-([0-9]{1,2})$`)

func (w rawWindow) validate() (Window, *fieldError) {
	var out Window
	first, last, isRange := strings.Cut(strings.ToLower(strings.TrimSpace(w.Day)), "-")
	if !isRange {
		last = first
	}
	from, ok1 := weekdays[first]
	to, ok2 := weekdays[last]
	if !ok1 || !ok2 {
		return Window{}, failf(".day", "day must be a day (mon) or a range of days (mon-fri)")
	}
	// Ranges may wrap around the weekend: fri-mon.
	for d := from; ; d = (d + 1) % 7 {
		out.Days[d] = true
		if d == to {
			break
		}
	}

	hours := timeRangePattern.FindStringSubmatch(strings.TrimSpace(w.Time))
	if hours == nil {
		return Window{}, failf(".time", "time must be a range of hours such as 9-17 (from 9:00 until 17:00)")
	}
	start, _ := strconv.Atoi(hours[1])
	end, _ := strconv.Atoi(hours[2])
	if end > 24 || start >= end {
		return Window{}, failf(".time", "time must be a range of hours such as 9-17 (from 9:00 until 17:00)")
	}
	out.StartHour, out.EndHour = start, end

	tz := strings.TrimSpace(w.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || strings.Contains(tz, "..") {
		return Window{}, failf(".timezone", "%q is not a time zone from the tz database", w.Timezone)
	}
	out.Location = loc

	if w.Replicas == nil {
		return Window{}, failf(".replicas", "replicas is required")
	}
	if *w.Replicas < 0 || *w.Replicas > maxIdleReplicas {
		return Window{}, failf(".replicas", "replicas must be between 0 and %d", maxIdleReplicas)
	}
	out.Replicas = *w.Replicas

	return out, nil
}

// Replicas is how many runners the schedule wants idle at now. Overlapping
// windows take the largest count. Usage-based idle is handled by the
// caller, which knows when a runner was last allocated.
func (i *Idle) Replicas(now time.Time) int {
	if i == nil {
		return 0
	}
	if i.Usage != nil {
		return 0
	}
	if len(i.Schedule) == 0 {
		return i.Always
	}
	wanted := 0
	for _, w := range i.Schedule {
		local := now.In(w.Location)
		if w.Days[local.Weekday()] && local.Hour() >= w.StartHour && local.Hour() < w.EndHour {
			wanted = max(wanted, w.Replicas)
		}
	}

	return wanted
}
