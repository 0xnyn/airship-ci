package github_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
)

func TestSignature(t *testing.T) {
	t.Parallel()
	secret, body := []byte("s3cret"), []byte(`{"action":"queued"}`)
	if !github.VerifySignature(secret, body, github.Sign(secret, body)) {
		t.Fatal("valid signature rejected")
	}
	for _, header := range []string{"", "sha1=abc", "sha256=zz", github.Sign([]byte("other"), body), github.Sign(secret, []byte("{}"))} {
		if github.VerifySignature(secret, body, header) {
			t.Fatalf("bad signature %q accepted", header)
		}
	}
	if github.VerifySignature(nil, body, github.Sign(nil, body)) {
		t.Fatal("an empty secret must never verify")
	}
}

func TestInstallationClientAgainstFake(t *testing.T) {
	t.Parallel()
	// The fake verifies the app JWT's RS256 signature, issuer and lifetime.
	gh := fakegithub.New(t)
	app := gh.App(t)
	ctx := context.Background()
	repo, err := github.ParseRepo("acme/api")
	if err != nil {
		t.Fatal(err)
	}
	client := app.Installation(9)
	jit, err := client.GenerateJITConfig(ctx, repo, "airship-1", []string{"self-hosted", "airship-x"})
	if err != nil || jit.EncodedConfig == "" {
		t.Fatalf("jit = %+v, %v", jit, err)
	}
	if _, err := client.GenerateJITConfig(ctx, repo, "airship-1", []string{"x"}); github.StatusOf(err) != http.StatusConflict {
		t.Fatalf("duplicate name: %v", err)
	}
	found, ok, err := client.FindRunnerByName(ctx, repo, "airship-1")
	if err != nil || !ok || found.ID != jit.Runner.ID {
		t.Fatalf("find by name = %+v %v %v", found, ok, err)
	}
	if err := client.DeleteRunner(ctx, repo, jit.Runner.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteRunner(ctx, repo, jit.Runner.ID); err != nil {
		t.Fatalf("deleting a deleted runner should succeed: %v", err)
	}
}

func TestParseRepo(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "acme", "/api", "acme/", "acme/api/x"} {
		if _, err := github.ParseRepo(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
