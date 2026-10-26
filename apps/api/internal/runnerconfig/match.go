package runnerconfig

import (
	"io"
	"regexp"
	"slices"
	"strings"
)

var errEOF = io.EOF

var (
	yamlTypeName  = regexp.MustCompile(` in type runnerconfig\.raw[A-Za-z]+`)
	yamlUnmarshal = regexp.MustCompile(`^yaml: (unmarshal errors:\n\s*)?`)
	yamlCannot    = regexp.MustCompile("cannot unmarshal !!\\w+ (`[^`]*`|[^ ]+) into ([][\\w.]+)")
)

// cleanYAMLError drops the parser's Go type names from its messages.
func cleanYAMLError(err error) error {
	msg := yamlTypeName.ReplaceAllString(err.Error(), "")
	msg = yamlUnmarshal.ReplaceAllString(msg, "")
	msg = strings.ReplaceAll(msg, "field ", "unknown field ")
	msg = strings.ReplaceAll(msg, " not found", "")
	msg = yamlCannot.ReplaceAllStringFunc(msg, func(m string) string {
		parts := yamlCannot.FindStringSubmatch(m)
		want := map[string]string{"[]string": "a list", "int": "a number", "bool": "true or false", "map[string]string": "a mapping"}[parts[2]]
		if want == "" {
			want = "a different kind of value"
		}

		return "expected " + want + ", not " + parts[1]
	})

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
func Plan(jobLabels []string, candidates []Candidate) []Attempt {
	var wanted, suffixed, other []string
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
		suffixed = append(suffixed, strings.ToLower(label))
	}
	if len(wanted) == 0 {
		return nil
	}

	var out []Attempt
	for _, c := range candidates {
		if !coversAll(c.Runner.Labels, wanted) {
			continue
		}
		for _, v := range c.Runner.Variants {
			labels := registeredLabels(c.Runner.Cloud, v.Arch, c.Runner.GPU || HasGPU(v.InstanceType), c.Runner.Labels, suffixed)
			if !coversAll(withAliases(labels), other) {
				continue
			}
			out = append(out, Attempt{Source: c.Source, Runner: c.Runner, Variant: v, Labels: labels})
		}
	}

	return out
}

// registeredLabels are what a runner registers with GitHub: the defaults
// every self-hosted runner carries, the cloud, whether it has a GPU (as
// Cirun labels them), the definition's labels and the job's own suffixed
// labels. The OS label is left to the runner, which knows what it runs on.
func registeredLabels(cloud string, arch string, gpu bool, defined, suffixed []string) []string {
	labels := []string{"self-hosted", arch, cloud, "cpu"}
	if gpu {
		labels[3] = "gpu"
	}
	for _, l := range slices.Concat(defined, suffixed) {
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}

	return labels
}

// withAliases adds the other spellings GitHub accepts for default labels,
// and the OS label the runner adds itself.
func withAliases(labels []string) []string {
	out := append(slices.Clone(labels), "linux", "windows")
	if slices.Contains(labels, "x64") {
		out = append(out, "x86_64")
	}

	return out
}

func coversAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}

	return true
}

// PoolAttempts are the attempts for a runner's warm pool: each variant,
// registered without any job's suffix, so any job asking for the
// runner's plain labels can take it.
func PoolAttempts(r Runner) []Attempt {
	out := make([]Attempt, 0, len(r.Variants))
	for _, v := range r.Variants {
		out = append(out, Attempt{Source: SourceRepository, Runner: r, Variant: v, Labels: registeredLabels(r.Cloud, v.Arch, r.GPU || HasGPU(v.InstanceType), r.Labels, nil)})
	}

	return out
}

// Covers reports whether a runner registered with labels can take a job
// asking for jobLabels, as GitHub decides it: every job label, compared
// case-insensitively, with the x64 alias GitHub accepts.
func Covers(labels, jobLabels []string) bool {
	lower := make([]string, 0, len(jobLabels))
	for _, l := range jobLabels {
		lower = append(lower, strings.ToLower(l))
	}

	return coversAll(withAliases(labels), lower)
}
