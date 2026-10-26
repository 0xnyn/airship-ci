package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OAuthConfig is the GitHub App's user authorization (sign-in) settings.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	// WebURL defaults to DefaultWebURL; APIURL to DefaultAPIURL.
	WebURL     string
	APIURL     string
	HTTPClient *http.Client
}

// OAuth signs users in with the GitHub App's user authorization flow.
type OAuth struct {
	cfg  OAuthConfig
	http *http.Client
}

// NewOAuth returns an OAuth helper.
func NewOAuth(cfg OAuthConfig) *OAuth {
	cfg.WebURL = strings.TrimRight(cmpOr(cfg.WebURL, DefaultWebURL), "/")
	cfg.APIURL = strings.TrimRight(cmpOr(cfg.APIURL, DefaultAPIURL), "/")
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	return &OAuth{cfg: cfg, http: client}
}

// AuthorizeURL is where the browser goes to approve sign-in. state must be a
// random value bound to the browser, checked again in the callback.
func (o *OAuth) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":    {o.cfg.ClientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}

	return o.cfg.WebURL + "/login/oauth/authorize?" + query.Encode()
}

// Exchange trades the callback's code for a user access token.
func (o *OAuth) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{
		"client_id":     {o.cfg.ClientID},
		"client_secret": {o.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.WebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange oauth code: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("decode oauth token: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("oauth exchange: %s: %s", out.Error, out.Description)
	}
	if out.AccessToken == "" {
		return "", errors.New("oauth exchange returned no token")
	}

	return out.AccessToken, nil
}

// User is the signed-in GitHub user.
type User struct {
	ID        int64   `json:"id"`
	Login     string  `json:"login"`
	Name      *string `json:"name"`
	AvatarURL *string `json:"avatar_url"`
}

// UserClient acts with a user's access token.
func (o *OAuth) UserClient(token string) *Client {
	return &Client{
		api:  o.cfg.APIURL,
		http: o.http,
		auth: func(context.Context) (string, error) { return "Bearer " + token, nil },
	}
}

// GetUser reads the authenticated user.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	var out User
	err := c.do(ctx, http.MethodGet, "/user", nil, &out)

	return out, err
}

// maxInstallationPages bounds how many installations sign-in reads (100 per page).
const maxInstallationPages = 10

// ListUserInstallations returns the app installations the user can access.
func (c *Client) ListUserInstallations(ctx context.Context) ([]Installation, error) {
	var all []Installation
	for page := 1; page <= maxInstallationPages; page++ {
		var out struct {
			Installations []Installation `json:"installations"`
		}
		path := fmt.Sprintf("/user/installations?per_page=100&page=%d", page)
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Installations...)
		if len(out.Installations) < 100 {
			break
		}
	}

	return all, nil
}

// OrgRole returns a user's role in an organization ("admin" or "member"),
// or "" when they are not an active member. It needs the app's
// organization "Members: read" permission.
func (c *Client) OrgRole(ctx context.Context, org, username string) (string, error) {
	var out struct {
		Role  string `json:"role"`
		State string `json:"state"`
	}
	path := "/orgs/" + url.PathEscape(org) + "/memberships/" + url.PathEscape(username)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		if IsNotFound(err) {
			return "", nil
		}

		return "", err
	}
	if out.State != "active" {
		return "", nil
	}

	return out.Role, nil
}
