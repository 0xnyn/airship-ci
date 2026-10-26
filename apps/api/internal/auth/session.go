// Package auth signs dashboard users in with GitHub and resolves their
// session on every request.
//
// Sessions are opaque random tokens in an HttpOnly cookie; the database
// keeps only their SHA-256, so a database leak does not leak sessions.
// Which organizations a user may see, and whether they administer them, is
// what GitHub reported at sign-in.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// SessionCookie holds the session token.
	SessionCookie  = "airship_session"
	sessionTTL     = 14 * 24 * time.Hour
	tokenBytes     = 32
	contextUserKey = contextKey("user")
)

type contextKey string

// HashToken returns the SHA-256 of a bearer secret, the only form secrets
// are stored in.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))

	return sum[:]
}

// NewToken returns a random URL-safe secret with a readable prefix.
func NewToken(prefix string) string {
	var b [tokenBytes]byte
	_, _ = rand.Read(b[:])

	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// Sessions creates and resolves sessions.
type Sessions struct {
	store  SessionStore
	secure bool
}

// SessionStore is the subset of db.Querier sessions need.
type SessionStore interface {
	CreateSession(ctx context.Context, arg db.CreateSessionParams) error
	GetSessionUser(ctx context.Context, tokenHash []byte) (db.User, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// NewSessions returns a Sessions. secure marks cookies Secure (HTTPS only).
func NewSessions(store SessionStore, secure bool) *Sessions {
	return &Sessions{store: store, secure: secure}
}

// Create starts a session for user and returns its cookie.
func (s *Sessions) Create(ctx context.Context, userID uuid.UUID) (*http.Cookie, error) {
	token := NewToken("")
	expires := time.Now().Add(sessionTTL)
	if err := s.store.CreateSession(ctx, db.CreateSessionParams{TokenHash: HashToken(token), UserID: userID, ExpiresAt: expires}); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return s.cookie(token, expires), nil
}

// Clear ends the session named by token, if any, and returns a cookie that
// removes it from the browser.
func (s *Sessions) Clear(ctx context.Context, token string) (*http.Cookie, error) {
	if token != "" {
		if err := s.store.DeleteSession(ctx, HashToken(token)); err != nil {
			return nil, fmt.Errorf("delete session: %w", err)
		}
	}
	cookie := s.cookie("", time.Unix(0, 0)) //nolint:gosec // see cookie
	cookie.MaxAge = -1

	return cookie, nil
}

func (s *Sessions) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is set outside development
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		// Lax keeps the cookie off cross-site POSTs (CSRF) while still sending
		// it on top-level navigations back from GitHub.
		SameSite: http.SameSiteLaxMode,
	}
}

// Resolve returns the user a session token belongs to.
func (s *Sessions) Resolve(ctx context.Context, token string) (db.User, bool, error) {
	if token == "" {
		return db.User{}, false, nil
	}
	user, err := s.store.GetSessionUser(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, false, nil
	}
	if err != nil {
		return db.User{}, false, fmt.Errorf("resolve session: %w", err)
	}

	return user, true, nil
}

// Require is Huma middleware that rejects requests without a valid session
// and puts the user in the request context.
//
// For methods that change state it also checks the Origin header. Lax
// cookies already stop cross-site form posts; this closes the remaining
// gap of sibling subdomains, which count as same-site.
func (s *Sessions) Require(api huma.API, allowedOrigins []string) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !sameOrigin(ctx, allowedOrigins) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "cross-origin request refused")

			return
		}
		token := ""
		if cookie, err := huma.ReadCookie(ctx, SessionCookie); err == nil {
			token = cookie.Value
		}
		user, ok, err := s.Resolve(ctx.Context(), token)
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "could not check the session")

			return
		}
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "sign in required")

			return
		}
		next(huma.WithValue(ctx, contextUserKey, user))
	}
}

// UserFrom returns the signed-in user set by Require.
func UserFrom(ctx context.Context) (db.User, bool) {
	user, ok := ctx.Value(contextUserKey).(db.User)

	return user, ok
}

// SafeNext returns next if it is a local path, so sign-in cannot be used
// as an open redirect, and "/" otherwise.
func SafeNext(next string) string {
	parsed, err := url.Parse(next)
	if err != nil || next == "" || next[0] != '/' || (len(next) > 1 && (next[1] == '/' || next[1] == '\\')) ||
		parsed.Scheme != "" || parsed.Host != "" {
		return "/"
	}

	return next
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}

// RequireOrigin is Huma middleware that refuses state-changing requests
// from origins other than the dashboard's, for endpoints that do not need a
// session (signing out).
func RequireOrigin(api huma.API, allowedOrigins []string) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !sameOrigin(ctx, allowedOrigins) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "cross-origin request refused")

			return
		}
		next(ctx)
	}
}

// sameOrigin reports whether a request may change state: reads always may;
// writes may unless the browser says they come from another origin.
func sameOrigin(ctx huma.Context, allowedOrigins []string) bool {
	if ctx.Method() == http.MethodGet || ctx.Method() == http.MethodHead {
		return true
	}
	origin := ctx.Header("Origin")

	return origin == "" || slices.Contains(allowedOrigins, origin)
}
