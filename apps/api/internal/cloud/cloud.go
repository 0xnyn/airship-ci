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
)

// Clouds the control plane can launch on. The AWS provider is the only one
// today; the interface is what a second cloud implements.
const (
	AWS = "aws"
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
	// Image is a provider image reference (an AMI ID), an alias such as
	// ubuntu2204, or empty for the provider's default Ubuntu.
	Image string
	// Arch is x64 or arm64, inferred from the instance type.
	Arch string
	Spot bool
	Disk Disk
	// SubnetID, SecurityGroupIDs and InstanceProfile override the
	// provider's defaults (the default VPC, an egress-only group, no role).
	SubnetID         string
	SecurityGroupIDs []string
	InstanceProfile  string
}

// Disk is the root volume.
type Disk struct {
	SizeGB     int
	Type       string
	IOPS       int
	Throughput int
	// InitRate is EBS's VolumeInitializationRate in MiB/s (0: default).
	InitRate int
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

// Instance is a machine the provider launched for a runner.
type Instance struct {
	RunnerID   string
	InstanceID string
	// Stopping is true once the machine is shutting down.
	Stopping bool
}

// Provider is one cloud. Implementations must only list and terminate
// machines they tagged for the installation.
type Provider interface {
	// Name is the cloud's name in config files ("aws").
	Name() string
	// Verify checks a secret works and reports who it is.
	Verify(ctx context.Context, secret []byte, region string) (Identity, error)
	// Launch starts a machine and returns its instance ID. Launching the
	// same runner twice must not create two machines.
	Launch(ctx context.Context, secret []byte, launch Launch) (string, error)
	// Terminate destroys a machine. A machine that is already gone is not
	// an error.
	Terminate(ctx context.Context, secret []byte, region, instanceID string) error
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
type LaunchError struct {
	Err       error
	Retryable bool
}

func (e *LaunchError) Error() string { return e.Err.Error() }

func (e *LaunchError) Unwrap() error { return e.Err }

// IsRetryable reports whether a launch error is worth retrying as is.
func IsRetryable(err error) bool {
	var launchErr *LaunchError

	return errors.As(err, &launchErr) && launchErr.Retryable
}

// ErrBadCredentials is returned when the cloud rejects the secret itself.
var ErrBadCredentials = errors.New("the cloud rejected the credentials")
