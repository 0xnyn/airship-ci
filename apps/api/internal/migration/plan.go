package migration

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"go.yaml.in/yaml/v3"
)

// Runner is a definition a plan adds to airship.yaml. It covers what
// moving off GitHub-hosted runners needs; anything more is edited in the
// file afterwards.
type Runner struct {
	Name         string
	Cloud        string
	InstanceType string
	MachineImage string
	Region       string
	Preemptible  bool
}

// Label is the runs-on label the definition serves.
func (r Runner) Label() string {
	return runnerconfig.LabelPrefix + strings.ToLower(r.Name)
}

// Move puts one job on a label.
type Move struct {
	Workflow string
	Job      string
	Label    string
}

// Plan is a migration: definitions to add, and jobs to move.
type Plan struct {
	Runners []Runner
	Moves   []Move
}

// Source is the repository as it is now.
type Source struct {
	// Config is the repository's airship.yaml; nil when it has none.
	Config []byte
	// Workflows maps each workflow file's path to its content.
	Workflows map[string][]byte
	// SharedLabels are served by the organization's airship.global.yaml.
	SharedLabels []string
}

// File is one file a plan changes.
type File struct {
	Path   string
	Before []byte
	After  []byte
	// Created is true for a file the repository does not have yet.
	Created bool
}

// Error is a plan that cannot be applied, worded for the person who made it.
type Error struct {
	// Path is the file the problem is in, when it is in one.
	Path   string
	Detail string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return e.Detail
	}

	return e.Path + ": " + e.Detail
}

func problem(file, format string, args ...any) *Error {
	return &Error{Path: file, Detail: fmt.Sprintf(format, args...)}
}

// runnerName keeps names to what also makes a valid label.
var runnerName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Apply returns the files a plan changes: airship.yaml first when the
// plan adds definitions, then each workflow it rewrites, in path order.
// The new airship.yaml is checked the way jobs will read it.
func Apply(src Source, plan Plan) ([]File, error) {
	if len(plan.Moves) == 0 && len(plan.Runners) == 0 {
		return nil, problem("", "pick a job to move or a runner to add")
	}
	if len(plan.Moves) > MaxJobs {
		return nil, problem("", "a plan moves at most %d jobs", MaxJobs)
	}
	if len(plan.Runners) > MaxRunners {
		return nil, problem("", "a plan adds at most %d runners", MaxRunners)
	}
	labels, err := servedLabels(src, plan.Runners)
	if err != nil {
		return nil, err
	}

	byWorkflow := map[string][]Assignment{}
	seen := map[string]bool{}
	for _, move := range plan.Moves {
		if _, ok := src.Workflows[move.Workflow]; !ok {
			return nil, problem(move.Workflow, "no such workflow in the repository")
		}
		key := move.Workflow + "\x00" + move.Job
		if seen[key] {
			return nil, problem(move.Workflow, "job %s is moved twice", move.Job)
		}
		seen[key] = true
		label := strings.ToLower(move.Label)
		if !labels[label] {
			return nil, problem(move.Workflow, "job %s: no runner serves %s", move.Job, move.Label)
		}
		byWorkflow[move.Workflow] = append(byWorkflow[move.Workflow], Assignment{Job: move.Job, Label: label})
	}

	files := []File{}
	if len(plan.Runners) > 0 {
		after, err := Config(src.Config, plan.Runners)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: runnerconfig.RepoFile, Before: src.Config, After: after, Created: src.Config == nil})
	}
	paths := make([]string, 0, len(byWorkflow))
	for p := range byWorkflow {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		before := src.Workflows[p]
		workflow := Parse(p, before)
		if workflow.Problem != "" {
			return nil, problem(p, "%s", workflow.Problem)
		}
		after, err := Rewrite(before, workflow, byWorkflow[p])
		if err != nil {
			return nil, &Error{Path: p, Detail: strings.TrimPrefix(err.Error(), p+" ")}
		}
		files = append(files, File{Path: p, Before: before, After: after})
	}

	return files, nil
}

// servedLabels is every label a moved job may use: the new definitions',
// the existing airship.yaml's and the organization's.
func servedLabels(src Source, runners []Runner) (map[string]bool, error) {
	labels := map[string]bool{}
	names := map[string]bool{}
	if src.Config != nil {
		existing, err := runnerconfig.Parse(runnerconfig.RepoFile, src.Config)
		if err != nil {
			return nil, problem(runnerconfig.RepoFile, "the repository's file has a problem; fix it first (%s)", strings.TrimPrefix(err.Error(), runnerconfig.RepoFile+": "))
		}
		for _, r := range existing.Runners {
			names[strings.ToLower(r.Name)] = true
			for _, l := range r.Labels {
				labels[strings.ToLower(l)] = true
			}
		}
	}
	for _, l := range src.SharedLabels {
		labels[strings.ToLower(l)] = true
	}
	for i, r := range runners {
		if !runnerName.MatchString(r.Name) {
			return nil, problem(runnerconfig.RepoFile, "runner name %q: use lowercase letters, digits, '.', '_' and '-'", r.Name)
		}
		if names[r.Name] {
			return nil, problem(runnerconfig.RepoFile, "a runner named %s is already defined", r.Name)
		}
		names[r.Name] = true
		if labels[r.Label()] && !slices.ContainsFunc(runners[:i], func(o Runner) bool { return o.Label() == r.Label() }) {
			return nil, problem(runnerconfig.RepoFile, "%s is already served by another runner; pick another name", r.Label())
		}
		labels[r.Label()] = true
	}

	return labels, nil
}

// Config returns airship.yaml with runners added. existing is the file as
// it is now, nil for none. When the file ends with its runners list the
// new definitions are appended as text, so its comments and layout stay;
// otherwise it is re-encoded.
func Config(existing []byte, runners []Runner) ([]byte, error) {
	var out []byte
	if len(bytes.TrimSpace(existing)) == 0 {
		var b bytes.Buffer
		b.WriteString("# Runners for this repository's GitHub Actions jobs.\n")
		b.WriteString("# A job asks for one with runs-on: <label>--${{ github.run_id }}\n")
		b.WriteString("runners:\n")
		for _, r := range runners {
			writeRunner(&b, r, 2)
		}
		out = b.Bytes()
	} else {
		var err error
		out, err = appendRunners(existing, runners)
		if err != nil {
			return nil, err
		}
	}
	if _, err := runnerconfig.Parse(runnerconfig.RepoFile, out); err != nil {
		return nil, &Error{Path: runnerconfig.RepoFile, Detail: strings.TrimPrefix(err.Error(), runnerconfig.RepoFile+": ")}
	}

	return out, nil
}

func appendRunners(existing []byte, runners []Runner) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, problem(runnerconfig.RepoFile, "the repository's file could not be read; fix it first")
	}
	root := doc.Content[0]
	key, list := mappingEntry(root, "runners")
	lastKey := len(root.Content) >= 2 && root.Content[len(root.Content)-2] == key
	if list != nil && list.Kind == yaml.SequenceNode && list.Style&yaml.FlowStyle == 0 && len(list.Content) > 0 && lastKey {
		var b bytes.Buffer
		b.Write(existing)
		if !bytes.HasSuffix(existing, []byte("\n")) {
			b.WriteByte('\n')
		}
		for _, r := range runners {
			writeRunner(&b, r, list.Column-1)
		}

		return b.Bytes(), nil
	}

	// Re-encode: comments mostly survive, layout may not.
	if list == nil || list.Kind != yaml.SequenceNode {
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if key == nil {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "runners"}, list)
		} else {
			for i := 0; i+1 < len(root.Content); i += 2 {
				if root.Content[i] == key {
					root.Content[i+1] = list
				}
			}
		}
	}
	list.Style = 0
	for _, r := range runners {
		var b bytes.Buffer
		writeRunner(&b, r, 0)
		var item yaml.Node
		if err := yaml.Unmarshal(b.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("encode runner %s: %w", r.Name, err)
		}
		list.Content = append(list.Content, item.Content[0].Content...)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode %s: %w", runnerconfig.RepoFile, err)
	}

	return b.Bytes(), nil
}

// writeRunner writes one list item, its dash indented by indent spaces.
func writeRunner(b *bytes.Buffer, r Runner, indent int) {
	pad := strings.Repeat(" ", indent)
	field := func(name, value string) {
		b.WriteString(pad + "  " + name + ": " + scalar(value) + "\n")
	}
	b.WriteString(pad + "- name: " + scalar(r.Name) + "\n")
	field("cloud", r.Cloud)
	field("instance_type", r.InstanceType)
	if r.MachineImage != "" {
		field("machine_image", r.MachineImage)
	}
	if r.Region != "" {
		field("region", r.Region)
	}
	if r.Preemptible {
		b.WriteString(pad + "  preemptible: true\n")
	}
	b.WriteString(pad + "  labels: [" + scalar(r.Label()) + "]\n")
}

// scalar spells a string as YAML would, quoting only when it must.
func scalar(s string) string {
	node := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return strconv.Quote(s)
	}

	return strings.TrimSuffix(string(out), "\n")
}
