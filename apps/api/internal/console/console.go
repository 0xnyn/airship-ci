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
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Store is the subset of db.Querier the dashboard needs.
type Store interface {
	GetUserInstallationByLogin(ctx context.Context, arg db.GetUserInstallationByLoginParams) (db.GetUserInstallationByLoginRow, error)

	CreateDataPlane(ctx context.Context, arg db.CreateDataPlaneParams) (db.DataPlane, error)
	ListDataPlanes(ctx context.Context, installationID int64) ([]db.DataPlane, error)
	GetDataPlane(ctx context.Context, arg db.GetDataPlaneParams) (db.DataPlane, error)
	RotateDataPlaneToken(ctx context.Context, arg db.RotateDataPlaneTokenParams) (db.DataPlane, error)
	DeleteDataPlane(ctx context.Context, arg db.DeleteDataPlaneParams) (int64, error)
	CountDataPlaneProfiles(ctx context.Context, dataPlaneID uuid.UUID) (int64, error)

	CreateProfile(ctx context.Context, arg db.CreateProfileParams) (db.Profile, error)
	ListProfiles(ctx context.Context, installationID int64) ([]db.Profile, error)
	GetProfile(ctx context.Context, arg db.GetProfileParams) (db.Profile, error)
	UpdateProfile(ctx context.Context, arg db.UpdateProfileParams) (db.Profile, error)

	ListJobs(ctx context.Context, arg db.ListJobsParams) ([]db.Job, error)
	JobStats(ctx context.Context, arg db.JobStatsParams) (db.JobStatsRow, error)
	ListRunners(ctx context.Context, arg db.ListRunnersParams) ([]db.Runner, error)
	CountActiveRunners(ctx context.Context, installationID int64) ([]db.CountActiveRunnersRow, error)
}

// Profiles deletes profiles; it lives in the fleet service because it has
// to coordinate with runner scheduling.
type Profiles interface {
	DeleteProfile(ctx context.Context, installationID int64, id uuid.UUID) error
}

// Config wires the dashboard API.
type Config struct {
	Store          Store
	Profiles       Profiles
	Sessions       *auth.Sessions
	AllowedOrigins []string
	// PublicURL is the control plane URL agents connect to.
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

	huma.Register(api, op("listDataPlanes", http.MethodGet, "/v1/orgs/{org}/data-planes", "List data planes", "Data planes", 0), h.listDataPlanes)
	huma.Register(api, op("createDataPlane", http.MethodPost, "/v1/orgs/{org}/data-planes", "Create a data plane", "Data planes", http.StatusCreated), h.createDataPlane)
	huma.Register(api, op("rotateDataPlaneToken", http.MethodPost, "/v1/orgs/{org}/data-planes/{id}/rotate-token", "Rotate a data plane's agent token", "Data planes", 0), h.rotateToken)
	huma.Register(api, op("deleteDataPlane", http.MethodDelete, "/v1/orgs/{org}/data-planes/{id}", "Delete a data plane", "Data planes", http.StatusNoContent), h.deleteDataPlane)

	huma.Register(api, op("listProfiles", http.MethodGet, "/v1/orgs/{org}/profiles", "List runner profiles", "Profiles", 0), h.listProfiles)
	huma.Register(api, op("createProfile", http.MethodPost, "/v1/orgs/{org}/profiles", "Create a runner profile", "Profiles", http.StatusCreated), h.createProfile)
	huma.Register(api, op("updateProfile", http.MethodPatch, "/v1/orgs/{org}/profiles/{id}", "Update a runner profile", "Profiles", 0), h.updateProfile)
	huma.Register(api, op("deleteProfile", http.MethodDelete, "/v1/orgs/{org}/profiles/{id}", "Delete a runner profile", "Profiles", http.StatusNoContent), h.deleteProfile)
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
		return db.Installation{}, huma.Error403Forbidden("only organization owners can change Airship settings")
	}

	return row.Installation, nil
}

// isUniqueViolation reports a unique constraint failure.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// onlineWindow is how recently an agent must have synced to count as
// online. Agents long-poll for 25 seconds, so this allows for one missed
// round trip.
const onlineWindow = 90 * time.Second
