package aws

import (
	"context"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestAgainstEmulator runs the provider's real SDK calls against an EC2
// emulator such as moto (`moto_server -p 5055`), set with
// AIRSHIP_AWS_ENDPOINT=http://localhost:5055. It checks request shapes,
// tagging, filtering, the default VPC lookup, security group creation and
// termination. It cannot check IAM, and moto does not honor ClientToken,
// so idempotency is covered by TestRepeatedLaunchSendsTheSameRequest.
func TestAgainstEmulator(t *testing.T) {
	endpoint := os.Getenv("AIRSHIP_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("AIRSHIP_AWS_ENDPOINT is not set")
	}
	ctx := context.Background()
	// The emulator keeps state between runs; scope this run by installation.
	installation := time.Now().UnixNano() % 1_000_000_000
	other := installation + 1
	p := New(Options{Endpoint: endpoint})
	secret, _ := EncodeSecret(Secret{AccessKeyID: "test", SecretAccessKey: "test"})

	identity, err := p.Verify(ctx, secret, "us-east-1")
	if err != nil || identity.AccountID == "" {
		t.Fatalf("verify = %+v, %v", identity, err)
	}

	client, ok := p.newClients(Secret{AccessKeyID: "test", SecretAccessKey: "test"}, "us-east-1").EC2.(*ec2.Client)
	if !ok {
		t.Fatal("expected a real EC2 client")
	}
	images, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{Owners: []string{"amazon"}})
	if err != nil || len(images.Images) == 0 {
		t.Fatalf("no emulator images: %v", err)
	}
	ami := awssdk.ToString(images.Images[0].ImageId)

	launch := func(installationID int64, runner string, spot bool) string {
		t.Helper()
		l := testLaunch()
		l.InstallationID, l.RunnerID, l.RunnerName = installationID, runner, "airship-"+runner[:8]
		l.Machine.Image, l.Machine.Spot = ami, spot
		id, err := p.Launch(ctx, secret, l)
		if err != nil {
			t.Fatalf("launch %s: %v", runner, err)
		}

		return id
	}
	first := launch(installation, "0199d1a8-0000-7000-8000-000000000001", false)
	launch(installation, "0199d1a8-0000-7000-8000-000000000002", false)
	launch(other, "0199d1a8-0000-7000-8000-000000000003", true)

	listed, err := p.List(ctx, secret, "us-east-1", installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("installation lists %d instances, want 2 (other installations' must not appear): %+v", len(listed), listed)
	}

	described, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{first}})
	if err != nil {
		t.Fatal(err)
	}
	instance := described.Reservations[0].Instances[0]
	if instance.MetadataOptions == nil || instance.MetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired {
		t.Errorf("IMDSv2 not required: %+v", instance.MetadataOptions)
	}
	if len(instance.SecurityGroups) != 1 || awssdk.ToString(instance.SecurityGroups[0].GroupName) != SecurityGroupName {
		t.Errorf("security groups = %+v, want %s", instance.SecurityGroups, SecurityGroupName)
	}
	behavior, err := client.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: &first, Attribute: ec2types.InstanceAttributeNameInstanceInitiatedShutdownBehavior,
	})
	if err != nil || awssdk.ToString(behavior.InstanceInitiatedShutdownBehavior.Value) != "terminate" {
		t.Errorf("shutdown behavior = %v, %v; instances must terminate themselves", behavior, err)
	}
	userData, err := client.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: &first, Attribute: ec2types.InstanceAttributeNameUserData,
	})
	if err != nil {
		t.Fatal(err)
	}
	script, _ := base64.StdEncoding.DecodeString(awssdk.ToString(userData.UserData.Value))
	if !strings.HasPrefix(string(script), "#!/bin/bash") || !strings.Contains(string(script), testLaunch().Boot.JITConfig) {
		t.Error("user data did not round-trip through EC2")
	}
	if tag := tagValue(instance.Tags, TagInstallation); tag != strconv.FormatInt(installation, 10) {
		t.Errorf("installation tag = %q", tag)
	}

	if err := p.Terminate(ctx, secret, "us-east-1", first, installation+1); err == nil {
		t.Fatal("another installation terminated our instance")
	}
	if err := p.Terminate(ctx, secret, "us-east-1", first, installation); err != nil {
		t.Fatal(err)
	}
	if err := p.Terminate(ctx, secret, "us-east-1", "i-0000000000000dead", installation); err != nil {
		t.Fatalf("terminating a missing instance: %v", err)
	}
	listed, err = p.List(ctx, secret, "us-east-1", installation)
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range listed {
		if inst.InstanceID == first && !inst.Stopping {
			t.Fatal("terminated instance is still listed as live")
		}
	}
	var _ cloud.Provider = p

	// The cache: a private bucket with a lifecycle rule, and federation
	// credentials scoped to it.
	bucket := "airship-cache-us-east-1-" + strconv.FormatInt(installation, 36)
	for range 2 { // idempotent
		if err := p.EnsureCacheBucket(ctx, secret, "us-east-1", bucket, 7); err != nil {
			t.Fatalf("ensure bucket: %v", err)
		}
	}
	s3Client, ok := p.newClients(Secret{AccessKeyID: "test", SecretAccessKey: "test"}, "us-east-1").S3.(*s3.Client)
	if !ok {
		t.Fatal("expected a real S3 client")
	}
	lifecycle, err := s3Client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: &bucket})
	if err != nil || len(lifecycle.Rules) != 1 || awssdk.ToInt32(lifecycle.Rules[0].Expiration.Days) != 7 {
		t.Fatalf("lifecycle = %+v, %v", lifecycle, err)
	}
	block, err := s3Client.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: &bucket})
	if err != nil || !awssdk.ToBool(block.PublicAccessBlockConfiguration.BlockPublicAcls) {
		t.Fatalf("public access block = %+v, %v", block, err)
	}
	grant, err := p.CacheGrant(ctx, secret, "us-east-1", bucket, "", "airship-abc", time.Hour)
	if err != nil {
		t.Fatalf("cache grant: %v", err)
	}
	if grant.AccessKeyID == "" || grant.SessionToken == "" || grant.Bucket != bucket || time.Until(grant.ExpiresAt) < 50*time.Minute {
		t.Fatalf("grant = %+v", grant)
	}
	// The grant's credentials can write to the bucket (moto does not
	// evaluate the session policy, so the scope itself is not checked here).
	granted := s3.NewFromConfig(awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(grant.AccessKeyID, grant.SecretAccessKey, grant.SessionToken),
	}, func(o *s3.Options) { o.BaseEndpoint = &endpoint; o.UsePathStyle = true })
	if _, err := granted.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: awssdk.String("v1/key"), Body: strings.NewReader("x")}); err != nil {
		t.Fatalf("put with granted credentials: %v", err)
	}
}
