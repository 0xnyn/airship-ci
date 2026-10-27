package config_test

import (
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/config"
)

func TestProductionGuards(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("CREDENTIALS_KEY", "c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0MTI=")
	t.Setenv("APP_ENV", "production")
	t.Setenv("DEV_LOGIN", "true")
	if _, err := config.Load(); err == nil {
		t.Fatal("DEV_LOGIN accepted in production")
	}
	t.Setenv("DEV_LOGIN", "false")
	if _, err := config.Load(); err == nil {
		t.Fatal("production started without a GitHub App")
	}
	t.Setenv("GITHUB_CLIENT_ID", "Iv1.x")
	t.Setenv("GITHUB_PRIVATE_KEY", "pem")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "s")
	t.Setenv("PUBLIC_URL", "http://api.example.com")
	t.Setenv("CREDENTIALS_KEY", "LwPTIJ8fQ2YUuXCSPYH/h/esBGBJ5udmiu5NaCngjaQ=")
	t.Setenv("PUBLIC_URL", "https://api.example.com")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), ".env.example") {
		t.Fatalf("the example CREDENTIALS_KEY was accepted in production: %v", err)
	}
	t.Setenv("CREDENTIALS_KEY", "c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0MTI=")
	t.Setenv("PUBLIC_URL", "http://api.example.com")
	if _, err := config.Load(); err == nil {
		t.Fatal("plain-http PUBLIC_URL accepted in production")
	}
	t.Setenv("PUBLIC_URL", "https://api.example.com")
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
}
