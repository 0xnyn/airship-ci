package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/migration"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/danielgtaylor/huma/v2"
)

// The migration assistant: read a repository's workflows, plan which jobs
// move to which Airship runners, preview the files, and open a pull
// request with them. Only organization owners use it, since it shows the
// content of repositories a member may not be able to read on GitHub.

// WorkflowJob is one job of a workflow and what moving it involves.
type WorkflowJob struct {
	ID     string   `doc:"Job ID, the key under jobs:"                                  json:"id"`
	Name   string   `doc:"The job's name: field, if it has one"                         json:"name"`
	RunsOn []string `doc:"runs-on labels as written; empty for a runner group or a reusable workflow call" json:"runsOn" nullable:"false"`
	Line   int      `doc:"Line of runs-on in the file"                                  json:"line"`
	Kind   string   `doc:"What runs-on asks for today"                                  enum:"hosted,airship,self-hosted,expression,group,reusable,other" json:"kind"`
	// Platform is empty when runs-on is not a GitHub-hosted label.
	Platform string `doc:"The GitHub-hosted machine's platform, when known"            enum:"linux-x64,linux-arm64,windows,macos," json:"platform"`
	Image    string `doc:"The closest machine_image alias to the hosted image"          json:"image"`
	Matrix   bool   `doc:"Whether the job has a strategy matrix"                        json:"matrix"`
	Editable bool   `doc:"Whether runs-on can be rewritten automatically"               json:"editable"`
	Reason   string `doc:"Why it cannot be moved automatically"                         json:"reason"`
}

// WorkflowFile is one workflow in .github/workflows.
type WorkflowFile struct {
	Path    string        `doc:"Path in the repository"                 json:"path"`
	Name    string        `doc:"The workflow's name: field"             json:"name"`
	Problem *string       `doc:"Why the file could not be read"         json:"problem"`
	Jobs    []WorkflowJob `doc:"Jobs in file order"                      json:"jobs" nullable:"false"`
}

// SkippedWorkflow is a workflow file that was not read.
type SkippedWorkflow struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ExistingRunnerConfig is the repository's airship.yaml today.
type ExistingRunnerConfig struct {
	SHA     string         `doc:"Blob SHA"                         json:"sha"`
	Problem *string        `doc:"Why the file cannot be used"      json:"problem"`
	Runners []ConfigRunner `doc:"Its runner definitions"           json:"runners" nullable:"false"`
}

// WorkflowScan is a repository's workflows, read for migrating.
type WorkflowScan struct {
	Repository    string                `doc:"owner/name"                                       json:"repository"`
	DefaultBranch string                `doc:"Branch the files were read from"                  json:"defaultBranch"`
	Workflows     []WorkflowFile        `json:"workflows" nullable:"false"`
	Skipped       []SkippedWorkflow     `json:"skipped" nullable:"false"`
	Config        *ExistingRunnerConfig `doc:"The repository's airship.yaml; absent when there is none" json:"config,omitempty"`
	SharedLabels  []string              `doc:"Labels the organization's airship.global.yaml serves" json:"sharedLabels" nullable:"false"`
}

type workflowScanOutput struct {
	Body WorkflowScan
}

func (h *handlers) scanWorkflows(ctx context.Context, in *repositoryInput) (*workflowScanOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	src, err := h.migrationSource(ctx, inst, in.Repo)
	if err != nil {
		return nil, err
	}
	out := WorkflowScan{
		Repository: src.Repository.FullName, DefaultBranch: src.Repository.DefaultBranch,
		Workflows: make([]WorkflowFile, 0, len(src.Workflows)), Skipped: []SkippedWorkflow{},
		SharedLabels: append([]string{}, src.SharedLabels...),
	}
	for _, file := range src.Workflows {
		out.Workflows = append(out.Workflows, workflowFile(migration.Parse(file.Path, file.Content)))
	}
	for p, reason := range src.Skipped {
		out.Skipped = append(out.Skipped, SkippedWorkflow{Path: p, Reason: reason})
	}
	if src.Config != nil {
		cfg := &ExistingRunnerConfig{SHA: src.Config.SHA, Runners: []ConfigRunner{}}
		file, err := runnerconfig.Parse(runnerconfig.RepoFile, src.Config.Content)
		if err != nil {
			detail := err.Error()
			cfg.Problem = &detail
		} else {
			cfg.Runners = summarizeRunners(file.Runners)
		}
		out.Config = cfg
	}

	return &workflowScanOutput{Body: out}, nil
}

func workflowFile(w migration.Workflow) WorkflowFile {
	out := WorkflowFile{Path: w.Path, Name: w.Name, Jobs: make([]WorkflowJob, 0, len(w.Jobs))}
	if w.Problem != "" {
		out.Problem = &w.Problem
	}
	for _, j := range w.Jobs {
		runsOn := j.RunsOn
		if runsOn == nil {
			runsOn = []string{}
		}
		out.Jobs = append(out.Jobs, WorkflowJob{
			ID: j.ID, Name: j.Name, RunsOn: runsOn, Line: j.Line, Kind: string(j.Kind),
			Platform: string(j.Platform), Image: j.Image, Matrix: j.Matrix, Editable: j.Editable, Reason: j.Reason,
		})
	}

	return out
}

// MigrationRunner is a runner definition to add to airship.yaml.
type MigrationRunner struct {
	Name         string `doc:"Definition name; its label is airship-<name>"       json:"name"         maxLength:"63"  minLength:"1" pattern:"^[a-z0-9][a-z0-9._-]*$"`
	Cloud        string `doc:"Where it launches"                                   json:"cloud"        enum:"aws,gcp,azure,daytona,runloop,e2b,novita,lelantos,namespace,tenki,modal"`
	InstanceType string `doc:"Machine shape, as airship.yaml spells it for the cloud" json:"instanceType" maxLength:"200" minLength:"1"`
	MachineImage string `doc:"Image; empty for the cloud's default"                json:"machineImage,omitempty" maxLength:"500"`
	Region       string `doc:"Region; empty for the connected cloud's default"     json:"region,omitempty" maxLength:"100"`
	Preemptible  bool   `doc:"Use spot capacity"                                   json:"preemptible,omitempty"`
}

// MigrationMove puts one job on a label.
type MigrationMove struct {
	Workflow string `doc:"Workflow path, such as .github/workflows/ci.yml" json:"workflow" maxLength:"300" minLength:"1"`
	Job      string `doc:"Job ID"                                           json:"job"      maxLength:"200" minLength:"1"`
	Label    string `doc:"Label the job runs on, such as airship-linux"     json:"label"    maxLength:"100" minLength:"1"`
}

// MigrationPlan is which jobs move where, and the definitions they need.
type MigrationPlan struct {
	Runners []MigrationRunner `doc:"Definitions to add to airship.yaml" json:"runners" maxItems:"20" nullable:"false"`
	Moves   []MigrationMove   `doc:"Jobs to move; may be empty when the plan only adds runners"                      json:"moves"   maxItems:"500" nullable:"false"`
}

type migrationInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Repo int64  `minimum:"1" path:"repo"`
	Body MigrationPlan
}

// MigrationFileChange is one file a plan writes.
type MigrationFileChange struct {
	Path    string `doc:"Path in the repository"                       json:"path"`
	Before  string `doc:"Content today; empty for a new file"          json:"before"`
	After   string `doc:"Content after the change"                     json:"after"`
	Created bool   `doc:"Whether the file is new"                       json:"created"`
}

// MigrationPreview is the files a plan writes.
type MigrationPreview struct {
	Files []MigrationFileChange `json:"files" nullable:"false"`
}

type migrationPreviewOutput struct {
	Body MigrationPreview
}

func (h *handlers) previewMigration(ctx context.Context, in *migrationInput) (*migrationPreviewOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	_, files, err := h.applyPlan(ctx, inst, in.Repo, in.Body)
	if err != nil {
		return nil, err
	}
	out := MigrationPreview{Files: make([]MigrationFileChange, 0, len(files))}
	for _, f := range files {
		out.Files = append(out.Files, MigrationFileChange{Path: f.Path, Before: string(f.Before), After: string(f.After), Created: f.Created})
	}

	return &migrationPreviewOutput{Body: out}, nil
}

// MigrationPullRequest is the pull request a migration opened.
type MigrationPullRequest struct {
	Number int    `doc:"Pull request number" json:"number"`
	URL    string `doc:"Where to review it"  format:"uri" json:"url"`
}

type migrationPullRequestOutput struct {
	Body MigrationPullRequest
}

func (h *handlers) openMigrationPullRequest(ctx context.Context, in *migrationInput) (*migrationPullRequestOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	src, files, err := h.applyPlan(ctx, inst, in.Repo, in.Body)
	if err != nil {
		return nil, err
	}
	shas := map[string]string{}
	for _, f := range src.Workflows {
		shas[f.Path] = f.SHA
	}
	if src.Config != nil {
		shas[src.Config.Path] = src.Config.SHA
	}
	changes := make([]fleet.MigrationChange, 0, len(files))
	for _, f := range files {
		changes = append(changes, fleet.MigrationChange{Path: f.Path, Content: f.After, SHA: shas[f.Path]})
	}
	pr, err := h.Fleet.OpenMigrationPullRequest(ctx, inst, src.Repository, changes, summarizePlan(in.Body))
	switch {
	case errors.Is(err, fleet.ErrMigrationForbidden):
		slog.InfoContext(ctx, "migration pull request refused", slog.Any("error", err))

		return nil, huma.Error409Conflict("the GitHub App may not write to " + src.Repository.FullName +
			". Give it Contents, Pull requests and Workflows write access on GitHub, or copy the files instead")
	case errors.Is(err, fleet.ErrMigrationConflict):
		return nil, huma.Error409Conflict("a file changed on GitHub since it was read; review the changes again")
	case errors.Is(err, fleet.ErrMigrationUnsupported):
		return nil, huma.Error501NotImplemented(err.Error())
	case err != nil:
		return nil, fmt.Errorf("open migration pull request: %w", err)
	}

	return &migrationPullRequestOutput{Body: MigrationPullRequest{Number: pr.Number, URL: pr.HTMLURL}}, nil
}

func (h *handlers) migrationSource(ctx context.Context, inst db.Installation, repoID int64) (fleet.MigrationSource, error) {
	src, err := h.Fleet.ReadMigrationSource(ctx, inst, repoID)
	switch {
	case errors.Is(err, fleet.ErrRepositoryNotFound):
		return src, huma.Error404NotFound("repository not found; is the GitHub App installed on it?")
	case errors.Is(err, fleet.ErrMigrationUnsupported):
		return src, huma.Error501NotImplemented(err.Error())
	case err != nil:
		slog.WarnContext(ctx, "read workflows", slog.Any("error", err))

		return src, huma.Error502BadGateway("could not read the repository's workflows from GitHub; try again in a moment")
	}

	return src, nil
}

// applyPlan reads the repository afresh and applies the plan to it, so
// what is previewed or proposed is always built from the files as they are.
func (h *handlers) applyPlan(ctx context.Context, inst db.Installation, repoID int64, plan MigrationPlan) (fleet.MigrationSource, []migration.File, error) {
	src, err := h.migrationSource(ctx, inst, repoID)
	if err != nil {
		return src, nil, err
	}
	in := migration.Source{Workflows: map[string][]byte{}, SharedLabels: src.SharedLabels}
	for _, f := range src.Workflows {
		in.Workflows[f.Path] = f.Content
	}
	if src.Config != nil {
		in.Config = src.Config.Content
	}
	p := migration.Plan{}
	for _, r := range plan.Runners {
		p.Runners = append(p.Runners, migration.Runner{
			Name: r.Name, Cloud: r.Cloud, InstanceType: strings.TrimSpace(r.InstanceType),
			MachineImage: strings.TrimSpace(r.MachineImage), Region: strings.TrimSpace(r.Region), Preemptible: r.Preemptible,
		})
	}
	for _, m := range plan.Moves {
		p.Moves = append(p.Moves, migration.Move{Workflow: m.Workflow, Job: m.Job, Label: m.Label})
	}
	files, err := migration.Apply(in, p)
	var perr *migration.Error
	if errors.As(err, &perr) {
		return src, nil, huma.Error422UnprocessableEntity(perr.Error())
	}
	if err != nil {
		return src, nil, fmt.Errorf("apply migration plan: %w", err)
	}

	return src, files, nil
}

// summarizePlan lists the moves for the pull request's description.
func summarizePlan(plan MigrationPlan) string {
	var b strings.Builder
	if len(plan.Runners) > 0 {
		b.WriteString("New runners in `" + runnerconfig.RepoFile + "`:\n\n")
		for _, r := range plan.Runners {
			fmt.Fprintf(&b, "- `%s%s`: %s `%s`\n", runnerconfig.LabelPrefix, r.Name, r.Cloud, r.InstanceType)
		}
		b.WriteString("\n")
	}
	b.WriteString("Jobs moved:\n\n")
	for _, m := range plan.Moves {
		fmt.Fprintf(&b, "- `%s` › `%s` → `%s`\n", path.Base(m.Workflow), m.Job, m.Label)
	}

	return strings.TrimRight(b.String(), "\n")
}
