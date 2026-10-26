package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"
)

// CacheTokenPrefix starts every runner cache token.
const CacheTokenPrefix = "asr_"

// ErrNoCacheGrant is returned when a token names no live cache-enabled
// runner.
var ErrNoCacheGrant = errors.New("no cache grant for this token")

// cacheBucketName is the bucket for an installation in a region: a fixed
// prefix, the region, and a hash so the name is unique across accounts
// without saying whose it is.
func cacheBucketName(installationID int64, region string) string {
	sum := sha256.Sum256([]byte("airship-cache:" + strconv.FormatInt(installationID, 10)))

	return "airship-cache-" + region + "-" + hex.EncodeToString(sum[:6])
}

// ensuredBuckets remembers buckets confirmed this process lifetime, so
// each launch does not repeat the bucket calls.
var ensuredBuckets sync.Map

// prepareCache makes sure the runner's bucket exists and gives the runner
// a token its cache proxy will present for credentials.
func (s *Service) prepareCache(ctx context.Context, runner db.Runner, provider cloud.Provider, secret []byte) (*cloud.CacheBoot, error) {
	cacher, ok := provider.(cloud.Cacher)
	if !ok {
		return nil, fmt.Errorf("%s has no cache support", provider.Name())
	}
	if s.cfg.PublicURL == "" {
		return nil, errors.New("PUBLIC_URL is not set, so runners cannot reach the control plane")
	}
	bucket := cacheBucketName(runner.InstallationID, runner.Region)
	key := strconv.FormatInt(runner.InstallationID, 10) + "/" + runner.Region
	if _, done := ensuredBuckets.Load(key); !done {
		if err := cacher.EnsureCacheBucket(ctx, secret, runner.Region, bucket, s.cfg.CacheRetentionDays); err != nil {
			return nil, err //nolint:wrapcheck // the provider's error names the bucket
		}
		ensuredBuckets.Store(key, time.Now())
	}
	token := auth.NewToken(CacheTokenPrefix)
	if err := s.q.SetRunnerCache(ctx, db.SetRunnerCacheParams{ID: runner.ID, CacheTokenHash: auth.HashToken(token), CacheBucket: &bucket}); err != nil {
		return nil, fmt.Errorf("store cache token: %w", err)
	}

	return &cloud.CacheBoot{ControlPlaneURL: s.cfg.PublicURL, Token: token}, nil
}

// CacheGrant exchanges a runner's cache token for credentials scoped to
// its repository's prefix in its bucket.
func (s *Service) CacheGrant(ctx context.Context, token string) (cloud.Grant, error) {
	row, err := s.q.AuthenticateRunnerCacheToken(ctx, auth.HashToken(token))
	if isNoRows(err) {
		return cloud.Grant{}, ErrNoCacheGrant
	}
	if err != nil {
		return cloud.Grant{}, fmt.Errorf("authenticate cache token: %w", err)
	}
	runner := row.Runner
	if runner.CacheBucket == nil {
		return cloud.Grant{}, ErrNoCacheGrant
	}
	provider, secret, err := s.credentialFor(ctx, runner)
	if err != nil {
		return cloud.Grant{}, err
	}
	cacher, ok := provider.(cloud.Cacher)
	if !ok {
		return cloud.Grant{}, ErrNoCacheGrant
	}
	grant, err := cacher.CacheGrant(ctx, secret, runner.Region, *runner.CacheBucket, runner.RepositoryFullName, runner.Name, s.cfg.CacheGrantTTL)
	if err != nil {
		return cloud.Grant{}, fmt.Errorf("cache grant: %w", err)
	}

	return grant, nil
}
