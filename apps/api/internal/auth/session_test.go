package auth_test

import (
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
)

func TestSafeNext(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/acme/jobs":             "/acme/jobs",
		"/":                      "/",
		"":                       "/",
		"//evil.example":         "/",
		"/\\evil.example":        "/",
		"https://evil.example/x": "/",
		"javascript:alert(1)":    "/",
		"acme":                   "/",
		"/acme?tab=runners#top":  "/acme?tab=runners#top",
	} {
		if got := auth.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokensAreRandomAndHashed(t *testing.T) {
	t.Parallel()
	a, b := auth.NewToken("asd_"), auth.NewToken("asd_")
	if a == b || len(a) < 40 {
		t.Fatalf("weak tokens: %q %q", a, b)
	}
	if string(auth.HashToken(a)) == a || len(auth.HashToken(a)) != 32 {
		t.Fatal("tokens must be stored as SHA-256")
	}
}
