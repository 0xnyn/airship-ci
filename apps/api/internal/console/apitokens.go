package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// APITokenPrefix starts every developer API token, so a leaked one is easy
// to recognize in logs and by secret scanners.
const APITokenPrefix = "asa_"

// maxAPITokens bounds the tokens one organization may hold.
const maxAPITokens = 25

const apiTokenKey = contextKey("apiToken")

type contextKey string

// APIToken is a developer API token, without its secret.
type APIToken struct {
	ID         uuid.UUID  `doc:"Token ID"                              format:"uuid" json:"id"`
	Name       string     `doc:"What the token is for"                 json:"name"`
	TokenHint  string     `doc:"First characters of the token"         json:"tokenHint"`
	CreatedAt  time.Time  `doc:"When it was created"                   json:"createdAt"`
	LastUsedAt *time.Time `doc:"When it was last used, to the minute"  json:"lastUsedAt"`
}

func apiTokenFromDB(t db.ApiToken) APIToken {
	return APIToken{ID: t.ID, Name: t.Name, TokenHint: t.TokenHint, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// APITokenList is an organization's developer API tokens.
type APITokenList struct {
	Items []APIToken `json:"items"`
}

type apiTokenListOutput struct {
	Body APITokenList
}

// CreateAPIToken is the request body for creating a developer API token.
type CreateAPIToken struct {
	Name string `doc:"What the token is for" json:"name" maxLength:"63" minLength:"1"`
}

type createAPITokenInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body CreateAPIToken
}

// APITokenSecret is a new token and its secret, shown only in this
// response; Airship keeps just its hash.
type APITokenSecret struct {
	APIToken APIToken `json:"apiToken"`
	Token    string   `doc:"The token. Store it now: it cannot be shown again." json:"token"`
}

type apiTokenSecretOutput struct {
	Body APITokenSecret
}

func registerAPITokens(api huma.API, h *handlers, op func(id, method, path, summary, tag string, status int) huma.Operation) {
	huma.Register(api, op("listApiTokens", http.MethodGet, "/v1/orgs/{org}/api-tokens", "List developer API tokens", "Developer API", 0), h.listAPITokens)
	huma.Register(api, op("createApiToken", http.MethodPost, "/v1/orgs/{org}/api-tokens", "Create a developer API token", "Developer API", http.StatusCreated), h.createAPIToken)
	huma.Register(api, op("deleteApiToken", http.MethodDelete, "/v1/orgs/{org}/api-tokens/{id}", "Revoke a developer API token", "Developer API", http.StatusNoContent), h.deleteAPIToken)

	// The developer API itself: bearer token only, never the session
	// cookie, so it cannot be driven from a browser tab.
	tokenOp := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{
			OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"Developer API"},
			DefaultStatus: status, Middlewares: huma.Middlewares{h.requireAPIToken(api)},
			Security: []map[string][]string{{"apiToken": {}}},
		}
	}
	huma.Register(api, tokenOp("apiListRepositories", http.MethodGet, "/v1/api/repositories", "List repositories", 0), h.apiListRepositories)
	huma.Register(api, tokenOp("apiActivateRepository", http.MethodPut, "/v1/api/repositories/{name}/activation", "Activate a repository", 0), h.apiActivateRepository)
	huma.Register(api, tokenOp("apiDeactivateRepository", http.MethodDelete, "/v1/api/repositories/{name}/activation", "Deactivate a repository", http.StatusNoContent), h.apiDeactivateRepository)

	// Cirun's developer API paths, so its client (cirun-py) works with
	// CIRUN_API_ENDPOINT=<airship>/api/v1 and an Airship token.
	huma.Register(api, tokenOp("cirunListRepositories", http.MethodGet, "/api/v1/repo", "List repositories (Cirun-compatible)", 0), h.apiListRepositories)
	huma.Register(api, tokenOp("cirunSetRepository", http.MethodPost, "/api/v1/repo", "Activate or deactivate a repository (Cirun-compatible)", 0), h.cirunSetRepository)
	huma.Register(api, tokenOp("cirunListClouds", http.MethodGet, "/api/v1/cloud-connect", "List connected clouds (Cirun-compatible)", 0), h.apiListClouds)
	huma.Register(api, tokenOp("cirunCloudConnect", http.MethodPost, "/api/v1/cloud-connect", "Connect a cloud (Cirun-compatible)", 0), h.cirunCloudConnect)
	huma.Register(api, tokenOp("cirunRunnerProvision", http.MethodGet, "/api/runner-provision", "Fetch a runner's boot script (provision_via: pull_from_api)", 0), h.runnerProvision)
}

func (h *handlers) apiListClouds(ctx context.Context, _ *struct{}) (*cloudListOutput, error) {
	rows, err := h.Store.ListCloudCredentials(ctx, principal(ctx).installation.ID)
	if err != nil {
		return nil, fmt.Errorf("list clouds: %w", err)
	}
	items := make([]Cloud, 0, len(rows))
	for _, row := range rows {
		items = append(items, cloudFromDB(row))
	}

	return &cloudListOutput{Body: CloudList{Items: items}}, nil
}

// CloudConnect is Cirun's cloud-connect request: a cloud name and that
// cloud's credentials, as `cirun cloud connect aws` sends them.
type CloudConnect struct {
	Cloud       string            `doc:"Cloud name" enum:"aws" json:"cloud"`
	Credentials map[string]string `doc:"Credentials; for aws: access_key, secret_key and optionally region" json:"credentials" maxProperties:"10"`
}

type cloudConnectInput struct {
	Body CloudConnect
}

func (h *handlers) cirunCloudConnect(ctx context.Context, in *cloudConnectInput) (*cloudOutput, error) {
	p := principal(ctx)
	creds := in.Body.Credentials
	secret, err := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: creds["access_key"], SecretAccessKey: creds["secret_key"]})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("credentials need access_key and secret_key")
	}
	region := creds["region"]
	if region == "" {
		region = DefaultRegion
	}
	if !regionPattern.MatchString(region) {
		return nil, huma.Error422UnprocessableEntity("region is not an AWS region")
	}
	row, err := h.connect(ctx, p.installation.ID, cloud.AWS, secret, region, p.token.CreatedBy)
	if err != nil {
		return nil, err
	}

	return &cloudOutput{Body: cloudFromDB(row)}, nil
}

var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)

type runnerProvisionInput struct {
	RunnerName string `doc:"The runner's name, from the instance's Name tag" maxLength:"64" minLength:"1" query:"runner_name" required:"true"`
}

type runnerProvisionOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// runnerProvision hands a booting machine its script, once. The script
// carries the runner's single-use GitHub registration, so it is served
// only to the organization's own API tokens and only while the runner
// has not fetched it.
func (h *handlers) runnerProvision(ctx context.Context, in *runnerProvisionInput) (*runnerProvisionOutput, error) {
	p := principal(ctx)
	runner, err := h.Store.MarkRunnerProvisioned(ctx, db.MarkRunnerProvisionedParams{InstallationID: p.installation.ID, Name: in.RunnerName})
	if isNoRows(err) {
		return nil, huma.Error404NotFound("no boot script for this runner: it is not launching with provision_via: pull_from_api, or already fetched it")
	}
	if err != nil {
		return nil, fmt.Errorf("mark provisioned: %w", err)
	}
	script, err := h.Fleet.ProvisionScript(ctx, runner.BootSecret)
	if errors.Is(err, fleet.ErrNotProvisionable) {
		return nil, huma.Error404NotFound(err.Error())
	}
	if err != nil {
		return nil, fmt.Errorf("provision script: %w", err)
	}

	return &runnerProvisionOutput{ContentType: "text/plain; charset=utf-8", Body: []byte(script)}, nil
}

// Token management is for owners only: a token can activate repositories,
// which spends cloud budget.

func (h *handlers) listAPITokens(ctx context.Context, in *orgInput) (*apiTokenListOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	rows, err := h.Store.ListAPITokens(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	items := make([]APIToken, 0, len(rows))
	for _, row := range rows {
		items = append(items, apiTokenFromDB(row))
	}

	return &apiTokenListOutput{Body: APITokenList{Items: items}}, nil
}

func (h *handlers) createAPIToken(ctx context.Context, in *createAPITokenInput) (*apiTokenSecretOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	n, err := h.Store.CountAPITokens(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("count api tokens: %w", err)
	}
	if n >= maxAPITokens {
		return nil, huma.Error409Conflict(fmt.Sprintf("an organization can have at most %d tokens; revoke one first", maxAPITokens))
	}
	user, _ := auth.UserFrom(ctx)
	token := auth.NewToken(APITokenPrefix)
	row, err := h.Store.CreateAPIToken(ctx, db.CreateAPITokenParams{
		InstallationID: inst.ID, Name: strings.TrimSpace(in.Body.Name), TokenHash: auth.HashToken(token),
		TokenHint: token[:len(APITokenPrefix)+4], CreatedBy: &user.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("create api token: %w", err)
	}

	return &apiTokenSecretOutput{Body: APITokenSecret{APIToken: apiTokenFromDB(row), Token: token}}, nil
}

func (h *handlers) deleteAPIToken(ctx context.Context, in *orgIDInput) (*struct{}, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	n, err := h.Store.DeleteAPIToken(ctx, db.DeleteAPITokenParams{InstallationID: inst.ID, ID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("delete api token: %w", err)
	}
	if n == 0 {
		return nil, huma.Error404NotFound("token not found")
	}

	return &struct{}{}, nil
}

type tokenPrincipal struct {
	token        db.ApiToken
	installation db.Installation
}

// requireAPIToken authenticates a developer API request. Tokens of
// suspended or uninstalled installations are refused.
func (h *handlers) requireAPIToken(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		token, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(token, APITokenPrefix) {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "missing API token: send Authorization: Bearer "+APITokenPrefix+"...")

			return
		}
		row, err := h.Store.AuthenticateAPIToken(ctx.Context(), auth.HashToken(token))
		if isNoRows(err) {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid API token")

			return
		}
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "could not check the API token")

			return
		}
		if err := h.Store.TouchAPIToken(ctx.Context(), row.ApiToken.ID); err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "could not check the API token")

			return
		}
		next(huma.WithValue(ctx, apiTokenKey, tokenPrincipal{token: row.ApiToken, installation: row.Installation}))
	}
}

func principal(ctx context.Context) tokenPrincipal {
	p, _ := ctx.Value(apiTokenKey).(tokenPrincipal)

	return p
}

func (h *handlers) apiListRepositories(ctx context.Context, _ *struct{}) (*repositoryListOutput, error) {
	items, err := h.repositories(ctx, principal(ctx).installation.ID)
	if err != nil {
		return nil, err
	}

	return &repositoryListOutput{Body: RepositoryList{Items: items}}, nil
}

type apiRepositoryInput struct {
	Name string `doc:"Repository name, without the owner" maxLength:"100" minLength:"1" path:"name"`
}

type apiActivateInput struct {
	Name string `doc:"Repository name, without the owner" maxLength:"100" minLength:"1" path:"name"`
	Body ActivateRepository
}

// findRepository looks a repository up by name among those the app can
// access (activate) or those activated (deactivate, which must work after
// the app lost access).
func findRepository(items []Repository, owner, name string) (Repository, bool) {
	full := owner + "/" + name
	for _, item := range items {
		if strings.EqualFold(item.FullName, full) {
			return item, true
		}
	}

	return Repository{}, false
}

var (
	errRepoNotFound = huma.Error404NotFound("repository not found; is the GitHub App installed on it?")
	errNotActivated = huma.Error404NotFound("repository is not activated")
)

func (h *handlers) apiActivateRepository(ctx context.Context, in *apiActivateInput) (*repositoryOutput, error) {
	item, err := h.activateByName(ctx, in.Name, in.Body.AllowPublic)
	if err != nil {
		return nil, err
	}

	return &repositoryOutput{Body: item}, nil
}

func (h *handlers) apiDeactivateRepository(ctx context.Context, in *apiRepositoryInput) (*struct{}, error) {
	if _, err := h.deactivateByName(ctx, in.Name); err != nil {
		return nil, err
	}

	return &struct{}{}, nil
}

func (h *handlers) activateByName(ctx context.Context, name string, allowPublic bool) (Repository, error) {
	p := principal(ctx)
	items, err := h.repositories(ctx, p.installation.ID)
	if err != nil {
		return Repository{}, err
	}
	repo, ok := findRepository(items, p.installation.AccountLogin, name)
	if !ok || !repo.Accessible {
		return Repository{}, errRepoNotFound
	}
	row, err := h.Fleet.ActivateRepository(ctx, p.installation.ID, repo.ID, allowPublic, p.token.CreatedBy)
	if errors.Is(err, fleet.ErrRepositoryNotFound) {
		return Repository{}, errRepoNotFound
	}
	if err != nil {
		return Repository{}, fmt.Errorf("activate repository: %w", err)
	}
	item := repositoryFromDB(row)
	item.Accessible = true

	return item, nil
}

// deactivateByName looks among activated repositories only, so a
// repository the app lost access to can still be deactivated.
func (h *handlers) deactivateByName(ctx context.Context, name string) (Repository, error) {
	p := principal(ctx)
	rows, err := h.Store.ListRepositories(ctx, p.installation.ID)
	if err != nil {
		return Repository{}, fmt.Errorf("list activated repositories: %w", err)
	}
	items := make([]Repository, 0, len(rows))
	for _, row := range rows {
		items = append(items, repositoryFromDB(row))
	}
	repo, ok := findRepository(items, p.installation.AccountLogin, name)
	if !ok {
		return Repository{}, errNotActivated
	}
	err = h.Fleet.DeactivateRepository(ctx, p.installation.ID, repo.ID)
	if errors.Is(err, fleet.ErrRepositoryNotFound) {
		return Repository{}, errNotActivated
	}
	if err != nil {
		return Repository{}, fmt.Errorf("deactivate repository: %w", err)
	}
	repo.Activated, repo.ActivatedAt, repo.Config = false, nil, nil

	return repo, nil
}

// SetRepository is Cirun's repository activation request, so Cirun's
// client (cirun-py, `cirun repo add`) works against Airship unchanged.
type SetRepository struct {
	Repository string `doc:"owner/name, or just the name" json:"repository" maxLength:"200" minLength:"1"`
	Active     bool   `doc:"true activates, false deactivates" json:"active"`
}

type setRepositoryInput struct {
	Body SetRepository
}

// cirunSetRepository activates or deactivates by owner/name. Unlike the
// Airship routes it is idempotent: deactivating an inactive repository
// succeeds, as Cirun's does.
func (h *handlers) cirunSetRepository(ctx context.Context, in *setRepositoryInput) (*repositoryOutput, error) {
	p := principal(ctx)
	name := in.Body.Repository
	if owner, rest, ok := strings.Cut(name, "/"); ok {
		if !strings.EqualFold(owner, p.installation.AccountLogin) {
			return nil, errRepoNotFound
		}
		name = rest
	}
	if in.Body.Active {
		item, err := h.activateByName(ctx, name, false)
		if err != nil {
			return nil, err
		}

		return &repositoryOutput{Body: item}, nil
	}
	gone, err := h.deactivateByName(ctx, name)
	if err != nil && !errors.Is(err, errNotActivated) {
		return nil, err
	}
	// Report the repository as GitHub and Airship now see it.
	items, err := h.repositories(ctx, p.installation.ID)
	if err != nil {
		return nil, err
	}
	if item, ok := findRepository(items, p.installation.AccountLogin, name); ok {
		return &repositoryOutput{Body: item}, nil
	}
	if gone.ID != 0 {
		return &repositoryOutput{Body: gone}, nil
	}

	return nil, errRepoNotFound
}
