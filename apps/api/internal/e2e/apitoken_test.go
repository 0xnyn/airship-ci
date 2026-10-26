package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// script calls the developer API with a bearer token and no cookies, as a
// CI job or a shell script would.
func (e *env) script(token, method, path string, body any, want int) map[string]any {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, e.api.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)

	return out
}

func TestDeveloperAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddRepository(1, github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"})

	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	created := e.call(http.MethodPost, "/v1/orgs/acme/api-tokens", map[string]any{"name": "ci"}, http.StatusCreated)
	token := created["token"].(string)
	if !strings.HasPrefix(token, "asa_") {
		t.Fatalf("token = %q", token)
	}
	listed := e.call(http.MethodGet, "/v1/orgs/acme/api-tokens", nil, http.StatusOK)["items"].([]any)
	if len(listed) != 1 || strings.Contains(string(mustJSON(listed)), token) {
		t.Fatalf("tokens = %v, want one, without its secret", listed)
	}

	// No token, a wrong token and the session cookie alone are all refused.
	e.script("", http.MethodGet, "/v1/api/repositories", nil, http.StatusUnauthorized)
	e.script("asa_wrong", http.MethodGet, "/v1/api/repositories", nil, http.StatusUnauthorized)
	e.call(http.MethodGet, "/v1/api/repositories", nil, http.StatusUnauthorized)

	repos := e.script(token, http.MethodGet, "/v1/api/repositories", nil, http.StatusOK)["items"].([]any)
	if len(repos) != 1 || repos[0].(map[string]any)["activated"] != false {
		t.Fatalf("repositories = %v", repos)
	}
	e.script(token, http.MethodPut, "/v1/api/repositories/missing/activation", map[string]any{}, http.StatusNotFound)
	got := e.script(token, http.MethodPut, "/v1/api/repositories/API/activation", map[string]any{}, http.StatusOK)
	if got["activated"] != true || got["fullName"] != "acme/api" {
		t.Fatalf("activate = %v", got)
	}
	dashboard := e.call(http.MethodGet, "/v1/orgs/acme/repositories", nil, http.StatusOK)["items"].([]any)
	if dashboard[0].(map[string]any)["activated"] != true {
		t.Fatalf("dashboard does not show the activation: %v", dashboard)
	}
	e.script(token, http.MethodDelete, "/v1/api/repositories/api/activation", nil, http.StatusNoContent)
	e.script(token, http.MethodDelete, "/v1/api/repositories/api/activation", nil, http.StatusNotFound)

	// A revoked token stops working at once.
	id := created["apiToken"].(map[string]any)["id"].(string)
	e.call(http.MethodDelete, "/v1/orgs/acme/api-tokens/"+id, nil, http.StatusNoContent)
	e.script(token, http.MethodGet, "/v1/api/repositories", nil, http.StatusUnauthorized)
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)

	return raw
}

// Cirun's own client (cirun-py) sends these requests; pointing its
// CIRUN_API_ENDPOINT at Airship must work unchanged.
func TestCirunCompatibleAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddRepository(1, github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"})
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	token := e.call(http.MethodPost, "/v1/orgs/acme/api-tokens", map[string]any{"name": "cirun-py"}, http.StatusCreated)["token"].(string)

	if items := e.script(token, http.MethodGet, "/api/v1/repo", nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("list = %v", items)
	}
	got := e.script(token, http.MethodPost, "/api/v1/repo", map[string]any{"repository": "acme/api", "active": true}, http.StatusOK)
	if got["activated"] != true {
		t.Fatalf("add = %v", got)
	}
	// Removing is idempotent and reports the repository as it now is.
	for range 2 {
		got = e.script(token, http.MethodPost, "/api/v1/repo", map[string]any{"repository": "acme/api", "active": false}, http.StatusOK)
		if got["activated"] != false || got["accessible"] != true || got["id"] != float64(5) {
			t.Fatalf("remove = %v", got)
		}
	}
	e.script(token, http.MethodPost, "/api/v1/repo", map[string]any{"repository": "other/api", "active": true}, http.StatusNotFound)
	e.script(token, http.MethodPost, "/api/v1/repo", map[string]any{"repository": "acme/missing", "active": false}, http.StatusNotFound)
	e.script("", http.MethodGet, "/api/v1/repo", nil, http.StatusUnauthorized)
}
