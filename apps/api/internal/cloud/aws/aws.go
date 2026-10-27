// Package aws launches each runner as its own EC2 instance in the
// customer's account, with the access key they connected in the dashboard.
// One VM per job is the isolation boundary: nothing a job does survives it.
package aws

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

const (
	// TagInstallation, TagRunner and TagRepository mark every instance and
	// volume the control plane launches. Listing and terminating are scoped
	// to TagInstallation, so one customer's runners never touch another's
	// even if two installations share an AWS account.
	TagInstallation = "airship:installation"
	TagRunner       = "airship:runner-id"
	TagRepository   = "airship:repository"

	// SecurityGroupName is the egress-only group created in the default VPC
	// when a runner definition names none.
	SecurityGroupName = "airship-runners"

	// EC2 rejects user data over 16 KiB (before base64).
	maxUserDataBytes = 16 << 10
	cacheTTL         = 10 * time.Minute
	imageCacheTTL    = time.Hour
	cacheLimit       = 10000
	requestTimeout   = 30 * time.Second
	maxRetries       = 3
)

// Secret is the credential a customer connects: an IAM user's access key.
type Secret struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// EncodeSecret serializes a secret for sealing.
func EncodeSecret(s Secret) ([]byte, error) {
	if s.AccessKeyID == "" || s.SecretAccessKey == "" {
		return nil, errors.New("access key ID and secret access key are required")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode secret: %w", err)
	}

	return raw, nil
}

// ParseSecret reads a secret from EncodeSecret.
func ParseSecret(raw []byte) (Secret, error) {
	var s Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secret{}, fmt.Errorf("decode aws secret: %w", err)
	}
	if s.AccessKeyID == "" || s.SecretAccessKey == "" {
		return Secret{}, errors.New("aws secret is incomplete")
	}

	return s, nil
}

// EC2 is the subset of the EC2 API the provider uses.
type EC2 interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(ctx context.Context, in *ec2.DescribeSecurityGroupsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	CreateSecurityGroup(ctx context.Context, in *ec2.CreateSecurityGroupInput, opts ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
}

// STS verifies credentials.
type STS interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Clients are the service clients for one credential in one region. S3
// and Federation are only needed by the cache.
type Clients struct {
	EC2        EC2
	STS        STS
	S3         S3
	Federation Federation
}

// Options configure the provider.
type Options struct {
	// Endpoint points every service at an emulator such as moto.
	Endpoint string
	// HTTPClient defaults to a client with a 30 second timeout.
	HTTPClient awssdk.HTTPClient
}

// Provider is the EC2 provider.
type Provider struct {
	newClients func(secret Secret, region string) Clients

	mu       sync.Mutex
	images   map[string]cached[resolvedImage]
	networks map[string]cached[network]
}

type cached[T any] struct {
	value T
	at    time.Time
}

// New returns a provider that talks to AWS.
func New(opts Options) *Provider {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = awshttp.NewBuildableClient().WithTimeout(requestTimeout)
	}

	return NewWithClients(func(secret Secret, region string) Clients {
		cfg := awssdk.Config{
			Region:      region,
			Credentials: credentials.NewStaticCredentialsProvider(secret.AccessKeyID, secret.SecretAccessKey, ""),
			HTTPClient:  httpClient,
			Retryer: func() awssdk.Retryer {
				return retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = maxRetries })
			},
		}
		endpoint := func(o *ec2.Options) {
			if opts.Endpoint != "" {
				o.BaseEndpoint = &opts.Endpoint
			}
		}
		stsEndpoint := func(o *sts.Options) {
			if opts.Endpoint != "" {
				o.BaseEndpoint = &opts.Endpoint
			}
		}

		s3Endpoint := func(o *s3.Options) {
			if opts.Endpoint != "" {
				o.BaseEndpoint = &opts.Endpoint
				o.UsePathStyle = true
			}
		}
		stsClient := sts.NewFromConfig(cfg, stsEndpoint)

		return Clients{EC2: ec2.NewFromConfig(cfg, endpoint), STS: stsClient, S3: s3.NewFromConfig(cfg, s3Endpoint), Federation: stsClient}
	})
}

// NewWithClients returns a provider that builds clients with fn; tests
// pass fakes.
func NewWithClients(fn func(secret Secret, region string) Clients) *Provider {
	return &Provider{newClients: fn, images: map[string]cached[resolvedImage]{}, networks: map[string]cached[network]{}}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.AWS }

// Verify implements cloud.Provider.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return cloud.Identity{}, err
	}
	out, err := p.newClients(secret, region).STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if isAuthError(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, errorMessage(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}

	return cloud.Identity{AccountID: awssdk.ToString(out.Account), Principal: awssdk.ToString(out.Arn)}, nil
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, err
	}
	client := p.newClients(secret, region).EC2
	in := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:" + TagInstallation), Values: []string{strconv.FormatInt(installationID, 10)}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"pending", "running", "shutting-down", "stopping", "stopped"}},
		},
		MaxResults: awssdk.Int32(1000),
	}
	out := []cloud.Instance{}
	paginator := ec2.NewDescribeInstancesPaginator(client, in)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe instances in %s: %w", region, err)
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				state := ec2types.InstanceStateNamePending
				if instance.State != nil {
					state = instance.State.Name
				}
				out = append(out, cloud.Instance{
					RunnerID:   tagValue(instance.Tags, TagRunner),
					InstanceID: awssdk.ToString(instance.InstanceId),
					Stopping:   state == ec2types.InstanceStateNameShuttingDown,
					Stopped:    state == ec2types.InstanceStateNameStopping || state == ec2types.InstanceStateNameStopped,
				})
			}
		}
	}

	return out, nil
}

// Terminate implements cloud.Provider. The instance must carry the
// installation's tag: an ID that was recorded wrongly, or reused by EC2
// for another account's machine, is refused rather than destroyed.
func (p *Provider) Terminate(ctx context.Context, raw []byte, region, instanceID string, installationID int64) error {
	secret, err := ParseSecret(raw)
	if err != nil {
		return err
	}
	client := p.newClients(secret, region).EC2
	described, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
		Filters:     []ec2types.Filter{{Name: awssdk.String("tag:" + TagInstallation), Values: []string{strconv.FormatInt(installationID, 10)}}},
	})
	if errorCode(err) == "InvalidInstanceID.NotFound" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("describe %s in %s: %w", instanceID, region, err)
	}
	if countInstances(described) == 0 {
		return fmt.Errorf("instance %s in %s is not tagged for installation %d; not terminating it", instanceID, region, installationID)
	}
	_, err = client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if errorCode(err) == "InvalidInstanceID.NotFound" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("terminate %s in %s: %w", instanceID, region, err)
	}

	return nil
}

func countInstances(out *ec2.DescribeInstancesOutput) int {
	n := 0
	for _, reservation := range out.Reservations {
		n += len(reservation.Instances)
	}

	return n
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	client := p.newClients(secret, launch.Region).EC2
	scope := secret.AccessKeyID + "@" + launch.Region

	image, err := p.resolveImage(ctx, client, scope, launch.Machine)
	if err != nil {
		return "", classify(err)
	}
	net, err := p.network(ctx, client, scope, launch.Machine)
	if err != nil {
		return "", classify(err)
	}
	userData, err := p.userData(launch, image)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	// Spread launches across zones, but deterministically per runner: a
	// repeated launch must send the same request with the same client token,
	// or EC2 rejects it as IdempotentParameterMismatch instead of returning
	// the instance the first call made.
	start := firstSubnet(launch.RunnerID)
	subnets := uint64(len(net.subnets))
	var lastErr error
	for attempt := range len(net.subnets) {
		subnet := net.subnets[(start+uint64(attempt))%subnets] //nolint:gosec // attempt < len(subnets)
		id, err := p.run(ctx, client, launch, image, net, subnet, userData, attempt)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if !isCapacityError(err) {
			break
		}
	}

	return "", classify(lastErr)
}

func (p *Provider) run(ctx context.Context, client EC2, launch cloud.Launch, image resolvedImage, net network, subnet, userData string, attempt int) (string, error) {
	m := launch.Machine
	tags := []ec2types.Tag{
		{Key: awssdk.String(TagInstallation), Value: awssdk.String(strconv.FormatInt(launch.InstallationID, 10))},
		{Key: awssdk.String(TagRunner), Value: awssdk.String(launch.RunnerID)},
		{Key: awssdk.String(TagRepository), Value: awssdk.String(launch.Repository)},
		{Key: awssdk.String("Name"), Value: awssdk.String(launch.RunnerName)},
	}
	in := &ec2.RunInstancesInput{
		// One token per runner, subnet attempt and market: a retried call
		// returns the instance the first call made instead of a second one.
		ClientToken:  awssdk.String(fmt.Sprintf("%s-%d-%t", launch.RunnerID, attempt, m.Spot)),
		ImageId:      awssdk.String(image.id),
		InstanceType: ec2types.InstanceType(m.InstanceType),
		MinCount:     awssdk.Int32(1),
		MaxCount:     awssdk.Int32(1),
		SubnetId:     awssdk.String(subnet),
		// Every way the runner exits ends with the instance deleted.
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
		MetadataOptions: &ec2types.InstanceMetadataOptionsRequest{
			HttpTokens:              ec2types.HttpTokensStateRequired,
			HttpEndpoint:            ec2types.InstanceMetadataEndpointStateEnabled,
			HttpPutResponseHopLimit: awssdk.Int32(1),
		},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{{
			DeviceName: awssdk.String(image.rootDevice),
			Ebs:        rootVolume(m.Disk),
		}},
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		},
	}
	if userData != "" {
		in.UserData = awssdk.String(userData)
	}
	if len(net.securityGroups) > 0 {
		in.SecurityGroupIds = net.securityGroups
	}
	if m.InstanceProfile != "" {
		if strings.HasPrefix(m.InstanceProfile, "arn:") {
			in.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{Arn: awssdk.String(m.InstanceProfile)}
		} else {
			in.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{Name: awssdk.String(m.InstanceProfile)}
		}
	}
	if m.Spot {
		in.InstanceMarketOptions = &ec2types.InstanceMarketOptionsRequest{
			MarketType: ec2types.MarketTypeSpot,
			SpotOptions: &ec2types.SpotMarketOptions{
				SpotInstanceType:             ec2types.SpotInstanceTypeOneTime,
				InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehaviorTerminate,
			},
		}
	}

	out, err := client.RunInstances(ctx, in)
	if err != nil {
		if isTransportError(err) {
			// The request may have reached EC2. The client token finds the
			// instance it made, if any; if even that cannot be learned the
			// launch stays ambiguous and the inventory scan settles it.
			if id, found := p.findByClientToken(ctx, client, awssdk.ToString(in.ClientToken)); found {
				return id, nil
			}

			return "", &cloud.LaunchError{Err: fmt.Errorf("run %s in %s: %w", m.InstanceType, subnet, err), Retryable: true, Ambiguous: true}
		}

		return "", fmt.Errorf("run %s in %s: %w", m.InstanceType, subnet, err)
	}
	if len(out.Instances) != 1 {
		return "", fmt.Errorf("run instances returned %d instances", len(out.Instances))
	}

	return awssdk.ToString(out.Instances[0].InstanceId), nil
}

// findByClientToken looks for the instance a RunInstances call with token
// created. found is false when there is none or EC2 could not be asked.
func (p *Provider) findByClientToken(ctx context.Context, client EC2, token string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("client-token"), Values: []string{token}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"pending", "running", "shutting-down", "stopping", "stopped"}},
		},
	})
	if err != nil {
		return "", false
	}
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			return awssdk.ToString(instance.InstanceId), true
		}
	}

	return "", false
}

// userData renders the boot script for the image's platform, base64
// encoded for EC2. provision_via: pull_from_api sends none.
func (p *Provider) userData(launch cloud.Launch, image resolvedImage) (string, error) {
	if launch.Boot.PullFromAPI {
		return "", nil
	}
	var script string
	var err error
	if image.windows {
		password := ""
		if launch.Boot.Windows.Interactive {
			password, err = randomPassword()
			if err != nil {
				return "", err
			}
		}
		script, err = bootscript.Windows(launch.Boot, launch.Machine.Arch, password)
	} else {
		script, err = bootscript.Linux(launch.Boot, launch.Machine.Arch)
	}
	if err != nil {
		return "", err
	}
	if len(script) > maxUserDataBytes {
		return "", fmt.Errorf("user data is %d bytes, over EC2's %d byte limit", len(script), maxUserDataBytes)
	}

	return base64.StdEncoding.EncodeToString([]byte(script)), nil
}

// randomPassword returns 24 alphanumeric characters for a Windows account.
func randomPassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 24)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("random password: %w", err)
		}
		out[i] = alphabet[n.Int64()]
	}

	return string(out), nil
}

// rootVolume is the encrypted root EBS volume, gp3 unless the definition
// asks for another type.
func rootVolume(disk cloud.Disk) *ec2types.EbsBlockDevice {
	volume := &ec2types.EbsBlockDevice{
		VolumeSize:          awssdk.Int32(int32(disk.SizeGB)), //nolint:gosec // validated by runnerconfig
		VolumeType:          ec2types.VolumeTypeGp3,
		DeleteOnTermination: awssdk.Bool(true),
		Encrypted:           awssdk.Bool(true),
	}
	if disk.Type != "" {
		volume.VolumeType = ec2types.VolumeType(disk.Type)
	}
	if disk.IOPS > 0 {
		volume.Iops = awssdk.Int32(int32(disk.IOPS)) //nolint:gosec // validated by runnerconfig
	}
	if disk.Throughput > 0 {
		volume.Throughput = awssdk.Int32(int32(disk.Throughput)) //nolint:gosec // validated by runnerconfig
	}
	if disk.InitRate > 0 {
		volume.VolumeInitializationRate = awssdk.Int32(int32(disk.InitRate)) //nolint:gosec // validated by runnerconfig
	}

	return volume
}

func firstSubnet(runnerID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runnerID))

	return h.Sum64()
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

func errorMessage(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() + ": " + apiErr.ErrorMessage()
	}

	return err.Error()
}

func isAuthError(err error) bool {
	switch errorCode(err) {
	case "InvalidClientTokenId", "SignatureDoesNotMatch", "AuthFailure", "UnrecognizedClientException", "InvalidAccessKeyId", "ExpiredToken":
		return true
	}

	return false
}

// isCapacityError reports errors another subnet (AZ) may not have.
func isCapacityError(err error) bool {
	switch errorCode(err) {
	case "InsufficientInstanceCapacity", "Unsupported", "InsufficientFreeAddressesInSubnet", "SpotMaxPriceTooLow":
		return true
	}

	return false
}

// classify marks errors worth retrying with a fresh runner on the same
// machine shape: capacity, limits, throttling, and failures to reach EC2
// at all (a timeout or a connection error says nothing about the
// machine). Everything EC2 itself refused moves the job on to its next
// fallback.
func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}
	if isTransportError(err) {
		return &cloud.LaunchError{Err: err, Retryable: true}
	}
	switch errorCode(err) {
	case "InsufficientInstanceCapacity", "InstanceLimitExceeded", "RequestLimitExceeded", "Unavailable",
		"InternalError", "InternalFailure", "ServiceUnavailable", "Throttling", "ThrottlingException",
		"RequestExpired", "RequestTimeout", "MaxSpotInstanceCountExceeded",
		"SpotMaxPriceTooLow", "InsufficientFreeAddressesInSubnet", "VcpuLimitExceeded":
		return &cloud.LaunchError{Err: err, Retryable: true}
	}

	return &cloud.LaunchError{Err: err}
}

// isTransportError reports an SDK call that never got an answer from
// EC2: a timeout, a connection failure, or a context that ended. The
// provider's own checks (an image that does not exist, no default VPC)
// are not SDK calls and stay permanent.
func isTransportError(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return false
	}
	var opErr *smithy.OperationError
	var netErr net.Error

	return errors.As(err, &opErr) || errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (p *Provider) cacheGetImage(key string) (resolvedImage, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.images[key]
	if !ok || time.Since(entry.at) > imageCacheTTL {
		return resolvedImage{}, false
	}

	return entry.value, true
}

func (p *Provider) cachePutImage(key string, value resolvedImage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.images) >= cacheLimit {
		clear(p.images)
	}
	p.images[key] = cached[resolvedImage]{value: value, at: time.Now()}
}

func (p *Provider) cacheGetNetwork(key string) (network, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.networks[key]
	if !ok || time.Since(entry.at) > cacheTTL {
		return network{}, false
	}

	return entry.value, true
}

func (p *Provider) cachePutNetwork(key string, value network) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.networks) >= cacheLimit {
		clear(p.networks)
	}
	p.networks[key] = cached[network]{value: value, at: time.Now()}
}
