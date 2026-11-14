package migration

import (
	"errors"
	"strings"
	"testing"
)

const ci = `name: CI
on: [push]

jobs:
  lint:
    runs-on: ubuntu-latest # fast
    steps:
      - run: make lint
  test:
    name: Test
    strategy:
      matrix:
        node: [20, 22]
    runs-on: [ubuntu-22.04]
    steps:
      - run: make test
  arm:
    runs-on: "ubuntu-24.04-arm"
    steps: []
  block:
    runs-on:
      - self-hosted
      - linux
    steps: []
  dynamic:
    runs-on: ${{ matrix.os }}
    steps: []
  grouped:
    runs-on:
      group: big
    steps: []
  done:
    runs-on: airship-build--${{ github.run_id }}
    steps: []
  call:
    uses: ./.github/workflows/reusable.yml
  windows:
    runs-on: windows-latest
    steps: []
`

func jobByID(t *testing.T, w Workflow, id string) Job {
	t.Helper()
	for _, j := range w.Jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %s", id)

	return Job{}
}

func TestParse(t *testing.T) {
	w := Parse(".github/workflows/ci.yml", []byte(ci))
	if w.Problem != "" {
		t.Fatal(w.Problem)
	}
	cases := []struct {
		id       string
		kind     Kind
		platform Platform
		image    string
		editable bool
		matrix   bool
	}{
		{"lint", KindHosted, PlatformLinuxX64, "ubuntu2404", true, false},
		{"test", KindHosted, PlatformLinuxX64, "ubuntu2204", true, true},
		{"arm", KindHosted, PlatformLinuxArm64, "ubuntu2404", true, false},
		{"block", KindSelfHosted, "", "", true, false},
		{"dynamic", KindExpression, "", "", false, false},
		{"grouped", KindGroup, "", "", false, false},
		{"done", KindAirship, "", "", false, false},
		{"call", KindReusable, "", "", false, false},
		{"windows", KindHosted, PlatformWindows, "", true, false},
	}
	for _, c := range cases {
		j := jobByID(t, w, c.id)
		if j.Kind != c.kind || j.Platform != c.platform || j.Image != c.image || j.Editable != c.editable || j.Matrix != c.matrix {
			t.Errorf("%s: got %+v", c.id, j)
		}
		if !j.Editable && j.Reason == "" {
			t.Errorf("%s: no reason given", c.id)
		}
	}
	if got := jobByID(t, w, "test").Name; got != "Test" {
		t.Errorf("name = %q", got)
	}
}

func TestParseProblems(t *testing.T) {
	for _, content := range []string{"jobs: [", "- a\n- b\n", "name: x\n"} {
		if w := Parse("x.yml", []byte(content)); w.Problem == "" {
			t.Errorf("%q: no problem reported", content)
		}
	}
}

func TestRewriteKeepsTheRestOfTheFile(t *testing.T) {
	w := Parse("ci.yml", []byte(ci))
	out, err := Rewrite([]byte(ci), w, []Assignment{
		{Job: "lint", Label: "airship-linux"},
		{Job: "test", Label: "airship-linux"},
		{Job: "arm", Label: "airship-arm"},
		{Job: "block", Label: "airship-linux"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"runs-on: ubuntu-latest # fast", "runs-on: airship-linux--${{ github.run_id }} # fast",
		"runs-on: [ubuntu-22.04]", "runs-on: airship-linux--${{ github.run_id }}-${{ strategy.job-index }}",
		`runs-on: "ubuntu-24.04-arm"`, "runs-on: airship-arm--${{ github.run_id }}",
		"runs-on:\n      - self-hosted\n      - linux\n", "runs-on: airship-linux--${{ github.run_id }}\n",
	).Replace(ci)
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	again := Parse("ci.yml", out)
	for _, id := range []string{"lint", "test", "arm", "block"} {
		if k := jobByID(t, again, id).Kind; k != KindAirship {
			t.Errorf("%s is %s after the rewrite", id, k)
		}
	}
}

func TestRewriteRefusesWhatItCannotEdit(t *testing.T) {
	w := Parse("ci.yml", []byte(ci))
	_, err := Rewrite([]byte(ci), w, []Assignment{{Job: "dynamic", Label: "airship-linux"}})
	if !errors.Is(err, ErrNotEditable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Rewrite([]byte(ci), w, []Assignment{{Job: "missing", Label: "airship-linux"}}); err == nil {
		t.Fatal("a missing job was accepted")
	}
}

func TestRewriteMultiByteAndCRLF(t *testing.T) {
	content := "name: \"Größe ✓\"\r\njobs:\r\n  büild:\r\n    runs-on: 'ubuntu-latest'\r\n    steps: []\r\n"
	w := Parse("ci.yml", []byte(content))
	out, err := Rewrite([]byte(content), w, []Assignment{{Job: "büild", Label: "airship-linux"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(content, "'ubuntu-latest'", "airship-linux--${{ github.run_id }}", 1)
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

func TestRewriteRefusesFoldedScalar(t *testing.T) {
	content := "jobs:\n  a:\n    runs-on: >-\n      ubuntu-latest\n    steps: []\n"
	j := Parse("ci.yml", []byte(content)).Jobs[0]
	if j.Editable {
		t.Fatal("a folded runs-on was marked editable")
	}
}

func TestIsWorkflowPath(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yml":         true,
		".github/workflows/ci.yaml":        true,
		".github/workflows/.yml":           false,
		".github/workflows/sub/ci.yml":     false,
		".github/workflows/README.md":      false,
		"airship.yaml":                     false,
		".github/workflows/../../evil.yml": false,
	} {
		if got := IsWorkflowPath(p); got != want {
			t.Errorf("%s: got %v", p, got)
		}
	}
}

func TestApplyNewConfig(t *testing.T) {
	src := Source{Workflows: map[string][]byte{".github/workflows/ci.yml": []byte(ci)}}
	files, err := Apply(src, Plan{
		Runners: []Runner{
			{Name: "linux", Cloud: "aws", InstanceType: "c7i.xlarge", MachineImage: "ubuntu2404", Preemptible: true},
			{Name: "arm", Cloud: "aws", InstanceType: "c7g.xlarge", Region: "eu-west-1"},
		},
		Moves: []Move{
			{Workflow: ".github/workflows/ci.yml", Job: "lint", Label: "airship-linux"},
			{Workflow: ".github/workflows/ci.yml", Job: "arm", Label: "airship-arm"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "airship.yaml" || !files[0].Created || files[1].Path != ".github/workflows/ci.yml" {
		t.Fatalf("files = %+v", files)
	}
	want := `runners:
  - name: linux
    cloud: aws
    instance_type: c7i.xlarge
    machine_image: ubuntu2404
    preemptible: true
    labels: [airship-linux]
  - name: arm
    cloud: aws
    instance_type: c7g.xlarge
    region: eu-west-1
    labels: [airship-arm]
`
	if got := string(files[0].After); !strings.HasSuffix(got, want) {
		t.Fatalf("airship.yaml:\n%s", got)
	}
}

func TestApplyAppendsToExistingConfig(t *testing.T) {
	existing := "# ours\nrunners:\n- name: build # keep\n  cloud: aws\n  instance_type: c7i.large\n  labels: [airship-build]\n"
	src := Source{Config: []byte(existing), Workflows: map[string][]byte{".github/workflows/ci.yml": []byte(ci)}}
	files, err := Apply(src, Plan{
		Runners: []Runner{{Name: "linux", Cloud: "gcp", InstanceType: "n2-standard-4"}},
		Moves: []Move{
			{Workflow: ".github/workflows/ci.yml", Job: "lint", Label: "airship-build"},
			{Workflow: ".github/workflows/ci.yml", Job: "test", Label: "airship-linux"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(files[0].After)
	if !strings.HasPrefix(got, existing) || !strings.Contains(got, "\n- name: linux\n  cloud: gcp\n") || files[0].Created {
		t.Fatalf("airship.yaml:\n%s", got)
	}
}

func TestApplyReEncodesAFlowList(t *testing.T) {
	existing := "runners: [{name: build, cloud: aws, instance_type: c7i.large, labels: [airship-build]}]\n"
	files, err := Apply(Source{Config: []byte(existing), Workflows: map[string][]byte{"w": []byte(ci)}}, Plan{
		Runners: []Runner{{Name: "linux", Cloud: "aws", InstanceType: "c7i.xlarge"}},
		Moves:   []Move{{Workflow: "w", Job: "lint", Label: "airship-linux"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(files[0].After)
	if !strings.Contains(got, "name: build") || !strings.Contains(got, "name: linux") {
		t.Fatalf("airship.yaml:\n%s", got)
	}
}

func TestApplyProblems(t *testing.T) {
	src := Source{
		Config:       []byte("runners:\n  - name: build\n    cloud: aws\n    instance_type: c7i.large\n    labels: [airship-build]\n"),
		Workflows:    map[string][]byte{"w": []byte(ci)},
		SharedLabels: []string{"airship-shared"},
	}
	lint := func(label string) []Move { return []Move{{Workflow: "w", Job: "lint", Label: label}} }
	cases := map[string]Plan{
		"no moves":       {},
		"unknown label":  {Moves: lint("airship-nope")},
		"bad name":       {Runners: []Runner{{Name: "Big Box", Cloud: "aws", InstanceType: "c7i.large"}}, Moves: lint("airship-build")},
		"duplicate name": {Runners: []Runner{{Name: "build", Cloud: "aws", InstanceType: "c7i.large"}}, Moves: lint("airship-build")},
		"invalid config": {Runners: []Runner{{Name: "x", Cloud: "aws", InstanceType: "nope"}}, Moves: lint("airship-x")},
		"unknown file":   {Moves: []Move{{Workflow: "missing", Job: "lint", Label: "airship-build"}}},
		"moved twice":    {Moves: append(lint("airship-build"), lint("airship-build")...)},
		"not editable":   {Moves: []Move{{Workflow: "w", Job: "dynamic", Label: "airship-build"}}},
	}
	for name, plan := range cases {
		_, err := Apply(src, plan)
		var perr *Error
		if !errors.As(err, &perr) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := Apply(src, Plan{Moves: lint("airship-shared")}); err != nil {
		t.Errorf("an organization label was refused: %v", err)
	}
}
