package runnerconfig

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

var errEOF = io.EOF

var (
	yamlTypeName  = regexp.MustCompile(` in type (\[\])?runnerconfig\.[A-Za-z]+`)
	yamlUnmarshal = regexp.MustCompile(`^yaml: (unmarshal errors:\n\s*)?`)
	yamlCannot    = regexp.MustCompile("cannot unmarshal !!(\\w+)(?: (`[^`]*`|[^ ]+))? into ((?:\\[\\])?[\\w.]+)")
)

// maxErrorLines bounds a YAML error: the parser reports every problem in
// the file, and a long one would not fit where the message is stored.
const maxErrorLines = 8

// cleanYAMLError drops the parser's Go type names from its messages and
// bounds their length.
func cleanYAMLError(err error) error {
	msg := yamlTypeName.ReplaceAllString(err.Error(), "")
	msg = yamlUnmarshal.ReplaceAllString(msg, "")
	msg = strings.ReplaceAll(msg, "field ", "unknown field ")
	msg = strings.ReplaceAll(msg, " not found", "")
	msg = yamlCannot.ReplaceAllStringFunc(msg, func(m string) string {
		parts := yamlCannot.FindStringSubmatch(m)
		want := map[string]string{"[]string": "a list", "int": "a number", "bool": "true or false", "map[string]string": "a mapping"}[parts[3]]
		switch {
		case want != "":
		case strings.HasPrefix(parts[3], "[]"):
			want = "a list"
		case strings.HasPrefix(parts[3], "runnerconfig."):
			want = "a mapping"
		default:
			want = "a different kind of value"
		}
		got := parts[2]
		if got == "" {
			got = map[string]string{"map": "a mapping", "seq": "a list", "str": "text", "null": "nothing"}[parts[1]]
		}
		if got == "" {
			got = parts[1]
		}

		return "expected " + want + ", not " + got
	})
	if lines := strings.Split(msg, "\n"); len(lines) > maxErrorLines {
		msg = strings.Join(lines[:maxErrorLines], "\n") + fmt.Sprintf("\n... and %d more", len(lines)-maxErrorLines)
	}

	return &plainError{msg: msg}
}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// BaseLabel returns the label a config file would define for a runs-on
// label: lowercased, without its per-run suffix. ok is false for labels
// that are not config labels.
func BaseLabel(label string) (string, bool) {
	lower := strings.ToLower(label)
	if !strings.HasPrefix(lower, LabelPrefix) {
		return "", false
	}
	base, _, _ := strings.Cut(lower, SuffixSeparator)

	return base, true
}

// WantsConfig reports whether a job asks for any config-defined label, and
// so whether its config files need reading at all.
func WantsConfig(jobLabels []string) bool {
	for _, label := range jobLabels {
		if _, ok := BaseLabel(label); ok {
			return true
		}
	}

	return false
}

// Source says where a runner definition came from.
type Source string

// Sources, in the order they are searched.
const (
	SourceRepository   Source = "repository"
	SourceOrganization Source = "organization"
)

// Candidate is a runner definition from one source.
type Candidate struct {
	Source Source
	Runner Runner
}

// Attempt is one way to serve a job: a runner definition, one of its
// machine shapes, and the labels the runner must register with.
type Attempt struct {
	Source  Source
	Runner  Runner
	Variant Variant
	// Labels are what the runner registers with GitHub: the defaults, the
	// definition's labels and the job's own suffixed labels, so the runner
	// is only handed jobs from the same run.
	Labels []string
}

// Plan resolves the attempts that could serve a job, in fallback order:
// every definition whose labels cover the job's config labels, repository
// definitions first, each with its variants in order. A variant is left out
// when the runner it would launch could not take the job (an arm64 job
// label on an x64 machine, say). An empty plan means nothing matches.
//
// A job that suffixes its labels (airship-build--<run id>) gets a runner
// registered with those labels only, so GitHub can hand it nothing but a
// job from that run; otherwise the runner carries the definition's labels
// and any job asking for them may take it.
func Plan(jobLabels []string, candidates []Candidate) []Attempt {
	var wanted, asked, other []string
	var suffixed bool
	for _, label := range jobLabels {
		if !jobLabelPattern.MatchString(label) || len(label) > maxLabelLength {
			return nil
		}
		base, ok := BaseLabel(label)
		if !ok {
			other = append(other, strings.ToLower(label))

			continue
		}
		wanted = append(wanted, base)
		asked = append(asked, strings.ToLower(label))
		suffixed = suffixed || base != strings.ToLower(label)
	}
	if len(wanted) == 0 {
		return nil
	}

	var out []Attempt
	for _, c := range candidates {
		if !coversAll(c.Runner.Labels, wanted) {
			continue
		}
		defined := c.Runner.Labels
		if suffixed {
			defined = nil
		}
		for _, v := range c.Runner.Variants {
			labels := registeredLabels(c.Runner.Cloud, v.Arch, c.Runner.GPU || HasGPU(c.Runner.Cloud, v.InstanceType), defined, asked)
			if !coversAll(withOSAliases(labels, c.Runner.Cloud, v.Image), other) {
				continue
			}
			if slices.ContainsFunc(out, func(a Attempt) bool { return a.Runner.Name == c.Runner.Name && a.Variant == v }) {
				continue // the same machine twice (a label and its suffixed form) is one attempt
			}
			out = append(out, Attempt{Source: c.Source, Runner: c.Runner, Variant: v, Labels: labels})
		}
	}

	return out
}

// registeredLabels are what a runner registers with GitHub: the defaults
// every self-hosted runner carries, the cloud, whether it has a GPU (as
// they are labelled), the definition's labels and the job's own config
// labels. The OS label is left to the runner, which knows what it runs on.
func registeredLabels(cloud string, arch string, gpu bool, defined, asked []string) []string {
	labels := []string{"self-hosted", arch, cloud, "cpu", ProductLabel}
	if gpu {
		labels[3] = "gpu"
	}
	for _, l := range slices.Concat(defined, asked) {
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}

	return labels
}

// withAliases adds the OS labels a runner may add itself, for a machine
// whose image is not known.
func withAliases(labels []string) []string {
	return withOSAliases(labels, "", "unknown")
}

// withOSAliases adds the OS label the runner adds itself. A known Ubuntu
// image (an alias, none, which means the default, or a Compute Engine
// ubuntu-* family) is Linux and a windows-* image is Windows; a custom
// AMI could be either until it boots. Every Daytona sandbox is Linux.
func withOSAliases(labels []string, cloud, image string) []string {
	switch {
	case cloud == CloudDaytona, image == "" || imageAliasPattern.MatchString(image) || strings.HasPrefix(image, "ubuntu-"):
		return append(slices.Clone(labels), "linux")
	case isWindowsImageName(image):
		return append(slices.Clone(labels), "windows")
	default:
		return append(slices.Clone(labels), "linux", "windows")
	}
}

func coversAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}

	return true
}

// PoolAttempts are the attempts for a runner's idle pool: each variant,
// registered without any job's suffix, so any job asking for the
// runner's plain labels can take it.
func PoolAttempts(source Source, r Runner) []Attempt {
	out := make([]Attempt, 0, len(r.Variants))
	for _, v := range r.Variants {
		out = append(out, Attempt{Source: source, Runner: r, Variant: v, Labels: registeredLabels(r.Cloud, v.Arch, r.GPU || HasGPU(r.Cloud, v.InstanceType), r.Labels, nil)})
	}

	return out
}

// Covers reports whether a runner registered with labels can take a job
// asking for jobLabels, as GitHub decides it: every job label, compared
// case-insensitively.
func Covers(labels, jobLabels []string) bool {
	lower := make([]string, 0, len(jobLabels))
	for _, l := range jobLabels {
		lower = append(lower, strings.ToLower(l))
	}

	return coversAll(withAliases(labels), lower)
}
