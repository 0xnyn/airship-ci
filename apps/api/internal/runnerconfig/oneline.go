package runnerconfig

import (
	"regexp"
	"strings"
)

// One-line runners describe the machine in the label itself, so a
// workflow needs no config file, as Cirun's "one line change" runners:
//
//	runs-on: cirun-aws-t2-large-ubuntu2204--${{ github.run_id }}
//	runs-on: cirun-aws-c7g-xlarge-spot
//
// AWS labels are the instance type with "." written as "-", then optional
// tokens: an Ubuntu release (ubuntu2204, the default, or ubuntu2404) and
// "spot". They launch in the connected cloud's default region.
const oneLineAWS = LabelPrefix + "aws-"

var (
	// EC2 sizes: nano ... 112xlarge, metal, metal-24xl.
	sizePattern   = regexp.MustCompile(`^(nano|micro|small|medium|large|xlarge|[0-9]{1,3}xlarge|metal)$`)
	metalSizeTail = regexp.MustCompile(`^[0-9]{1,3}xl$`)
	// EC2 families: letters, a generation number (u-6tb1 puts a dash
	// before it), optional letters, and an optional dashed suffix such
	// as c7i-flex, u7i-12tb or mac2-m2. "spot" and the image tokens are
	// never families, so a label that misplaces them is refused rather
	// than launched as spot.large.
	familyPattern = regexp.MustCompile(`^[a-z]{1,8}-?[0-9]{1,2}[a-z0-9]{0,8}(-[a-z0-9]{1,8})?$`)
)

// OneLine reads a one-line runner from a runs-on label (with or without
// its suffix). ok is false for labels that are not one-line labels.
func OneLine(label string) (Runner, bool) {
	base, isConfig := BaseLabel(label)
	if !isConfig || !strings.HasPrefix(base, oneLineAWS) {
		return Runner{}, false
	}
	variant, ok := oneLineEC2(strings.TrimPrefix(base, oneLineAWS))
	if !ok || !labelPattern.MatchString(base) {
		return Runner{}, false
	}
	variant.Disk = Disk{SizeGB: DefaultDiskGB, Type: DefaultDiskType}

	return Runner{Name: base, Cloud: CloudAWS, Labels: []string{base}, Variants: []Variant{variant}}, true
}

func oneLineEC2(rest string) (Variant, bool) {
	tokens := strings.Split(rest, "-")
	var out Variant
	// Optional trailing tokens, in either order.
	for len(tokens) > 2 {
		last := tokens[len(tokens)-1]
		if last == "spot" && !out.Spot {
			out.Spot = true
		} else if imageAliasPattern.MatchString(last) && out.Image == "" {
			out.Image = last
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
	for _, token := range familyTokens {
		if token == "spot" || imageAliasPattern.MatchString(token) {
			return Variant{}, false
		}
	}
	out.InstanceType = family + "." + size
	out.Arch = ArchForInstanceType(out.InstanceType)

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
