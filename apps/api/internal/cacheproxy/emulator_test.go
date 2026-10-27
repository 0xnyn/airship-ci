package cacheproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestS3StoreAgainstEmulator runs the S3 store's real SDK calls against
// moto at AIRSHIP_AWS_ENDPOINT: a single-request put from staged files
// (which must be seekable for a plain-http endpoint), a multipart put,
// ranged reads, and listing.
func TestS3StoreAgainstEmulator(t *testing.T) {
	endpoint := os.Getenv("AIRSHIP_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("AIRSHIP_AWS_ENDPOINT is not set")
	}
	ctx := context.Background()
	client := s3.NewFromConfig(awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) { o.BaseEndpoint = &endpoint; o.UsePathStyle = true })
	bucket := "airship-cache-store-test"
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &bucket}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	store := NewS3Store(client, bucket)

	small := randomBytes(3_000_000)
	if err := store.Put(ctx, "v/small", int64(len(small)), bytes.NewReader(small)); err != nil {
		t.Fatalf("put small: %v", err)
	}
	if size, err := store.Head(ctx, "v/small"); err != nil || size != int64(len(small)) {
		t.Fatalf("head small = %d, %v", size, err)
	}
	body, total, err := store.Get(ctx, "v/small", 1_000_000, 1000)
	if err != nil {
		t.Fatalf("ranged get: %v", err)
	}
	got, _ := io.ReadAll(body)
	_ = body.Close()
	if total != int64(len(small)) || !bytes.Equal(got, small[1_000_000:1_001_000]) {
		t.Fatalf("ranged get = %d bytes of %d", len(got), total)
	}

	// Past one part: sent as multipart straight from the seekable body.
	large := randomBytes(uploadPartSize + 1_000_000)
	if err := store.Put(ctx, "v/large", int64(len(large)), bytes.NewReader(large)); err != nil {
		t.Fatalf("put large: %v", err)
	}
	body, total, err = store.Get(ctx, "v/large", 0, -1)
	if err != nil {
		t.Fatalf("get large: %v", err)
	}
	got, _ = io.ReadAll(body)
	_ = body.Close()
	if total != int64(len(large)) || !bytes.Equal(got, large) {
		t.Fatalf("get large = %d bytes of %d, equal %v", len(got), total, bytes.Equal(got, large))
	}

	objects, err := store.List(ctx, "v/")
	if err != nil || len(objects) != 2 {
		t.Fatalf("list = %+v, %v", objects, err)
	}
	if _, err := store.Head(ctx, "v/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head of a missing key = %v", err)
	}
}
