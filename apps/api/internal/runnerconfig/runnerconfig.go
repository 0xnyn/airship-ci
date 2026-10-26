// Package runnerconfig parses runners-as-code: the .cirun.yml file in a
// repository, and the organization-wide .cirun.global.yml and .access.yml
// files in the organization's .cirun repository.
//
// The format is Cirun's: a list of runners, each with a cloud, machine
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
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// File names and conventions, as Cirun defines them.
const (
	// RepoFile is read from the repository a job runs in.
	RepoFile = ".cirun.yml"
	// GlobalRepo is the organization repository holding shared files.
	GlobalRepo = ".cirun"
	// GlobalFile defines runners every activated repository may use.
	GlobalFile = ".cirun.global.yml"
	// AccessFile restricts who may launch the global runners.
	AccessFile = ".access.yml"
	// LabelPrefix starts every label a config file defines. Only jobs that
	// ask for such a label cause config files to be read, so ordinary jobs
	// cost no GitHub API calls.
	LabelPrefix = "cirun-"
	// SuffixSeparator splits a runs-on label into the label a config file
	// defines and a suffix, such as the run ID, that makes the runner unique
	// to one workflow run: cirun-gpu--${{ github.run_id }}.
	SuffixSeparator = "--"
	// MaxFileBytes bounds every config file.
	MaxFileBytes = 64 << 10
)

// Clouds a runner can name. Cirun's other clouds (gcp, azure, do,
// openstack, oracle, vast_ai, on_prem) are recognized so the error says
// they are not supported here rather than "unknown cloud".
const (
	CloudAWS = "aws"
)

var otherClouds = []string{"gcp", "azure", "do", "digitalocean", "openstack", "oracle", "vast_ai", "on_prem"}

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
	// DefaultDiskGB and DefaultDiskType are Cirun's root volume defaults.
	DefaultDiskGB   = 75
	DefaultDiskType = "gp2"
)

var (
	namePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	labelPattern        = regexp.MustCompile(`^cirun-[a-z0-9][a-z0-9._-]{0,62}$`)
	jobLabelPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	instancePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)
	amiPattern          = regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)
	regionPattern       = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
	envNamePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	subnetPattern       = regexp.MustCompile(`^subnet-[0-9a-f]{8,17}$`)
	sgPattern           = regexp.MustCompile(`^sg-[0-9a-f]{8,17}$`)
	profilePattern      = regexp.MustCompile(`^(arn:aws[a-z-]*:iam::[0-9]{12}:instance-profile/)?[A-Za-z0-9+=,.@_/-]{1,128}$`)
	windowsPathPattern  = regexp.MustCompile(`^[A-Za-z]:\\[A-Za-z0-9_.\\-]{1,200}$`)
	windowsUserPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)
	imageAliasPattern   = regexp.MustCompile(`^ubuntu(2204|2404)$`)
	maxSecurityGroupIDs = 5
	// Graviton families have a "g" after the generation number: c7g, m6gd,
	// t4g, c7gn, x2gd, is4gen. GPU families such as g5 and g4dn do not.
	gravitonPattern = regexp.MustCompile(`^(a1|[a-z]+[0-9]+[a-z]*g[a-z]*)$`)
	// GPU families: g*, p*, gr*, inf*, trn*, dl*.
	gpuFamilyPattern = regexp.MustCompile(`^(g[0-9]|gr[0-9]|p[0-9]|inf[0-9]|trn[0-9]|dl[0-9])`)
)

// File is a parsed .cirun.yml or .cirun.global.yml.
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
	// GPU asks for NVIDIA drivers on the machine (Cirun's gpu: field).
	GPU bool
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
	Idle             *Idle
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
	// Image is an AMI ID, an alias such as ubuntu2204, or empty for the
	// cloud's default Ubuntu.
	Image  string
	Region string
	Spot   bool
	Arch   string
	Disk   Disk
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

// rawExtra is Cirun's extra_config for AWS. Keys from its other clouds
// are rejected as unknown fields, which names the key in the error.
type rawExtra struct {
	BlockDeviceMappings []rawDisk         `yaml:"BlockDeviceMappings"`
	EnvVars             map[string]string `yaml:"env_vars"`
	Cache               bool              `yaml:"cache"`
	ProvisionVia        string            `yaml:"provision_via"`
	RunAs               string            `yaml:"run_as"`
	RunnerUser          string            `yaml:"runner_user"`
	RunnerPath          string            `yaml:"runner_path"`
	SubnetID            string            `yaml:"SubnetId"`
	SecurityGroupIDs    []string          `yaml:"SecurityGroupIds"`
	IamInstanceProfile  string            `yaml:"IamInstanceProfile"`
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
	Replicas int    `yaml:"replicas"`
}

type rawUsageIdle struct {
	Trigger  string `yaml:"trigger"`
	Replicas int    `yaml:"replicas"`
	Duration int    `yaml:"duration"`
}

// listOf accepts a scalar or a list of scalars, as Cirun does for fallbacks.
type listOf []string

func (l *listOf) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*l = listOf{node.Value}

		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: expected a value or a list of values", node.Line)
	}
	out := make(listOf, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: expected a value, not a %s", item.Line, kindName(item.Kind))
		}
		out = append(out, item.Value)
	}
	*l = out

	return nil
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
	case cloud == CloudAWS:
		out.Cloud = CloudAWS
	case cloud == "":
		return Runner{}, failf(".cloud", "cloud is required: aws")
	case slices.Contains(otherClouds, cloud):
		return Runner{}, failf(".cloud", "cloud %q is not supported yet; only aws is", r.Cloud)
	default:
		return Runner{}, failf(".cloud", "cloud %q is unknown; use aws", r.Cloud)
	}
	if r.GPU.Kind != 0 {
		if r.GPU.Kind != yaml.ScalarNode || strings.TrimSpace(r.GPU.Value) == "" {
			return Runner{}, failf(".gpu", "gpu must be a GPU name, such as nvidia-tesla-t4")
		}
		out.GPU = true
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

	variants, ferr := expandVariants(r, disk)
	if ferr != nil {
		return Runner{}, ferr
	}
	out.Variants = variants

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
	if e == nil {
		return disk, nil
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
		if d.Iops < 0 || d.Iops > 256000 {
			return Disk{}, failf(".extra_config.BlockDeviceMappings[0].Iops", "Iops must be between 0 and 256000")
		}
		if d.Throughput < 0 || d.Throughput > 2000 {
			return Disk{}, failf(".extra_config.BlockDeviceMappings[0].Throughput", "Throughput must be between 0 and 2000 MiB/s")
		}
		if d.Throughput > 0 && disk.Type != "gp3" {
			return Disk{}, failf(".extra_config.BlockDeviceMappings[0].Throughput", "Throughput only applies to gp3 volumes")
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
func expandVariants(r rawRunner, disk Disk) ([]Variant, *fieldError) {
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
		if v.Region != "" && !regionPattern.MatchString(v.Region) {
			return nil, failf(".region"+index, "%q is not an AWS region (such as us-east-1)", v.Region)
		}
		if !instancePattern.MatchString(v.InstanceType) || !strings.Contains(v.InstanceType, ".") {
			return nil, failf(".instance_type"+index, "%q is not an EC2 instance type (such as c7i.xlarge)", v.InstanceType)
		}
		if v.Image != "" && !amiPattern.MatchString(v.Image) && !imageAliasPattern.MatchString(v.Image) {
			return nil, failf(".machine_image"+index, "%q must be an AMI ID (ami-...), ubuntu2204 or ubuntu2404", v.Image)
		}
		v.Arch = ArchForInstanceType(v.InstanceType)
		out = append(out, v)
	}

	return out, nil
}

// ArchForInstanceType infers the CPU architecture of an EC2 instance type.
func ArchForInstanceType(instanceType string) string {
	family, _, _ := strings.Cut(instanceType, ".")
	if gravitonPattern.MatchString(family) {
		return "arm64"
	}

	return "x64"
}

// HasGPU reports whether an EC2 instance family carries an accelerator.
func HasGPU(instanceType string) bool {
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

	var start, end int
	if _, err := fmt.Sscanf(strings.TrimSpace(w.Time), "%d-%d", &start, &end); err != nil || start < 0 || end > 24 || start >= end {
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

	if w.Replicas < 0 || w.Replicas > maxIdleReplicas {
		return Window{}, failf(".replicas", "replicas must be between 0 and %d", maxIdleReplicas)
	}
	out.Replicas = w.Replicas

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
