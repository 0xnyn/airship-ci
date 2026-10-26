package fleet

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

// Skip reasons for config-file runners, matching jobs.skip_reason.
const (
	SkipRepositoryInactive = "repository_inactive"
	SkipConfigInvalid      = "config_invalid"
	SkipAccessDenied       = "access_denied"
	SkipNoDataPlane        = "no_data_plane"
)

// providerForCloud is the agent provider each config cloud launches with.
var providerForCloud = map[string]string{
	runnerconfig.CloudAWS:    "aws",
	runnerconfig.CloudOnPrem: "docker",
}

// testProvider is the in-memory provider tests run agents with; it stands
// in for any cloud.
const testProvider = "fake"

const (
	// branchConfigTTL bounds how stale a default-branch file read can be on
	// a replica that did not see the push that changed it.
	branchConfigTTL = time.Minute
	// runTTL is how long a run attempt's facts are reused for its jobs.
	runTTL = 10 * time.Minute
	// cacheLimit bounds each cache; past it the cache starts over.
	cacheLimit = 2000
)

// verdict is why a job will not be served; empty reason means it will.
type verdict struct {
	reason string
	detail string
}

// planJob decides how a queued job is served: by a dashboard profile, by a
// config-file runner, or not at all. Errors are GitHub or database
// failures, worth retrying; a job Airship will not run gets a verdict.
func (s *Service) planJob(ctx context.Context, installation db.Installation, repo github.Repository, job github.WorkflowJob) (Plan, verdict, error) {
	labels := lowerAll(job.Labels)
	candidates, err := s.q.FindProfilesByLabels(ctx, db.FindProfilesByLabelsParams{InstallationID: installation.ID, Labels: labels})
	if err != nil {
		return nil, verdict{}, fmt.Errorf("find profiles: %w", err)
	}
	if len(candidates) > 0 {
		match := MatchProfile(labels, candidates, repo.Private)
		if installation.SuspendedAt != nil && match.Profile != nil {
			return nil, verdict{reason: SkipInstallationSuspended}, nil
		}
		if match.Profile == nil {
			return nil, verdict{reason: match.SkipReason}, nil
		}

		return Plan{profileAttempt(*match.Profile)}, verdict{}, nil
	}

	if !runnerconfig.WantsConfig(job.Labels) {
		return nil, verdict{}, nil
	}
	if installation.SuspendedAt != nil {
		return nil, verdict{reason: SkipInstallationSuspended}, nil
	}

	return s.planFromConfig(ctx, installation, repo, job)
}

func (s *Service) planFromConfig(ctx context.Context, installation db.Installation, repo github.Repository, job github.WorkflowJob) (Plan, verdict, error) {
	activated, err := s.q.GetRepository(ctx, db.GetRepositoryParams{InstallationID: installation.ID, ID: repo.ID})
	if isNoRows(err) {
		return nil, verdict{reason: SkipRepositoryInactive, detail: repo.FullName + " is not activated for runner config files; an admin can activate it in the Airship dashboard"}, nil
	}
	if err != nil {
		return nil, verdict{}, fmt.Errorf("get repository: %w", err)
	}
	if !repo.Private && !activated.AllowPublic {
		return nil, verdict{reason: SkipPublicRepository, detail: "public repositories must be activated with public access allowed"}, nil
	}
	ghRepo, err := github.ParseRepo(repo.FullName)
	if err != nil {
		return nil, verdict{}, err
	}

	run, err := s.runAttempt(ctx, installation.ID, ghRepo, job.RunID, job.RunAttempt)
	if err != nil {
		return nil, verdict{}, fmt.Errorf("get run: %w", err)
	}
	// Code from a fork must not choose the machines it runs on, so its jobs
	// use the config on the default branch.
	ref := job.HeadSHA
	if run.FromFork() || ref == "" {
		ref = activated.DefaultBranch
	}

	var candidates []runnerconfig.Candidate
	repoFile, err := s.readRunnerFile(ctx, installation.ID, ghRepo, runnerconfig.RepoFile, ref, ref != activated.DefaultBranch)
	if err != nil {
		return nil, verdict{}, err
	}
	if repoFile.problem != "" {
		return nil, verdict{reason: SkipConfigInvalid, detail: repoFile.problem}, nil
	}
	for _, r := range repoFile.file.Runners {
		candidates = append(candidates, runnerconfig.Candidate{Source: runnerconfig.SourceRepository, Runner: r})
	}

	org, err := s.orgConfig(ctx, installation)
	if err != nil {
		return nil, verdict{}, err
	}
	for _, r := range org.file.Runners {
		candidates = append(candidates, runnerconfig.Candidate{Source: runnerconfig.SourceOrganization, Runner: r})
	}

	// A one-line label names its machine in runs-on, which a fork's pull
	// request can edit, so forks only get runners from config files.
	if !run.FromFork() {
		candidates = runnerconfig.WithOneLine(job.Labels, candidates)
	}

	attempts := runnerconfig.Plan(job.Labels, candidates)
	if len(attempts) == 0 {
		detail := fmt.Sprintf("no runner in %s or %s serves %s", runnerconfig.RepoFile, runnerconfig.GlobalFile, strings.Join(job.Labels, ", "))
		if run.FromFork() {
			detail += " (pull requests from forks cannot use one-line labels)"
		}
		if org.problem != "" {
			detail += "; " + org.problem
		}

		return nil, verdict{reason: SkipUnsatisfiableLabels, detail: detail}, nil
	}

	attempts, denied, err := s.checkAccess(ctx, installation.ID, ghRepo, run, org, attempts)
	if err != nil {
		return nil, verdict{}, err
	}
	if len(attempts) == 0 {
		return nil, verdict{reason: SkipAccessDenied, detail: denied}, nil
	}

	planes, err := s.q.ListDataPlanes(ctx, installation.ID)
	if err != nil {
		return nil, verdict{}, fmt.Errorf("list data planes: %w", err)
	}
	plan, missing := resolveDataPlanes(wantsFor(attempts), planes)
	if len(plan) == 0 {
		return nil, verdict{reason: SkipNoDataPlane, detail: missing}, nil
	}

	return plan, verdict{}, nil
}

// want is a config attempt before a data plane is chosen for it. Warm
// pools store these, so a data plane connected later is still used.
type want struct {
	Name      string   `json:"name"`
	Cloud     string   `json:"cloud"`
	Region    string   `json:"region,omitempty"`
	DataPlane string   `json:"dataPlane,omitempty"`
	Spec      Spec     `json:"spec"`
	Labels    []string `json:"labels"`
	Source    string   `json:"source"`
}

func wantFor(a runnerconfig.Attempt) want {
	return want{
		Source:    string(a.Source),
		Name:      a.Runner.Name,
		Cloud:     a.Runner.Cloud,
		Region:    a.Variant.Region,
		DataPlane: a.Runner.DataPlane,
		Spec: Spec{
			Arch:           a.Variant.Arch,
			InstanceType:   a.Variant.InstanceType,
			Image:          a.Variant.Image,
			DiskGB:         a.Variant.Disk.SizeGB,
			DiskType:       a.Variant.Disk.Type,
			DiskIOPS:       a.Variant.Disk.IOPS,
			DiskThroughput: a.Variant.Disk.Throughput,
			Spot:           a.Variant.Spot,
			Env:            a.Runner.Env,
			Privileged:     a.Runner.Privileged,
		},
		Labels: a.Labels,
	}
}

func wantsFor(attempts []runnerconfig.Attempt) []want {
	out := make([]want, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, wantFor(a))
	}

	return out
}

// resolveDataPlanes turns wants into a plan, with one attempt per data
// plane each can launch on. When the plan is empty, missing says why.
func resolveDataPlanes(wants []want, planes []db.DataPlane) (Plan, string) {
	var plan Plan
	var missing []string
	for _, w := range wants {
		matched := matchDataPlanes(planes, w.Cloud, w.Region, w.DataPlane)
		if len(matched) == 0 {
			missing = append(missing, w.describe())
		}
		for _, dp := range matched {
			plan = append(plan, Attempt{Source: w.Source, Name: w.Name, DataPlaneID: dp.ID, Spec: w.Spec, Labels: w.Labels})
		}
	}
	if len(plan) > 0 {
		return plan, ""
	}
	slices.Sort(missing)

	return nil, "no connected data plane matches " + strings.Join(slices.Compact(missing), "; ")
}

func (w want) describe() string {
	out := "cloud " + w.Cloud
	if w.Region != "" {
		out += " in " + w.Region
	}
	if w.DataPlane != "" {
		out = "data plane " + w.DataPlane + " (" + out + ")"
	}

	return out
}

// matchDataPlanes lists the data planes a runner can launch on, by name
// when it names one, otherwise by the cloud and region its agent reported.
// An agent that never connected has reported neither, so it only matches
// by name.
func matchDataPlanes(planes []db.DataPlane, cloud, region, name string) []db.DataPlane {
	var out []db.DataPlane
	for _, dp := range planes {
		if name != "" && !strings.EqualFold(dp.Name, name) {
			continue
		}
		provider := deref(dp.AgentProvider)
		switch {
		case provider == "":
			if name == "" {
				continue
			}
		case provider == testProvider:
		case provider != providerForCloud[cloud]:
			continue
		}
		if dpRegion := deref(dp.AgentRegion); region != "" && dpRegion != "" && !strings.EqualFold(dpRegion, region) {
			continue
		}
		if region != "" && deref(dp.AgentRegion) == "" && provider == "aws" {
			continue // an AWS agent always knows its region; this one is elsewhere
		}
		out = append(out, dp)
	}
	slices.SortFunc(out, func(a, b db.DataPlane) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// checkAccess drops organization runners the organization's .access.yml
// does not let this run use. denied explains the first refusal.
func (s *Service) checkAccess(ctx context.Context, installationID int64, repo github.Repo, run github.RunAttempt, org orgFiles, attempts []runnerconfig.Attempt) ([]runnerconfig.Attempt, string, error) {
	if org.access == nil {
		return attempts, "", nil
	}
	verdicts := map[string]bool{}
	var out []runnerconfig.Attempt
	denied := ""
	for _, a := range attempts {
		if a.Source != runnerconfig.SourceOrganization {
			out = append(out, a)

			continue
		}
		allowed, seen := verdicts[a.Runner.Name]
		if !seen {
			var err error
			allowed, err = s.allowedBy(ctx, installationID, repo, run, org.access, a.Runner.Name)
			if err != nil {
				return nil, "", err
			}
			verdicts[a.Runner.Name] = allowed
		}
		if allowed {
			out = append(out, a)
		} else if denied == "" {
			denied = fmt.Sprintf("%s does not let %s launch %s on %s", runnerconfig.AccessFile, cmpOr(run.Starter(), "this run"), a.Runner.Name, repo.FullName())
			if run.PullRequest() {
				denied += " from a pull request"
			}
		}
	}

	return out, denied, nil
}

func (s *Service) allowedBy(ctx context.Context, installationID int64, repo github.Repo, run github.RunAttempt, access *runnerconfig.Access, runner string) (bool, error) {
	policies, restricted := access.Restricted(runner, repo.Name)
	if !restricted {
		return true, nil
	}
	actor := strings.ToLower(run.Starter())
	var roles []string
	rolesRead := false
	for _, p := range policies {
		if run.PullRequest() && !p.PullRequest {
			continue
		}
		if p.Open() {
			return true, nil
		}
		if actor == "" {
			continue
		}
		if slices.Contains(p.Users, actor) {
			return true, nil
		}
		if len(p.Roles) > 0 {
			if !rolesRead {
				var err error
				roles, err = s.github.RepoRoles(ctx, installationID, repo, actor)
				if err != nil {
					return false, fmt.Errorf("read repository role: %w", err)
				}
				rolesRead = true
			}
			for _, role := range roles {
				if role != "" && slices.Contains(p.Roles, role) {
					return true, nil
				}
			}
		}
		for _, team := range p.Teams {
			member, err := s.github.TeamMember(ctx, installationID, repo.Owner, team, actor)
			if err != nil {
				return false, fmt.Errorf("read team membership: %w", err)
			}
			if member {
				return true, nil
			}
		}
	}

	return false, nil
}

// runnerFile is a parsed runner file, or why it could not be used. A
// missing file is an empty one.
type runnerFile struct {
	file    runnerconfig.File
	sha     string
	problem string
}

// orgFiles is an organization's .airship repository: its runners and
// access rules. problem explains a file that could not be used, which then
// counts as empty, except that a broken .access.yml closes every runner
// rather than opening them.
type orgFiles struct {
	file    runnerconfig.File
	access  *runnerconfig.Access
	sha     string
	problem string
}

type cacheEntry[T any] struct {
	value   T
	expires time.Time
}

// configCache holds config reads. Files at a commit never change and are
// kept until the cache fills; branch reads expire.
type configCache struct {
	mu    sync.Mutex
	files map[string]cacheEntry[runnerFile]
	orgs  map[int64]cacheEntry[orgFiles]
	runs  map[string]cacheEntry[github.RunAttempt]
}

func newConfigCache() *configCache {
	return &configCache{
		files: map[string]cacheEntry[runnerFile]{},
		orgs:  map[int64]cacheEntry[orgFiles]{},
		runs:  map[string]cacheEntry[github.RunAttempt]{},
	}
}

func cacheGet[K comparable, T any](mu *sync.Mutex, m map[K]cacheEntry[T], key K) (T, bool) {
	mu.Lock()
	defer mu.Unlock()
	entry, ok := m[key]
	if !ok || (!entry.expires.IsZero() && time.Now().After(entry.expires)) {
		var zero T

		return zero, false
	}

	return entry.value, true
}

func cachePut[K comparable, T any](mu *sync.Mutex, m map[K]cacheEntry[T], key K, value T, ttl time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	if len(m) >= cacheLimit {
		clear(m)
	}
	entry := cacheEntry[T]{value: value}
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	m[key] = entry
}

// ForgetConfig drops cached default-branch reads for a repository, after a
// push that may have changed them.
func (s *Service) ForgetConfig(installationID int64, repoFullName string) {
	c := s.configs
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := strings.ToLower(repoFullName) + "@"
	for key := range c.files {
		if strings.HasPrefix(key, prefix) {
			delete(c.files, key)
		}
	}
	delete(c.orgs, installationID)
}

// readRunnerFile reads a repository's .airship.yml at ref. pinned says ref
// is a commit, whose content never changes.
func (s *Service) readRunnerFile(ctx context.Context, installationID int64, repo github.Repo, path, ref string, pinned bool) (runnerFile, error) {
	key := strings.ToLower(repo.FullName()) + "@" + ref + ":" + path
	if cached, ok := cacheGet(&s.configs.mu, s.configs.files, key); ok {
		return cached, nil
	}
	f, found, err := s.github.GetFile(ctx, installationID, repo, path, ref, runnerconfig.MaxFileBytes)
	var out runnerFile
	switch {
	case errors.Is(err, github.ErrFileTooLarge):
		out.problem = tooLarge(path)
	case err != nil:
		return runnerFile{}, fmt.Errorf("read %s: %w", path, err)
	case found:
		out.sha = f.SHA
		file, perr := runnerconfig.Parse(path, f.Content)
		out.file, out.problem = file, errorText(perr)
	}
	ttl := branchConfigTTL
	if pinned {
		ttl = 0
	}
	cachePut(&s.configs.mu, s.configs.files, key, out, ttl)

	return out, nil
}

// orgConfig reads the organization's .airship repository.
func (s *Service) orgConfig(ctx context.Context, installation db.Installation) (orgFiles, error) {
	if installation.AccountType != "Organization" {
		return orgFiles{}, nil
	}
	if cached, ok := cacheGet(&s.configs.mu, s.configs.orgs, installation.ID); ok {
		return cached, nil
	}
	repo := github.Repo{Owner: installation.AccountLogin, Name: runnerconfig.GlobalRepo}
	var out orgFiles

	global, found, err := s.github.GetFile(ctx, installation.ID, repo, runnerconfig.GlobalFile, "", runnerconfig.MaxFileBytes)
	switch {
	case errors.Is(err, github.ErrFileTooLarge):
		out.problem = tooLarge(runnerconfig.GlobalFile)
	case err != nil:
		return orgFiles{}, fmt.Errorf("read %s: %w", runnerconfig.GlobalFile, err)
	case found:
		out.sha = global.SHA
		file, perr := runnerconfig.Parse(runnerconfig.GlobalFile, global.Content)
		if perr != nil {
			out.problem = perr.Error()
		} else {
			out.file = file
		}
	}

	if len(out.file.Runners) > 0 {
		access, found, err := s.github.GetFile(ctx, installation.ID, repo, runnerconfig.AccessFile, "", runnerconfig.MaxFileBytes)
		switch {
		case errors.Is(err, github.ErrFileTooLarge):
			out = orgFiles{problem: tooLarge(runnerconfig.AccessFile) + "; organization runners are closed until it is fixed"}
		case err != nil:
			return orgFiles{}, fmt.Errorf("read %s: %w", runnerconfig.AccessFile, err)
		case found:
			parsed, perr := runnerconfig.ParseAccess(access.Content)
			if perr != nil {
				// Failing open would hand restricted runners to everyone.
				out = orgFiles{problem: perr.Error() + "; organization runners are closed until it is fixed"}
			} else {
				out.access = &parsed
			}
		}
	}
	cachePut(&s.configs.mu, s.configs.orgs, installation.ID, out, branchConfigTTL)

	return out, nil
}

func (s *Service) runAttempt(ctx context.Context, installationID int64, repo github.Repo, runID int64, attempt int32) (github.RunAttempt, error) {
	key := fmt.Sprintf("%s/%d/%d", strings.ToLower(repo.FullName()), runID, attempt)
	if cached, ok := cacheGet(&s.configs.mu, s.configs.runs, key); ok {
		return cached, nil
	}
	run, err := s.github.GetRunAttempt(ctx, installationID, repo, runID, attempt)
	if err != nil {
		return github.RunAttempt{}, err //nolint:wrapcheck // wrapped by the caller
	}
	cachePut(&s.configs.mu, s.configs.runs, key, run, runTTL)

	return run, nil
}

func tooLarge(file string) string {
	return (&runnerconfig.Error{File: file, Detail: fmt.Sprintf("file is over %d bytes", runnerconfig.MaxFileBytes)}).Error()
}

func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
