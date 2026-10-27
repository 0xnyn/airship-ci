// Package console serves the dashboard API: everything a signed-in user
// does within one GitHub organization. Every route is scoped by the {org}
// path parameter, and every resource id is checked against that org, so a
// user can never reach another tenant's data by guessing ids.
package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Store is the subset of db.Querier the dashboard needs.
type Store interface {
	GetUserInstallationByLogin(ctx context.Context, arg db.GetUserInstallationByLoginParams) (db.GetUserInstallationByLoginRow, error)
	SetInstallationMaxRunners(ctx context.Context, arg db.SetInstallationMaxRunnersParams) (db.Installation, error)
	ListInstallationMembers(ctx context.Context, installationID int64) ([]db.ListInstallationMembersRow, error)

	ListJobs(ctx context.Context, arg db.ListJobsParams) ([]db.Job, error)
	JobStats(ctx context.Context, arg db.JobStatsParams) (db.JobStatsRow, error)
	JobDailyStats(ctx context.Context, arg db.JobDailyStatsParams) ([]db.JobDailyStatsRow, error)
	JobRepositoryStats(ctx context.Context, arg db.JobRepositoryStatsParams) ([]db.JobRepositoryStatsRow, error)
	ListRunners(ctx context.Context, arg db.ListRunnersParams) ([]db.Runner, error)
	CountRunnersByState(ctx context.Context, installationID int64) ([]db.CountRunnersByStateRow, error)
	ListEvents(ctx context.Context, arg db.ListEventsParams) ([]db.Event, error)

	ListCloudCredentials(ctx context.Context, installationID int64) ([]db.CloudCredential, error)

	ListRepositories(ctx context.Context, installationID int64) ([]db.Repository, error)
	ListInstallationIdlePools(ctx context.Context, installationID int64) ([]db.IdlePool, error)
	CountInstallationPoolRunners(ctx context.Context, installationID int64) ([]db.CountInstallationPoolRunnersRow, error)

	CreateAPIToken(ctx context.Context, arg db.CreateAPITokenParams) (db.ApiToken, error)
	ListAPITokens(ctx context.Context, installationID int64) ([]db.ApiToken, error)
	CountAPITokens(ctx context.Context, installationID int64) (int64, error)
	DeleteAPIToken(ctx context.Context, arg db.DeleteAPITokenParams) (int64, error)
	AuthenticateAPIToken(ctx context.Context, tokenHash []byte) (db.AuthenticateAPITokenRow, error)
	TouchAPIToken(ctx context.Context, id uuid.UUID) error
	MarkRunnerProvisioned(ctx context.Context, arg db.MarkRunnerProvisionedParams) (db.MarkRunnerProvisionedRow, error)
}

// Fleet is the scheduling work the dashboard triggers: it coordinates with
// runners in flight or calls GitHub and the clouds, so it lives in the
// fleet service.
type Fleet interface {
	ListInstallationRepositories(ctx context.Context, installationID int64) ([]github.Repository, error)
	ActivateRepository(ctx context.Context, installationID, repoID int64, allowPublic bool, by *uuid.UUID) (db.Repository, error)
	DeactivateRepository(ctx context.Context, installationID, repoID int64) error
	OrganizationConfig(ctx context.Context, installation db.Installation) (fleet.OrganizationConfig, error)
	ConnectCloud(ctx context.Context, installationID int64, cloud string, secret []byte, defaultRegion string, by *uuid.UUID) (db.CloudCredential, error)
	DisconnectCloud(ctx context.Context, installationID int64, cloud string) error
	ProvisionScript(ctx context.Context, runnerID uuid.UUID, sealed []byte) (string, error)
	ReadAccessControl(ctx context.Context, installation db.Installation) (fleet.AccessControl, error)
	UpdateAccessControl(ctx context.Context, installation db.Installation, changes []runnerconfig.AccessChange) (fleet.AccessControlUpdate, error)
}

// Config wires the dashboard API.
type Config struct {
	Store          Store
	Fleet          Fleet
	Sessions       *auth.Sessions
	AllowedOrigins []string
	// PublicURL is the control plane's URL, shown in setup instructions.
	PublicURL string
}

type handlers struct {
	Config
}

// Register adds the dashboard endpoints.
func Register(api huma.API, cfg Config) {
	h := &handlers{Config: cfg}
	secured := huma.Middlewares{cfg.Sessions.Require(api, cfg.AllowedOrigins)}
	op := func(id, method, path, summary, tag string, status int) huma.Operation {
		return huma.Operation{
			OperationID: id, Method: method, Path: path, Summary: summary,
			Tags: []string{tag}, DefaultStatus: status, Middlewares: secured,
		}
	}

	huma.Register(api, op("getOverview", http.MethodGet, "/v1/orgs/{org}/overview", "Get an organization's overview", "Activity", 0), h.overview)
	huma.Register(api, op("listJobs", http.MethodGet, "/v1/orgs/{org}/jobs", "List jobs", "Activity", 0), h.listJobs)
	huma.Register(api, op("listRunners", http.MethodGet, "/v1/orgs/{org}/runners", "List runners", "Activity", 0), h.listRunners)
	huma.Register(api, op("listEvents", http.MethodGet, "/v1/orgs/{org}/events", "List events", "Activity", 0), h.listEvents)
	huma.Register(api, op("getAnalytics", http.MethodGet, "/v1/orgs/{org}/analytics", "Get usage analytics", "Activity", 0), h.analytics)

	huma.Register(api, op("listClouds", http.MethodGet, "/v1/orgs/{org}/clouds", "List connected clouds", "Cloud", 0), h.listClouds)
	huma.Register(api, op("connectAws", http.MethodPut, "/v1/orgs/{org}/clouds/aws", "Connect AWS", "Cloud", 0), h.connectAWS)
	huma.Register(api, op("connectGcp", http.MethodPut, "/v1/orgs/{org}/clouds/gcp", "Connect Google Cloud", "Cloud", 0), h.connectGCP)
	huma.Register(api, op("disconnectCloud", http.MethodDelete, "/v1/orgs/{org}/clouds/{cloud}", "Disconnect a cloud", "Cloud", http.StatusNoContent), h.disconnectCloud)

	huma.Register(api, op("listRepositories", http.MethodGet, "/v1/orgs/{org}/repositories", "List repositories and their runner config", "Repositories", 0), h.listRepositories)
	huma.Register(api, op("activateRepository", http.MethodPut, "/v1/orgs/{org}/repositories/{repo}/activation", "Activate a repository", "Repositories", 0), h.activateRepository)
	huma.Register(api, op("deactivateRepository", http.MethodDelete, "/v1/orgs/{org}/repositories/{repo}/activation", "Deactivate a repository", "Repositories", http.StatusNoContent), h.deactivateRepository)
	huma.Register(api, op("getOrganizationConfig", http.MethodGet, "/v1/orgs/{org}/runner-config", "Get the organization's runner config", "Repositories", 0), h.organizationConfig)
	huma.Register(api, op("validateRunnerConfig", http.MethodPost, "/v1/orgs/{org}/runner-config/validate", "Check a runner config file", "Repositories", 0), h.validateConfig)
	huma.Register(api, op("listPools", http.MethodGet, "/v1/orgs/{org}/pools", "List idle runner pools", "Repositories", 0), h.listPools)

	huma.Register(api, op("listMembers", http.MethodGet, "/v1/orgs/{org}/members", "List organization members with dashboard access", "Organization", 0), h.listMembers)
	huma.Register(api, op("getSettings", http.MethodGet, "/v1/orgs/{org}/settings", "Get organization settings", "Organization", 0), h.getSettings)
	huma.Register(api, op("updateSettings", http.MethodPatch, "/v1/orgs/{org}/settings", "Update organization settings", "Organization", 0), h.updateSettings)

	registerAPITokens(api, h, op)
}

type orgInput struct {
	Org string `doc:"GitHub organization login" maxLength:"100" minLength:"1" path:"org"`
}

type orgIDInput struct {
	Org string    `doc:"GitHub organization login" maxLength:"100" minLength:"1" path:"org"`
	ID  uuid.UUID `doc:"Resource ID"               format:"uuid"   path:"id"`
}

// org resolves the {org} path parameter to an installation the user can
// see. admin additionally requires the user to administer it.
func (h *handlers) org(ctx context.Context, login string, admin bool) (db.Installation, error) {
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return db.Installation{}, huma.Error401Unauthorized("sign in required")
	}
	row, err := h.Store.GetUserInstallationByLogin(ctx, db.GetUserInstallationByLoginParams{UserID: user.ID, Login: login})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Installation{}, huma.Error404NotFound("organization not found")
	}
	if err != nil {
		return db.Installation{}, fmt.Errorf("resolve organization: %w", err)
	}
	if admin && row.Role != "admin" {
		return db.Installation{}, huma.Error403Forbidden("only organization owners can change settings")
	}
	if admin && row.Installation.SuspendedAt != nil {
		// Nothing can run while GitHub has the app suspended, as the
		// developer API already says; reads stay open so the state shows.
		return db.Installation{}, huma.Error409Conflict("the GitHub App installation is suspended; unsuspend it on GitHub first")
	}

	return row.Installation, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
