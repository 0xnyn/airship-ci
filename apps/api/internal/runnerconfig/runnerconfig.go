// Package runnerconfig parses runners-as-code: the .airship.yml file in a
// repository, and the organization-wide .airship.global.yml and .access.yml
// files in the organization's .airship repository.
//
// The format follows Cirun's .cirun.yml so teams can move between the two:
// a list of runners, each with a cloud, machine shape and labels. List
// values for instance_type, machine_image, region and preemptible describe
// fallbacks, tried in order when a launch fails. Where Cirun holds cloud
// credentials and picks a region itself, Airship launches through the
// customer's own data plane agents, so cloud and region select a data plane.
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

// File names and conventions.
const (
	// RepoFile is read from the repository a job runs in.
	RepoFile = ".airship.yml"
	// GlobalRepo is the organization repository holding shared files.
	GlobalRepo = ".airship"
	// GlobalFile defines runners every activated repository may use.
	GlobalFile = ".airship.global.yml"
	// AccessFile restricts who may launch the global runners.
	AccessFile = ".access.yml"
	// LabelPrefix starts every label a config file defines. Only jobs that
	// ask for such a label cause Airship to read config files, so ordinary
	// jobs cost no GitHub API calls.
	LabelPrefix = "airship-"
	// SuffixSeparator splits a runs-on label into the label a config file
	// defines and a suffix, such as the run ID, that makes the runner unique
	// to one workflow run: airship-gpu--${{ github.run_id }}.
	SuffixSeparator = "--"
	// MaxFileBytes bounds every config file.
	MaxFileBytes = 64 << 10
)

// Clouds a runner can name. Both on_prem (Cirun's name) and docker select
// data planes whose agent runs the Docker provider.
const (
	CloudAWS    = "aws"
	CloudOnPrem = "on_prem"
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
	defaultDiskGB   = 75
	maxLabelLength  = 100
)

var (
	namePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	labelPattern        = regexp.MustCompile(`^airship-[a-z0-9][a-z0-9._-]{0,62}$`)
	jobLabelPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	instancePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)
	shapePattern        = regexp.MustCompile(`^\d{1,3}vcpu-\d{1,4}gb(-\d{1,5}gb)?$`)
	amiPattern          = regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)
	ssmPattern          = regexp.MustCompile(`^resolve:ssm:/[A-Za-z0-9_./-]{1,1000}$`)
	dockerImagePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,254}$`)
	regionPattern       = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	dataPlaneNameLength = 63
	envNamePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// Graviton families have a "g" after the generation number: c7g, m6gd,
	// t4g, c7gn, x2gd, is4gen. GPU families such as g5 and g4dn do not.
	gravitonPattern = regexp.MustCompile(`^(a1|[a-z]+[0-9]+[a-z]*g[a-z]*)$`)
)

// File is a parsed .airship.yml or .airship.global.yml.
type File struct {
	Runners []Runner
}

// Runner is one runner definition.
type Runner struct {
	Name  string
	Cloud string
	// DataPlane, when set, names the one data plane to launch on.
	DataPlane string
	// Labels are the runs-on labels this runner serves, all prefixed.
	Labels []string
	// Variants are the machine shapes to try, in order.
	Variants []Variant
	// Env is set in the runner's environment for every job.
	Env map[string]string
	// Privileged runs on_prem containers privileged, for docker-in-docker.
	Privileged bool
	Idle       *Idle
}

// Variant is one machine shape a runner may launch as.
type Variant struct {
	InstanceType string
	// Image is an AMI, resolve:ssm:<parameter> or a container image; empty
	// means the agent's default.
	Image  string
	Region string
	Spot   bool
	Arch   string
	Disk   Disk
}

// Disk is the root volume. Type, IOPS and Throughput are AWS-only and zero
// means the provider's default.
type Disk struct {
	SizeGB     int
	Type       string
	IOPS       int
	Throughput int
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
		if runner.Idle != nil && name == GlobalFile {
			// An idle runner would be registered on the organization, where
			// any repository could take it, activated or not.
			return File{}, &Error{File: name, Path: path + ".idle", Detail: "idle runners are defined in each repository's " + RepoFile}
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
	Name         string     `yaml:"name"`
	Cloud        string     `yaml:"cloud"`
	DataPlane    string     `yaml:"data_plane"`
	Arch         string     `yaml:"arch"`
	InstanceType listOf     `yaml:"instance_type"`
	MachineImage listOf     `yaml:"machine_image"`
	Region       listOf     `yaml:"region"`
	Preemptible  listOf     `yaml:"preemptible"`
	Labels       []string   `yaml:"labels"`
	ExtraConfig  *rawExtra  `yaml:"extra_config"`
	Idle         *rawIdle   `yaml:"idle"`
	GPU          *yaml.Node `yaml:"gpu"`
}

type rawExtra struct {
	BlockDeviceMappings []rawDisk         `yaml:"BlockDeviceMappings"`
	EnvVars             map[string]string `yaml:"env_vars"`
	Executor            string            `yaml:"executor"`
	Privileged          bool              `yaml:"privileged"`
}

type rawDisk struct {
	DeviceName string `yaml:"DeviceName"`
	VolumeSize int    `yaml:"VolumeSize"`
	VolumeType string `yaml:"VolumeType"`
	Iops       int    `yaml:"Iops"`
	Throughput int    `yaml:"Throughput"`
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
	out := Runner{Name: strings.TrimSpace(r.Name), DataPlane: strings.TrimSpace(r.DataPlane)}
	if !namePattern.MatchString(out.Name) {
		return Runner{}, failf(".name", "name is required: letters, digits, '.', '_' or '-', up to 63 characters")
	}

	switch strings.ToLower(r.Cloud) {
	case CloudAWS:
		out.Cloud = CloudAWS
	case CloudOnPrem, "docker":
		out.Cloud = CloudOnPrem
	case "":
		return Runner{}, failf(".cloud", "cloud is required: aws or on_prem")
	default:
		return Runner{}, failf(".cloud", "cloud %q is not supported; Airship launches through your data plane agents, which support aws and on_prem", r.Cloud)
	}
	if len(out.DataPlane) > dataPlaneNameLength {
		return Runner{}, failf(".data_plane", "data plane names are at most %d characters", dataPlaneNameLength)
	}
	if r.GPU != nil {
		return Runner{}, failf(".gpu", "gpu is not needed on Airship: pick a GPU instance type (g5.xlarge, g6.xlarge, ...) and an image with drivers")
	}

	labels, ferr := validateLabels(r.Labels)
	if ferr != nil {
		return Runner{}, ferr
	}
	out.Labels = labels

	if r.Arch != "" && r.Arch != "x64" && r.Arch != "arm64" {
		return Runner{}, failf(".arch", "arch must be x64 or arm64")
	}

	disk, env, privileged, ferr := r.ExtraConfig.validate(out.Cloud)
	if ferr != nil {
		return Runner{}, ferr
	}
	out.Env, out.Privileged = env, privileged

	variants, ferr := expandVariants(r, out.Cloud, disk)
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

func (e *rawExtra) validate(cloud string) (Disk, map[string]string, bool, *fieldError) {
	disk := Disk{SizeGB: defaultDiskGB}
	if e == nil {
		return disk, nil, false, nil
	}

	if len(e.BlockDeviceMappings) > 1 {
		return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings", "only the root volume can be configured")
	}
	if len(e.BlockDeviceMappings) == 1 {
		if cloud != CloudAWS {
			return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings", "BlockDeviceMappings only applies to aws; size on_prem disks in instance_type")
		}
		d := e.BlockDeviceMappings[0]
		if d.VolumeSize != 0 {
			if d.VolumeSize < 8 || d.VolumeSize > 16384 {
				return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings[0].VolumeSize", "VolumeSize must be between 8 and 16384 GiB")
			}
			disk.SizeGB = d.VolumeSize
		}
		switch d.VolumeType {
		case "", "gp3", "gp2", "io1", "io2":
			disk.Type = d.VolumeType
		default:
			return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings[0].VolumeType", "VolumeType must be gp3, gp2, io1 or io2")
		}
		if d.Iops < 0 || d.Iops > 256000 {
			return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings[0].Iops", "Iops must be between 0 and 256000")
		}
		if d.Throughput < 0 || d.Throughput > 2000 {
			return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings[0].Throughput", "Throughput must be between 0 and 2000 MiB/s")
		}
		if d.Throughput > 0 && disk.Type != "" && disk.Type != "gp3" {
			return Disk{}, nil, false, failf(".extra_config.BlockDeviceMappings[0].Throughput", "Throughput only applies to gp3 volumes")
		}
		disk.IOPS, disk.Throughput = d.Iops, d.Throughput
	}

	switch e.Executor {
	case "", "docker":
	default:
		return Disk{}, nil, false, failf(".extra_config.executor", "executor %q is not supported; the Airship agent runs on_prem runners as docker containers", e.Executor)
	}
	if e.Privileged && cloud != CloudOnPrem {
		return Disk{}, nil, false, failf(".extra_config.privileged", "privileged only applies to on_prem runners")
	}

	env, ferr := validateEnv(e.EnvVars)
	if ferr != nil {
		return Disk{}, nil, false, ferr
	}

	return disk, env, e.Privileged, nil
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
func expandVariants(r rawRunner, cloud string, disk Disk) ([]Variant, *fieldError) {
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
			Arch:         r.Arch,
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
			return nil, failf(".region"+index, "%q is not a region", v.Region)
		}

		switch cloud {
		case CloudAWS:
			if !instancePattern.MatchString(v.InstanceType) || !strings.Contains(v.InstanceType, ".") {
				return nil, failf(".instance_type"+index, "%q is not an EC2 instance type (such as c7i.xlarge)", v.InstanceType)
			}
			if v.Image != "" && !amiPattern.MatchString(v.Image) && !ssmPattern.MatchString(v.Image) {
				return nil, failf(".machine_image"+index, "%q must be an AMI ID (ami-...) or resolve:ssm:<parameter>", v.Image)
			}
			if v.Arch == "" {
				v.Arch = ArchForInstanceType(v.InstanceType)
			}
		case CloudOnPrem:
			if !shapePattern.MatchString(v.InstanceType) {
				return nil, failf(".instance_type"+index, "%q must be a shape like 4vcpu-8gb or 4vcpu-8gb-50gb", v.InstanceType)
			}
			if v.Image != "" && !dockerImagePattern.MatchString(v.Image) {
				return nil, failf(".machine_image"+index, "%q is not a container image reference", v.Image)
			}
			if v.Spot {
				return nil, failf(".preemptible"+index, "preemptible does not apply to on_prem runners")
			}
			if v.Arch == "" {
				v.Arch = "x64"
			}
		}
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
