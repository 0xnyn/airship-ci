package cacheproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Grant is what the control plane hands a runner: where its cache lives
// and short-lived credentials for exactly that.
type Grant struct {
	Cloud  string `json:"cloud"`
	Bucket string `json:"bucket"`
	// AccountID owns the bucket; every S3 call names it.
	AccountID       string    `json:"accountId,omitempty"`
	Region          string    `json:"region"`
	Prefix          string    `json:"prefix"`
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	SessionToken    string    `json:"sessionToken"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// GrantPath is the control plane endpoint runners fetch grants from.
const GrantPath = "/v1/runner/cache-grant"

// GrantClient fetches grants from the control plane with the runner's
// token.
type GrantClient struct {
	URL   string
	Token string
	HTTP  *http.Client

	mu   sync.Mutex
	last Grant
}

// Fetch asks the control plane for a fresh grant.
func (c *GrantClient) Fetch(ctx context.Context) (Grant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+GrantPath, nil)
	if err != nil {
		return Grant{}, fmt.Errorf("build grant request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", "airship-cacheproxy")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var resp *http.Response
	var raw []byte
	for attempt := range fetchAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Grant{}, ctx.Err() //nolint:wrapcheck // the context's own reason
			case <-time.After(fetchRetryDelay):
			}
		}
		resp, err = client.Do(req.Clone(ctx))
		if err != nil {
			err = fmt.Errorf("fetch cache grant: %w", err)

			continue
		}
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, maxJSONBody))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			err = nil

			break
		}
		err = fmt.Errorf("fetch cache grant: %s: %s", resp.Status, raw)
		if resp.StatusCode < http.StatusInternalServerError {
			break // the control plane refused the token; asking again will not help
		}
	}
	if err != nil {
		return Grant{}, err
	}
	var grant Grant
	if err := json.Unmarshal(raw, &grant); err != nil {
		return Grant{}, fmt.Errorf("decode cache grant: %w", err)
	}
	if grant.Bucket == "" || grant.AccessKeyID == "" {
		return Grant{}, errors.New("cache grant is incomplete")
	}
	c.mu.Lock()
	c.last = grant
	c.mu.Unlock()

	return grant, nil
}

// Retrieve implements aws.CredentialsProvider, so the S3 client refreshes
// credentials through the control plane before they expire.
func (c *GrantClient) Retrieve(ctx context.Context) (awssdk.Credentials, error) {
	grant, err := c.Fetch(ctx)
	if err != nil {
		return awssdk.Credentials{}, err
	}

	return credentialsFor(grant), nil
}

// HandleFailToRefresh implements the credential cache's refresh fallback:
// while the last grant is still valid, a control plane that cannot be
// reached does not fail the cache call that needed a refresh.
func (c *GrantClient) HandleFailToRefresh(_ context.Context, prev awssdk.Credentials, err error) (awssdk.Credentials, error) {
	c.mu.Lock()
	last := c.last
	c.mu.Unlock()
	if last.AccessKeyID != "" && time.Now().Before(last.ExpiresAt) {
		return credentialsFor(last), nil
	}
	if prev.HasKeys() && !prev.Expired() {
		return prev, nil
	}

	return awssdk.Credentials{}, err
}

func credentialsFor(grant Grant) awssdk.Credentials {
	return awssdk.Credentials{
		AccessKeyID: grant.AccessKeyID, SecretAccessKey: grant.SecretAccessKey, SessionToken: grant.SessionToken,
		CanExpire: true, Expires: grant.ExpiresAt, Source: "airship",
	}
}

const (
	fetchAttempts   = 3
	fetchRetryDelay = 2 * time.Second
)

// NewS3Client builds an S3 client that authenticates through grants.
func NewS3Client(grants *GrantClient, region, endpoint string) *s3.Client {
	cfg := awssdk.Config{
		Region:      region,
		Credentials: awssdk.NewCredentialsCache(grants, func(o *awssdk.CredentialsCacheOptions) { o.ExpiryWindow = 5 * time.Minute }),
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
			o.UsePathStyle = true
		}
	})
}
