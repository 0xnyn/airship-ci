// Package aws launches each runner as its own EC2 instance in the
// customer's account. One VM per job is the isolation boundary: nothing a
// job does survives it.
package aws

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

const (
	// TagDataPlane and TagRunner mark every instance and volume the agent
	// launches. The IAM policy in deploy/aws only lets the agent terminate
	// instances carrying its own data plane tag.
	TagDataPlane = "airship:data-plane"
	TagRunner    = "airship:runner-id"

	ssmPrefix     = "resolve:ssm:"
	imageCacheTTL = time.Hour
)

// EC2 is the subset of the EC2 API the provider uses.
type EC2 interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// SSM resolves public AMI parameters such as Canonical's Ubuntu images.
type SSM interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Config is the customer-owned infrastructure the agent launches into. It
// lives with the agent, not in the control plane: Airship never needs to
// know the customer's network layout.
type Config struct {
	// Subnets are tried in turn; spreading them over AZs survives a
	// capacity shortage in one zone.
	Subnets []string
	// SecurityGroups for runner instances. Runners need egress only.
	SecurityGroups []string
	// InstanceProfile optionally gives runners an IAM role. Leave it empty
	// unless jobs need AWS access; anything a job can reach, every job can.
	InstanceProfile string
	// DefaultImages by arch, used when a profile names no image. AMI IDs or
	// "resolve:ssm:<parameter>".
	DefaultImages map[string]string
	// SpotFallback launches on-demand when spot capacity is unavailable.
	SpotFallback bool
	// MetadataHopLimit is the IMDSv2 hop limit. 1 keeps containers in a job
	// from reaching instance metadata; 2 allows it.
	MetadataHopLimit int32
}

// DefaultImages are Canonical's current Ubuntu 24.04 AMIs.
var DefaultImages = map[string]string{
	"x64":   ssmPrefix + "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id",
	"arm64": ssmPrefix + "/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id",
}

// Provider is the EC2 provider.
type Provider struct {
	ec2 EC2
	ssm SSM
	cfg Config

	mu     sync.Mutex
	images map[string]resolvedImage
}

type resolvedImage struct {
	id         string
	rootDevice string
	at         time.Time
}

// New returns an EC2 provider.
func New(ec2Client EC2, ssmClient SSM, cfg Config) (*Provider, error) {
	if len(cfg.Subnets) == 0 {
		return nil, errors.New("aws provider: at least one subnet is required")
	}
	if cfg.DefaultImages == nil {
		cfg.DefaultImages = DefaultImages
	}
	if cfg.MetadataHopLimit == 0 {
		cfg.MetadataHopLimit = 1
	}

	return &Provider{ec2: ec2Client, ssm: ssmClient, cfg: cfg, images: map[string]resolvedImage{}}, nil
}

// Name implements agent.Provider.
func (*Provider) Name() string { return "aws" }

// List implements agent.Provider.
func (p *Provider) List(ctx context.Context, dataPlaneID string) ([]agent.Instance, error) {
	in := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:" + TagDataPlane), Values: []string{dataPlaneID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"pending", "running", "shutting-down", "stopping", "stopped"}},
		},
		MaxResults: awssdk.Int32(1000),
	}
	var out []agent.Instance
	paginator := ec2.NewDescribeInstancesPaginator(p.ec2, in)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe instances: %w", err)
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				out = append(out, agent.Instance{
					RunnerID:   tagValue(instance.Tags, TagRunner),
					InstanceID: awssdk.ToString(instance.InstanceId),
					Stopping:   instance.State != nil && instance.State.Name == ec2types.InstanceStateNameShuttingDown,
				})
			}
		}
	}

	return out, nil
}

// Launch implements agent.Provider.
func (p *Provider) Launch(ctx context.Context, dataPlaneID string, launch agentproto.Launch) (string, error) {
	script, err := userData(launch)
	if err != nil {
		return "", &agent.LaunchError{Err: err}
	}
	imageRef := launch.Profile.Image
	if imageRef == "" {
		imageRef = p.cfg.DefaultImages[launch.Profile.Arch]
	}
	image, err := p.resolveImage(ctx, imageRef)
	if err != nil {
		return "", classify(err)
	}

	spot := launch.Profile.Spot
	// Spread launches across zones, but deterministically per runner: a
	// repeated launch must send the same request with the same client token,
	// or EC2 rejects it as IdempotentParameterMismatch instead of returning
	// the instance the first call made.
	start := firstSubnet(launch.RunnerID)
	subnets := uint64(len(p.cfg.Subnets))
	var lastErr error
	for attempt := range len(p.cfg.Subnets) {
		subnet := p.cfg.Subnets[(start+uint64(attempt))%subnets]
		id, err := p.run(ctx, dataPlaneID, launch, image, subnet, script, spot, attempt)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if isSpotUnavailable(err) && spot && p.cfg.SpotFallback {
			spot = false
			if id, err := p.run(ctx, dataPlaneID, launch, image, subnet, script, false, attempt); err == nil {
				return id, nil
			} else {
				lastErr = err
			}
		}
		if !isCapacityError(lastErr) {
			break
		}
	}

	return "", classify(lastErr)
}

func (p *Provider) run(ctx context.Context, dataPlaneID string, launch agentproto.Launch, image resolvedImage, subnet, script string, spot bool, attempt int) (string, error) {
	tags := []ec2types.Tag{
		{Key: awssdk.String(TagDataPlane), Value: awssdk.String(dataPlaneID)},
		{Key: awssdk.String(TagRunner), Value: awssdk.String(launch.RunnerID)},
		{Key: awssdk.String("Name"), Value: awssdk.String(launch.RunnerName)},
		{Key: awssdk.String("airship:profile"), Value: awssdk.String(launch.Profile.Label)},
	}
	in := &ec2.RunInstancesInput{
		// One token per runner, subnet attempt and market: a retried call
		// returns the instance the first call made instead of a second one.
		ClientToken:  awssdk.String(fmt.Sprintf("%s-%d-%t", launch.RunnerID, attempt, spot)),
		ImageId:      awssdk.String(image.id),
		InstanceType: ec2types.InstanceType(launch.Profile.InstanceType),
		MinCount:     awssdk.Int32(1),
		MaxCount:     awssdk.Int32(1),
		SubnetId:     awssdk.String(subnet),
		UserData:     awssdk.String(script),
		// Every way the runner exits ends with the instance deleted.
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
		MetadataOptions: &ec2types.InstanceMetadataOptionsRequest{
			HttpTokens:              ec2types.HttpTokensStateRequired,
			HttpEndpoint:            ec2types.InstanceMetadataEndpointStateEnabled,
			HttpPutResponseHopLimit: awssdk.Int32(p.cfg.MetadataHopLimit),
		},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{{
			DeviceName: awssdk.String(image.rootDevice),
			Ebs:        rootVolume(launch.Profile),
		}},
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		},
	}
	if len(p.cfg.SecurityGroups) > 0 {
		in.SecurityGroupIds = p.cfg.SecurityGroups
	}
	if p.cfg.InstanceProfile != "" {
		in.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{Name: awssdk.String(p.cfg.InstanceProfile)}
	}
	if spot {
		in.InstanceMarketOptions = &ec2types.InstanceMarketOptionsRequest{
			MarketType: ec2types.MarketTypeSpot,
			SpotOptions: &ec2types.SpotMarketOptions{
				SpotInstanceType:             ec2types.SpotInstanceTypeOneTime,
				InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehaviorTerminate,
			},
		}
	}

	out, err := p.ec2.RunInstances(ctx, in)
	if err != nil {
		return "", fmt.Errorf("run instances in %s: %w", subnet, err)
	}
	if len(out.Instances) != 1 {
		return "", fmt.Errorf("run instances returned %d instances", len(out.Instances))
	}

	return awssdk.ToString(out.Instances[0].InstanceId), nil
}

// rootVolume is the encrypted root EBS volume for a profile, gp3 unless
// the profile asks for another type.
func rootVolume(profile agentproto.LaunchProfile) *ec2types.EbsBlockDevice {
	volume := &ec2types.EbsBlockDevice{
		VolumeSize:          awssdk.Int32(int32(profile.DiskGB)), //nolint:gosec // bounded by the API
		VolumeType:          ec2types.VolumeTypeGp3,
		DeleteOnTermination: awssdk.Bool(true),
		Encrypted:           awssdk.Bool(true),
	}
	if profile.DiskType != "" {
		volume.VolumeType = ec2types.VolumeType(profile.DiskType)
	}
	if profile.DiskIOPS > 0 {
		volume.Iops = awssdk.Int32(int32(profile.DiskIOPS)) //nolint:gosec // validated by the control plane
	}
	if profile.DiskThroughput > 0 {
		volume.Throughput = awssdk.Int32(int32(profile.DiskThroughput)) //nolint:gosec // validated by the control plane
	}

	return volume
}

func firstSubnet(runnerID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runnerID))

	return h.Sum64()
}

// Terminate implements agent.Provider.
func (p *Provider) Terminate(ctx context.Context, instanceID string) error {
	_, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if errorCode(err) == "InvalidInstanceID.NotFound" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("terminate %s: %w", instanceID, err)
	}

	return nil
}

// resolveImage turns an image reference into an AMI ID and its root
// device name, which the disk size override needs.
func (p *Provider) resolveImage(ctx context.Context, ref string) (resolvedImage, error) {
	p.mu.Lock()
	cached, ok := p.images[ref]
	p.mu.Unlock()
	if ok && time.Since(cached.at) < imageCacheTTL {
		return cached, nil
	}

	id := ref
	if name, isSSM := strings.CutPrefix(ref, ssmPrefix); isSSM {
		out, err := p.ssm.GetParameter(ctx, &ssm.GetParameterInput{Name: awssdk.String(name)})
		if err != nil {
			return resolvedImage{}, fmt.Errorf("resolve image parameter %s: %w", name, err)
		}
		id = awssdk.ToString(out.Parameter.Value)
	}
	out, err := p.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{id}})
	if err != nil {
		return resolvedImage{}, fmt.Errorf("describe image %s: %w", id, err)
	}
	if len(out.Images) != 1 || out.Images[0].RootDeviceName == nil {
		return resolvedImage{}, fmt.Errorf("image %s not found", id)
	}
	image := resolvedImage{id: id, rootDevice: awssdk.ToString(out.Images[0].RootDeviceName), at: time.Now()}

	p.mu.Lock()
	p.images[ref] = image
	p.mu.Unlock()

	return image, nil
}

func tagValue(tags []ec2types.Tag, key string) string {
	for _, tag := range tags {
		if awssdk.ToString(tag.Key) == key {
			return awssdk.ToString(tag.Value)
		}
	}

	return ""
}

func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

// isCapacityError reports errors another subnet (AZ) may not have.
func isCapacityError(err error) bool {
	switch errorCode(err) {
	case "InsufficientInstanceCapacity", "Unsupported", "InsufficientFreeAddressesInSubnet", "SpotMaxPriceTooLow":
		return true
	}

	return false
}

func isSpotUnavailable(err error) bool {
	switch errorCode(err) {
	case "InsufficientInstanceCapacity", "SpotMaxPriceTooLow", "MaxSpotInstanceCountExceeded":
		return true
	}

	return false
}

// classify marks errors worth retrying with a fresh runner.
func classify(err error) error {
	switch errorCode(err) {
	case "InsufficientInstanceCapacity", "InstanceLimitExceeded", "RequestLimitExceeded", "Unavailable",
		"InternalError", "ServiceUnavailable", "Throttling", "MaxSpotInstanceCountExceeded",
		"SpotMaxPriceTooLow", "InsufficientFreeAddressesInSubnet", "VcpuLimitExceeded":
		return &agent.LaunchError{Err: err, Retryable: true}
	}

	return &agent.LaunchError{Err: err}
}
