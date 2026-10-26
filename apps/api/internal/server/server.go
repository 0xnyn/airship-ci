// Package server assembles the HTTP API: the Huma operation registry, its
// OpenAPI document, the GitHub webhook and the middleware around them.
package server

import (
	"log/slog"
	"net/http"

	"github.com/wizenheimer/airship/apps/api/internal/agentapi"
	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/console"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/health"
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

// NewAPI registers every operation on mux and returns the Huma API.
//
// It must not touch Deps at registration time: cmd/openapi calls it with
// zero-value Deps just to read the generated OpenAPI document.
func NewAPI(mux *http.ServeMux, deps Deps) huma.API {
	cfg := huma.DefaultConfig(Title, deps.Version)
	cfg.Info.Description = "Airship is a managed control plane for GitHub Actions runners that run in your own cloud. " +
		"The TypeScript client in packages/client is generated from this document."
	// Drop the $schema property and Link header Huma adds to every response;
	// they leak into the generated TypeScript types without helping callers.
	cfg.CreateHooks = nil

	api := humago.New(mux, cfg)

	health.Register(api, deps.DB, deps.Version)
	auth.Register(api, mux, auth.Config{
		Store: deps.Queries, Sessions: deps.Sessions, OAuth: deps.OAuth, App: deps.App,
		PublicURL: deps.PublicURL, WebURL: deps.WebURL, AppSlug: deps.AppSlug, DevLogin: deps.DevLogin,
		AllowedOrigins: deps.CORSOrigins, Logger: deps.Logger,
	})
	console.Register(api, console.Config{
		Profiles: deps.Fleet,
		Store:    deps.Queries, Sessions: deps.Sessions, AllowedOrigins: deps.CORSOrigins, PublicURL: deps.PublicURL,
	})
	agentapi.Register(api, deps.Queries, deps.Fleet)

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
