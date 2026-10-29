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

// scriptText calls the developer API and returns the body as text.
func (e *env) scriptText(token, path string, want int) string {
	e.t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, e.api.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("GET %s = %d, want %d: %s", path, resp.StatusCode, want, raw)
	}

	return string(raw)
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)

	return raw
}

// The /api/v1 developer API: these are the requests an automation client
// sends, and they must keep working unchanged.
func TestCompatibleDeveloperAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddRepository(1, github.Repository{ID: 5, FullName: "acme/api", Private: true, DefaultBranch: "main"})
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	token := e.call(http.MethodPost, "/v1/orgs/acme/api-tokens", map[string]any{"name": "release-script"}, http.StatusCreated)["token"].(string)

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

	// Connecting AWS: an access key and a secret key.
	if items := e.script(token, http.MethodGet, "/api/v1/cloud-connect", nil, http.StatusOK)["items"].([]any); len(items) != 0 {
		t.Fatalf("clouds before connecting = %v", items)
	}
	connected := e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{
		"cloud": "aws", "credentials": map[string]string{"access_key": "AKIAIOSFODNN7EXAMPLE", "secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
	}, http.StatusOK)
	if connected["cloud"] != "aws" || connected["defaultRegion"] != "eu-west-1" || connected["identity"] != "AKIAIOSFODNN7EXAMPLE" {
		t.Fatalf("cloud-connect = %v", connected)
	}
	e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "gcp", "credentials": map[string]string{}}, http.StatusUnprocessableEntity)
	// Connecting GCP: the key file's fields, plus a region.
	gcp := e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "gcp", "credentials": map[string]string{
		"type": "service_account", "project_id": "acme-ci-123456", "private_key_id": "k1", "private_key": "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n",
		"client_email": "airship@acme-ci-123456.iam.gserviceaccount.com", "client_id": "1", "auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token", "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/airship", "universe_domain": "googleapis.com", "region": "europe-west4-b",
	}}, http.StatusOK)
	if gcp["cloud"] != "gcp" || gcp["defaultRegion"] != "europe-west4-b" || gcp["identity"] != "airship@acme-ci-123456.iam.gserviceaccount.com" {
		t.Fatalf("gcp cloud-connect = %v", gcp)
	}
	// Daytona is Airship's own: api_key, and optionally api_url and region.
	e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "daytona", "credentials": map[string]string{"region": "us"}}, http.StatusUnprocessableEntity)
	e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "daytona", "credentials": map[string]string{"api_key": "dtn_0123456789abcdef0123456789abcdef", "region": "Not A Region"}}, http.StatusUnprocessableEntity)
	daytona := e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "daytona", "credentials": map[string]string{"api_key": "dtn_0123456789abcdef0123456789abcdef"}}, http.StatusOK)
	if daytona["cloud"] != "daytona" || daytona["defaultRegion"] != "us" || daytona["identity"] != "dtn***def" {
		t.Fatalf("daytona cloud-connect = %v", daytona)
	}
	// Connecting Azure: the subscription, tenant, client
	// ID and secret.
	e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "azure", "credentials": map[string]string{"subscription_id": "x"}}, http.StatusUnprocessableEntity)
	azure := e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "azure", "credentials": map[string]string{
		"subscription_id": "0F0E0D0C-0B0A-4908-8706-050403020100", "tenant_id": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		"client_id": "12345678-9abc-4def-8123-456789abcdef", "client_secret": "Q~8f3a9c2e7b1d4f6a8c0e2b4d6f8a1c3e5b7d9f1a", "region": "westeurope",
	}}, http.StatusOK)
	if azure["cloud"] != "azure" || azure["defaultRegion"] != "westeurope" || azure["identity"] != "12345678-9abc-4def-8123-456789abcdef" || azure["accountId"] != "123456789012" {
		t.Fatalf("azure cloud-connect = %v", azure)
	}
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/azure", nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/daytona", nil, http.StatusNoContent)
	e.script(token, http.MethodDelete, "/v1/orgs/acme/clouds/gcp", nil, http.StatusUnauthorized) // tokens cannot reach the dashboard API
	e.call(http.MethodDelete, "/v1/orgs/acme/clouds/gcp", nil, http.StatusNoContent)
	e.script(token, http.MethodPost, "/api/v1/cloud-connect", map[string]any{"cloud": "aws", "credentials": map[string]string{"access_key": "x"}}, http.StatusUnprocessableEntity)
	if items := e.script(token, http.MethodGet, "/api/v1/cloud-connect", nil, http.StatusOK)["items"].([]any); len(items) != 1 {
		t.Fatalf("clouds after connecting = %v", items)
	}
}

// The access-control route edits access.yaml through a
// pull request against the organization's .airship repository.
func TestAccessControlAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddRepository(1, github.Repository{ID: 7, FullName: "acme/.airship", Private: true, DefaultBranch: "main"})
	e.gh.SetFile("acme/.airship", "main", "airship.global.yaml", []byte("runners:\n  - {name: gpu, cloud: aws, instance_type: g5.xlarge, labels: [airship-gpu]}\n"))
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	token := e.call(http.MethodPost, "/v1/orgs/acme/api-tokens", map[string]any{"name": "release-script"}, http.StatusCreated)["token"].(string)

	got := e.script(token, http.MethodGet, "/api/v1/access-control", nil, http.StatusOK)
	if got["found"] != false {
		t.Fatalf("access control before any file = %v", got)
	}

	granted := e.script(token, http.MethodPut, "/api/v1/access-control", map[string]any{
		"org": "acme",
		"repository_resource_access": []map[string]any{{
			"repository": "acme/api", "resources": []string{"gpu"}, "action": "add",
			"teams": []string{"ml"}, "users": []string{"alice"}, "policy_args": map[string]any{"pull_request": true},
		}},
	}, http.StatusOK)
	url, _ := granted["pull_request_url"].(string)
	if !strings.HasPrefix(url, "https://github.com/acme/.airship/pull/") {
		t.Fatalf("grant = %v", granted)
	}
	pulls := e.gh.PullRequests()
	if len(pulls) != 1 || pulls[0].Base != "main" || !strings.HasPrefix(pulls[0].Head, "airship/access-control-") {
		t.Fatalf("pull requests = %+v", pulls)
	}
	content, ok := e.gh.FileAt("acme/.airship", pulls[0].Head, "access.yaml")
	if !ok || !strings.Contains(string(content), "repo: api") || !strings.Contains(string(content), "- ml") || !strings.Contains(string(content), "pull_request: true") || !strings.Contains(string(content), "resource: gpu") {
		t.Fatalf("proposed access.yaml:\n%s", content)
	}
	// The default branch is untouched until the pull request is merged.
	if _, exists := e.gh.FileAt("acme/.airship", "main", "access.yaml"); exists {
		t.Fatal("the default branch was written directly")
	}

	// Merged: a revoke starts from the merged file.
	e.gh.SetFile("acme/.airship", "main", "access.yaml", content)
	revoked := e.script(token, http.MethodPut, "/api/v1/access-control", map[string]any{
		"repository_resource_access": []map[string]any{{"repository": "api", "resources": []string{"gpu"}, "action": "remove"}},
	}, http.StatusOK)
	doc := revoked["access_yml"].(map[string]any)
	if rules, _ := doc["access_control"].([]any); len(rules) != 0 {
		t.Fatalf("after revoke: %v", doc)
	}
	e.script(token, http.MethodPut, "/api/v1/access-control", map[string]any{"org": "globex", "repository_resource_access": []map[string]any{{"repository": "api", "resources": []string{"gpu"}}}}, http.StatusForbidden)
	e.script(token, http.MethodPut, "/api/v1/access-control", map[string]any{"repository_resource_access": []map[string]any{{"repository": "api", "resources": []string{"gpu"}, "users_from_json": "https://x"}}}, http.StatusUnprocessableEntity)
}
