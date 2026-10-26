package runnerconfig

import (
	"regexp"
	"strings"
)

// One-line runners describe the machine in the label itself, so a
// workflow needs no config file:
//
//	runs-on: airship-aws-c7i-xlarge--${{ github.run_id }}
//	runs-on: airship-aws-t4g-large-ubuntu2204-spot
//	runs-on: airship-onprem-4vcpu-8gb
//
// AWS labels are the instance type with "." written as "-", then optional
// tokens: an Ubuntu release (ubuntu2204, ubuntu2404; default: the agent's
// image) and "spot".
const (
	oneLineAWS    = LabelPrefix + "aws-"
	oneLineOnPrem = LabelPrefix + "onprem-"
)

var (
	// EC2 sizes: nano ... 48xlarge, metal, metal-24xl.
	sizePattern   = regexp.MustCompile(`^(nano|micro|small|medium|large|xlarge|[0-9]{1,2}xlarge|metal)$`)
	metalSizeTail = regexp.MustCompile(`^[0-9]{1,3}xl$`)
	familyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

// ubuntuImages maps one-line image tokens to Canonical's SSM parameters,
// by architecture (the volume type segment changed with 23.10).
var ubuntuImages = map[string]map[string]string{
	"ubuntu2204": {
		"x64":   "resolve:ssm:/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
		"arm64": "resolve:ssm:/aws/service/canonical/ubuntu/server/22.04/stable/current/arm64/hvm/ebs-gp2/ami-id",
	},
	"ubuntu2404": {
		"x64":   "resolve:ssm:/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id",
		"arm64": "resolve:ssm:/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id",
	},
}

// OneLine reads a one-line runner from a runs-on label (with or without
// its suffix). ok is false for labels that are not one-line labels.
func OneLine(label string) (Runner, bool) {
	base, isConfig := BaseLabel(label)
	if !isConfig {
		return Runner{}, false
	}
	var variant Variant
	cloud := ""
	switch {
	case strings.HasPrefix(base, oneLineAWS):
		cloud = CloudAWS
		v, ok := oneLineEC2(strings.TrimPrefix(base, oneLineAWS))
		if !ok {
			return Runner{}, false
		}
		variant = v
	case strings.HasPrefix(base, oneLineOnPrem):
		cloud = CloudOnPrem
		shape := strings.TrimPrefix(base, oneLineOnPrem)
		if !shapePattern.MatchString(shape) {
			return Runner{}, false
		}
		variant = Variant{InstanceType: shape, Arch: "x64"}
	default:
		return Runner{}, false
	}
	variant.Disk = Disk{SizeGB: defaultDiskGB}
	if !labelPattern.MatchString(base) {
		return Runner{}, false
	}

	return Runner{Name: base, Cloud: cloud, Labels: []string{base}, Variants: []Variant{variant}}, true
}

func oneLineEC2(rest string) (Variant, bool) {
	tokens := strings.Split(rest, "-")
	var out Variant
	image := ""
	// Optional trailing tokens, in either order.
	for len(tokens) > 2 {
		last := tokens[len(tokens)-1]
		if last == "spot" && !out.Spot {
			out.Spot = true
		} else if ubuntuImages[last] != nil && image == "" {
			image = last
		} else {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) < 2 {
		return Variant{}, false
	}
	size := tokens[len(tokens)-1]
	familyTokens := tokens[:len(tokens)-1]
	if metalSizeTail.MatchString(size) && len(familyTokens) > 1 && familyTokens[len(familyTokens)-1] == "metal" {
		size = "metal-" + size
		familyTokens = familyTokens[:len(familyTokens)-1]
	} else if !sizePattern.MatchString(size) {
		return Variant{}, false
	}
	family := strings.Join(familyTokens, "-")
	if !familyPattern.MatchString(family) {
		return Variant{}, false
	}
	out.InstanceType = family + "." + size
	out.Arch = ArchForInstanceType(out.InstanceType)
	if image != "" {
		out.Image = ubuntuImages[image][out.Arch]
	}

	return out, true
}

// WithOneLine adds one-line runners for the job's labels that no
// definition in candidates serves, after them: a config file always wins
// over a label that happens to parse as a one-line runner.
func WithOneLine(jobLabels []string, candidates []Candidate) []Candidate {
	out := candidates
	for _, label := range jobLabels {
		base, ok := BaseLabel(label)
		if !ok {
			continue
		}
		defined := false
		for _, c := range candidates {
			if coversAll(c.Runner.Labels, []string{base}) {
				defined = true

				break
			}
		}
		if defined {
			continue
		}
		if runner, ok := OneLine(label); ok {
			out = append(out, Candidate{Source: SourceRepository, Runner: runner})
		}
	}

	return out
}
