package runnerconfig

import (
	"strings"
	"testing"
)

func TestOneLine(t *testing.T) {
	t.Parallel()
	ubuntu2204arm := ubuntuImages["ubuntu2204"]["arm64"]
	cases := []struct {
		label string
		want  *Variant
		cloud string
	}{
		{label: "airship-aws-c7i-xlarge", cloud: CloudAWS, want: &Variant{InstanceType: "c7i.xlarge", Arch: "x64"}},
		{label: "airship-aws-c7i-xlarge--12345", cloud: CloudAWS, want: &Variant{InstanceType: "c7i.xlarge", Arch: "x64"}},
		{label: "AIRSHIP-AWS-M7I-2XLARGE", cloud: CloudAWS, want: &Variant{InstanceType: "m7i.2xlarge", Arch: "x64"}},
		{label: "airship-aws-t4g-large-ubuntu2204-spot", cloud: CloudAWS, want: &Variant{InstanceType: "t4g.large", Arch: "arm64", Spot: true, Image: ubuntu2204arm}},
		{label: "airship-aws-t4g-large-spot-ubuntu2204", cloud: CloudAWS, want: &Variant{InstanceType: "t4g.large", Arch: "arm64", Spot: true, Image: ubuntu2204arm}},
		{label: "airship-aws-c6g-metal", cloud: CloudAWS, want: &Variant{InstanceType: "c6g.metal", Arch: "arm64"}},
		{label: "airship-aws-m7i-metal-24xl", cloud: CloudAWS, want: &Variant{InstanceType: "m7i.metal-24xl", Arch: "x64"}},
		{label: "airship-aws-c7i-48xlarge", cloud: CloudAWS, want: &Variant{InstanceType: "c7i.48xlarge", Arch: "x64"}},
		{label: "airship-onprem-4vcpu-8gb", cloud: CloudOnPrem, want: &Variant{InstanceType: "4vcpu-8gb", Arch: "x64"}},
		{label: "airship-onprem-4vcpu-8gb--99", cloud: CloudOnPrem, want: &Variant{InstanceType: "4vcpu-8gb", Arch: "x64"}},

		{label: "airship-build"},
		{label: "airship-aws-c7i"},
		{label: "airship-aws-c7i-huge"},
		{label: "airship-aws-spot"},
		{label: "airship-aws-c7i-xlarge-spot-spot"},
		{label: "airship-aws-c7i-xlarge-windows"},
		{label: "airship-onprem-big"},
		{label: "ubuntu-latest"},
		{label: "aws-c7i-xlarge"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got, ok := OneLine(tc.label)
			if tc.want == nil {
				if ok {
					t.Fatalf("OneLine(%q) = %+v, want not a one-line label", tc.label, got)
				}

				return
			}
			if !ok {
				t.Fatalf("OneLine(%q) not recognised", tc.label)
			}
			want := *tc.want
			want.Disk = Disk{SizeGB: defaultDiskGB}
			if got.Cloud != tc.cloud || len(got.Variants) != 1 || got.Variants[0] != want {
				t.Fatalf("OneLine(%q) = %s %+v, want %s %+v", tc.label, got.Cloud, got.Variants, tc.cloud, want)
			}
			base, _ := BaseLabel(tc.label)
			if len(got.Labels) != 1 || got.Labels[0] != base {
				t.Fatalf("labels = %v, want [%s]", got.Labels, base)
			}
		})
	}
}

func TestWithOneLinePlan(t *testing.T) {
	t.Parallel()
	file := mustParse(t, `
runners:
  - name: pinned
    cloud: aws
    instance_type: m7i.large
    labels: [airship-aws-c7i-xlarge]
`)
	repo := []Candidate{{Source: SourceRepository, Runner: file.Runners[0]}}

	// The file defines the label, so it wins and no one-line attempt is added.
	plan := Plan([]string{"airship-aws-c7i-xlarge"}, WithOneLine([]string{"airship-aws-c7i-xlarge"}, repo))
	if len(plan) != 1 || plan[0].Variant.InstanceType != "m7i.large" {
		t.Fatalf("defined label: plan = %+v", plan)
	}

	// Undefined labels become one-line runners registered with the suffix.
	jobLabels := []string{"airship-aws-t4g-large--77"}
	plan = Plan(jobLabels, WithOneLine(jobLabels, repo))
	if len(plan) != 1 || plan[0].Variant.InstanceType != "t4g.large" {
		t.Fatalf("one-line: plan = %+v", plan)
	}
	got := strings.Join(plan[0].Labels, ",")
	if got != "self-hosted,linux,arm64,airship-aws-t4g-large,airship-aws-t4g-large--77" {
		t.Fatalf("labels = %s", got)
	}

	// A job asking for x64 cannot run on a Graviton one-liner.
	jobLabels = []string{"x64", "airship-aws-t4g-large"}
	if plan := Plan(jobLabels, WithOneLine(jobLabels, nil)); len(plan) != 0 {
		t.Fatalf("arch mismatch: plan = %+v", plan)
	}
}
