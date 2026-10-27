// Package runnerapi serves what runner machines ask the control plane for
// at boot: the cache proxy binary and, with the runner's cache token, the
// credentials for its cache bucket.
package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/assets"
	"github.com/wizenheimer/airship/apps/api/internal/cacheproxy"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
)

// Granter exchanges runner cache tokens for credentials.
type Granter interface {
	CacheGrant(ctx context.Context, token string) (cloud.Grant, error)
}

type grantInput struct {
	Authorization string `doc:"Bearer runner cache token" header:"Authorization"`
}

type grantOutput struct {
	Body cacheproxy.Grant
}

type assetInput struct {
	OS   string `doc:"Operating system" enum:"linux" path:"os"`
	Arch string `doc:"CPU architecture" enum:"x64,arm64" path:"arch"`
}

type assetOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// Register adds the runner endpoints.
func Register(api huma.API, granter Granter) {
	huma.Register(api, huma.Operation{
		OperationID: "runnerCacheGrant",
		Method:      http.MethodGet,
		Path:        cacheproxy.GrantPath,
		Summary:     "Fetch cache bucket credentials for a runner",
		Description: "Called by the cache proxy on a runner machine with the token its boot script carries. The credentials are scoped to the runner's repository prefix in the installation's bucket and expire; the proxy refreshes them.",
		Tags:        []string{"Runner"},
	}, func(ctx context.Context, in *grantInput) (*grantOutput, error) {
		token, ok := strings.CutPrefix(in.Authorization, "Bearer ")
		if !ok || !strings.HasPrefix(token, fleet.CacheTokenPrefix) {
			return nil, huma.Error401Unauthorized("missing runner token")
		}
		grant, err := granter.CacheGrant(ctx, token)
		if errors.Is(err, fleet.ErrNoCacheGrant) {
			return nil, huma.Error401Unauthorized("invalid runner token")
		}
		if err != nil {
			return nil, fmt.Errorf("cache grant: %w", err)
		}

		return &grantOutput{Body: cacheproxy.Grant{
			Cloud: cloud.AWS, Bucket: grant.Bucket, Region: grant.Region, Prefix: grant.Prefix, AccountID: grant.AccountID,
			AccessKeyID: grant.AccessKeyID, SecretAccessKey: grant.SecretAccessKey, SessionToken: grant.SessionToken,
			ExpiresAt: grant.ExpiresAt.UTC().Truncate(time.Second),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getCacheProxyBinary",
		Method:      http.MethodGet,
		Path:        "/v1/assets/cacheproxy/{os}/{arch}",
		Summary:     "Download the cache proxy binary",
		Description: "Runner machines with extra_config.cache fetch this at boot.",
		Tags:        []string{"Runner"},
	}, func(_ context.Context, in *assetInput) (*assetOutput, error) {
		arch := map[string]string{"x64": "amd64", "arm64": "arm64"}[in.Arch]
		data, ok := assets.CacheProxy(in.OS, arch)
		if !ok {
			return nil, huma.Error404NotFound("this control plane was built without the cache proxy for " + in.OS + "/" + in.Arch)
		}

		return &assetOutput{ContentType: "application/octet-stream", Body: data}, nil
	})
}
