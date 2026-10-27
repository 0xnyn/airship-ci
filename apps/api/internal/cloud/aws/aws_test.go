package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

type fakeEC2 struct {
	mu         sync.Mutex
	runs       []*ec2.RunInstancesInput
	failures   []error // consumed by RunInstances in order
	listed     *ec2.DescribeInstancesInput
	instances  []ec2types.Instance
	images     map[string]ec2types.Image
	imageCall  int
	groups     int
	subnets    []ec2types.Subnet
	noDefault  bool
	terminated bool
}

func (f *fakeEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, in)
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		if err != nil {
			return nil, err
		}
	}

	return &ec2.RunInstancesOutput{Instances: []ec2types.Instance{{InstanceId: awssdk.String("i-123")}}}, nil
}

// DescribeInstances behaves as EC2 does: an explicit ID that does not exist
// is an error, and tag filters narrow the result.
func (f *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = in
	var matched []ec2types.Instance
	for _, instance := range f.instances {
		if len(in.InstanceIds) > 0 && !slices.Contains(in.InstanceIds, awssdk.ToString(instance.InstanceId)) {
			continue
		}
		ok := true
		for _, filter := range in.Filters {
			if key, isTag := strings.CutPrefix(awssdk.ToString(filter.Name), "tag:"); isTag && !slices.Contains(filter.Values, tagValue(instance.Tags, key)) {
				ok = false
			}
			if awssdk.ToString(filter.Name) == "client-token" && !slices.Contains(filter.Values, awssdk.ToString(instance.ClientToken)) {
				ok = false
			}
		}
		if ok {
			matched = append(matched, instance)
		}
	}
	if len(in.InstanceIds) > 0 && len(matched) == 0 {
		for _, id := range in.InstanceIds {
			if !slices.ContainsFunc(f.instances, func(i ec2types.Instance) bool { return awssdk.ToString(i.InstanceId) == id }) {
				return nil, &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound"}
			}
		}
	}

	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: matched}}}, nil
}

func (f *fakeEC2) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = true
	for _, instance := range f.instances {
		if awssdk.ToString(instance.InstanceId) == in.InstanceIds[0] {
			return &ec2.TerminateInstancesOutput{}, nil
		}
	}

	return nil, &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound"}
}

func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageCall++
	if len(in.ImageIds) > 0 {
		if image, ok := f.images[in.ImageIds[0]]; ok {
			return &ec2.DescribeImagesOutput{Images: []ec2types.Image{image}}, nil
		}

		return nil, &smithy.GenericAPIError{Code: "InvalidAMIID.NotFound"}
	}
	// An alias lookup: two Canonical images, the older listed first. The
	// name filter must spell the architecture as Canonical does (amd64,
	// arm64), or nothing matches, as on EC2.
	arch := ec2types.ArchitectureValuesX8664
	name := ""
	for _, filter := range in.Filters {
		switch awssdk.ToString(filter.Name) {
		case "architecture":
			arch = ec2types.ArchitectureValues(filter.Values[0])
		case "name":
			name = filter.Values[0]
		}
	}
	canonical := map[ec2types.ArchitectureValues]string{ec2types.ArchitectureValuesX8664: "-amd64-server-", ec2types.ArchitectureValuesArm64: "-arm64-server-"}[arch]
	if !strings.Contains(name, canonical) {
		return &ec2.DescribeImagesOutput{}, nil
	}

	return &ec2.DescribeImagesOutput{Images: []ec2types.Image{
		{ImageId: awssdk.String("ami-old"), CreationDate: awssdk.String("2025-01-01T00:00:00.000Z"), RootDeviceName: awssdk.String("/dev/sda1"), Architecture: arch},
		{ImageId: awssdk.String("ami-new-" + string(arch)), CreationDate: awssdk.String("2026-01-01T00:00:00.000Z"), RootDeviceName: awssdk.String("/dev/sda1"), Architecture: arch},
	}}, nil
}

func (f *fakeEC2) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if len(in.SubnetIds) > 0 {
		return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{SubnetId: awssdk.String(in.SubnetIds[0]), VpcId: awssdk.String("vpc-custom"), AvailabilityZone: awssdk.String("us-east-1a")}}}, nil
	}
	if f.noDefault {
		return &ec2.DescribeSubnetsOutput{}, nil
	}
	if f.subnets != nil {
		return &ec2.DescribeSubnetsOutput{Subnets: f.subnets}, nil
	}

	return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{
		{SubnetId: awssdk.String("subnet-b"), VpcId: awssdk.String("vpc-default"), AvailabilityZone: awssdk.String("us-east-1b")},
		{SubnetId: awssdk.String("subnet-a"), VpcId: awssdk.String("vpc-default"), AvailabilityZone: awssdk.String("us-east-1a")},
	}}, nil
}

func (f *fakeEC2) DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.groups > 0 {
		return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: []ec2types.SecurityGroup{{GroupId: awssdk.String("sg-airship")}}}, nil
	}

	return &ec2.DescribeSecurityGroupsOutput{}, nil
}

func (f *fakeEC2) CreateSecurityGroup(_ context.Context, in *ec2.CreateSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if awssdk.ToString(in.GroupName) != SecurityGroupName {
		return nil, errors.New("wrong group name")
	}
	f.groups++

	return &ec2.CreateSecurityGroupOutput{GroupId: awssdk.String("sg-airship")}, nil
}

type fakeSTS struct{ fail bool }

func (f fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.fail {
		return nil, &smithy.GenericAPIError{Code: "InvalidClientTokenId", Message: "The security token included in the request is invalid."}
	}

	return &sts.GetCallerIdentityOutput{Account: awssdk.String("123456789012"), Arn: awssdk.String("arn:aws:iam::123456789012:user/ci")}, nil
}

var testSecret, _ = EncodeSecret(Secret{AccessKeyID: "AKIATEST", SecretAccessKey: "secret"})

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID: "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40", RunnerName: "airship-abc", InstallationID: 42, Repository: "acme/api", Region: "us-east-1",
		Machine: cloud.Machine{InstanceType: "c7i.xlarge", Arch: "x64", Spot: true, Disk: cloud.Disk{SizeGB: 100}},
		Boot:    cloud.Boot{JITConfig: "ZXlKaGJHY2lPaUpJVXpJMU5pSjk=", RunnerVersion: "2.338.2", MaxLifetimeSeconds: 3600},
	}
}

func newProvider(ec2Client *fakeEC2) *Provider {
	return NewWithClients(func(Secret, string) Clients { return Clients{EC2: ec2Client, STS: fakeSTS{}} })
}

func TestLaunchRequest(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{}
	id, err := newProvider(ec2Client).Launch(context.Background(), testSecret, testLaunch())
	if err != nil || id != "i-123" {
		t.Fatalf("launch = %q, %v", id, err)
	}
	in := ec2Client.runs[0]
	if awssdk.ToString(in.ImageId) != "ami-new-x86_64" {
		t.Errorf("image = %s; newest Ubuntu 22.04 not chosen", awssdk.ToString(in.ImageId))
	}
	if in.InstanceInitiatedShutdownBehavior != ec2types.ShutdownBehaviorTerminate {
		t.Error("instance must terminate on shutdown")
	}
	if in.MetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired || awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit) != 1 {
		t.Error("IMDSv2 must be required with hop limit 1")
	}
	if in.InstanceMarketOptions == nil || in.InstanceMarketOptions.MarketType != ec2types.MarketTypeSpot {
		t.Error("spot machine launched on demand")
	}
	if got := in.SecurityGroupIds; len(got) != 1 || got[0] != "sg-airship" || ec2Client.groups != 1 {
		t.Errorf("security groups = %v (created %d)", got, ec2Client.groups)
	}
	if awssdk.ToString(in.SubnetId) != "subnet-a" && awssdk.ToString(in.SubnetId) != "subnet-b" {
		t.Errorf("subnet = %s, want a default subnet", awssdk.ToString(in.SubnetId))
	}
	ebs := in.BlockDeviceMappings[0].Ebs
	if awssdk.ToInt32(ebs.VolumeSize) != 100 || !awssdk.ToBool(ebs.Encrypted) || !awssdk.ToBool(ebs.DeleteOnTermination) {
		t.Errorf("root volume = %+v", ebs)
	}
	if !strings.HasPrefix(awssdk.ToString(in.ClientToken), testLaunch().RunnerID) || len(awssdk.ToString(in.ClientToken)) > 64 {
		t.Errorf("client token %q must derive from the runner and fit EC2's 64 chars", awssdk.ToString(in.ClientToken))
	}
	tags := map[string]string{}
	for _, tag := range in.TagSpecifications[0].Tags {
		tags[awssdk.ToString(tag.Key)] = awssdk.ToString(tag.Value)
	}
	if tags[TagInstallation] != "42" || tags[TagRunner] != testLaunch().RunnerID || tags["Name"] != "airship-abc" {
		t.Errorf("tags = %v", tags)
	}
	script, _ := base64.StdEncoding.DecodeString(awssdk.ToString(in.UserData))
	if !strings.Contains(string(script), "ACTIONS_RUNNER_INPUT_JITCONFIG='"+testLaunch().Boot.JITConfig+"'") {
		t.Error("user data does not pass the jit config through the environment")
	}

	// The second launch reuses the cached image and network.
	calls := ec2Client.imageCall
	if _, err := newProvider(ec2Client).Launch(context.Background(), testSecret, testLaunch()); err != nil {
		t.Fatal(err)
	}
	if ec2Client.imageCall == calls {
		t.Log("separate providers do not share caches, as expected")
	}
}

func TestLaunchOverridesAndWindows(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{images: map[string]ec2types.Image{
		"ami-win": {ImageId: awssdk.String("ami-win"), RootDeviceName: awssdk.String("/dev/sda1"), Platform: ec2types.PlatformValuesWindows, Architecture: ec2types.ArchitectureValuesX8664},
	}}
	launch := testLaunch()
	launch.Machine.Image = "ami-win"
	launch.Machine.Spot = false
	launch.Machine.SubnetID = "subnet-custom"
	launch.Machine.SecurityGroupIDs = []string{"sg-mine"}
	launch.Machine.InstanceProfile = "arn:aws:iam::1:instance-profile/ci"
	launch.Machine.Disk = cloud.Disk{SizeGB: 200, Type: "gp3", IOPS: 6000, Throughput: 1000, InitRate: 300}
	if _, err := newProvider(ec2Client).Launch(context.Background(), testSecret, launch); err != nil {
		t.Fatal(err)
	}
	in := ec2Client.runs[0]
	if awssdk.ToString(in.SubnetId) != "subnet-custom" || in.SecurityGroupIds[0] != "sg-mine" || ec2Client.groups != 0 {
		t.Errorf("network overrides ignored: %s %v", awssdk.ToString(in.SubnetId), in.SecurityGroupIds)
	}
	if in.IamInstanceProfile == nil || awssdk.ToString(in.IamInstanceProfile.Arn) == "" {
		t.Error("instance profile arn not set")
	}
	if in.InstanceMarketOptions != nil {
		t.Error("on-demand machine launched as spot")
	}
	ebs := in.BlockDeviceMappings[0].Ebs
	if awssdk.ToInt32(ebs.Iops) != 6000 || awssdk.ToInt32(ebs.Throughput) != 1000 || awssdk.ToInt32(ebs.VolumeInitializationRate) != 300 {
		t.Errorf("volume tuning = %+v", ebs)
	}
	script, _ := base64.StdEncoding.DecodeString(awssdk.ToString(in.UserData))
	if !strings.HasPrefix(string(script), "<powershell>") {
		t.Errorf("windows image got a bash script: %.40s", script)
	}

	launch.Boot.PullFromAPI = true
	if _, err := newProvider(ec2Client).Launch(context.Background(), testSecret, launch); err != nil {
		t.Fatal(err)
	}
	if ec2Client.runs[1].UserData != nil {
		t.Error("provision_via: pull_from_api must send no user data")
	}
}

func TestLaunchArchMismatchAndMissingImage(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{images: map[string]ec2types.Image{
		"ami-arm": {ImageId: awssdk.String("ami-arm"), RootDeviceName: awssdk.String("/dev/sda1"), Architecture: ec2types.ArchitectureValuesArm64},
	}}
	launch := testLaunch()
	launch.Machine.Image = "ami-arm"
	_, err := newProvider(ec2Client).Launch(context.Background(), testSecret, launch)
	if err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "arm64") {
		t.Fatalf("arch mismatch = %v", err)
	}
	launch.Machine.Image = "ami-nope"
	if _, err := newProvider(ec2Client).Launch(context.Background(), testSecret, launch); err == nil || cloud.IsRetryable(err) {
		t.Fatalf("missing image = %v", err)
	}
	if len(ec2Client.runs) != 0 {
		t.Fatal("launched despite image problems")
	}
}

func TestLaunchWithoutDefaultVPC(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{noDefault: true}
	_, err := newProvider(ec2Client).Launch(context.Background(), testSecret, testLaunch())
	if err == nil || !strings.Contains(err.Error(), "no default VPC") || cloud.IsRetryable(err) {
		t.Fatalf("err = %v", err)
	}
}

func capacityErr() error { return &smithy.GenericAPIError{Code: "InsufficientInstanceCapacity"} }

func TestLaunchTriesAnotherSubnetOnCapacity(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{failures: []error{capacityErr(), nil}}
	if _, err := newProvider(ec2Client).Launch(context.Background(), testSecret, testLaunch()); err != nil {
		t.Fatal(err)
	}
	if len(ec2Client.runs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(ec2Client.runs))
	}
	if awssdk.ToString(ec2Client.runs[0].SubnetId) == awssdk.ToString(ec2Client.runs[1].SubnetId) {
		t.Error("second attempt should use the other subnet")
	}
	if *ec2Client.runs[0].ClientToken == *ec2Client.runs[1].ClientToken {
		t.Error("each different request needs its own client token, or EC2 rejects it as a mismatch")
	}
	if ec2Client.runs[1].InstanceMarketOptions == nil {
		t.Error("spot must stay spot: fallback to on-demand is the config's job (preemptible lists)")
	}
}

func TestLaunchErrorsAreClassified(t *testing.T) {
	t.Parallel()
	for code, retryable := range map[string]bool{"InsufficientInstanceCapacity": true, "RequestLimitExceeded": true, "InvalidAMIID.NotFound": false, "UnauthorizedOperation": false} {
		failures := []error{&smithy.GenericAPIError{Code: code}, &smithy.GenericAPIError{Code: code}}
		_, err := newProvider(&fakeEC2{failures: failures}).Launch(context.Background(), testSecret, testLaunch())
		var launchErr *cloud.LaunchError
		if !errors.As(err, &launchErr) || launchErr.Retryable != retryable {
			t.Errorf("%s: retryable = %v, want %v", code, launchErr != nil && launchErr.Retryable, retryable)
		}
	}
}

// A call that never reached EC2 says nothing about the machine, so it is
// retried rather than moving the job to its next fallback.
// A RunInstances call that gets no answer may have made an instance. The
// client token finds it when it did; when nothing is found the launch is
// ambiguous and retryable, for the inventory scan to settle.
func TestTransportErrorsAreRetryable(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"timeout":   context.DeadlineExceeded,
		"operation": &smithy.OperationError{ServiceID: "EC2", OperationName: "RunInstances", Err: errors.New("dial tcp: i/o timeout")},
	} {
		_, err := newProvider(&fakeEC2{failures: []error{failure, failure}}).Launch(context.Background(), testSecret, testLaunch())
		if err == nil || !cloud.IsRetryable(err) || !cloud.IsAmbiguous(err) {
			t.Errorf("%s: err = %v, want retryable and ambiguous", name, err)
		}
	}

	// The request did reach EC2: the instance carrying the call's client
	// token is the launch's answer, and no second RunInstances is made.
	launch := testLaunch()
	client := &fakeEC2{
		failures: []error{context.DeadlineExceeded},
		instances: []ec2types.Instance{
			{InstanceId: awssdk.String("i-other"), ClientToken: awssdk.String("someone-else"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			{InstanceId: awssdk.String("i-mine"), ClientToken: awssdk.String(fmt.Sprintf("%s-0-%t", launch.RunnerID, launch.Machine.Spot)), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
		},
	}
	id, err := newProvider(client).Launch(context.Background(), testSecret, launch)
	if err != nil || id != "i-mine" {
		t.Fatalf("launch after a lost answer = %q, %v; want the instance found by client token", id, err)
	}
	if len(client.runs) != 1 {
		t.Fatalf("RunInstances called %d times, want 1", len(client.runs))
	}
}

func TestListFiltersByInstallationAndMarksShuttingDown(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{instances: []ec2types.Instance{
		{InstanceId: awssdk.String("i-1"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, Tags: []ec2types.Tag{{Key: awssdk.String(TagRunner), Value: awssdk.String("r1")}, {Key: awssdk.String(TagInstallation), Value: awssdk.String("42")}}},
		{InstanceId: awssdk.String("i-2"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameShuttingDown}, Tags: []ec2types.Tag{{Key: awssdk.String(TagInstallation), Value: awssdk.String("42")}}},
		{InstanceId: awssdk.String("i-3"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, Tags: []ec2types.Tag{{Key: awssdk.String(TagInstallation), Value: awssdk.String("7")}}},
	}}
	list, err := newProvider(ec2Client).List(context.Background(), testSecret, "us-east-1", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].RunnerID != "r1" || list[0].Stopping || !list[1].Stopping {
		t.Fatalf("list = %+v", list)
	}
	if v := ec2Client.listed.Filters[0].Values; awssdk.ToString(ec2Client.listed.Filters[0].Name) != "tag:"+TagInstallation || v[0] != "42" {
		t.Fatal("listing is not scoped to the installation's tag")
	}
}

func TestTerminateIgnoresMissingInstances(t *testing.T) {
	t.Parallel()
	if err := newProvider(&fakeEC2{}).Terminate(context.Background(), testSecret, "us-east-1", "i-gone", 42); err != nil {
		t.Fatal(err)
	}
}

// Terminate describes the instance filtered by the installation's tag and
// refuses when the filter matches nothing: the instance is not ours.
func TestTerminateChecksOwnership(t *testing.T) {
	t.Parallel()
	owned := []ec2types.Instance{{InstanceId: awssdk.String("i-1"), Tags: []ec2types.Tag{{Key: awssdk.String(TagInstallation), Value: awssdk.String("42")}}}}
	ec2Client := &fakeEC2{instances: owned}
	if err := newProvider(ec2Client).Terminate(context.Background(), testSecret, "us-east-1", "i-1", 42); err != nil {
		t.Fatal(err)
	}
	filter := ec2Client.listed.Filters[0]
	if awssdk.ToString(filter.Name) != "tag:"+TagInstallation || filter.Values[0] != "42" || ec2Client.listed.InstanceIds[0] != "i-1" {
		t.Fatalf("describe was not scoped to the installation: %+v", ec2Client.listed)
	}
	if !ec2Client.terminated {
		t.Fatal("owned instance was not terminated")
	}
	other := &fakeEC2{instances: owned}
	if err := newProvider(other).Terminate(context.Background(), testSecret, "us-east-1", "i-1", 7); err == nil || other.terminated {
		t.Fatalf("an instance outside the installation's tag was terminated: err = %v", err)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	identity, err := newProvider(&fakeEC2{}).Verify(context.Background(), testSecret, "us-east-1")
	if err != nil || identity.AccountID != "123456789012" {
		t.Fatalf("verify = %+v, %v", identity, err)
	}
	bad := NewWithClients(func(Secret, string) Clients { return Clients{STS: fakeSTS{fail: true}} })
	if _, err := bad.Verify(context.Background(), testSecret, "us-east-1"); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Fatalf("bad credentials = %v", err)
	}
	if _, err := bad.Verify(context.Background(), []byte(`{"accessKeyId":""}`), "us-east-1"); err == nil {
		t.Fatal("incomplete secret accepted")
	}
}

// A repeated launch of one runner must send an identical request, so EC2's
// client token returns the first instance instead of rejecting the call.
func TestRepeatedLaunchSendsTheSameRequest(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2{subnets: []ec2types.Subnet{
		{SubnetId: awssdk.String("subnet-a"), VpcId: awssdk.String("vpc-default"), AvailabilityZone: awssdk.String("a")},
		{SubnetId: awssdk.String("subnet-b"), VpcId: awssdk.String("vpc-default"), AvailabilityZone: awssdk.String("b")},
		{SubnetId: awssdk.String("subnet-c"), VpcId: awssdk.String("vpc-default"), AvailabilityZone: awssdk.String("c")},
	}}
	p := newProvider(fake)
	for range 3 {
		if _, err := p.Launch(context.Background(), testSecret, testLaunch()); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range fake.runs[1:] {
		if *run.ClientToken != *fake.runs[0].ClientToken || *run.SubnetId != *fake.runs[0].SubnetId {
			t.Fatalf("repeat launch sent token %s in %s, first sent %s in %s",
				*run.ClientToken, *run.SubnetId, *fake.runs[0].ClientToken, *fake.runs[0].SubnetId)
		}
	}

	other := testLaunch()
	subnets := map[string]bool{}
	for i := range 30 {
		other.RunnerID = fmt.Sprintf("0199d1a8-0000-7000-8000-%012d", i)
		if _, err := p.Launch(context.Background(), testSecret, other); err != nil {
			t.Fatal(err)
		}
		subnets[*fake.runs[len(fake.runs)-1].SubnetId] = true
	}
	if len(subnets) != 3 {
		t.Fatalf("30 runners landed in %d of 3 subnets", len(subnets))
	}
}

func TestRootVolumeTuning(t *testing.T) {
	t.Parallel()
	ebs := rootVolume(cloud.Disk{SizeGB: 200, Type: "io2", IOPS: 8000})
	if ebs.VolumeType != ec2types.VolumeTypeIo2 || awssdk.ToInt32(ebs.Iops) != 8000 || ebs.Throughput != nil || !awssdk.ToBool(ebs.Encrypted) {
		t.Fatalf("io2 volume = %+v", ebs)
	}
	ebs = rootVolume(cloud.Disk{SizeGB: 50, Throughput: 500})
	if ebs.VolumeType != ec2types.VolumeTypeGp3 || awssdk.ToInt32(ebs.Throughput) != 500 || ebs.Iops != nil {
		t.Fatalf("gp3 volume = %+v", ebs)
	}
}
