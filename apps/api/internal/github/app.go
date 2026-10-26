// Package github is a small client for the parts of the GitHub API Airship
// uses: GitHub App authentication, just-in-time runner registration, job and
// runner lookups, webhook delivery replay and the user sign-in flow.
//
// It is hand-written rather than generated so every call, timeout and error
// path is visible, and so tests can point it at a fake server.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAPIURL is GitHub.com's REST API.
	DefaultAPIURL = "https://api.github.com"
	// DefaultWebURL is GitHub.com, where users authorize the app.
	DefaultWebURL = "https://github.com"

	apiVersion     = "2022-11-28"
	requestTimeout = 15 * time.Second
	// GitHub rejects app JWTs that expire more than 10 minutes out and
	// recommends backdating iat by 60 seconds for clock drift.
	jwtLifetime = 9 * time.Minute
	jwtBackdate = 60 * time.Second
	// Installation tokens live for an hour; refresh well before that.
	tokenRefreshMargin = 5 * time.Minute
	maxResponseBytes   = 8 << 20
)

// AppConfig identifies a GitHub App.
type AppConfig struct {
	// ClientID is the app's client ID, used as the JWT issuer.
	ClientID string
	// PrivateKeyPEM is the app's RSA private key in PEM form.
	PrivateKeyPEM []byte
	// APIURL defaults to DefaultAPIURL.
	APIURL string
	// HTTPClient defaults to a client with a 15 second timeout.
	HTTPClient *http.Client
	// Now defaults to time.Now; tests override it.
	Now func() time.Time
}

// App authenticates as a GitHub App and hands out installation clients.
type App struct {
	clientID string
	key      *rsa.PrivateKey
	api      string
	http     *http.Client
	now      func() time.Time

	mu     sync.Mutex
	tokens map[int64]installationToken
}

type installationToken struct {
	token     string
	expiresAt time.Time
}

// NewApp parses the app's private key and returns an App.
func NewApp(cfg AppConfig) (*App, error) {
	if cfg.ClientID == "" {
		return nil, errors.New("github app: client id is required")
	}
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	app := &App{
		clientID: cfg.ClientID,
		key:      key,
		api:      strings.TrimRight(cmpOr(cfg.APIURL, DefaultAPIURL), "/"),
		http:     cfg.HTTPClient,
		now:      cfg.Now,
		tokens:   map[int64]installationToken{},
	}
	if app.http == nil {
		app.http = &http.Client{Timeout: requestTimeout}
	}
	if app.now == nil {
		app.now = time.Now
	}

	return app, nil
}

func parsePrivateKey(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("github app: private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app: private key is not an RSA key")
	}

	return key, nil
}

// JWT returns a short-lived token that authenticates as the app itself.
func (a *App) JWT() (string, error) {
	now := a.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": a.clientID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims: %w", err)
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Installation returns a client that acts as one installation.
func (a *App) Installation(id int64) *Client {
	return &Client{
		api:  a.api,
		http: a.http,
		auth: func(ctx context.Context) (string, error) {
			token, err := a.installationToken(ctx, id)
			if err != nil {
				return "", err
			}

			return "token " + token, nil
		},
	}
}

// appClient is a client authenticated with the app JWT.
func (a *App) appClient() *Client {
	return &Client{
		api:  a.api,
		http: a.http,
		auth: func(context.Context) (string, error) {
			jwt, err := a.JWT()
			if err != nil {
				return "", err
			}

			return "Bearer " + jwt, nil
		},
	}
}

func (a *App) installationToken(ctx context.Context, id int64) (string, error) {
	a.mu.Lock()
	cached, ok := a.tokens[id]
	a.mu.Unlock()
	if ok && a.now().Add(tokenRefreshMargin).Before(cached.expiresAt) {
		return cached.token, nil
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/app/installations/" + strconv.FormatInt(id, 10) + "/access_tokens"
	if err := a.appClient().do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return "", fmt.Errorf("create installation token: %w", err)
	}

	a.mu.Lock()
	a.tokens[id] = installationToken{token: out.Token, expiresAt: out.ExpiresAt}
	a.mu.Unlock()

	return out.Token, nil
}

// ForgetInstallation drops a cached token, for example after an uninstall.
func (a *App) ForgetInstallation(id int64) {
	a.mu.Lock()
	delete(a.tokens, id)
	a.mu.Unlock()
}

// Installation as returned by GET /app/installations/{id}.
type Installation struct {
	ID          int64      `json:"id"`
	Account     Account    `json:"account"`
	SuspendedAt *time.Time `json:"suspended_at"`
}

// Account is a GitHub user or organization.
type Account struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

// GetInstallation reads one installation of the app.
func (a *App) GetInstallation(ctx context.Context, id int64) (Installation, error) {
	var out Installation
	err := a.appClient().do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(id, 10), nil, &out)

	return out, err
}

// HookDelivery summarizes one webhook delivery attempt.
type HookDelivery struct {
	ID          int64     `json:"id"`
	GUID        string    `json:"guid"`
	DeliveredAt time.Time `json:"delivered_at"`
	Redelivery  bool      `json:"redelivery"`
	Status      string    `json:"status"`
	StatusCode  int       `json:"status_code"`
	Event       string    `json:"event"`
}

// ListHookDeliveries returns one page of the app's webhook deliveries,
// newest first, and the cursor for the next page ("" at the end).
func (a *App) ListHookDeliveries(ctx context.Context, cursor string) ([]HookDelivery, string, error) {
	query := url.Values{"per_page": {"100"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var out []HookDelivery
	header, err := a.appClient().doHeader(ctx, http.MethodGet, "/app/hook/deliveries?"+query.Encode(), nil, &out)
	if err != nil {
		return nil, "", err
	}

	return out, nextCursor(header.Get("Link")), nil
}

// Redeliver asks GitHub to send a delivery again.
func (a *App) Redeliver(ctx context.Context, deliveryID int64) error {
	path := "/app/hook/deliveries/" + strconv.FormatInt(deliveryID, 10) + "/attempts"

	return a.appClient().do(ctx, http.MethodPost, path, nil, nil)
}

// nextCursor extracts the cursor of the rel="next" link, if any.
func nextCursor(link string) string {
	for part := range strings.SplitSeq(link, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 || strings.TrimSpace(segments[1]) != `rel="next"` {
			continue
		}
		raw := strings.Trim(strings.TrimSpace(segments[0]), "<>")
		parsed, err := url.Parse(raw)
		if err != nil {
			return ""
		}

		return parsed.Query().Get("cursor")
	}

	return ""
}

// Client calls the REST API with one set of credentials.
type Client struct {
	api  string
	http *http.Client
	auth func(ctx context.Context) (string, error)
}

// APIError is a non-2xx response from GitHub.
type APIError struct {
	Status  int
	Message string
	// RetryAfter is set when GitHub asked the caller to back off.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %d %s", e.Status, e.Message)
}

// StatusOf returns the HTTP status of a GitHub error, or 0.
func StatusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}

	return 0
}

// IsNotFound reports whether err is a GitHub 404.
func IsNotFound(err error) bool {
	return StatusOf(err) == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	_, err := c.doHeader(ctx, method, path, body, out)

	return err
}

// doHeader makes a request, decodes a 2xx body into out and returns the
// response headers.
func (c *Client) doHeader(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	authorization, err := c.auth(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "airship")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github %s %s: %w", method, redactPath(path), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read github response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, apiError(resp, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("decode github response: %w", err)
		}
	}

	return resp.Header, nil
}

func apiError(resp *http.Response, raw []byte) *APIError {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body)
	apiErr := &APIError{Status: resp.StatusCode, Message: cmpOr(body.Message, http.StatusText(resp.StatusCode))}

	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		apiErr.RetryAfter = time.Duration(seconds) * time.Second
	} else if resp.Header.Get("X-Ratelimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			apiErr.RetryAfter = max(time.Until(time.Unix(reset, 0)), time.Second)
		}
	}

	return apiErr
}

// redactPath drops the query string, which can carry cursors and codes.
func redactPath(path string) string {
	before, _, _ := strings.Cut(path, "?")

	return before
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
