package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Repository is a repository the app can access, and whether its runner
// config file is active.
type Repository struct {
	ID            int64  `doc:"GitHub repository ID"                                   json:"id"`
	FullName      string `doc:"owner/name"                                             json:"fullName"`
	Private       bool   `doc:"Whether the repository is private"                      json:"private"`
	DefaultBranch string `doc:"Branch idle runner config is read from"                 json:"defaultBranch"`
	// Accessible is false for an activated repository the app can no
	// longer see.
	Accessible  bool         `doc:"Whether the app can still access the repository"  json:"accessible"`
	Activated   bool         `doc:"Whether its .cirun.yml launches runners"           json:"activated"`
	AllowPublic bool         `doc:"Serve jobs while the repository is public"         json:"allowPublic"`
	ActivatedAt *time.Time   `doc:"When it was activated"                             json:"activatedAt"`
	Config      *ConfigState `doc:"The default branch's .cirun.yml as last read"      json:"config"`
}

// ConfigState is the result of reading a config file.
type ConfigState struct {
	SHA       *string    `doc:"Blob SHA of the file; null when there is none" json:"sha"`
	Error     *string    `doc:"Why the file cannot be used"                   json:"error"`
	CheckedAt *time.Time `doc:"When it was last read"                         json:"checkedAt"`
}

func repositoryFromDB(r db.Repository) Repository {
	at := r.ActivatedAt

	return Repository{
		ID: r.ID, FullName: r.FullName, Private: r.Private, DefaultBranch: r.DefaultBranch,
		Activated: true, AllowPublic: r.AllowPublic, ActivatedAt: &at,
		Config: &ConfigState{SHA: r.ConfigSha, Error: r.ConfigError, CheckedAt: r.ConfigCheckedAt},
	}
}

// RepositoryList is every repository the app can access, activated ones
// first.
type RepositoryList struct {
	Items []Repository `json:"items"`
}

type repositoryListOutput struct {
	Body RepositoryList
}

func (h *handlers) listRepositories(ctx context.Context, in *orgInput) (*repositoryListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	items, err := h.repositories(ctx, inst.ID)
	if err != nil {
		return nil, err
	}

	return &repositoryListOutput{Body: RepositoryList{Items: items}}, nil
}

// repositories merges what the app can access with what is activated.
func (h *handlers) repositories(ctx context.Context, installationID int64) ([]Repository, error) {
	activated, err := h.Store.ListRepositories(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("list activated repositories: %w", err)
	}
	remote, err := h.Fleet.ListInstallationRepositories(ctx, installationID)
	if err != nil {
		return nil, huma.Error502BadGateway("could not list repositories from GitHub", err)
	}

	byID := map[int64]Repository{}
	for _, row := range activated {
		byID[row.ID] = repositoryFromDB(row)
	}
	items := make([]Repository, 0, len(remote)+len(activated))
	for _, r := range remote {
		item, ok := byID[r.ID]
		if !ok {
			item = Repository{ID: r.ID, FullName: r.FullName, Private: r.Private, DefaultBranch: r.DefaultBranch}
		}
		item.Accessible = true
		delete(byID, r.ID)
		items = append(items, item)
	}
	for _, item := range byID {
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b Repository) int {
		if a.Activated != b.Activated {
			if a.Activated {
				return -1
			}

			return 1
		}

		return strings.Compare(strings.ToLower(a.FullName), strings.ToLower(b.FullName))
	})

	return items, nil
}

// ActivateRepository is the request body for activating a repository.
type ActivateRepository struct {
	AllowPublic bool `doc:"Serve jobs while the repository is public. Public repositories run code from fork pull requests on your machines." json:"allowPublic,omitempty" default:"false"`
}

type repositoryInput struct {
	Org  string `doc:"GitHub organization login" maxLength:"100" minLength:"1" path:"org"`
	Repo int64  `doc:"GitHub repository ID"      minimum:"1"   path:"repo"`
}

type activateRepositoryInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Repo int64  `minimum:"1" path:"repo"`
	Body ActivateRepository
}

type repositoryOutput struct {
	Body Repository
}

func (h *handlers) activateRepository(ctx context.Context, in *activateRepositoryInput) (*repositoryOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	user, _ := auth.UserFrom(ctx)
	row, err := h.Fleet.ActivateRepository(ctx, inst.ID, in.Repo, in.Body.AllowPublic, &user.ID)
	if errors.Is(err, fleet.ErrRepositoryNotFound) {
		return nil, huma.Error404NotFound("repository not found; is the GitHub App installed on it?")
	}
	if err != nil {
		return nil, fmt.Errorf("activate repository: %w", err)
	}
	item := repositoryFromDB(row)
	item.Accessible = true

	return &repositoryOutput{Body: item}, nil
}

func (h *handlers) deactivateRepository(ctx context.Context, in *repositoryInput) (*struct{}, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	err = h.Fleet.DeactivateRepository(ctx, inst.ID, in.Repo)
	if errors.Is(err, fleet.ErrRepositoryNotFound) {
		return nil, huma.Error404NotFound("repository is not activated")
	}
	if err != nil {
		return nil, fmt.Errorf("deactivate repository: %w", err)
	}

	return &struct{}{}, nil
}

// Pool is an idle runner pool from an idle: section.
type Pool struct {
	ID         uuid.UUID  `doc:"Pool ID"                                         format:"uuid" json:"id"`
	Repository string     `doc:"Repository whose .cirun.yml defines it, or the organization" json:"repository"`
	Name       string     `doc:"Runner definition name"                          json:"name"`
	Mode       string     `doc:"How the pool's size is decided"                  enum:"fixed,schedule,usage" json:"mode"`
	Desired    int        `doc:"Idle runners the pool wants now"                 json:"desired"`
	Idle       int32      `doc:"Runners waiting for a job"                       json:"idle"`
	Taken      int32      `doc:"Pool runners that took a job and are not done"   json:"taken"`
	WarmUntil  *time.Time `doc:"Usage-based pools stay warm until this time"     json:"warmUntil"`
	UpdatedAt  time.Time  `doc:"When the definition last changed"                json:"updatedAt"`
}

// PoolList is every warm pool in an organization.
type PoolList struct {
	Items []Pool `json:"items"`
}

type poolListOutput struct {
	Body PoolList
}

func (h *handlers) listPools(ctx context.Context, in *orgInput) (*poolListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	pools, err := h.Store.ListInstallationIdlePools(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list pools: %w", err)
	}
	counts, err := h.Store.CountInstallationPoolRunners(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("count pool runners: %w", err)
	}
	repos, err := h.Store.ListRepositories(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	names := map[int64]string{}
	for _, r := range repos {
		names[r.ID] = r.FullName
	}
	byPool := map[uuid.UUID]db.CountInstallationPoolRunnersRow{}
	for _, c := range counts {
		if c.PoolID != nil {
			byPool[*c.PoolID] = c
		}
	}

	now := time.Now()
	items := make([]Pool, 0, len(pools))
	for _, p := range pools {
		var spec fleet.IdleSpec
		if err := json.Unmarshal(p.Idle, &spec); err != nil {
			return nil, fmt.Errorf("decode pool: %w", err)
		}
		mode := "fixed"
		switch {
		case spec.UsageMinutes > 0:
			mode = "usage"
		case len(spec.Schedule) > 0:
			mode = "schedule"
		}
		repository := "organization (.cirun.global.yml)"
		if p.RepositoryID != nil {
			repository = names[*p.RepositoryID]
		}
		items = append(items, Pool{
			ID: p.ID, Repository: repository, Name: p.Name, Mode: mode,
			Desired: spec.Desired(now, p.UsageUntil), Idle: byPool[p.ID].Idle, Taken: byPool[p.ID].Taken,
			WarmUntil: p.UsageUntil, UpdatedAt: p.UpdatedAt,
		})
	}

	return &poolListOutput{Body: PoolList{Items: items}}, nil
}

// ValidateConfig is a config file to check.
type ValidateConfig struct {
	File    string `doc:"Which file this is" enum:".cirun.yml,.cirun.global.yml,.access.yml" json:"file"`
	Content string `doc:"The file's content" json:"content" maxLength:"65536"`
}

type validateConfigInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body ValidateConfig
}

// ConfigProblem is why a config file cannot be used.
type ConfigProblem struct {
	Path   string `doc:"Where in the file, such as runners[0].instance_type" json:"path"`
	Detail string `doc:"What is wrong"                                         json:"detail"`
}

// ConfigRunner summarizes one runner definition.
type ConfigRunner struct {
	Name     string   `doc:"Runner name"                                  json:"name"`
	Cloud    string   `doc:"Where it launches"                            enum:"aws" json:"cloud"`
	Labels   []string `doc:"runs-on labels it serves"                     json:"labels"`
	Machines []string `doc:"Machine shapes to try, in fallback order"     json:"machines"`
	Idle     string   `doc:"Idle pool summary, if it has one"             json:"idle,omitempty"`
}

// ConfigValidation is the result of checking a config file.
type ConfigValidation struct {
	Valid   bool           `doc:"Whether the file can be used"   json:"valid"`
	Problem *ConfigProblem `doc:"The first problem found"        json:"problem"`
	Runners []ConfigRunner `doc:"Runner definitions in the file" json:"runners"`
}

type configValidationOutput struct {
	Body ConfigValidation
}

func (h *handlers) validateConfig(ctx context.Context, in *validateConfigInput) (*configValidationOutput, error) {
	if _, err := h.org(ctx, in.Org, false); err != nil {
		return nil, err
	}
	out := ConfigValidation{Runners: []ConfigRunner{}}
	var err error
	if in.Body.File == runnerconfig.AccessFile {
		_, err = runnerconfig.ParseAccess([]byte(in.Body.Content))
	} else {
		var file runnerconfig.File
		file, err = runnerconfig.Parse(in.Body.File, []byte(in.Body.Content))
		out.Runners = summarizeRunners(file.Runners)
	}
	if err != nil {
		out.Problem = configProblem(err)

		return &configValidationOutput{Body: out}, nil
	}
	out.Valid = true

	return &configValidationOutput{Body: out}, nil
}

func configProblem(err error) *ConfigProblem {
	var cfgErr *runnerconfig.Error
	if errors.As(err, &cfgErr) {
		return &ConfigProblem{Path: cfgErr.Path, Detail: cfgErr.Detail}
	}

	return &ConfigProblem{Detail: err.Error()}
}

func summarizeRunners(runners []runnerconfig.Runner) []ConfigRunner {
	out := make([]ConfigRunner, 0, len(runners))
	for _, r := range runners {
		item := ConfigRunner{Name: r.Name, Cloud: r.Cloud, Labels: r.Labels, Machines: []string{}}
		for _, v := range r.Variants {
			machine := v.InstanceType + " (" + v.Arch
			if v.Region != "" {
				machine += ", " + v.Region
			}
			if v.Spot {
				machine += ", spot"
			}
			item.Machines = append(item.Machines, machine+")")
		}
		if r.Idle != nil {
			item.Idle = idleSummary(r.Idle)
		}
		out = append(out, item)
	}

	return out
}

func idleSummary(idle *runnerconfig.Idle) string {
	switch {
	case idle.Usage != nil:
		return fmt.Sprintf("%d idle for %s after a job starts", idle.Usage.Replicas, idle.Usage.Duration)
	case len(idle.Schedule) > 0:
		return fmt.Sprintf("scheduled, %d windows", len(idle.Schedule))
	default:
		return fmt.Sprintf("%d always idle", idle.Always)
	}
}

// OrganizationRunnerConfig is the organization's .cirun repository as
// jobs see it now.
type OrganizationRunnerConfig struct {
	Found      bool           `doc:"Whether the organization has a .cirun.global.yml" json:"found"`
	SHA        *string        `doc:"Blob SHA of .cirun.global.yml"                   json:"sha"`
	Restricted bool           `doc:"Whether .access.yml limits who can use the runners" json:"restricted"`
	Problem    *string        `doc:"Why the files cannot be used"                      json:"problem"`
	Runners    []ConfigRunner `doc:"Organization runner definitions"                   json:"runners"`
}

type organizationConfigOutput struct {
	Body OrganizationRunnerConfig
}

func (h *handlers) organizationConfig(ctx context.Context, in *orgInput) (*organizationConfigOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	cfg, err := h.Fleet.OrganizationConfig(ctx, inst)
	if err != nil {
		return nil, huma.Error502BadGateway("could not read the organization's .cirun repository", err)
	}
	out := OrganizationRunnerConfig{Found: cfg.Found, Restricted: cfg.Restricted, Runners: summarizeRunners(cfg.Runners)}
	if cfg.SHA != "" {
		out.SHA = &cfg.SHA
	}
	if cfg.Problem != "" {
		out.Problem = &cfg.Problem
	}

	return &organizationConfigOutput{Body: out}, nil
}
