// Package server assembles the HTTP API: the Huma operation registry, its
// OpenAPI document, the GitHub webhook and the middleware around them.
package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/console"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/health"
	"github.com/wizenheimer/airship/apps/api/internal/runnerapi"
	"github.com/wizenheimer/airship/apps/api/internal/webhook"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/rs/cors"
)

// Title is the API name shown in the OpenAPI document and docs page.
const Title = "Airship API"

// Deps are the runtime dependencies the handlers need.
type Deps struct {
	DB       health.Pinger
	Queries  *db.Queries
	Fleet    *fleet.Service
	Sessions *auth.Sessions
	// OAuth and App are nil without a configured GitHub App.
	OAuth         *github.OAuth
	App           *github.App
	WebhookSecret []byte
	Version       string
	Logger        *slog.Logger

	PublicURL   string
	WebURL      string
	AppSlug     string
	DevLogin    bool
	CORSOrigins []string
}

func init() {
	// Huma's default error puts the offending value into each detail. For
	// a cloud secret that fails validation that would echo the secret, so
	// details carry their location and message only.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		details := make([]*huma.ErrorDetail, 0, len(errs))
		for _, err := range errs {
			if err == nil {
				continue
			}
			var detailer huma.ErrorDetailer
			if errors.As(err, &detailer) {
				detail := detailer.ErrorDetail()
				details = append(details, &huma.ErrorDetail{Message: detail.Message, Location: detail.Location})

				continue
			}
			details = append(details, &huma.ErrorDetail{Message: err.Error()})
		}

		return &huma.ErrorModel{Status: status, Title: http.StatusText(status), Detail: msg, Errors: details}
	}
}

// NewAPI registers every operation on mux and returns the Huma API.
//
// It must not touch Deps at registration time: cmd/openapi calls it with
// zero-value Deps just to read the generated OpenAPI document.
func NewAPI(mux *http.ServeMux, deps Deps) huma.API {
	cfg := huma.DefaultConfig(Title, deps.Version)
	cfg.Info.Description = "Airship runs GitHub Actions jobs on ephemeral machines in your own cloud account, " +
		"configured by an airship.yaml in each repository. The TypeScript client in packages/client is generated from this document."
	// Drop the $schema property and Link header Huma adds to every response;
	// they leak into the generated TypeScript types without helping callers.
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"apiToken": {Type: "http", Scheme: "bearer", Description: "Developer API token (asa_...), created by an organization owner in the dashboard."},
	}

	api := humago.New(mux, cfg)

	health.Register(api, deps.DB, deps.Version)
	auth.Register(api, mux, auth.Config{
		Store: deps.Queries, Sessions: deps.Sessions, OAuth: deps.OAuth, App: deps.App,
		PublicURL: deps.PublicURL, WebURL: deps.WebURL, AppSlug: deps.AppSlug, DevLogin: deps.DevLogin,
		AllowedOrigins: deps.CORSOrigins, Logger: deps.Logger,
	})
	console.Register(api, console.Config{
		Fleet: deps.Fleet,
		Store: deps.Queries, Sessions: deps.Sessions, AllowedOrigins: deps.CORSOrigins, PublicURL: deps.PublicURL,
	})
	runnerapi.Register(api, deps.Fleet)

	return api
}

// NewHandler returns the full HTTP handler: routes plus middleware.
func NewHandler(deps Deps) http.Handler {
	mux := http.NewServeMux()
	NewAPI(mux, deps)
	if len(deps.WebhookSecret) > 0 {
		mux.Handle("POST "+webhook.Path, webhook.New(deps.WebhookSecret, deps.Fleet, deps.Logger))
	}

	var handler http.Handler = mux
	handler = cors.New(cors.Options{
		AllowedOrigins: deps.CORSOrigins,
		AllowedMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPatch,
			http.MethodPut, http.MethodDelete, http.MethodOptions,
		},
		AllowedHeaders:   []string{"Content-Type"},
		AllowCredentials: true,
	}).Handler(handler)
	handler = securityHeaders(handler)
	handler = logRequests(deps.Logger, handler)
	handler = recoverPanics(deps.Logger, handler)

	return handler
}
