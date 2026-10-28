// Package config loads the api's settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is every setting the api reads at startup.
type Config struct {
	// Env names the deployment: development, test or production.
	Env string `env:"APP_ENV" envDefault:"development"`
	// Port is the TCP port the HTTP server listens on.
	Port int `env:"PORT" envDefault:"8080"`
	// DatabaseURL is a Postgres connection string.
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	// CORSOrigins lists the browser origins allowed to call the api.
	CORSOrigins []string `env:"CORS_ORIGINS" envDefault:"http://localhost:3000" envSeparator:","`
	// LogLevel is one of debug, info, warn or error.
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"info"`

	// PublicURL is where browsers and GitHub reach this api.
	PublicURL string `env:"PUBLIC_URL" envDefault:"http://localhost:8080"`
	// DashURL is the dashboard, where sign-in returns to.
	DashURL string `env:"DASH_URL" envDefault:"http://localhost:3000"`

	GitHub GitHub `envPrefix:"GITHUB_"`

	// DevLogin enables a password-less sign-in for local development. It is
	// refused outside APP_ENV=development.
	DevLogin bool `env:"DEV_LOGIN" envDefault:"false"`

	// CredentialsKey is the base64 32-byte key that seals customers' cloud
	// credentials in the database.
	CredentialsKey string `env:"CREDENTIALS_KEY,required,notEmpty"`
	// AWSEndpoint points the AWS provider at an emulator (moto) in tests.
	AWSEndpoint string `env:"AWS_ENDPOINT"`
	// GCPEndpoint points the Compute Engine provider at a stand-in API.
	GCPEndpoint string `env:"GCP_ENDPOINT"`
	// DaytonaEndpoint points the Daytona provider at a stand-in API, in
	// place of the URL each credential carries.
	DaytonaEndpoint string `env:"DAYTONA_ENDPOINT"`
	// AzureEndpoint points the Azure provider at a stand-in Resource
	// Manager API.
	AzureEndpoint string `env:"AZURE_ENDPOINT"`

	// RunnerVersion is the actions/runner release agents install. GitHub
	// stops sending jobs to runners more than 30 days behind a release.
	RunnerVersion string `env:"RUNNER_VERSION" envDefault:"2.338.2"`
	// RunnerMaxLifetime is the hard limit instances enforce on themselves.
	RunnerMaxLifetime time.Duration `env:"RUNNER_MAX_LIFETIME" envDefault:"8h"`
	// RunnerIdleTimeout is how long a launched runner waits for a job.
	RunnerIdleTimeout time.Duration `env:"RUNNER_IDLE_TIMEOUT" envDefault:"10m"`
	// ReconcileInterval is how often the reconciler runs.
	ReconcileInterval time.Duration `env:"RECONCILE_INTERVAL" envDefault:"15s"`
	// LaunchInterval bounds how long a pending runner waits for a launcher
	// pass when no webhook woke it on this replica.
	LaunchInterval time.Duration `env:"LAUNCH_INTERVAL" envDefault:"2s"`
}

// GitHub is the GitHub App the api acts as.
type GitHub struct {
	// AppSlug names the app in its install URL (github.com/apps/<slug>).
	AppSlug string `env:"APP_SLUG"`
	// ClientID identifies the app for JWTs and user sign-in.
	ClientID string `env:"CLIENT_ID"`
	// ClientSecret completes user sign-in.
	ClientSecret string `env:"CLIENT_SECRET"`
	// PrivateKey is the app's PEM private key. PrivateKeyFile is read
	// instead when set.
	PrivateKey     string `env:"PRIVATE_KEY"`
	PrivateKeyFile string `env:"PRIVATE_KEY_FILE"`
	// WebhookSecret verifies webhook signatures.
	WebhookSecret string `env:"WEBHOOK_SECRET"`
	// APIURL and WebURL point at GitHub Enterprise or a fake in tests.
	APIURL string `env:"API_URL" envDefault:"https://api.github.com"`
	WebURL string `env:"WEB_URL" envDefault:"https://github.com"`
}

// Configured reports whether the app's credentials are present.
func (g GitHub) Configured() bool {
	return g.ClientID != "" && (g.PrivateKey != "" || g.PrivateKeyFile != "") && g.WebhookSecret != ""
}

// PrivateKeyPEM returns the app key from the variable or the file.
func (g GitHub) PrivateKeyPEM() ([]byte, error) {
	if g.PrivateKeyFile != "" {
		raw, err := os.ReadFile(g.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read GITHUB_PRIVATE_KEY_FILE: %w", err)
		}

		return raw, nil
	}
	// Single-line env vars often carry the PEM with literal \n.
	return []byte(strings.ReplaceAll(g.PrivateKey, `\n`, "\n")), nil
}

// Load reads the configuration from the environment.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	if c.DevLogin && !c.IsDevelopment() {
		return errors.New("load config: DEV_LOGIN is only allowed with APP_ENV=development")
	}
	if !c.IsDevelopment() && !c.GitHub.Configured() {
		return errors.New("load config: GITHUB_CLIENT_ID, GITHUB_PRIVATE_KEY and GITHUB_WEBHOOK_SECRET are required outside development")
	}
	if !c.IsDevelopment() && !strings.HasPrefix(c.PublicURL, "https://") {
		return errors.New("load config: PUBLIC_URL must be https outside development")
	}
	if !c.IsDevelopment() && strings.Contains(c.CredentialsKey, exampleCredentialsKey) {
		return errors.New("load config: CREDENTIALS_KEY is the key from .env.example; generate one with: openssl rand -base64 32")
	}

	return nil
}

// exampleCredentialsKey is the key .env.example ships for local
// development. It is public, so it must never seal real credentials.
const exampleCredentialsKey = "LwPTIJ8fQ2YUuXCSPYH/h/esBGBJ5udmiu5NaCngjaQ=" //nolint:gosec // the public key from .env.example, listed so it is refused

// IsDevelopment reports whether the api runs on a developer machine.
func (c Config) IsDevelopment() bool {
	return c.Env == "development"
}
