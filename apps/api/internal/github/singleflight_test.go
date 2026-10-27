package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// Many goroutines asking for one installation's token at once cost one
// POST, not one each.
func TestInstallationTokenRefreshIsSingleFlight(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
			posts.Add(1)
			time.Sleep(50 * time.Millisecond) // let the others pile up
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"ghs_x","expires_at":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"}`))
		case r.URL.Path == "/repos/acme/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"full_name":"acme/api","private":true,"default_branch":"main"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	app, err := github.NewApp(github.AppConfig{
		ClientID:      "Iv1.test",
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		APIURL:        server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := app.Installation(7).GetRepository(context.Background(), github.Repo{Owner: "acme", Name: "api"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := posts.Load(); n != 1 {
		t.Fatalf("access token requested %d times, want 1", n)
	}
}
