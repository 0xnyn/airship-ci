// Command api runs the Airship control plane: the HTTP API, the GitHub
// webhook receiver, the runner launcher and the background reconcilers.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
	azurecloud "github.com/wizenheimer/airship/apps/api/internal/cloud/azure"
	daytonacloud "github.com/wizenheimer/airship/apps/api/internal/cloud/daytona"
	gcpcloud "github.com/wizenheimer/airship/apps/api/internal/cloud/gcp"
	"github.com/wizenheimer/airship/apps/api/internal/config"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/secrets"
	"github.com/wizenheimer/airship/apps/api/internal/server"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	shutdownTimeout    = 30 * time.Second
	redeliveryInterval = 2 * time.Minute
	// Dashboard roles are re-checked with GitHub this often, so removing
	// someone from the organization revokes their access within the hour.
	roleRefreshAfter    = time.Hour
	roleRefreshInterval = time.Minute
	roleRefreshBatch    = 100
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("api stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A local .env is optional; real environment variables always win.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	keys, err := secrets.ParseKeys(cfg.CredentialsKey)
	if err != nil {
		return fmt.Errorf("CREDENTIALS_KEY: %w", err)
	}
	keyring, err := secrets.New(keys...)
	if err != nil {
		return fmt.Errorf("CREDENTIALS_KEY: %w", err)
	}

	app, oauth, err := newGitHub(cfg)
	if err != nil {
		return err
	}
	if app == nil {
		logger.Warn("no GitHub App configured: sign-in, webhooks and runner launches are disabled")
	}

	fleetCfg := fleet.DefaultConfig()
	fleetCfg.RunnerVersion = cfg.RunnerVersion
	fleetCfg.MaxLifetime = cfg.RunnerMaxLifetime
	fleetCfg.IdleTimeout = cfg.RunnerIdleTimeout
	fleetCfg.PublicURL = cfg.PublicURL
	var gh fleet.GitHub = unconfiguredGitHub{}
	if app != nil {
		gh = fleet.AppGitHub{App: app}
	}
	clouds := cloud.Registry{
		cloud.AWS:     awscloud.New(awscloud.Options{Endpoint: cfg.AWSEndpoint}),
		cloud.GCP:     gcpcloud.New(gcpcloud.Options{Endpoint: cfg.GCPEndpoint}),
		cloud.Daytona: daytonacloud.New(daytonacloud.Options{Endpoint: cfg.DaytonaEndpoint}),
		cloud.Azure:   azurecloud.New(azurecloud.Options{Endpoint: cfg.AzureEndpoint, Logger: logger}),
	}
	scheduler := fleet.New(pool, gh, clouds, keyring, fleetCfg, logger)

	queries := db.New(pool)
	handler := server.NewHandler(server.Deps{
		DB:            pool,
		Queries:       queries,
		Fleet:         scheduler,
		Sessions:      auth.NewSessions(queries, !cfg.IsDevelopment()),
		OAuth:         oauth,
		App:           app,
		WebhookSecret: []byte(cfg.GitHub.WebhookSecret),
		Version:       version,
		Logger:        logger,
		PublicURL:     cfg.PublicURL,
		WebURL:        cfg.WebURL,
		AppSlug:       cfg.GitHub.AppSlug,
		DevLogin:      cfg.DevLogin,
		CORSOrigins:   cfg.CORSOrigins,
	})

	var background sync.WaitGroup
	background.Go(func() { scheduler.Run(ctx, cfg.ReconcileInterval) })
	background.Go(func() { scheduler.RunLauncher(ctx, cfg.LaunchInterval) })
	if app != nil {
		background.Go(func() { fleet.NewRedeliverer(pool, app, logger).Run(ctx, redeliveryInterval) })
		roles := &auth.RoleRefresher{Store: queries, App: app, Logger: logger, RefreshAfter: roleRefreshAfter, Batch: roleRefreshBatch}
		background.Go(func() { roles.Run(ctx, roleRefreshInterval) })
	}

	// Requests in flight when the signal arrives finish during Shutdown's
	// drain instead of being cancelled with it; the background loops stop
	// at once.
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	srv := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		// Long enough for the cache proxy binary to download on a slow
		// link; everything else answers in milliseconds.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
		BaseContext:  func(net.Listener) context.Context { return requestCtx },
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("api listening", slog.String("addr", srv.Addr), slog.String("docs", fmt.Sprintf("http://localhost:%d/docs", cfg.Port)))
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	cancelRequests()
	background.Wait()
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return nil
}

func newGitHub(cfg config.Config) (*github.App, *github.OAuth, error) {
	if !cfg.GitHub.Configured() {
		return nil, nil, nil
	}
	key, err := cfg.GitHub.PrivateKeyPEM()
	if err != nil {
		return nil, nil, err
	}
	app, err := github.NewApp(github.AppConfig{ClientID: cfg.GitHub.ClientID, PrivateKeyPEM: key, APIURL: cfg.GitHub.APIURL})
	if err != nil {
		return nil, nil, fmt.Errorf("configure github app: %w", err)
	}
	var oauth *github.OAuth
	if cfg.GitHub.ClientSecret != "" {
		oauth = github.NewOAuth(github.OAuthConfig{
			ClientID: cfg.GitHub.ClientID, ClientSecret: cfg.GitHub.ClientSecret,
			WebURL: cfg.GitHub.WebURL, APIURL: cfg.GitHub.APIURL,
		})
	}

	return app, oauth, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsDevelopment() {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
