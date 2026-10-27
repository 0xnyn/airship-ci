package runnerconfig

import (
	"strings"
	"testing"
)

// Labels that misplace the spot or image tokens, or name no real family,
// are not one-line runners; large sizes and dashed families are.
func TestOneLineFamilies(t *testing.T) {
	t.Parallel()
	for label, want := range map[string]string{
		"airship-aws-spot-large":               "",
		"airship-aws-ubuntu2204-large":         "",
		"airship-aws-x-metal":                  "",
		"airship-aws-c7i-flex-large":           "c7i-flex.large",
		"airship-aws-u7i-12tb-224xlarge":       "u7i-12tb.224xlarge",
		"airship-aws-u-6tb1-112xlarge":         "u-6tb1.112xlarge",
		"airship-aws-mac2-m2-metal":            "mac2-m2.metal",
		"airship-aws-m7i-metal-24xl-spot":      "m7i.metal-24xl",
		"airship-aws-t3-large-ubuntu2404-spot": "t3.large",
	} {
		runner, ok := OneLine(label)
		got := ""
		if ok {
			got = runner.Variants[0].InstanceType
		}
		if got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

// The same machine asked for under a label and its suffixed form is one
// attempt, and a job asking for windows on a known Ubuntu image gets
// nothing rather than a machine that can never take it.
func TestPlanDedupesAndKnowsTheOS(t *testing.T) {
	t.Parallel()
	f := mustParse(t, `
runners:
  - name: a
    cloud: aws
    instance_type: c7i.large
    labels: [airship-a]
  - name: custom
    cloud: aws
    instance_type: c7i.large
    machine_image: ami-0123456789abcdef0
    labels: [airship-custom]
`)
	candidates := []Candidate{{Source: SourceRepository, Runner: f.Runners[0]}, {Source: SourceRepository, Runner: f.Runners[1]}}
	if plan := Plan([]string{"airship-a", "airship-a--1"}, candidates); len(plan) != 1 {
		t.Fatalf("label and its suffixed form = %d attempts, want 1", len(plan))
	}
	if plan := Plan([]string{"airship-a", "windows"}, candidates); len(plan) != 0 {
		t.Fatalf("windows on the default Ubuntu image = %d attempts, want 0", len(plan))
	}
	if plan := Plan([]string{"airship-a", "linux"}, candidates); len(plan) != 1 {
		t.Fatalf("linux on the default Ubuntu image = %d attempts, want 1", len(plan))
	}
	if plan := Plan([]string{"airship-custom", "windows"}, candidates); len(plan) != 1 {
		t.Fatalf("windows on a custom AMI = %d attempts, want 1 (unknown until it boots)", len(plan))
	}
	if plan := Plan([]string{"airship-a", "x86_64"}, candidates); len(plan) != 0 {
		t.Fatalf("x86_64 is not a label GitHub runners carry; plan = %d", len(plan))
	}
}

func TestInstanceTypeShape(t *testing.T) {
	t.Parallel()
	for value, ok := range map[string]bool{"c7i.large": true, "u-6tb1.112xlarge": true, "m7i.metal-24xl": true, "5.5": false, "c7i.": false, "c7i": false} {
		_, err := Parse(RepoFile, []byte("runners:\n  - name: a\n    cloud: aws\n    instance_type: \""+value+"\"\n    labels: [airship-a]\n"))
		if (err == nil) != ok {
			t.Errorf("instance_type %q: err = %v, want ok=%v", value, err, ok)
		}
	}
}

// The developer API's edits are validated before they are written, and a
// new policy never takes another repository's id.
func TestAccessApplyValidatesAndKeepsIDsUnique(t *testing.T) {
	t.Parallel()
	doc := AccessDocument{Version: 1, Policies: []PolicyDocument{{ID: "api-policy", Repo: "other"}}}
	for name, change := range map[string]AccessChange{
		"resource": {Repository: "api", Resources: []string{"not a runner!"}},
		"user":     {Repository: "api", Resources: []string{"gpu"}, Users: []string{"not a login!"}},
		"team":     {Repository: "api", Resources: []string{"gpu"}, Teams: []string{"ml team"}},
		"role":     {Repository: "api", Resources: []string{"gpu"}, Roles: []string{"owner!"}},
	} {
		if err := doc.Apply(change); err == nil || !strings.Contains(err.Error(), "is not") {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if len(doc.AccessControl) != 0 || len(doc.Policies) != 1 {
		t.Fatalf("a refused change touched the document: %+v", doc)
	}
	if err := doc.Apply(AccessChange{Repository: "acme/api", Resources: []string{"gpu"}, Users: []string{" alice "}}); err != nil {
		t.Fatal(err)
	}
	if len(doc.Policies) != 2 || doc.Policies[1].ID != "api-policy-2" || doc.Policies[1].Repo != "api" {
		t.Fatalf("policies = %+v, want api's own policy with a free id", doc.Policies)
	}
	access, err := doc.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if policies, restricted := access.Restricted("gpu", "api"); !restricted || len(policies) != 1 || policies[0].Users[0] != "alice" {
		t.Fatalf("compiled access = %+v, %v", policies, restricted)
	}
}
