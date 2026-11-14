// Package migration moves a repository's GitHub Actions workflows onto
// Airship runners: it reads each workflow's jobs and their runs-on, plans
// an airship.yaml for the machines they need, and rewrites runs-on in
// place.
//
// Everything here is pure, like runnerconfig: the workflow files come in
// as bytes and go out as bytes. Reading them from GitHub and opening the
// pull request happen in fleet.
//
// Rewrites are textual. A workflow is parsed only to find where each
// runs-on value sits; the edit then replaces exactly that span, so
// comments, quoting, key order and blank lines everywhere else in the
// file are left as the author wrote them.
package migration

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"go.yaml.in/yaml/v3"
)

// WorkflowDir is where GitHub reads workflows from.
const WorkflowDir = ".github/workflows"

// Limits that keep one repository from asking for unbounded work.
const (
	// MaxWorkflows is how many workflow files a scan reads.
	MaxWorkflows = 50
	// MaxWorkflowBytes bounds each workflow file read.
	MaxWorkflowBytes = 256 << 10
	// MaxJobs bounds the jobs one plan may move.
	MaxJobs = 500
	// MaxRunners bounds the new definitions one plan may add.
	MaxRunners = 20
)

// IsWorkflowPath reports whether p is a workflow file GitHub runs: a
// .yml or .yaml file directly in .github/workflows.
func IsWorkflowPath(p string) bool {
	dir, file := path.Split(p)
	ext := path.Ext(file)

	return dir == WorkflowDir+"/" && file != ext && (ext == ".yml" || ext == ".yaml")
}

// Kind says what a job's runs-on asks for today.
type Kind string

// Kinds of runs-on.
const (
	// KindHosted is a GitHub-hosted runner label such as ubuntu-latest.
	KindHosted Kind = "hosted"
	// KindAirship already asks for an Airship label.
	KindAirship Kind = "airship"
	// KindSelfHosted asks for some other self-hosted runner.
	KindSelfHosted Kind = "self-hosted"
	// KindExpression is computed by an expression, such as a matrix value.
	KindExpression Kind = "expression"
	// KindGroup names a runner group (runs-on: {group: ...}).
	KindGroup Kind = "group"
	// KindReusable calls a reusable workflow, which has no runs-on of its own.
	KindReusable Kind = "reusable"
	// KindOther is any label this package does not recognize, such as a
	// larger runner's custom name.
	KindOther Kind = "other"
)

// Platform is the operating system and architecture a hosted label means.
type Platform string

// Platforms of GitHub-hosted runners.
const (
	PlatformLinuxX64   Platform = "linux-x64"
	PlatformLinuxArm64 Platform = "linux-arm64"
	PlatformWindows    Platform = "windows"
	PlatformMacOS      Platform = "macos"
)

// Job is one job of a workflow, as migrating it needs to know it.
type Job struct {
	ID   string
	Name string
	// RunsOn is the labels as written; empty for a group or reusable call.
	RunsOn []string
	// Line is where runs-on (or the job, without one) is, 1-based.
	Line int
	Kind Kind
	// Platform and Image describe a hosted label's machine, when known.
	Platform Platform
	// Image is the closest machine_image alias, such as ubuntu2404.
	Image string
	// Matrix is true for a job with a strategy matrix: each leg needs a
	// runner of its own.
	Matrix bool
	// Editable says whether runs-on can be rewritten automatically;
	// Reason says why not.
	Editable bool
	Reason   string

	span span
}

// Workflow is one parsed workflow file.
type Workflow struct {
	Path string
	// Name is the workflow's name: field, if it has one.
	Name string
	Jobs []Job
	// Problem is set when the file could not be read; Jobs is then empty.
	Problem string
}

// span is the bytes of a runs-on value that a rewrite replaces.
type span struct {
	start, end int
}

// Parse reads a workflow file's jobs. A file that is not a workflow
// GitHub would run comes back with a Problem rather than an error, so one
// broken file does not hide the rest.
func Parse(name string, content []byte) Workflow {
	out := Workflow{Path: name, Jobs: []Job{}}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		out.Problem = "not valid YAML: " + firstLine(err.Error())

		return out
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		out.Problem = "not a workflow: the file is not a mapping"

		return out
	}
	if name := mappingValue(doc.Content[0], "name"); name != nil && name.Kind == yaml.ScalarNode {
		out.Name = name.Value
	}
	jobs := mappingValue(doc.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		out.Problem = "not a workflow: it has no jobs"

		return out
	}
	lines := lineOffsets(content)
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		key, body := jobs.Content[i], jobs.Content[i+1]
		out.Jobs = append(out.Jobs, parseJob(content, lines, key, body))
	}

	return out
}

func parseJob(content []byte, lines []int, key, body *yaml.Node) Job {
	job := Job{ID: key.Value, Line: key.Line}
	if body == nil || body.Kind != yaml.MappingNode {
		job.Kind = KindOther
		job.Reason = "the job is not a mapping"

		return job
	}
	if name := mappingValue(body, "name"); name != nil && name.Kind == yaml.ScalarNode {
		job.Name = name.Value
	}
	if strategy := mappingValue(body, "strategy"); strategy != nil && strategy.Kind == yaml.MappingNode {
		job.Matrix = mappingValue(strategy, "matrix") != nil
	}
	if mappingValue(body, "uses") != nil {
		job.Kind = KindReusable
		job.Reason = "it calls a reusable workflow; move that workflow's jobs instead"

		return job
	}
	runsOnKey, runsOn := mappingEntry(body, "runs-on")
	if runsOn == nil {
		job.Kind = KindOther
		job.Reason = "it has no runs-on"

		return job
	}
	job.Line = runsOnKey.Line
	job.RunsOn, job.Kind = classify(runsOn)
	job.Platform, job.Image = platform(job.RunsOn)
	switch job.Kind {
	case KindAirship:
		job.Reason = "it already runs on Airship"
	case KindExpression:
		job.Reason = "runs-on is an expression; change it by hand"
	case KindGroup:
		job.Reason = "runs-on names a runner group; change it by hand"
	case KindHosted, KindSelfHosted, KindOther, KindReusable:
		s, reason := valueSpan(content, lines, runsOnKey, runsOn)
		job.span, job.Reason = s, reason
		job.Editable = reason == ""
	}

	return job
}

// classify returns runs-on's labels and what they ask for.
func classify(node *yaml.Node) ([]string, Kind) {
	var labels []string
	switch node.Kind {
	case yaml.ScalarNode:
		labels = []string{node.Value}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return nil, KindExpression
			}
			labels = append(labels, item.Value)
		}
	case yaml.MappingNode:
		return nil, KindGroup
	case yaml.AliasNode, yaml.DocumentNode:
		// An alias: the value lives elsewhere in the file.
		return nil, KindExpression
	}

	for _, label := range labels {
		if _, ok := runnerconfig.BaseLabel(label); ok {
			return labels, KindAirship
		}
	}
	for _, label := range labels {
		if strings.Contains(label, "${{") {
			return labels, KindExpression
		}
	}
	if p, _ := platform(labels); p != "" {
		return labels, KindHosted
	}
	if slices.ContainsFunc(labels, func(l string) bool { return strings.EqualFold(l, "self-hosted") }) {
		return labels, KindSelfHosted
	}

	return labels, KindOther
}

// platform reads a GitHub-hosted runner label. GitHub retires old images,
// so a label for one maps to the closest image Airship still offers.
func platform(labels []string) (Platform, string) {
	if len(labels) != 1 {
		return "", ""
	}
	label := strings.ToLower(labels[0])
	switch {
	case label == "ubuntu-latest", label == "ubuntu-24.04":
		return PlatformLinuxX64, "ubuntu2404"
	case label == "ubuntu-22.04", label == "ubuntu-20.04":
		return PlatformLinuxX64, "ubuntu2204"
	case label == "ubuntu-24.04-arm", label == "ubuntu-latest-arm":
		return PlatformLinuxArm64, "ubuntu2404"
	case label == "ubuntu-22.04-arm":
		return PlatformLinuxArm64, "ubuntu2204"
	case strings.HasPrefix(label, "windows-"):
		return PlatformWindows, ""
	case strings.HasPrefix(label, "macos-"):
		return PlatformMacOS, ""
	}

	return "", ""
}

// valueSpan finds the bytes a rewrite replaces: the scalar or flow
// sequence itself, or for a block sequence everything from just after
// "runs-on:" to the end of its last item.
func valueSpan(content []byte, lines []int, key, value *yaml.Node) (span, string) {
	start := offset(content, lines, value.Line, value.Column)
	if start < 0 {
		return span{}, "runs-on could not be located in the file"
	}
	switch {
	case value.Kind == yaml.ScalarNode:
		end, ok := scalarEnd(content, start, value.Style)
		if !ok {
			return span{}, "runs-on spans several lines; change it by hand"
		}

		return span{start, end}, ""
	case value.Kind == yaml.SequenceNode && value.Style&yaml.FlowStyle != 0:
		end, ok := flowEnd(content, start)
		if !ok {
			return span{}, "runs-on spans several lines; change it by hand"
		}

		return span{start, end}, ""
	case value.Kind == yaml.SequenceNode && len(value.Content) > 0:
		last := value.Content[len(value.Content)-1]
		lastStart := offset(content, lines, last.Line, last.Column)
		if lastStart < 0 {
			return span{}, "runs-on could not be located in the file"
		}
		end, ok := scalarEnd(content, lastStart, last.Style)
		if !ok {
			return span{}, "runs-on spans several lines; change it by hand"
		}
		keyEnd := offset(content, lines, key.Line, key.Column)
		colon := bytes.IndexByte(content[keyEnd:], ':')
		if keyEnd < 0 || colon < 0 {
			return span{}, "runs-on could not be located in the file"
		}

		return span{keyEnd + colon + 1, end}, ""
	}

	return span{}, "runs-on is empty"
}

// scalarEnd returns where a one-line scalar starting at start ends.
func scalarEnd(content []byte, start int, style yaml.Style) (int, bool) {
	lineEnd := start + lineLength(content[start:])
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		for i := start + 1; i < lineEnd; i++ {
			switch content[i] {
			case '\\':
				i++
			case '"':
				return i + 1, true
			}
		}

		return 0, false
	case style&yaml.SingleQuotedStyle != 0:
		for i := start + 1; i < lineEnd; i++ {
			if content[i] != '\'' {
				continue
			}
			if i+1 < lineEnd && content[i+1] == '\'' {
				i++

				continue
			}

			return i + 1, true
		}

		return 0, false
	case style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return 0, false
	}
	// A plain scalar runs to a comment or the end of the line. One that
	// continues on the next line is rare enough in runs-on to refuse.
	text := content[start:lineEnd]
	if i := bytes.Index(text, []byte(" #")); i >= 0 {
		text = text[:i]
	}
	end := start + len(bytes.TrimRight(text, " \t\r"))
	if next := lineEnd + 1; next < len(content) && continuesPlain(content[next:]) {
		return 0, false
	}

	return end, true
}

// continuesPlain is a rough check for a plain scalar carried onto the
// next line: a line indented further that is not a key or a comment.
// Only runs-on: values are checked, so a false positive just asks the
// user to edit by hand.
func continuesPlain(rest []byte) bool {
	line := rest[:lineLength(rest)]
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) == 0 || trimmed[0] == '#' || trimmed[0] == '-' {
		return false
	}

	return !bytes.Contains(trimmed, []byte(":")) && len(line) > len(trimmed)
}

// flowEnd returns where a one-line flow sequence starting at start ends.
func flowEnd(content []byte, start int) (int, bool) {
	lineEnd := start + lineLength(content[start:])
	depth := 0
	var quote byte
	for i := start; i < lineEnd; i++ {
		c := content[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}

	return 0, false
}

// Assignment moves one job onto an Airship label.
type Assignment struct {
	Job string
	// Label is the label a definition serves, such as airship-linux.
	Label string
}

// ErrNotEditable is returned for an assignment to a job whose runs-on
// cannot be rewritten automatically.
var ErrNotEditable = errors.New("runs-on cannot be rewritten automatically")

// RunsOn is the runs-on value a job on label gets: suffixed with the run
// ID, so a runner launched for one run cannot take another's job, and for
// a matrix job with the leg's index too, so every leg gets its own.
func RunsOn(label string, matrix bool) string {
	suffix := "${{ github.run_id }}"
	if matrix {
		suffix += "-${{ strategy.job-index }}"
	}

	return label + runnerconfig.SuffixSeparator + suffix
}

// Rewrite returns content with each assigned job's runs-on replaced.
// Everything else in the file is unchanged byte for byte.
func Rewrite(content []byte, workflow Workflow, assignments []Assignment) ([]byte, error) {
	byID := map[string]Job{}
	for _, job := range workflow.Jobs {
		byID[job.ID] = job
	}
	type edit struct {
		span
		text string
	}
	edits := make([]edit, 0, len(assignments))
	for _, a := range assignments {
		job, ok := byID[a.Job]
		if !ok {
			return nil, fmt.Errorf("%s has no job %q", workflow.Path, a.Job)
		}
		if !job.Editable {
			return nil, fmt.Errorf("%s job %s: %w: %s", workflow.Path, a.Job, ErrNotEditable, job.Reason)
		}
		text := RunsOn(a.Label, job.Matrix)
		// A block sequence's span starts right after the colon.
		if job.span.start > 0 && content[job.span.start-1] == ':' {
			text = " " + text
		}
		edits = append(edits, edit{span: job.span, text: text})
	}
	slices.SortFunc(edits, func(a, b edit) int { return cmp.Compare(b.start, a.start) })
	out := bytes.Clone(content)
	for i, e := range edits {
		if i > 0 && e.end > edits[i-1].start {
			return nil, fmt.Errorf("%s: two jobs share one runs-on", workflow.Path)
		}
		out = slices.Concat(out[:e.start], []byte(e.text), out[e.end:])
	}

	return out, nil
}

func mappingEntry(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i], node.Content[i+1]
		}
	}

	return nil, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	_, value := mappingEntry(node, key)

	return value
}

// lineOffsets returns the byte offset each line starts at.
func lineOffsets(content []byte) []int {
	offsets := []int{0}
	for i, c := range content {
		if c == '\n' {
			offsets = append(offsets, i+1)
		}
	}

	return offsets
}

// offset converts the parser's 1-based line and column (counted in
// characters) to a byte offset, or -1.
func offset(content []byte, lines []int, line, column int) int {
	if line < 1 || line > len(lines) || column < 1 {
		return -1
	}
	at := lines[line-1]
	for range column - 1 {
		if at >= len(content) || content[at] == '\n' {
			return -1
		}
		_, size := utf8.DecodeRune(content[at:])
		at += size
	}

	return at
}

func lineLength(b []byte) int {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return i
	}

	return len(b)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return strings.TrimPrefix(line, "yaml: ")
}
