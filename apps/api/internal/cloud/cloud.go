// Package cloud is the control plane's view of a cloud provider: launch a
// runner machine with the customer's credentials, list the machines it
// launched, and terminate them.
//
// Credentials are opaque bytes here; each provider defines its own secret
// format and parses it. The control plane decrypts the secret, hands it to
// the provider for one call, and never logs it.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Clouds the control plane can launch on, by their name in config files.
const (
	AWS     = "aws"
	GCP     = "gcp"
	Daytona = "daytona"
)

// Identity is who a credential authenticates as, as the cloud reports it.
type Identity struct {
	// AccountID is the cloud account, such as an AWS account ID.
	AccountID string
	// Principal names the user or role, such as an IAM ARN.
	Principal string
}

// Machine is the shape of a runner machine.
type Machine struct {
	InstanceType string
	// Image is a provider image reference (an AMI ID, a Compute Engine
	// image family or image path), an alias such as ubuntu2204, or empty
	// for the provider's default Ubuntu.
	Image string
	// Arch is x64 or arm64, inferred from the instance type.
	Arch string
	Spot bool
	Disk Disk
	// Zone pins the machine to one zone of the region. Empty lets the
	// provider spread launches over the region's zones and move on when
	// one is out of capacity.
	Zone string
	// GPUType is the accelerator a definition asked for (gpu: in the
	// config), attached on clouds where a GPU is a separate resource.
	GPUType string
	// SubnetID, SecurityGroupIDs and InstanceProfile override the AWS
	// provider's defaults (the default VPC, an egress-only group, no role).
	SubnetID         string
	SecurityGroupIDs []string
	InstanceProfile  string
	// GCP holds the Compute Engine settings from extra_config.
	GCP *GCPOptions
	// Daytona holds the sandbox settings for the Daytona provider.
	Daytona *DaytonaOptions
}

// DaytonaOptions shape a Daytona sandbox. A sandbox is either built from a
// container image with the resources below, or started from a snapshot the
// customer made (which fixes the resources itself).
type DaytonaOptions struct {
	// Snapshot names a Daytona snapshot to start from. Empty means build
	// from Machine.Image with CPU, MemoryGB and Disk.SizeGB.
	Snapshot string `json:"snapshot,omitempty"`
	// CPU is the number of vCPUs; MemoryGB the memory in GB.
	CPU      int `json:"cpu,omitempty"`
	MemoryGB int `json:"memoryGb,omitempty"`
	// Docker installs and starts a Docker daemon in the sandbox before the
	// runner, for jobs that use containers.
	Docker bool `json:"docker,omitempty"`
	// Volumes are Daytona volumes mounted into the sandbox.
	Volumes []DaytonaVolume `json:"volumes,omitempty"`
	// NetworkAllowList and DomainAllowList restrict the sandbox's egress to
	// these CIDRs and domains (comma-separated), on top of what the runner
	// itself needs to reach.
	NetworkAllowList string `json:"networkAllowList,omitempty"`
	DomainAllowList  string `json:"domainAllowList,omitempty"`
}

// DaytonaVolume mounts a volume at a path.
type DaytonaVolume struct {
	// Volume is the volume's ID or name.
	Volume    string `json:"volume"`
	MountPath string `json:"mountPath"`
	Subpath   string `json:"subpath,omitempty"`
}

// Disk is the root volume. Type is a provider volume type (gp3, pd-ssd).
type Disk struct {
	SizeGB     int
	Type       string
	IOPS       int
	Throughput int
	// InitRate is EBS's VolumeInitializationRate in MiB/s (0: default).
	InitRate int
}

// GCPOptions are the Compute Engine settings a definition may set in
// extra_config, as Cirun names them.
type GCPOptions struct {
	// Project overrides the credential's project.
	Project string `json:"project,omitempty"`
	// AdditionalDisk attaches a second, empty persistent disk.
	AdditionalDisk *GCPDisk `json:"additionalDisk,omitempty"`
	// NetworkInterfaces replace the default interface on the default
	// network with an external address.
	NetworkInterfaces []GCPNetworkInterface `json:"networkInterfaces,omitempty"`
	// ServiceAccounts are attached to the machine; none by default.
	ServiceAccounts []GCPServiceAccount `json:"serviceAccounts,omitempty"`
	// NoPublicIP launches without an external address and routes egress
	// through a Cloud NAT the provider creates in the region.
	NoPublicIP bool `json:"noPublicIp,omitempty"`
	// OnHostMaintenance is MIGRATE or TERMINATE (empty: the provider's
	// choice, TERMINATE for spot and GPU machines).
	OnHostMaintenance string `json:"onHostMaintenance,omitempty"`
	// ExistingDisk attaches a disk the customer already has. It is never
	// deleted with the machine, and a zonal disk pins the launch to its
	// zone.
	ExistingDisk *GCPExistingDisk `json:"existingDisk,omitempty"`
}

// GCPExistingDisk is a persistent disk to attach by reference.
type GCPExistingDisk struct {
	// Source is projects/P/zones/Z/disks/N or projects/P/regions/R/disks/N
	// (the project may be left out for the launch's project).
	Source     string `json:"source"`
	DeviceName string `json:"deviceName,omitempty"`
	// Mode is READ_WRITE (default) or READ_ONLY; Interface SCSI or NVME.
	Mode      string `json:"mode,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// GCPDisk is an extra persistent disk.
type GCPDisk struct {
	SizeGB int    `json:"sizeGb"`
	Type   string `json:"type,omitempty"`
}

// GCPNetworkInterface is one network interface, as Compute Engine's API
// names its fields.
type GCPNetworkInterface struct {
	Network       string            `json:"network,omitempty"`
	Subnetwork    string            `json:"subnetwork,omitempty"`
	NicType       string            `json:"nicType,omitempty"`
	StackType     string            `json:"stackType,omitempty"`
	AccessConfigs []GCPAccessConfig `json:"accessConfigs,omitempty"`
}

// GCPAccessConfig gives an interface an external address.
type GCPAccessConfig struct {
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	NetworkTier string `json:"networkTier,omitempty"`
}

// GCPServiceAccount is a service account the machine runs as.
type GCPServiceAccount struct {
	Email  string   `json:"email"`
	Scopes []string `json:"scopes,omitempty"`
}

// CacheBoot tells a machine where to fetch its cache credentials from:
// the cache proxy on the machine presents Token to the control plane.
type CacheBoot struct {
	ControlPlaneURL string
	Token           string
}

// Grant is short-lived access to one cache prefix in one bucket.
type Grant struct {
	Bucket string
	Region string
	Prefix string
	// AccountID owns the bucket, when the provider knows it.
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	ExpiresAt       time.Time
}

// Cacher is what a provider offers when it can back the cache: a bucket
// per installation and region, and credentials scoped to one repository's
// prefix in it. Providers without object storage leave it unimplemented.
type Cacher interface {
	// EnsureCacheBucket creates the bucket if it does not exist, private,
	// encrypted, with entries expiring after retention days.
	EnsureCacheBucket(ctx context.Context, secret []byte, region, bucket string, retentionDays int) error
	// CacheGrant returns credentials that can read and write prefix in
	// bucket for about ttl.
	CacheGrant(ctx context.Context, secret []byte, region, bucket, prefix, name string, ttl time.Duration) (Grant, error)
}

// Boot is what the machine runs at startup.
type Boot struct {
	// JITConfig is the single-use GitHub runner registration. It is a
	// credential: it goes into the boot script and nowhere else.
	JITConfig     string
	RunnerVersion string
	// Env is set for every job on the runner.
	Env map[string]string
	// MaxLifetimeSeconds is when the machine must shut itself down even if
	// the control plane is unreachable.
	MaxLifetimeSeconds int
	// GPU installs NVIDIA drivers on Linux images that lack them.
	GPU bool
	// PullFromAPI leaves the machine without a boot script; its image fetches
	// the script from the developer API instead (provision_via).
	PullFromAPI bool
	// Windows settings; ignored on Linux images.
	Windows Windows
	// Cache, when set, starts the cache proxy on the machine.
	Cache *CacheBoot
}

// Windows tunes the runner on Windows images.
type Windows struct {
	// Interactive runs the runner in a logged-in desktop session (run_as:
	// interactive) instead of as a background process.
	Interactive bool
	// User is the local account the interactive session logs in as.
	User string
	// Path is where the runner is installed (default C:\actions-runner).
	Path string
}

// Launch is one runner machine to start.
type Launch struct {
	RunnerID       string
	RunnerName     string
	InstallationID int64
	Repository     string
	Region         string
	Machine        Machine
	Boot           Boot
}

// Instance is a machine the provider launched for a runner. InstanceID is
// whatever the provider needs to find it again (an EC2 instance ID, a
// Compute Engine zone/name); the control plane only stores and echoes it.
type Instance struct {
	RunnerID   string
	InstanceID string
	// Stopping is true once the machine is shutting down.
	Stopping bool
	// Stopped is true for a machine someone stopped by hand: it runs no
	// job and still costs its disk, so it is treated as gone and removed.
	Stopped bool
}

// Provider is one cloud. Implementations must only list and terminate
// machines they tagged for the installation.
//
// Every call may be repeated: the control plane retries after crashes and
// timeouts, and two replicas may act on the same runner. Launch must be
// idempotent per runner, Terminate per instance, and List must describe
// the whole region or fail.
type Provider interface {
	// Name is the cloud's name in config files ("aws", "gcp").
	Name() string
	// Verify checks a secret works and reports who it is. region is the
	// default location the credential was connected with.
	Verify(ctx context.Context, secret []byte, region string) (Identity, error)
	// Launch starts a machine and returns its instance ID. Launching the
	// same runner twice must not create two machines. When the cloud may
	// have created the machine but did not say so, the error is a
	// LaunchError with Ambiguous set, and the next List settles it.
	Launch(ctx context.Context, secret []byte, launch Launch) (string, error)
	// Terminate destroys a machine the provider launched for
	// installationID, and refuses one it did not. A machine that is
	// already gone is not an error.
	Terminate(ctx context.Context, secret []byte, region, instanceID string, installationID int64) error
	// List returns every live machine tagged for installationID in region.
	// It must return an error rather than a partial list: an instance
	// missing from a successful list is presumed gone.
	List(ctx context.Context, secret []byte, region string, installationID int64) ([]Instance, error)
}

// Registry maps cloud names to providers.
type Registry map[string]Provider

// Get returns the provider for a cloud.
func (r Registry) Get(cloud string) (Provider, error) {
	p, ok := r[cloud]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCloud, cloud)
	}

	return p, nil
}

// ErrUnsupportedCloud is returned for a cloud with no provider.
var ErrUnsupportedCloud = errors.New("unsupported cloud")

// LaunchError is a failed launch. Retryable failures (capacity, throttling)
// let the scheduler try again; others move on to the next fallback.
// Ambiguous failures are those where the cloud may have created the
// machine anyway (a request that timed out after it was sent): the
// scheduler must not treat the machine as absent until a List says so.
type LaunchError struct {
	Err       error
	Retryable bool
	Ambiguous bool
}

func (e *LaunchError) Error() string { return e.Err.Error() }

func (e *LaunchError) Unwrap() error { return e.Err }

// IsRetryable reports whether a launch error is worth retrying as is.
func IsRetryable(err error) bool {
	var launchErr *LaunchError

	return errors.As(err, &launchErr) && launchErr.Retryable
}

// IsAmbiguous reports whether a launch may have created a machine after
// all.
func IsAmbiguous(err error) bool {
	var launchErr *LaunchError

	return errors.As(err, &launchErr) && launchErr.Ambiguous
}

// ErrBadCredentials is returned when the cloud rejects the secret itself.
var ErrBadCredentials = errors.New("the cloud rejected the credentials")
