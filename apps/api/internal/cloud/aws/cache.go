package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// S3 is the subset of the S3 API the cache needs.
type S3 interface {
	HeadBucket(ctx context.Context, in *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	CreateBucket(ctx context.Context, in *s3.CreateBucketInput, opts ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutPublicAccessBlock(ctx context.Context, in *s3.PutPublicAccessBlockInput, opts ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
	PutBucketLifecycleConfiguration(ctx context.Context, in *s3.PutBucketLifecycleConfigurationInput, opts ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error)
}

// Federation mints scoped temporary credentials.
type Federation interface {
	GetFederationToken(ctx context.Context, in *sts.GetFederationTokenInput, opts ...func(*sts.Options)) (*sts.GetFederationTokenOutput, error)
}

const (
	// Federation tokens live between 15 minutes and 36 hours.
	minGrantTTL = 15 * time.Minute
	maxGrantTTL = 36 * time.Hour
)

// EnsureCacheBucket implements cloud.Cacher. The bucket is private, its
// objects encrypted by default (SSE-S3 is S3's default), and a lifecycle
// rule expires entries after retentionDays. Creating a bucket that exists
// in this account is a no-op.
func (p *Provider) EnsureCacheBucket(ctx context.Context, raw []byte, region, bucket string, retentionDays int) error {
	secret, err := ParseSecret(raw)
	if err != nil {
		return err
	}
	client := p.newClients(secret, region).S3
	if client == nil {
		return errors.New("this provider has no S3 client")
	}
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket}); err == nil {
		return nil
	}
	in := &s3.CreateBucketInput{Bucket: &bucket}
	if region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{LocationConstraint: s3types.BucketLocationConstraint(region)}
	}
	if _, err := client.CreateBucket(ctx, in); err != nil {
		switch errorCode(err) {
		case "BucketAlreadyOwnedByYou":
		case "BucketAlreadyExists":
			return fmt.Errorf("bucket %s is taken by another account", bucket)
		default:
			return fmt.Errorf("create bucket %s: %w", bucket, err)
		}
	}
	_, err = client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: &bucket,
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls: awssdk.Bool(true), BlockPublicPolicy: awssdk.Bool(true),
			IgnorePublicAcls: awssdk.Bool(true), RestrictPublicBuckets: awssdk.Bool(true),
		},
	})
	if err != nil {
		return fmt.Errorf("block public access on %s: %w", bucket, err)
	}
	_, err = client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket: &bucket,
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{Rules: []s3types.LifecycleRule{{
			ID:                             awssdk.String("airship-cache-expiry"),
			Status:                         s3types.ExpirationStatusEnabled,
			Filter:                         &s3types.LifecycleRuleFilter{Prefix: awssdk.String("")},
			Expiration:                     &s3types.LifecycleExpiration{Days: awssdk.Int32(int32(retentionDays))}, //nolint:gosec // small, configured
			AbortIncompleteMultipartUpload: &s3types.AbortIncompleteMultipartUpload{DaysAfterInitiation: awssdk.Int32(1)},
		}}},
	})
	if err != nil {
		return fmt.Errorf("set lifecycle on %s: %w", bucket, err)
	}

	return nil
}

// CacheGrant implements cloud.Cacher with sts:GetFederationToken: the
// connected IAM user's own permissions, narrowed by an inline policy to
// one prefix of one bucket. No role has to exist in the account.
func (p *Provider) CacheGrant(ctx context.Context, raw []byte, region, bucket, prefix, name string, ttl time.Duration) (cloud.Grant, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return cloud.Grant{}, err
	}
	client := p.newClients(secret, region).Federation
	if client == nil {
		return cloud.Grant{}, errors.New("this provider has no STS client")
	}
	objects := "arn:aws:s3:::" + bucket + "/*"
	listing := map[string]any{
		"Effect":   "Allow",
		"Action":   []string{"s3:ListBucket"},
		"Resource": "arn:aws:s3:::" + bucket,
	}
	if prefix != "" {
		objects = "arn:aws:s3:::" + bucket + "/" + prefix + "/*"
		listing["Condition"] = map[string]any{"StringLike": map[string]any{"s3:prefix": []string{prefix + "/*"}}}
	}
	policy, err := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":   "Allow",
				"Action":   []string{"s3:GetObject", "s3:PutObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"},
				"Resource": objects,
			},
			listing,
		},
	})
	if err != nil {
		return cloud.Grant{}, fmt.Errorf("encode policy: %w", err)
	}
	ttl = min(max(ttl, minGrantTTL), maxGrantTTL)
	out, err := client.GetFederationToken(ctx, &sts.GetFederationTokenInput{
		Name:            awssdk.String(sanitizeFederationName(name)),
		Policy:          awssdk.String(string(policy)),
		DurationSeconds: awssdk.Int32(int32(ttl.Seconds())), //nolint:gosec // bounded above
	})
	if err != nil {
		return cloud.Grant{}, fmt.Errorf("get federation token: %w", err)
	}
	creds := out.Credentials

	return cloud.Grant{
		Bucket: bucket, Region: region, Prefix: prefix,
		AccessKeyID: awssdk.ToString(creds.AccessKeyId), SecretAccessKey: awssdk.ToString(creds.SecretAccessKey),
		SessionToken: awssdk.ToString(creds.SessionToken), ExpiresAt: awssdk.ToTime(creds.Expiration),
	}, nil
}

// sanitizeFederationName fits STS's name rules: 2-32 characters from a
// small set.
func sanitizeFederationName(name string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(name) && len(out) < 32; i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '=', c == ',', c == '.', c == '@':
			out = append(out, c)
		}
	}
	for len(out) < 2 {
		out = append(out, 'x')
	}

	return string(out)
}
