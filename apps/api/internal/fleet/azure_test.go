package fleet_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
)

const azureConfig = `
runners:
  - name: vm
    cloud: azure
    instance_type: [Standard_D4s_v5, Standard_D2pls_v5]
    machine_image:
      - Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest
      - {publisher: Canonical, offer: 0001-com-ubuntu-server-jammy, sku: 22_04-lts-arm64}
    region: [westeurope, ""]
    preemptible: [false, true]
    labels: [airship-vm]
    extra_config:
      enable_public_ip: false
      cache: true
      storageProfile:
        osDisk:
          diskSizeGB: 128
          managedDisk:
            storageAccountType: Premium_LRS
  - name: win
    cloud: azure
    instance_type: Standard_D4s_v5
    machine_image: MicrosoftWindowsDesktop:windows-11:win11-24h2-pro:latest
    labels: [airship-win]
    extra_config:
      run_as: interactive
`

// An Azure definition launches a virtual machine shaped as the config
// says, in the region it names or the credential's default, with its
// extra_config carried to the provider. cache: true is a warning, not a
// proxy: Azure has no object storage the cache proxy speaks (the harness
// registers it without cache support, as the real provider is).
func TestAzureRunnersLaunchAsVirtualMachines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.connectAzure("eastus")
	h.setConfig(headSHA, azureConfig)

	h.queue("airship-vm")
	h.queue("airship-win")
	if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
		t.Fatal(err)
	}
	instances := h.azure.Instances()
	if len(instances) != 2 || len(h.aws.Instances()) != 0 || len(h.gcp.Instances()) != 0 {
		t.Fatalf("azure instances = %d, aws = %d, gcp = %d", len(instances), len(h.aws.Instances()), len(h.gcp.Instances()))
	}
	byImage := map[string]cloud.Launch{}
	for _, instance := range instances {
		byImage[instance.Launch.Machine.Image] = instance.Launch
	}
	vm, ok := byImage["Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest"]
	if !ok {
		t.Fatalf("no launch with the first image: %v", byImage)
	}
	m := vm.Machine
	if vm.Region != "westeurope" || m.InstanceType != "Standard_D4s_v5" || m.Arch != "x64" || m.Spot || m.Disk.SizeGB != 128 || m.Disk.Type != "Premium_LRS" ||
		m.Azure == nil || !m.Azure.NoPublicIP || m.GCP != nil || m.Daytona != nil || vm.Boot.Cache != nil || vm.Boot.Windows.Interactive {
		t.Errorf("vm launch = %+v, machine %+v, azure %+v", vm, m, m.Azure)
	}
	win, ok := byImage["MicrosoftWindowsDesktop:windows-11:win11-24h2-pro:latest"]
	if !ok {
		t.Fatalf("no Windows launch: %v", byImage)
	}
	if win.Region != "eastus" || win.Machine.Disk.SizeGB != 0 || win.Machine.Azure != nil || !win.Boot.Windows.Interactive {
		t.Errorf("windows launch = %+v, machine %+v", win, win.Machine)
	}
	wantStates(t, h, "running", "running")

	// The first shape failing for want of capacity, the second (arm, spot,
	// the credential's region) is tried.
	g := newHarness(t)
	g.connectAzure("eastus")
	g.setConfig(headSHA, azureConfig)
	g.azure.FailLaunch = func(l cloud.Launch) error {
		if l.Machine.InstanceType == "Standard_D4s_v5" {
			return &cloud.LaunchError{Err: errors.New("AllocationFailed"), Retryable: true}
		}

		return nil
	}
	g.queue("airship-vm")
	g.launch()
	g.launch()
	launches := g.azure.Instances()
	if len(launches) != 1 || launches[0].Launch.Region != "eastus" || launches[0].Launch.Machine.InstanceType != "Standard_D2pls_v5" || launches[0].Launch.Machine.Arch != "arm64" || !launches[0].Launch.Machine.Spot {
		t.Fatalf("fallback launch = %+v", launches)
	}
}

// The inventory scan asks a cloud that builds machines from several
// resources to sweep up what failed launches left behind, after the
// machines themselves were handled; a sweep that fails is not a scan that
// fails.
func TestScanSweepsOrphanedResources(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.connectAzure("eastus")
	h.setConfig(headSHA, azureConfig)
	h.queue("airship-vm")
	h.launch()
	h.reconcile()
	if h.azure.Sweeps() != 1 {
		t.Fatalf("sweeps = %d, want one for the scanned scope", h.azure.Sweeps())
	}
	h.azure.FailSweep = true
	h.reconcile()
	wantStates(t, h, "running")
	if h.azure.Sweeps() != 2 {
		t.Fatalf("sweeps after a failure = %d", h.azure.Sweeps())
	}
}

// A launch that outlives its lease (a cloud that takes minutes to build a
// machine) renews it, so the reconciler does not give the runner up and
// retry the job while the machine is still being made.
func TestSlowLaunchRenewsItsLease(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(cfg *fleet.Config) {
		cfg.LeaseDuration = 2 * time.Second
		cfg.LaunchTimeout = 20 * time.Second
	})
	h.connectAzure("eastus")
	h.setConfig(headSHA, azureConfig)
	started := make(chan struct{})
	h.azure.Delay = func(ctx context.Context, _ cloud.Launch) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3500 * time.Millisecond):
			return nil
		}
	}
	h.queue("airship-vm")
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
			t.Error(err)
		}
	})
	<-started
	// Past the original lease, while the cloud is still at work.
	time.Sleep(2500 * time.Millisecond)
	h.reconcile()
	wantStates(t, h, "launching")
	wg.Wait()
	wantStates(t, h, "running")
	if len(h.azure.Instances()) != 1 {
		t.Fatalf("instances = %d", len(h.azure.Instances()))
	}
}

// A launch whose runner was given up on meanwhile (its lease expired
// while the database was unreachable, or the runner was finished for any
// other reason) is cut short: it must not make a machine nobody tracks,
// and a cut-short launch is reported ambiguous for the scan to settle.
func TestLaunchStopsWhenItsLeaseIsLost(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(cfg *fleet.Config) {
		cfg.LeaseDuration = 2 * time.Second
		cfg.LaunchTimeout = 20 * time.Second
	})
	h.connectAzure("eastus")
	h.setConfig(headSHA, azureConfig)
	started := make(chan struct{})
	ended := make(chan error, 1)
	h.azure.Delay = func(ctx context.Context, _ cloud.Launch) error {
		close(started)
		select {
		case <-ctx.Done():
			ended <- ctx.Err()

			return ctx.Err()
		case <-time.After(15 * time.Second):
			ended <- nil

			return nil
		}
	}
	h.queue("airship-vm")
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := h.svc.LaunchOnce(h.ctx); err != nil {
			t.Error(err)
		}
	})
	<-started
	// The reconciler of another replica gives the runner up as if the
	// lease had lapsed (the renewal is refused from then on).
	h.exec("update runners set state = 'finished', finish_reason = 'lease_expired', finished_at = now(), lease_expires_at = null where state = 'launching'")
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("the launch ran to completion without a lease")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the launch was not cut short after its lease was lost")
	}
	wg.Wait()
	if len(h.azure.Instances()) != 0 {
		t.Fatal("a machine was made for a runner that was given up on")
	}
}

// A launch is bounded by LaunchTimeout however often its lease renews.
func TestLaunchTimeoutBoundsALaunch(t *testing.T) {
	t.Parallel()
	h := newHarnessWith(t, func(cfg *fleet.Config) {
		cfg.LeaseDuration = 2 * time.Second
		cfg.LaunchTimeout = 1500 * time.Millisecond
	})
	h.connectAzure("eastus")
	h.setConfig(headSHA, azureConfig)
	h.azure.Delay = func(ctx context.Context, _ cloud.Launch) error {
		<-ctx.Done()

		return ctx.Err()
	}
	h.queue("airship-vm")
	start := time.Now()
	h.launch()
	if time.Since(start) > 10*time.Second {
		t.Fatal("the launch was not bounded")
	}
	// Cut short, the provider reports the launch ambiguous: the runner is
	// unconfirmed and the job retried on the next attempt.
	wantStates(t, h, "finished:launch_unconfirmed", "pending")
}
