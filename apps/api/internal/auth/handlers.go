package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

const (
	tag         = "Auth"
	stateCookie = "airship_oauth_state"
	stateTTL    = 10 * time.Minute
	// callbackPath is registered as the app's callback URL.
	callbackPath = "/v1/auth/github/callback"
	loginPath    = "/v1/auth/github/login"
	roleAdmin    = "admin"
	roleMember   = "member"
	// devGitHubID marks the development user; real GitHub ids are positive.
	devGitHubID = -1
)

// AccountStore is what sign-in writes.
type AccountStore interface {
	SessionStore
	UpsertUser(ctx context.Context, arg db.UpsertUserParams) (db.User, error)
	UpsertInstallation(ctx context.Context, arg db.UpsertInstallationParams) (db.Installation, error)
	UpsertMembership(ctx context.Context, arg db.UpsertMembershipParams) error
	DeleteOtherMemberships(ctx context.Context, arg db.DeleteOtherMembershipsParams) error
	ListUserInstallations(ctx context.Context, userID uuid.UUID) ([]db.ListUserInstallationsRow, error)
	ListLiveInstallations(ctx context.Context) ([]db.Installation, error)
}

// Config wires the auth handlers.
type Config struct {
	Store    AccountStore
	Sessions *Sessions
	// OAuth and App are nil when no GitHub App is configured; GitHub
	// sign-in is then unavailable.
	OAuth *github.OAuth
	App   *github.App
	// PublicURL is this api's external base URL; WebURL the dashboard's.
	PublicURL string
	WebURL    string
	// AppSlug builds the "install the app" link.
	AppSlug string
	// DevLogin enables POST /v1/auth/dev-login.
	DevLogin       bool
	AllowedOrigins []string
	Logger         *slog.Logger
}

type handlers struct {
	Config
}

// Register adds the auth endpoints. The redirect-based sign-in flow is
// plain HTTP on mux; the JSON endpoints are Huma operations.
func Register(api huma.API, mux *http.ServeMux, cfg Config) {
	h := &handlers{Config: cfg}

	mux.HandleFunc("GET "+loginPath, h.login)
	mux.HandleFunc("GET "+callbackPath, h.callback)

	huma.Register(api, huma.Operation{
		OperationID: "getMe",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "Get the signed-in user",
		Description: "Returns the user and the GitHub organizations they can manage in Airship.",
		Tags:        []string{tag},
		Middlewares: huma.Middlewares{cfg.Sessions.Require(api, cfg.AllowedOrigins)},
	}, h.me)

	huma.Register(api, huma.Operation{
		OperationID: "getAuthConfig",
		Method:      http.MethodGet,
		Path:        "/v1/auth/config",
		Summary:     "Get sign-in options",
		Tags:        []string{tag},
	}, h.config)

	huma.Register(api, huma.Operation{
		OperationID:   "signOut",
		Method:        http.MethodPost,
		Path:          "/v1/auth/sign-out",
		Summary:       "Sign out",
		Tags:          []string{tag},
		Middlewares:   huma.Middlewares{RequireOrigin(api, cfg.AllowedOrigins)},
		DefaultStatus: http.StatusNoContent,
	}, h.signOut)

	// Always registered so the API document does not depend on local
	// config; it answers 404 unless development sign-in is enabled.
	huma.Register(api, huma.Operation{
		OperationID: "devSignIn",
		Method:      http.MethodPost,
		Path:        "/v1/auth/dev-sign-in",
		Summary:     "Sign in without GitHub (development only)",
		Tags:        []string{tag},
		Middlewares: huma.Middlewares{RequireOrigin(api, cfg.AllowedOrigins)},
	}, h.devSignIn)
}

// Organization is a GitHub account with the app installed that the user
// can see.
type Organization struct {
	Login          string `doc:"GitHub account login"                       json:"login"`
	Type           string `doc:"GitHub account type"                        enum:"Organization,User" json:"type"`
	Role           string `doc:"The user's role; only admins change settings" enum:"admin,member" json:"role"`
	InstallationID int64  `doc:"GitHub App installation ID"                 json:"installationId"`
	Suspended      bool   `doc:"Whether the installation is suspended"      json:"suspended"`
}

// Me is the signed-in user.
type Me struct {
	Login         string         `doc:"GitHub login"                   json:"login"`
	Name          *string        `doc:"Display name"                   json:"name"`
	AvatarURL     *string        `doc:"Avatar image URL"               json:"avatarUrl"`
	Organizations []Organization `doc:"Organizations with Airship installed" json:"organizations"`
}

type meOutput struct {
	Body Me
}

func (h *handlers) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	user, _ := UserFrom(ctx)
	rows, err := h.Store.ListUserInstallations(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	orgs := make([]Organization, 0, len(rows))
	for _, row := range rows {
		orgs = append(orgs, Organization{
			Login:          row.Installation.AccountLogin,
			Type:           row.Installation.AccountType,
			Role:           row.Role,
			InstallationID: row.Installation.ID,
			Suspended:      row.Installation.SuspendedAt != nil,
		})
	}

	return &meOutput{Body: Me{Login: user.Login, Name: user.Name, AvatarURL: user.AvatarUrl, Organizations: orgs}}, nil
}

// AuthConfig tells the dashboard how users can sign in.
type AuthConfig struct {
	GitHubSignInURL *string `doc:"Where to send the browser to sign in with GitHub" json:"githubSignInUrl"`
	InstallURL      *string `doc:"Where to install the GitHub App"                 json:"installUrl"`
	DevSignIn       bool    `doc:"Whether development sign-in is enabled"         json:"devSignIn"`
}

type authConfigOutput struct {
	Body AuthConfig
}

func (h *handlers) config(context.Context, *struct{}) (*authConfigOutput, error) {
	out := AuthConfig{DevSignIn: h.DevLogin}
	if h.OAuth != nil {
		signIn := strings.TrimRight(h.PublicURL, "/") + loginPath
		out.GitHubSignInURL = &signIn
	}
	if h.AppSlug != "" {
		install := "https://github.com/apps/" + h.AppSlug + "/installations/new"
		out.InstallURL = &install
	}

	return &authConfigOutput{Body: out}, nil
}

func (h *handlers) redirectURI() string {
	return strings.TrimRight(h.PublicURL, "/") + callbackPath
}

// login starts GitHub sign-in. The state value ties the callback to this
// browser: it is stored in a short-lived cookie and must come back intact.
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	if h.OAuth == nil {
		http.Error(w, "GitHub sign-in is not configured", http.StatusServiceUnavailable)

		return
	}
	state := NewToken("")
	next := SafeNext(r.URL.Query().Get("next"))
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set outside development
		Name:     stateCookie,
		Value:    state + "|" + next,
		Path:     "/v1/auth",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.Sessions.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.OAuth.AuthorizeURL(state, h.redirectURI()), http.StatusFound)
}

func (h *handlers) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cookie, err := r.Cookie(stateCookie)
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set outside development
		Name: stateCookie, Path: "/v1/auth", MaxAge: -1, HttpOnly: true, Secure: h.Sessions.secure, SameSite: http.SameSiteLaxMode,
	})
	if err != nil || h.OAuth == nil {
		h.failSignIn(w, r, "sign-in expired, try again")

		return
	}
	state, next, _ := strings.Cut(cookie.Value, "|")
	got := r.URL.Query().Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(got)) != 1 {
		h.failSignIn(w, r, "sign-in could not be verified, try again")

		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		h.failSignIn(w, r, "GitHub did not approve the sign-in")

		return
	}

	userID, err := h.signIn(ctx, code)
	if err != nil {
		h.Logger.ErrorContext(ctx, "github sign-in", slog.Any("error", err))
		h.failSignIn(w, r, "GitHub sign-in failed")

		return
	}
	session, err := h.Sessions.Create(ctx, userID)
	if err != nil {
		h.Logger.ErrorContext(ctx, "create session", slog.Any("error", err))
		h.failSignIn(w, r, "could not start a session")

		return
	}
	http.SetCookie(w, session)
	// SafeNext only allows local paths, appended to our own dashboard URL.
	http.Redirect(w, r, strings.TrimRight(h.WebURL, "/")+SafeNext(next), http.StatusFound) //nolint:gosec // validated above
}

func (h *handlers) failSignIn(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, strings.TrimRight(h.WebURL, "/")+"/sign-in?error="+urlQueryEscape(message), http.StatusFound)
}

// signIn exchanges the code, records the user, and refreshes which
// installations they can see and in which role.
func (h *handlers) signIn(ctx context.Context, code string) (uuid.UUID, error) {
	token, err := h.OAuth.Exchange(ctx, code, h.redirectURI())
	if err != nil {
		return uuid.Nil, err
	}
	client := h.OAuth.UserClient(token)
	ghUser, err := client.GetUser(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("get user: %w", err)
	}
	installations, err := client.ListUserInstallations(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("list user installations: %w", err)
	}

	user, err := h.Store.UpsertUser(ctx, db.UpsertUserParams{GithubID: ghUser.ID, Login: ghUser.Login, Name: ghUser.Name, AvatarUrl: ghUser.AvatarURL})
	if err != nil {
		return uuid.Nil, fmt.Errorf("save user: %w", err)
	}

	keep := make([]int64, 0, len(installations))
	for _, inst := range installations {
		accountType := "Organization"
		if inst.Account.Type == "User" {
			accountType = "User"
		}
		if _, err := h.Store.UpsertInstallation(ctx, db.UpsertInstallationParams{
			ID: inst.ID, AccountID: inst.Account.ID, AccountLogin: inst.Account.Login,
			AccountType: accountType, SuspendedAt: inst.SuspendedAt,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("save installation: %w", err)
		}
		role, err := resolveRole(ctx, h.App, account{InstallationID: inst.ID, ID: inst.Account.ID, Login: inst.Account.Login, Type: accountType}, ghUser.ID, ghUser.Login)
		if err != nil {
			// Leave whatever access the user had; the refresher retries.
			h.Logger.WarnContext(ctx, "could not read organization role", slog.String("org", inst.Account.Login), slog.Any("error", err))
			keep = append(keep, inst.ID)

			continue
		}
		if role == "" {
			continue // not a member: pruned below
		}
		if err := h.Store.UpsertMembership(ctx, db.UpsertMembershipParams{UserID: user.ID, InstallationID: inst.ID, Role: role}); err != nil {
			return uuid.Nil, fmt.Errorf("save membership: %w", err)
		}
		keep = append(keep, inst.ID)
	}
	if err := h.Store.DeleteOtherMemberships(ctx, db.DeleteOtherMembershipsParams{UserID: user.ID, KeepIds: keep}); err != nil {
		return uuid.Nil, fmt.Errorf("prune memberships: %w", err)
	}

	return user.ID, nil
}

type signOutInput struct {
	Session string `cookie:"airship_session"`
}

type cookieOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

func (h *handlers) signOut(ctx context.Context, in *signOutInput) (*cookieOutput, error) {
	cookie, err := h.Sessions.Clear(ctx, in.Session)
	if err != nil {
		return nil, err
	}

	return &cookieOutput{SetCookie: cookie}, nil
}

// devSignIn signs in a local user who administers every installation, and
// creates a demo installation when there is none. It only works when
// DevLogin is set, which config refuses outside development.
func (h *handlers) devSignIn(ctx context.Context, _ *struct{}) (*cookieOutput, error) {
	if !h.DevLogin {
		return nil, huma.Error404NotFound("development sign-in is disabled")
	}
	name := "Local Developer"
	user, err := h.Store.UpsertUser(ctx, db.UpsertUserParams{GithubID: devGitHubID, Login: "dev", Name: &name})
	if err != nil {
		return nil, fmt.Errorf("save dev user: %w", err)
	}
	installations, err := h.Store.ListLiveInstallations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	if len(installations) == 0 {
		demo, err := h.Store.UpsertInstallation(ctx, db.UpsertInstallationParams{ID: 1, AccountID: 1, AccountLogin: "acme", AccountType: "Organization"})
		if err != nil {
			return nil, fmt.Errorf("create demo installation: %w", err)
		}
		installations = append(installations, demo)
	}
	for _, inst := range installations {
		if err := h.Store.UpsertMembership(ctx, db.UpsertMembershipParams{UserID: user.ID, InstallationID: inst.ID, Role: roleAdmin}); err != nil {
			return nil, fmt.Errorf("save membership: %w", err)
		}
	}
	cookie, err := h.Sessions.Create(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return &cookieOutput{SetCookie: cookie}, nil
}
