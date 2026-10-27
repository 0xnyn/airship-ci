package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

// Organization idle runners come from idle: sections in .cirun.global.yml.
// They register on the organization, in a runner group per definition
// that is visible only to the activated repositories allowed to use it
// (every activated repository, or those named in the definition's
// .access.yml policies). Per-person rules cannot apply to a runner booted
// before any job exists; the group is the boundary.

const (
	orgConfigResync = time.Hour
	orgConfigRetry  = time.Minute
	orgConfigBatch  = 20
)

// runnerGroupName is the organization runner group for a definition.
func runnerGroupName(definition string) string {
	return "airship-" + definition
}

// refreshOrganizationConfigs rereads .cirun.global.yml for organizations
// pushed to, activated in, or not read for a while, and updates their
// pools and runner groups.
func (s *Service) refreshOrganizationConfigs(ctx context.Context) error {
	now := time.Now()
	installations, err := s.q.ListStaleOrganizationConfigs(ctx, db.ListStaleOrganizationConfigsParams{
		RetryBefore: now.Add(-orgConfigRetry), CheckedBefore: now.Add(-orgConfigResync), MaxResults: orgConfigBatch,
	})
	if err != nil {
		return fmt.Errorf("list stale organization configs: %w", err)
	}
	for _, installation := range installations {
		if err := s.RefreshOrganizationConfig(ctx, installation); err != nil {
			s.log.WarnContext(ctx, "refresh organization config", slog.String("org", installation.AccountLogin), slog.Any("error", err))
		}
	}

	return nil
}

// RefreshOrganizationConfig reads the organization's .cirun.global.yml
// and makes its pools and runner groups match.
func (s *Service) RefreshOrganizationConfig(ctx context.Context, installation db.Installation) error {
	if err := s.q.TouchOrganizationConfig(ctx, installation.ID); err != nil {
		return fmt.Errorf("touch organization config: %w", err)
	}
	if installation.AccountType != "Organization" {
		return nil
	}
	s.ForgetConfig(installation.ID, installation.AccountLogin+"/"+runnerconfig.GlobalRepo)
	org, err := s.orgConfig(ctx, installation)
	if err != nil {
		return err
	}
	if org.problem != "" && (installation.OrgConfigError == nil || *installation.OrgConfigError != org.problem) {
		s.record(ctx, installation.ID, "config.invalid", levelError, installation.AccountLogin+"/"+runnerconfig.GlobalRepo+": "+org.problem,
			eventFields{repository: installation.AccountLogin + "/" + runnerconfig.GlobalRepo})
	}
	if org.problem != "" {
		// Unreadable or broken: keep the pools it had, as a repository
		// does, so a typo does not drop warm capacity. Jobs see the problem
		// through the config cache either way.
		return s.wrap("save organization config status", s.q.SetOrganizationConfig(ctx, db.SetOrganizationConfigParams{ID: installation.ID, Sha: nilIfEmpty(org.sha), Error: nilIfEmpty(org.problem)}))
	}

	repos, err := s.q.ListRepositories(ctx, installation.ID)
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}
	var keep []string
	for _, r := range org.file.Runners {
		if r.Idle == nil {
			continue
		}
		keep = append(keep, r.Name)
		plan, err := json.Marshal(wantsFor(runnerconfig.PoolAttempts(runnerconfig.SourceOrganization, r)))
		if err != nil {
			return fmt.Errorf("encode pool plan: %w", err)
		}
		idle, err := json.Marshal(idleSpecFor(r.Idle))
		if err != nil {
			return fmt.Errorf("encode idle: %w", err)
		}
		pool, err := s.q.UpsertIdlePool(ctx, db.UpsertIdlePoolParams{
			InstallationID: installation.ID, Name: r.Name, Plan: plan, Idle: idle, SourceSha: org.sha,
		})
		if err != nil {
			return fmt.Errorf("save organization pool: %w", err)
		}
		// The group decides which repositories GitHub may hand the pool's
		// runners to.
		groupID, err := s.github.EnsureRunnerGroup(ctx, installation.ID, installation.AccountLogin, runnerGroupName(r.Name), allowedRepositories(org, r.Name, repos))
		if err != nil {
			s.record(ctx, installation.ID, "config.invalid", levelError,
				fmt.Sprintf("Idle runners for %s need a runner group GitHub refused to create: %s (the app needs the organization's Self-hosted runners permission)", r.Name, err.Error()),
				eventFields{})

			return fmt.Errorf("runner group for %s: %w", r.Name, err)
		}
		if err := s.q.SetIdlePoolRunnerGroup(ctx, db.SetIdlePoolRunnerGroupParams{ID: pool.ID, RunnerGroupID: &groupID}); err != nil {
			return fmt.Errorf("save runner group: %w", err)
		}
	}
	if keep == nil {
		keep = []string{}
	}
	if _, err := s.q.DeleteOrganizationIdlePoolsExcept(ctx, db.DeleteOrganizationIdlePoolsExceptParams{InstallationID: installation.ID, Keep: keep}); err != nil {
		return fmt.Errorf("delete organization pools: %w", err)
	}
	if err := s.q.SetOrganizationConfig(ctx, db.SetOrganizationConfigParams{ID: installation.ID, Sha: nilIfEmpty(org.sha), Error: nilIfEmpty(org.problem)}); err != nil {
		return fmt.Errorf("save organization config status: %w", err)
	}
	s.fillInstallationPools(ctx, installation.ID)

	return nil
}

// allowedRepositories are the activated repositories a definition's idle
// runners may serve: all of them, or those its .access.yml policies name.
func allowedRepositories(org orgFiles, definition string, repos []db.Repository) []int64 {
	out := []int64{}
	for _, repo := range repos {
		if !repo.Private && !repo.AllowPublic {
			continue
		}
		if org.access != nil {
			name := repo.FullName
			if _, after, ok := strings.Cut(repo.FullName, "/"); ok {
				name = after
			}
			if policies, restricted := org.access.Restricted(definition, name); restricted && len(policies) == 0 {
				continue
			}
		}
		out = append(out, repo.ID)
	}
	slices.Sort(out)

	return out
}
