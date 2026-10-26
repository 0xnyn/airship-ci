package aws

import (
	"context"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// TestAgainstEmulator runs the provider's real SDK calls against an EC2
// emulator such as moto (`moto_server -p 5055`), set with
// AIRSHIP_AWS_ENDPOINT=http://localhost:5055. It checks request shapes,
// tagging, filtering and termination. It cannot check IAM, and moto does
// not honor ClientToken, so idempotency is covered by
// TestRepeatedLaunchSendsTheSameRequest instead.
func TestAgainstEmulator(t *testing.T) {
	endpoint := os.Getenv("AIRSHIP_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("AIRSHIP_AWS_ENDPOINT is not set")
	}
	ctx := context.Background()
	// The emulator keeps state between runs; scope this run by data plane.
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	dp1, dp2 := "dp-1-"+suffix, "dp-2-"+suffix
	cfg := awssdk.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}
	ec2Client := ec2.NewFromConfig(cfg, func(o *ec2.Options) { o.BaseEndpoint = &endpoint })
	ssmClient := ssm.NewFromConfig(cfg, func(o *ssm.Options) { o.BaseEndpoint = &endpoint })

	vpc, err := ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: awssdk.String("10.9.0.0/16")})
	if err != nil {
		t.Fatal(err)
	}
	subnet, err := ec2Client.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: vpc.Vpc.VpcId, CidrBlock: awssdk.String("10.9.1.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	images, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{Owners: []string{"amazon"}})
	if err != nil || len(images.Images) == 0 {
		t.Fatalf("no emulator images: %v", err)
	}
	param := "/airship-test/ami-id"
	if _, err := ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name: &param, Value: images.Images[0].ImageId, Type: ssmtypes.ParameterTypeString, Overwrite: awssdk.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}

	p, err := New(ec2Client, ssmClient, Config{
		Subnets:       []string{*subnet.Subnet.SubnetId},
		DefaultImages: map[string]string{"x64": ssmPrefix + param},
		SpotFallback:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	launch := func(dataPlane, runner string) string {
		t.Helper()
		l := testLaunch()
		l.RunnerID, l.RunnerName = runner, "airship-"+runner[:8]
		l.Profile.Spot = dataPlane == dp2 // one spot launch, the rest on-demand
		id, err := p.Launch(ctx, dataPlane, l)
		if err != nil {
			t.Fatalf("launch %s: %v", runner, err)
		}

		return id
	}
	first := launch(dp1, "0199d1a8-0000-7000-8000-000000000001")
	launch(dp1, "0199d1a8-0000-7000-8000-000000000002")
	launch(dp2, "0199d1a8-0000-7000-8000-000000000003")

	listed, err := p.List(ctx, dp1)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("data plane lists %d instances, want 2 (other data planes' must not appear): %+v", len(listed), listed)
	}

	described, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{first}})
	if err != nil {
		t.Fatal(err)
	}
	instance := described.Reservations[0].Instances[0]
	if instance.MetadataOptions == nil || instance.MetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired {
		t.Errorf("IMDSv2 not required: %+v", instance.MetadataOptions)
	}
	behavior, err := ec2Client.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: &first, Attribute: ec2types.InstanceAttributeNameInstanceInitiatedShutdownBehavior,
	})
	if err != nil || awssdk.ToString(behavior.InstanceInitiatedShutdownBehavior.Value) != "terminate" {
		t.Errorf("shutdown behavior = %v, %v; instances must terminate themselves", behavior, err)
	}
	userData, err := ec2Client.DescribeInstanceAttribute(ctx, &ec2.DescribeInstanceAttributeInput{
		InstanceId: &first, Attribute: ec2types.InstanceAttributeNameUserData,
	})
	if err != nil {
		t.Fatal(err)
	}
	script, _ := base64.StdEncoding.DecodeString(awssdk.ToString(userData.UserData.Value))
	if !strings.HasPrefix(string(script), "#!/bin/bash") || !strings.Contains(string(script), testLaunch().JITConfig) {
		t.Error("user data did not round-trip through EC2")
	}

	if err := p.Terminate(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := p.Terminate(ctx, "i-0000000000000dead"); err != nil {
		t.Fatalf("terminating a missing instance: %v", err)
	}
	listed, err = p.List(ctx, dp1)
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range listed {
		if inst.InstanceID == first && !inst.Stopping {
			t.Fatal("terminated instance is still listed as live")
		}
	}
}
