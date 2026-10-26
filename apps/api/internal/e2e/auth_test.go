package e2e_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// signIn runs the browser side of GitHub sign-in and returns the final
// redirect location.
func (e *env) signIn(code, next string, tamper bool) string {
	e.t.Helper()
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.api.URL+"/v1/auth/github/login?next="+url.QueryEscape(next), nil)
	resp, err := e.browser.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	authorize, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound || !strings.HasPrefix(authorize.String(), e.gh.URL+"/login/oauth/authorize") {
		e.t.Fatalf("login redirect = %d %s", resp.StatusCode, authorize)
	}
	state := authorize.Query().Get("state")
	if tamper {
		state = "attacker-state"
	}
	// GitHub sends the browser back with a code and the same state.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, e.api.URL+"/v1/auth/github/callback?code="+code+"&state="+state, nil)
	resp, err = e.browser.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()

	return resp.Header.Get("Location")
}

func orgRoles(me map[string]any) map[string]string {
	out := map[string]string{}
	for _, org := range me["organizations"].([]any) {
		o := org.(map[string]any)
		out[o["login"].(string)] = o["role"].(string)
	}

	return out
}

func TestGitHubSignInAndRoles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddInstallation(github.Installation{ID: 2, Account: github.Account{ID: 2, Login: "globex", Type: "Organization"}})
	e.gh.AddInstallation(github.Installation{ID: 3, Account: github.Account{ID: 50, Login: "octo", Type: "User"}})
	e.gh.SetOrgRole("acme", "octo", "admin")
	e.gh.SetOrgRole("globex", "octo", "member")
	e.gh.AddUser("code-1", github.User{ID: 50, Login: "octo"}, 1, 2, 3)

	if got := e.signIn("code-1", "/acme/jobs", false); got != origin+"/acme/jobs" {
		t.Fatalf("after sign-in redirected to %q", got)
	}
	roles := orgRoles(e.call(http.MethodGet, "/v1/me", nil, http.StatusOK))
	if roles["acme"] != "admin" || roles["globex"] != "member" || roles["octo"] != "admin" {
		t.Fatalf("roles = %v", roles)
	}

	// Members can look but not change anything.
	e.call(http.MethodGet, "/v1/orgs/globex/data-planes", nil, http.StatusOK)
	e.call(http.MethodPost, "/v1/orgs/globex/data-planes", map[string]any{"name": "x"}, http.StatusForbidden)

	// A data plane from one organization cannot back another's profile.
	globexPlane := e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "acme-plane"}, http.StatusCreated)
	e.gh.SetOrgRole("globex", "octo", "admin")
	e.gh.AddUser("code-2", github.User{ID: 50, Login: "octo"}, 1, 2)
	e.signIn("code-2", "/", false)
	e.call(http.MethodPost, "/v1/orgs/globex/profiles", map[string]any{
		"label": "airship-x", "dataPlaneId": globexPlane["dataPlane"].(map[string]any)["id"], "arch": "x64", "instanceType": "m7i.large",
	}, http.StatusUnprocessableEntity)

	// Signing in again drops installations GitHub no longer reports.
	roles = orgRoles(e.call(http.MethodGet, "/v1/me", nil, http.StatusOK))
	if _, ok := roles["octo"]; ok || roles["globex"] != "admin" {
		t.Fatalf("memberships not refreshed: %v", roles)
	}

	// Signing out ends the session.
	e.call(http.MethodPost, "/v1/auth/sign-out", nil, http.StatusNoContent)
	e.call(http.MethodGet, "/v1/me", nil, http.StatusUnauthorized)
}

func TestSignInRejectsForgedStateAndOpenRedirects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.AddUser("code-1", github.User{ID: 50, Login: "octo"}, 1)

	if got := e.signIn("code-1", "/", true); !strings.HasPrefix(got, origin+"/sign-in?error=") {
		t.Fatalf("forged state redirected to %q", got)
	}
	e.call(http.MethodGet, "/v1/me", nil, http.StatusUnauthorized)

	for _, next := range []string{"//evil.example/x", "https://evil.example", "/\\evil.example"} {
		if got := e.signIn("code-1", next, false); got != origin+"/" {
			t.Fatalf("next=%q redirected to %q", next, got)
		}
	}
}

func TestProfileValidation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.call(http.MethodPost, "/v1/auth/dev-sign-in", nil, http.StatusNoContent)
	plane := e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "main"}, http.StatusCreated)
	planeID := plane["dataPlane"].(map[string]any)["id"]

	for _, label := range []string{"self-hosted", "ubuntu-latest", "linux"} {
		e.call(http.MethodPost, "/v1/orgs/acme/profiles", map[string]any{
			"label": label, "dataPlaneId": planeID, "arch": "x64", "instanceType": "m7i.large",
		}, http.StatusUnprocessableEntity)
	}
	profile := e.call(http.MethodPost, "/v1/orgs/acme/profiles", map[string]any{
		"label": "airship-8cpu", "dataPlaneId": planeID, "arch": "arm64", "instanceType": "c8g.2xlarge",
	}, http.StatusCreated)
	if profile["diskGb"].(float64) != 75 || profile["maxRunners"].(float64) != 20 || profile["allowPublicRepos"] != false {
		t.Fatalf("defaults not applied: %v", profile)
	}
	e.call(http.MethodPost, "/v1/orgs/acme/profiles", map[string]any{
		"label": "airship-8cpu", "dataPlaneId": planeID, "arch": "x64", "instanceType": "m7i.large",
	}, http.StatusConflict)
	// A data plane with profiles cannot be deleted out from under them.
	e.call(http.MethodDelete, "/v1/orgs/acme/data-planes/"+planeID.(string), nil, http.StatusConflict)
	e.call(http.MethodDelete, "/v1/orgs/acme/profiles/"+profile["id"].(string), nil, http.StatusNoContent)
	e.call(http.MethodDelete, "/v1/orgs/acme/data-planes/"+planeID.(string), nil, http.StatusNoContent)
}

func TestOnlyOrganizationMembersSeeAnOrganization(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// carol is a member of acme, an outside collaborator on one initech
	// repository, and a collaborator on dave's personal repositories.
	// GitHub lists all three installations to her.
	e.gh.AddInstallation(github.Installation{ID: 4, Account: github.Account{ID: 4, Login: "initech", Type: "Organization"}})
	e.gh.AddInstallation(github.Installation{ID: 5, Account: github.Account{ID: 70, Login: "dave", Type: "User"}})
	e.gh.SetOrgRole("acme", "carol", "member")
	e.gh.AddUser("code-carol", github.User{ID: 60, Login: "carol"}, 1, 4, 5)
	e.signIn("code-carol", "/", false)

	roles := orgRoles(e.call(http.MethodGet, "/v1/me", nil, http.StatusOK))
	if len(roles) != 1 || roles["acme"] != "member" {
		t.Fatalf("roles = %v, want only acme as member", roles)
	}
	e.call(http.MethodGet, "/v1/orgs/initech/jobs", nil, http.StatusNotFound)
	e.call(http.MethodGet, "/v1/orgs/dave/jobs", nil, http.StatusNotFound)
}

func TestRoleChangesReachExistingSessions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gh.SetOrgRole("acme", "carol", "member")
	e.gh.AddUser("code-carol", github.User{ID: 60, Login: "carol"}, 1)
	e.signIn("code-carol", "/", false)
	e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "x"}, http.StatusForbidden)

	refresher := &auth.RoleRefresher{Store: db.New(e.pool), App: e.app, Logger: slog.New(slog.DiscardHandler), RefreshAfter: time.Hour, Batch: 10}
	refresh := func() {
		t.Helper()
		if _, err := e.pool.Exec(context.Background(), "update memberships set refreshed_at = now() - interval '2 hours'"); err != nil {
			t.Fatal(err)
		}
		if err := refresher.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	// Promoted on GitHub: admin without signing in again.
	e.gh.SetOrgRole("acme", "carol", "admin")
	refresh()
	e.call(http.MethodPost, "/v1/orgs/acme/data-planes", map[string]any{"name": "x"}, http.StatusCreated)

	// Removed from the organization: access ends.
	e.gh.SetOrgRole("acme", "carol", "")
	refresh()
	e.call(http.MethodGet, "/v1/orgs/acme/jobs", nil, http.StatusNotFound)

	// A GitHub outage changes nothing.
	e.gh.SetOrgRole("acme", "carol", "member")
	e.signIn("code-carol", "/", false)
	e.gh.FailNext("GET membership", http.StatusBadGateway)
	refresh()
	e.call(http.MethodGet, "/v1/orgs/acme/jobs", nil, http.StatusOK)
}
