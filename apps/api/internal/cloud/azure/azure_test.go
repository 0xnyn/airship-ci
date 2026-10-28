package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/azure/azuretest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
)

const (
	subscription = "0f0e0d0c-0b0a-4908-8706-050403020100"
	tenant       = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	clientID     = "12345678-9abc-4def-8123-456789abcdef"
	region       = "eastus"
	runnerID     = "0192f3a4-5b6c-7d8e-9f00-112233445566"
	name         = "airship-0a1b2c3d4e5f"
	installation = 42
)

var testSecret = mustSecret(Secret{SubscriptionID: subscription, TenantID: tenant, ClientID: clientID, ClientSecret: "s3cr3t~value"})

func mustSecret(s Secret) []byte {
	raw, err := EncodeSecret(s)
	if err != nil {
		panic(err)
	}

	return raw
}

type fixture struct {
	t        *testing.T
	server   *azuretest.Server
	provider *Provider
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	server := azuretest.New(t, subscription)
	linux := func(arch armcompute.ArchitectureTypes) armcompute.VirtualMachineImageProperties {
		return armcompute.VirtualMachineImageProperties{
			Architecture: to.Ptr(arch),
			OSDiskImage:  &armcompute.OSDiskImage{OperatingSystem: to.Ptr(armcompute.OperatingSystemTypesLinux)},
			Features:     []*armcompute.VirtualMachineImageFeature{{Name: to.Ptr("SecurityType"), Value: to.Ptr("TrustedLaunchSupported")}},
		}
	}
	for _, version := range []string{"22.04.202509010", "22.04.202601010", "22.04.202512150"} {
		server.AddPlatformImage(region, "Canonical", "0001-com-ubuntu-server-jammy", "22_04-lts-gen2", version, linux(armcompute.ArchitectureTypesX64))
	}
	server.AddPlatformImage(region, "Canonical", "0001-com-ubuntu-server-jammy", "22_04-lts-arm64", "22.04.202601010", linux(armcompute.ArchitectureTypesArm64))
	server.AddPlatformImage(region, "MicrosoftWindowsDesktop", "windows-11", "win11-24h2-pro", "26100.1.260101", armcompute.VirtualMachineImageProperties{
		Architecture: to.Ptr(armcompute.ArchitectureTypesX64),
		OSDiskImage:  &armcompute.OSDiskImage{OperatingSystem: to.Ptr(armcompute.OperatingSystemTypesWindows)},
		Features:     []*armcompute.VirtualMachineImageFeature{{Name: to.Ptr("SecurityType"), Value: to.Ptr("TrustedLaunch")}},
	})
	provider := New(Options{Endpoint: server.URL, Credential: azuretest.Credential{}})

	return &fixture{t: t, server: server, provider: provider}
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       runnerID,
		RunnerName:     name,
		InstallationID: installation,
		Repository:     "acme/widgets",
		Region:         region,
		Machine:        cloud.Machine{InstanceType: "Standard_D4s_v5", Arch: "x64"},
		Boot: cloud.Boot{
			JITConfig:          base64.StdEncoding.EncodeToString([]byte(`{"jit":"config"}`)),
			RunnerVersion:      "2.338.2",
			MaxLifetimeSeconds: 3600,
		},
	}
}

func windowsLaunch() cloud.Launch {
	l := testLaunch()
	l.Machine.Image = "MicrosoftWindowsDesktop:windows-11:win11-24h2-pro:latest"

	return l
}

func (f *fixture) launch(l cloud.Launch) (string, error) {
	return f.provider.Launch(context.Background(), testSecret, l)
}

func (f *fixture) mustLaunch(l cloud.Launch) *armcompute.VirtualMachine {
	f.t.Helper()
	id, err := f.launch(l)
	if err != nil {
		f.t.Fatalf("launch: %v", err)
	}
	if id != l.RunnerName {
		f.t.Fatalf("instance ID = %q, want the VM name %q", id, l.RunnerName)
	}
	vm := f.server.VM(id)
	if vm == nil {
		f.t.Fatalf("launch returned %s but no such VM exists", id)
	}

	return vm
}

func (f *fixture) list() []cloud.Instance {
	f.t.Helper()
	out, err := f.provider.List(context.Background(), testSecret, region, installation)
	if err != nil {
		f.t.Fatalf("list: %v", err)
	}

	return out
}

func customData(vm *armcompute.VirtualMachine) string {
	if vm.Properties.OSProfile == nil || vm.Properties.OSProfile.CustomData == nil {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(*vm.Properties.OSProfile.CustomData)
	if err != nil {
		return ""
	}

	return string(raw)
}

// shorten makes the waits the provider does fit a test.
func shorten(t *testing.T) {
	t.Helper()
	saved := []*time.Duration{&operationTimeout, &vmOperationTimeout, &runCommandTimeout, &listTimeout, &pollFrequency}
	values := make([]time.Duration, len(saved))
	for i, d := range saved {
		values[i] = *d
	}
	t.Cleanup(func() {
		for i, d := range saved {
			*d = values[i]
		}
	})
	operationTimeout, vmOperationTimeout, runCommandTimeout, listTimeout = 500*time.Millisecond, 500*time.Millisecond, 500*time.Millisecond, 500*time.Millisecond
	pollFrequency = 20 * time.Millisecond
}

func TestVerify(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	identity, err := f.provider.Verify(context.Background(), testSecret, "uksouth")
	if err != nil || identity.AccountID != subscription || identity.Principal != clientID {
		t.Fatalf("verify = %+v, %v", identity, err)
	}
	if _, err := f.provider.Verify(context.Background(), testSecret, "East US"); err == nil {
		t.Fatal("a display name was accepted as a region")
	}
	if _, err := f.provider.Verify(context.Background(), testSecret, "northpole"); err == nil || !strings.Contains(err.Error(), "not available to subscription") {
		t.Fatalf("unknown region: err = %v", err)
	}
	if _, err := f.provider.Verify(context.Background(), []byte(`{"subscription_id":"nope"}`), region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Fatalf("malformed secret: err = %v, want bad credentials", err)
	}
	other := mustSecret(Secret{SubscriptionID: "ffffffff-ffff-4fff-8fff-ffffffffffff", TenantID: tenant, ClientID: clientID, ClientSecret: "x"})
	if _, err := f.provider.Verify(context.Background(), other, region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Fatalf("subscription the principal cannot see: err = %v, want bad credentials", err)
	}
	f.server.SubscriptionState = "Disabled"
	if _, err := f.provider.Verify(context.Background(), testSecret, region); !errors.Is(err, cloud.ErrBadCredentials) || !strings.Contains(err.Error(), "Disabled") {
		t.Fatalf("disabled subscription: err = %v", err)
	}
}

func TestLaunchRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	vm := f.mustLaunch(testLaunch())
	props := vm.Properties
	if got := string(*props.HardwareProfile.VMSize); got != "Standard_D4s_v5" {
		t.Fatalf("size = %s", got)
	}
	if *vm.Location != region {
		t.Fatalf("location = %s", *vm.Location)
	}
	ref := props.StorageProfile.ImageReference
	if *ref.Publisher != "Canonical" || *ref.SKU != "22_04-lts-gen2" || *ref.Version != "latest" {
		t.Fatalf("image reference = %+v, want the default Ubuntu with version latest", *ref)
	}
	disk := props.StorageProfile.OSDisk
	if *disk.Name != name+"-osdisk" || *disk.DiskSizeGB != DefaultLinuxDiskGB || *disk.DeleteOption != armcompute.DiskDeleteOptionTypesDelete {
		t.Fatalf("os disk = %+v", *disk)
	}
	if props.SecurityProfile != nil {
		t.Fatal("a Linux image that merely supports Trusted Launch should launch with the standard security type")
	}
	if props.Priority != nil {
		t.Fatal("not spot")
	}
	for tag, want := range map[string]string{TagInstallation: "42", TagRunner: runnerID, TagRepository: "acme/widgets"} {
		if got := deref(vm.Tags[tag]); got != want {
			t.Fatalf("tag %s = %q, want %q", tag, got, want)
		}
	}
	if _, err := time.Parse(time.RFC3339, deref(vm.Tags[TagCreated])); err != nil {
		t.Fatalf("created tag: %v", err)
	}
	os := props.OSProfile
	if *os.AdminUsername != adminUsername || *os.ComputerName != name || !*os.LinuxConfiguration.DisablePasswordAuthentication {
		t.Fatalf("os profile = %+v", *os)
	}
	if key := *os.LinuxConfiguration.SSH.PublicKeys[0].KeyData; !strings.HasPrefix(key, "ssh-rsa ") {
		t.Fatalf("ssh key = %q", key)
	}
	script := customData(vm)
	if !strings.HasPrefix(script, "#!/") || !strings.Contains(script, "2.338.2") || !strings.Contains(script, "shutdown") {
		t.Fatalf("custom data is not the boot script:\n%s", script)
	}
	nics := props.NetworkProfile.NetworkInterfaces
	if len(nics) != 1 || *nics[0].Properties.DeleteOption != armcompute.DeleteOptionsDelete {
		t.Fatalf("network interfaces = %+v", nics)
	}
	nic := f.server.NICs()[name+"-nic"]
	if nic == nil {
		t.Fatal("no interface was made")
	}
	cfg := nic.Properties.IPConfigurations[0].Properties
	if !strings.HasSuffix(*cfg.Subnet.ID, "/virtualNetworks/airship-runners-eastus/subnets/runners") {
		t.Fatalf("subnet = %s", *cfg.Subnet.ID)
	}
	if cfg.PublicIPAddress == nil || *cfg.PublicIPAddress.Properties.DeleteOption != armnetwork.DeleteOptionsDelete {
		t.Fatalf("public address = %+v, want one deleted with the machine", cfg.PublicIPAddress)
	}
	if ip := f.server.PublicIPs()[name+"-ip"]; ip == nil || *ip.SKU.Name != armnetwork.PublicIPAddressSKUNameStandard {
		t.Fatalf("public address = %+v", ip)
	}
	if groups := f.server.ResourceGroups(); len(groups) != 1 || groups[0] != ResourceGroup {
		t.Fatalf("resource groups = %v", groups)
	}
	vnet := f.server.VNets()[networkName(region)]
	if vnet == nil || vnet.Tags[TagManaged] == nil {
		t.Fatalf("virtual network = %+v, want one tagged as managed", vnet)
	}
	subnet := vnet.Properties.Subnets[0]
	if *subnet.Name != subnetName || subnet.Properties.NetworkSecurityGroup == nil || *subnet.Properties.DefaultOutboundAccess {
		t.Fatalf("subnet = %+v", *subnet.Properties)
	}
	if len(f.server.NATs()) != 0 {
		t.Fatal("a NAT gateway was made for a launch with a public address")
	}
	if f.server.RunCommandCreates != 0 {
		t.Fatal("a Linux image boots from custom data, not a run command")
	}
	// The VM lists as running.
	instances := f.list()
	if len(instances) != 1 || instances[0].RunnerID != runnerID || instances[0].InstanceID != name || instances[0].Stopped || instances[0].Stopping {
		t.Fatalf("list = %+v", instances)
	}
}

func TestRepeatedLaunchIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := f.mustLaunch(testLaunch())
	second := f.mustLaunch(testLaunch())
	if *first.ID != *second.ID || f.server.VMCreates != 1 || f.server.NICCreates != 1 || f.server.IPCreates != 1 {
		t.Fatalf("second launch made new resources: creates = %d/%d/%d", f.server.VMCreates, f.server.NICCreates, f.server.IPCreates)
	}
	// Another runner that ends up with the same name is refused, not adopted.
	other := testLaunch()
	other.RunnerID = "ffffffff-0000-7000-8000-000000000000"
	_, err := f.launch(other)
	if err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "not this runner's") {
		t.Fatalf("a foreign VM with the runner's name: err = %v", err)
	}
}

func TestLaunchFailuresAfterAcceptanceCleanUp(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		failure   azuretest.Failure
		retryable bool
		contains  string
	}{
		"no capacity": {azuretest.Failure{Code: "AllocationFailed", Message: "Allocation failed. We do not have sufficient capacity for the requested VM size in this region."}, true, "sufficient capacity"},
		"quota":       {azuretest.Failure{Code: "OperationNotAllowed", Message: "Operation could not be completed as it results in exceeding approved standardDSv5Family Cores quota."}, true, "quota"},
		"marketplace": {azuretest.Failure{Code: "ResourcePurchaseValidationFailed", Message: "User failed validation to purchase resources."}, false, "marketplace terms"},
		"bad size":    {azuretest.Failure{Code: "SkuNotAvailable", Message: "The requested VM size for resource is currently not available."}, false, "not available"},
	}
	for tc, c := range cases {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.server.FailVMCreate = &c.failure
			_, err := f.launch(testLaunch())
			if err == nil || cloud.IsRetryable(err) != c.retryable || cloud.IsAmbiguous(err) || !strings.Contains(err.Error(), c.contains) {
				t.Fatalf("err = %v, retryable = %v, want retryable %v, not ambiguous, mentioning %q", err, cloud.IsRetryable(err), c.retryable, c.contains)
			}
			if f.server.VM(name) != nil || len(f.server.NICs()) != 0 || len(f.server.PublicIPs()) != 0 {
				t.Fatalf("the failed VM and its interface and address should be gone: vm = %v, nics = %d, ips = %d", f.server.VM(name) != nil, len(f.server.NICs()), len(f.server.PublicIPs()))
			}
			// The next attempt starts over.
			if _, err := f.launch(testLaunch()); err != nil {
				t.Fatalf("relaunch after failure: %v", err)
			}
		})
	}
}

func TestRefusedCreationLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reject    azuretest.HTTPError
		retryable bool
	}{
		"forbidden":                {azuretest.HTTPError{Status: http.StatusForbidden, Code: "AuthorizationFailed", Message: "The client does not have authorization to perform action 'Microsoft.Compute/virtualMachines/write'."}, false},
		"bad request":              {azuretest.HTTPError{Status: http.StatusBadRequest, Code: "InvalidParameter", Message: "The value of parameter osProfile.computerName is invalid."}, false},
		"throttled with a verdict": {azuretest.HTTPError{Status: http.StatusConflict, Code: "OperationNotAllowed", Message: "Operation could not be completed as it results in exceeding approved quota."}, true},
	}
	for tc, c := range cases {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.server.RejectVMCreate = &c.reject
			_, err := f.launch(testLaunch())
			if err == nil || cloud.IsRetryable(err) != c.retryable || cloud.IsAmbiguous(err) {
				t.Fatalf("err = %v, retryable = %v, want %v", err, cloud.IsRetryable(err), c.retryable)
			}
			if f.server.VM(name) != nil || len(f.server.NICs()) != 0 || len(f.server.PublicIPs()) != 0 {
				t.Fatalf("a refused creation should take its interface and address with it: nics = %d, ips = %d", len(f.server.NICs()), len(f.server.PublicIPs()))
			}
		})
	}
	// An address that cannot be made leaves no interface either.
	f := newFixture(t)
	f.server.RejectIPCreate = &azuretest.HTTPError{Status: http.StatusBadRequest, Code: "PublicIPCountLimitReached", Message: "Cannot create more than 10 public IP addresses for this subscription in this region."}
	_, err := f.launch(testLaunch())
	if err == nil || !cloud.IsRetryable(err) || len(f.server.NICs()) != 0 {
		t.Fatalf("address limit: err = %v, nics = %d", err, len(f.server.NICs()))
	}
}

// A lost answer is settled by looking the VM up by name; the SDK's own
// retries hide a single loss.
func TestLostAnswersAreSettledOrAmbiguous(t *testing.T) {
	shorten(t)
	f := newFixture(t)
	f.server.DropVMCreates = 3 // the request and both SDK retries
	id, err := f.launch(testLaunch())
	if err != nil || id != name || f.server.VM(name) == nil {
		t.Fatalf("dropped answer, VM exists: id = %s, err = %v", id, err)
	}
	if f.server.VMCreates != 3 {
		t.Fatalf("creates = %d, want the request and two retries", f.server.VMCreates)
	}

	// A VM still creating when the launch runs out of time is ambiguous:
	// it is real and the scan lists it.
	g := newFixture(t)
	g.server.SlowVMCreates = true
	_, err = g.launch(testLaunch())
	if err == nil || !cloud.IsAmbiguous(err) || !cloud.IsRetryable(err) {
		t.Fatalf("creation still in progress: err = %v, want ambiguous and retryable", err)
	}
	if g.server.VM(name) == nil {
		t.Fatal("the VM being created should still exist")
	}
	instances, err := g.provider.List(context.Background(), testSecret, region, installation)
	if err != nil || len(instances) != 1 || instances[0].RunnerID != runnerID || instances[0].Stopped {
		t.Fatalf("a VM still creating should list as the runner's, alive: %+v, %v", instances, err)
	}
	// Once it is done, the retry adopts it without creating again.
	g.server.FinishVMCreates()
	id, err = g.launch(testLaunch())
	if err != nil || id != name || g.server.VMCreates != 1 {
		t.Fatalf("retry after ambiguity: id = %s, err = %v, creates = %d", id, err, g.server.VMCreates)
	}

	// Polling that keeps failing is settled the same way.
	h := newFixture(t)
	h.server.FailOperationPolls = 3
	if _, err := h.launch(testLaunch()); err != nil {
		t.Fatalf("failed polls: %v", err)
	}
}

func TestHungOperationIsSettledByLookup(t *testing.T) {
	shorten(t)
	f := newFixture(t)
	f.server.HangOperations = true
	id, err := f.launch(testLaunch())
	if err != nil || id != name {
		t.Fatalf("hung operation with a finished VM: id = %s, err = %v", id, err)
	}
}

func TestLaunchContextEndsAnAdoptionWait(t *testing.T) {
	shorten(t)
	vmOperationTimeout = time.Minute // the launch's context ends first
	f := newFixture(t)
	f.server.SlowVMCreates = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := f.provider.Launch(ctx, testSecret, testLaunch())
		result <- err
	}()
	// Once the creation was accepted, the launcher runs out of time.
	for f.server.VM(name) == nil {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	err := <-result
	if err == nil || !cloud.IsAmbiguous(err) || !cloud.IsRetryable(err) {
		t.Fatalf("err = %v, want ambiguous and retryable", err)
	}
	if f.server.VM(name) == nil {
		t.Fatal("a VM whose creation outlived the launch must be left for the scan, not deleted")
	}
}

func TestAdoptingAFailedVMRemovesIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	failed := armcompute.VirtualMachine{
		Location:   to.Ptr(region),
		Tags:       map[string]*string{TagInstallation: to.Ptr("42"), TagRunner: to.Ptr(runnerID)},
		Properties: &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr("Failed")},
	}
	f.server.PutVM(name, failed)
	_, err := f.launch(testLaunch())
	if err == nil || !cloud.IsRetryable(err) || cloud.IsAmbiguous(err) {
		t.Fatalf("err = %v, want retryable", err)
	}
	if f.server.VM(name) != nil {
		t.Fatal("the failed VM should have been removed")
	}
	if _, err := f.launch(testLaunch()); err != nil {
		t.Fatalf("relaunch: %v", err)
	}
}

func TestSpotAndDisk(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.Spot = true
	l.Machine.Disk = cloud.Disk{SizeGB: 200, Type: "Premium_LRS"}
	vm := f.mustLaunch(l)
	props := vm.Properties
	if *props.Priority != armcompute.VirtualMachinePriorityTypesSpot || *props.EvictionPolicy != armcompute.VirtualMachineEvictionPolicyTypesDelete || *props.BillingProfile.MaxPrice != -1 {
		t.Fatalf("spot settings = %+v", *props)
	}
	disk := props.StorageProfile.OSDisk
	if *disk.DiskSizeGB != 200 || *disk.ManagedDisk.StorageAccountType != armcompute.StorageAccountTypesPremiumLRS {
		t.Fatalf("disk = %+v", *disk)
	}
}

func TestPullFromAPILeavesNoScriptBehind(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Boot.PullFromAPI = true
	vm := f.mustLaunch(l)
	if vm.Properties.OSProfile.CustomData != nil {
		t.Fatal("an image that pulls its boot from the API should get no custom data")
	}
	w := windowsLaunch()
	w.Boot.PullFromAPI = true
	f.mustLaunch(w)
	if f.server.RunCommandCreates != 0 {
		t.Fatal("an image that pulls its boot from the API should get no run command")
	}
}

func TestWindowsBootsThroughARunCommandOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := windowsLaunch()
	l.Boot.Windows.Interactive = true
	vm := f.mustLaunch(l)
	props := vm.Properties
	os := props.OSProfile
	if os.CustomData != nil || os.WindowsConfiguration == nil || *os.WindowsConfiguration.EnableAutomaticUpdates || os.AdminPassword == nil {
		t.Fatalf("windows os profile = %+v", *os)
	}
	if got := *os.ComputerName; got != "air0a1b2c3d4e5f" || len(got) > maxWindowsComputerName {
		t.Fatalf("computer name = %q", got)
	}
	if props.SecurityProfile == nil || *props.SecurityProfile.SecurityType != armcompute.SecurityTypesTrustedLaunch || *props.SecurityProfile.UefiSettings.SecureBootEnabled {
		t.Fatalf("Windows 11 requires Trusted Launch: %+v", props.SecurityProfile)
	}
	if props.StorageProfile.OSDisk.DiskSizeGB != nil {
		t.Fatal("a Windows disk keeps the image's size unless the config says")
	}
	cmd := f.server.RunCommand(name, runCommandName)
	if cmd == nil || !*cmd.Properties.AsyncExecution {
		t.Fatalf("run command = %+v, want an asynchronous one", cmd)
	}
	script := *cmd.Properties.Source.Script
	if !strings.Contains(script, "2.338.2") || !strings.Contains(script, "Stop-Computer") && !strings.Contains(script, "shutdown") {
		t.Fatalf("run command script:\n%s", script)
	}
	if strings.Contains(script, *os.AdminPassword) {
		t.Fatal("the admin password must not be the interactive session's password")
	}
	// A repeated launch must not run the script again: that would start a
	// second runner process on the same machine.
	f.mustLaunch(l)
	if f.server.RunCommandRuns != 1 || f.server.VMCreates != 1 {
		t.Fatalf("relaunch ran the script %d times, created %d VMs", f.server.RunCommandRuns, f.server.VMCreates)
	}
}

func TestRunCommandFailureRemovesTheMachine(t *testing.T) {
	shorten(t)
	cases := map[string]struct {
		arrange   func(*azuretest.Server)
		retryable bool
	}{
		"refused": {func(s *azuretest.Server) {
			s.RejectRunCommand = &azuretest.HTTPError{Status: http.StatusBadRequest, Code: "InvalidParameter", Message: "The value of parameter runCommand is invalid."}
		}, false},
		"failed": {func(s *azuretest.Server) {
			s.FailRunCommand = &azuretest.Failure{Code: "VMAgentStatusCommunicationError", Message: "VM has not reported status for VM agent."}
		}, true},
	}
	for tc, c := range cases {
		t.Run(tc, func(t *testing.T) {
			f := newFixture(t)
			c.arrange(f.server)
			_, err := f.launch(windowsLaunch())
			if err == nil || cloud.IsRetryable(err) != c.retryable || cloud.IsAmbiguous(err) {
				t.Fatalf("err = %v, retryable = %v, want %v and not ambiguous", err, cloud.IsRetryable(err), c.retryable)
			}
			if f.server.VM(name) != nil {
				t.Fatal("a VM that cannot be given its boot script must be removed: it would never register")
			}
		})
	}
	// A throttled answer is retried by the SDK itself.
	f := newFixture(t)
	f.server.RejectRunCommand = &azuretest.HTTPError{Status: http.StatusTooManyRequests, Code: "TooManyRequests", Message: "Slow down."}
	if id, err := f.launch(windowsLaunch()); err != nil || id != name || f.server.RunCommandRuns != 1 {
		t.Fatalf("throttled run command: id = %s, err = %v, runs = %d", id, err, f.server.RunCommandRuns)
	}
	// A lost answer whose command did get created is a success. Each
	// retried PUT runs the script again, which the script's own marker
	// makes harmless.
	g := newFixture(t)
	g.server.DropRunCommands = 3
	id, err := g.launch(windowsLaunch())
	if err != nil || id != name || g.server.RunCommand(name, runCommandName) == nil {
		t.Fatalf("dropped run command answer: id = %s, err = %v", id, err)
	}
}

func TestVMAgentFailureAfterAcceptanceIsRetryable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.FailVMCreate = &azuretest.Failure{Code: "OSProvisioningTimedOut", Message: "OS Provisioning for VM did not finish in the allotted time."}
	_, err := f.launch(testLaunch())
	if err == nil || !cloud.IsRetryable(err) {
		t.Fatalf("err = %v, want retryable", err)
	}
	if f.server.VM(name) != nil {
		t.Fatal("the VM should have been removed")
	}
}

func TestNoPublicIPUsesOneNATPerRegion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.Azure = &cloud.AzureOptions{NoPublicIP: true}
	f.mustLaunch(l)
	nic := f.server.NICs()[name+"-nic"]
	if nic.Properties.IPConfigurations[0].Properties.PublicIPAddress != nil {
		t.Fatal("a machine without a public address got one")
	}
	ips := f.server.PublicIPs()
	if len(ips) != 1 || ips[natName(region)+ipSuffix] == nil {
		t.Fatalf("public addresses = %v, want only the NAT's", ips)
	}
	nats := f.server.NATs()
	if len(nats) != 1 || nats[natName(region)] == nil {
		t.Fatalf("NAT gateways = %v", nats)
	}
	subnet := f.server.VNets()[networkName(region)].Properties.Subnets[0]
	if subnet.Properties.NatGateway == nil || !strings.HasSuffix(*subnet.Properties.NatGateway.ID, "/natGateways/"+natName(region)) {
		t.Fatalf("subnet NAT = %+v", subnet.Properties.NatGateway)
	}
	// A second launch, and a launch with a public address, share it all.
	second := l
	second.RunnerID, second.RunnerName = "ffffffff-0000-7000-8000-000000000001", "airship-second"
	f.mustLaunch(second)
	third := testLaunch()
	third.RunnerID, third.RunnerName = "ffffffff-0000-7000-8000-000000000002", "airship-third"
	f.mustLaunch(third)
	if len(f.server.NATs()) != 1 || len(f.server.VNets()) != 1 || len(f.server.PublicIPs()) != 2 {
		t.Fatalf("shared network: nats = %d, vnets = %d, ips = %d", len(f.server.NATs()), len(f.server.VNets()), len(f.server.PublicIPs()))
	}
	// Terminating a machine removes its interface, not the shared network.
	if err := f.provider.Terminate(context.Background(), testSecret, region, "airship-third", installation); err != nil {
		t.Fatal(err)
	}
	if f.server.VM("airship-third") != nil || f.server.NICs()["airship-third-nic"] != nil || f.server.PublicIPs()["airship-third-ip"] != nil {
		t.Fatal("the machine's own resources should be gone")
	}
	if len(f.server.NATs()) != 1 || len(f.server.VNets()) != 1 {
		t.Fatal("the shared network should remain")
	}
}

// A network with Airship's name but not its tag was made by somebody
// else, and runners do not go on it.
func TestNetworkThatIsNotAirshipsIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	vnet := f.server.VNets()[networkName(region)]
	vnet.Tags = nil
	f.server.PutVNet(*vnet)
	if err := f.provider.Terminate(context.Background(), testSecret, region, name, installation); err != nil {
		t.Fatal(err)
	}
	f.provider.forgetSetups() // as a fresh replica, or one whose cache expired

	_, err := f.launch(testLaunch())
	if err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "not created by Airship") {
		t.Fatalf("foreign network: err = %v", err)
	}
	if len(f.server.NICs()) != 0 || len(f.server.PublicIPs()) != 0 {
		t.Fatal("nothing should be made on a network that is not Airship's")
	}
}

// forgetSetups empties the setup cache, as time would.
func (p *Provider) forgetSetups() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.setups)
}

func TestTerminateChecksOwnership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	err := f.provider.Terminate(context.Background(), testSecret, region, name, installation+1)
	if err == nil || !strings.Contains(err.Error(), "not tagged for installation") || f.server.VM(name) == nil {
		t.Fatalf("another installation's terminate: err = %v, vm gone = %v", err, f.server.VM(name) == nil)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, "no-such-vm", installation); err != nil {
		t.Fatalf("terminating a missing VM: %v", err)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, "Not A Name", installation); err == nil {
		t.Fatal("a malformed instance ID was accepted")
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, name, installation); err != nil || f.server.VM(name) != nil {
		t.Fatalf("terminate: err = %v, vm gone = %v", err, f.server.VM(name) == nil)
	}
	if f.server.NICs()[name+"-nic"] != nil || f.server.PublicIPs()[name+"-ip"] != nil {
		t.Fatal("the interface and address should go with the VM")
	}
	// Terminating twice, and while a deletion is in progress, is fine.
	if err := f.provider.Terminate(context.Background(), testSecret, region, name, installation); err != nil {
		t.Fatal(err)
	}
	g := newFixture(t)
	g.mustLaunch(testLaunch())
	g.server.SlowDeletes = true
	for range 2 {
		if err := g.provider.Terminate(context.Background(), testSecret, region, name, installation); err != nil {
			t.Fatal(err)
		}
	}
	instances := g.list()
	if len(instances) != 1 || !instances[0].Stopping || g.server.VMDeletes != 1 {
		t.Fatalf("deleting VM: list = %+v, deletes = %d", instances, g.server.VMDeletes)
	}
	g.server.FinishDeletes()
	if len(g.list()) != 0 {
		t.Fatal("a deleted VM still lists")
	}
}

func TestListScopesByRegionInstallationAndState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	tagged := func(inst, runner string) map[string]*string {
		return map[string]*string{TagInstallation: to.Ptr(inst), TagRunner: to.Ptr(runner)}
	}
	f.server.PutVM("airship-other-installation", armcompute.VirtualMachine{Location: to.Ptr(region), Tags: tagged("43", "r-other")})
	f.server.PutVM("airship-other-region", armcompute.VirtualMachine{Location: to.Ptr("uksouth"), Tags: tagged("42", "r-region")})
	f.server.PutVM("airship-untagged", armcompute.VirtualMachine{Location: to.Ptr(region)})
	f.server.PutVM("airship-stopped", armcompute.VirtualMachine{Location: to.Ptr(region), Tags: tagged("42", "r-stopped")})
	f.server.SetPowerState("airship-stopped", "stopped")
	f.server.PutVM("airship-deallocated", armcompute.VirtualMachine{Location: to.Ptr(region), Tags: tagged("42", "r-deallocated")})
	f.server.SetPowerState("airship-deallocated", "deallocated")
	f.server.PutVM("airship-never-allocated", armcompute.VirtualMachine{
		Location: to.Ptr(region), Tags: tagged("42", "r-never"),
		Properties: &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr("Failed")},
	})
	f.server.SetPowerState("airship-never-allocated", "")
	f.server.PutVM("airship-failed-but-running", armcompute.VirtualMachine{
		Location: to.Ptr(region), Tags: tagged("42", "r-failed-running"),
		Properties: &armcompute.VirtualMachineProperties{ProvisioningState: to.Ptr("Failed")},
	})
	// A VM with the region's name in another case lists all the same.
	f.server.PutVM("airship-cased", armcompute.VirtualMachine{Location: to.Ptr("EastUS"), Tags: tagged("42", "r-cased")})

	got := map[string]cloud.Instance{}
	for _, i := range f.list() {
		got[i.InstanceID] = i
	}
	want := map[string]cloud.Instance{
		name:                         {RunnerID: runnerID, InstanceID: name},
		"airship-stopped":            {RunnerID: "r-stopped", InstanceID: "airship-stopped", Stopped: true},
		"airship-deallocated":        {RunnerID: "r-deallocated", InstanceID: "airship-deallocated", Stopped: true},
		"airship-never-allocated":    {RunnerID: "r-never", InstanceID: "airship-never-allocated", Stopped: true},
		"airship-failed-but-running": {RunnerID: "r-failed-running", InstanceID: "airship-failed-but-running"},
		"airship-cased":              {RunnerID: "r-cased", InstanceID: "airship-cased"},
	}
	if len(got) != len(want) {
		t.Fatalf("list = %+v, want %+v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("list[%s] = %+v, want %+v", id, got[id], w)
		}
	}
	if _, err := f.provider.List(context.Background(), testSecret, "RegionOne", installation); err == nil {
		t.Fatal("a malformed region was accepted")
	}
}

func TestListFailsRatherThanReportingNothing(t *testing.T) {
	shorten(t)
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	f.server.UnreachableList = true
	if _, err := f.provider.List(context.Background(), testSecret, region, installation); err == nil {
		t.Fatal("an unreachable API listed nothing without an error: the reconciler would mark every runner lost")
	}
	f.server.UnreachableList = false
	f.server.HangList = true
	start := time.Now()
	if _, err := f.provider.List(context.Background(), testSecret, region, installation); err == nil {
		t.Fatal("a hung list returned")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the list did not time out")
	}
}

func TestSweepRemovesOrphanedNetworkResources(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	tags := func(created string) map[string]*string {
		return map[string]*string{TagInstallation: to.Ptr("42"), TagCreated: to.Ptr(created)}
	}
	f.server.PutNIC("orphan-nic", armnetwork.Interface{Location: to.Ptr(region), Tags: tags(old)})
	f.server.PutNIC("fresh-nic", armnetwork.Interface{Location: to.Ptr(region), Tags: tags(fresh)})
	f.server.PutNIC("other-installation-nic", armnetwork.Interface{Location: to.Ptr(region), Tags: map[string]*string{TagInstallation: to.Ptr("43"), TagCreated: to.Ptr(old)}})
	f.server.PutNIC("other-region-nic", armnetwork.Interface{Location: to.Ptr("uksouth"), Tags: tags(old)})
	f.server.PutNIC("untagged-nic", armnetwork.Interface{Location: to.Ptr(region)})
	f.server.PutPublicIP("orphan-ip", armnetwork.PublicIPAddress{Location: to.Ptr(region), Tags: tags(old)})
	f.server.PutPublicIP("fresh-ip", armnetwork.PublicIPAddress{Location: to.Ptr(region), Tags: tags(fresh)})
	f.server.PutPublicIP("untagged-ip", armnetwork.PublicIPAddress{Location: to.Ptr(region)})
	// An old address still attached to an old orphaned interface: the
	// interface goes first, the address once it is free (next sweep).
	f.server.PutPublicIP("attached-ip", armnetwork.PublicIPAddress{Location: to.Ptr(region), Tags: tags(old), Properties: &armnetwork.PublicIPAddressPropertiesFormat{IPConfiguration: &armnetwork.IPConfiguration{ID: to.Ptr("x")}}})

	if err := f.provider.Sweep(context.Background(), testSecret, region, installation); err != nil {
		t.Fatal(err)
	}
	nics, ips := f.server.NICs(), f.server.PublicIPs()
	for _, kept := range []string{name + "-nic", "fresh-nic", "other-installation-nic", "other-region-nic", "untagged-nic"} {
		if nics[kept] == nil {
			t.Fatalf("interface %s was swept", kept)
		}
	}
	if nics["orphan-nic"] != nil {
		t.Fatal("the orphaned interface was kept")
	}
	for _, kept := range []string{name + "-ip", "fresh-ip", "untagged-ip", "attached-ip"} {
		if ips[kept] == nil {
			t.Fatalf("address %s was swept", kept)
		}
	}
	if ips["orphan-ip"] != nil {
		t.Fatal("the orphaned address was kept")
	}
	// The sweep is rate limited per scope: a second call right away does
	// nothing, another scope's goes ahead.
	f.server.PutNIC("orphan-nic-2", armnetwork.Interface{Location: to.Ptr(region), Tags: tags(old)})
	if err := f.provider.Sweep(context.Background(), testSecret, region, installation); err != nil || f.server.NICs()["orphan-nic-2"] == nil {
		t.Fatalf("second sweep: err = %v, swept = %v", err, f.server.NICs()["orphan-nic-2"] == nil)
	}
	f.server.PutNIC("uk-orphan", armnetwork.Interface{Location: to.Ptr("uksouth"), Tags: tags(old)})
	if err := f.provider.Sweep(context.Background(), testSecret, "uksouth", installation); err != nil || f.server.NICs()["uk-orphan"] != nil {
		t.Fatalf("other region's sweep: err = %v, kept = %v", err, f.server.NICs()["uk-orphan"] != nil)
	}
}

func TestImages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.AddPlatformImage(region, "Canonical", "0001-com-ubuntu-server-jammy", "22_04-lts-gen2", "22.04.202310010", armcompute.VirtualMachineImageProperties{
		OSDiskImage:            &armcompute.OSDiskImage{OperatingSystem: to.Ptr(armcompute.OperatingSystemTypesLinux)},
		ImageDeprecationStatus: &armcompute.ImageDeprecationStatus{ImageState: to.Ptr(armcompute.ImageStateDeprecated)},
	})
	f.server.AddPlatformImage(region, "Canonical", "ubuntu-24_04-lts", "server", "24.04.202601010", armcompute.VirtualMachineImageProperties{
		Architecture: to.Ptr(armcompute.ArchitectureTypesX64),
		OSDiskImage:  &armcompute.OSDiskImage{OperatingSystem: to.Ptr(armcompute.OperatingSystemTypesLinux)},
		Plan:         &armcompute.PurchasePlan{Name: to.Ptr("plan"), Product: to.Ptr("product"), Publisher: to.Ptr("Canonical")},
	})
	f.server.AddPlatformImage(region, "Deprecated", "offer", "sku", "1.0.0", armcompute.VirtualMachineImageProperties{
		OSDiskImage:            &armcompute.OSDiskImage{OperatingSystem: to.Ptr(armcompute.OperatingSystemTypesLinux)},
		ImageDeprecationStatus: &armcompute.ImageDeprecationStatus{ImageState: to.Ptr(armcompute.ImageStateDeprecated)},
	})
	managedID := f.server.AddManagedImage("images", "runner-2026", armcompute.Image{
		Location:   to.Ptr(region),
		Properties: &armcompute.ImageProperties{StorageProfile: &armcompute.ImageStorageProfile{OSDisk: &armcompute.ImageOSDisk{OSType: to.Ptr(armcompute.OperatingSystemTypesLinux), OSState: to.Ptr(armcompute.OperatingSystemStateTypesGeneralized)}}},
	})
	specializedID := f.server.AddManagedImage("images", "snapshot", armcompute.Image{
		Location:   to.Ptr(region),
		Properties: &armcompute.ImageProperties{StorageProfile: &armcompute.ImageStorageProfile{OSDisk: &armcompute.ImageOSDisk{OSType: to.Ptr(armcompute.OperatingSystemTypesLinux), OSState: to.Ptr(armcompute.OperatingSystemStateTypesSpecialized)}}},
	})
	galleryID := f.server.AddGalleryImage("images", "ci", "ubuntu-arm", armcompute.GalleryImage{
		Location: to.Ptr(region),
		Properties: &armcompute.GalleryImageProperties{
			OSType: to.Ptr(armcompute.OperatingSystemTypesLinux), OSState: to.Ptr(armcompute.OperatingSystemStateTypesGeneralized),
			Architecture: to.Ptr(armcompute.ArchitectureArm64),
		},
	}, "1.0.0", "1.2.0")
	windowsGalleryID := f.server.AddGalleryImage("images", "ci", "win", armcompute.GalleryImage{
		Location: to.Ptr(region),
		Properties: &armcompute.GalleryImageProperties{
			OSType: to.Ptr(armcompute.OperatingSystemTypesWindows), OSState: to.Ptr(armcompute.OperatingSystemStateTypesSpecialized),
			Features: []*armcompute.GalleryImageFeature{{Name: to.Ptr("SecurityType"), Value: to.Ptr("TrustedLaunchSupported")}},
		},
	}, "2.0.0")

	cases := map[string]struct {
		image, arch string
		check       func(*testing.T, *armcompute.VirtualMachine)
		wantErr     string
	}{
		"default alias on arm64": {"", "arm64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if *vm.Properties.StorageProfile.ImageReference.SKU != "22_04-lts-arm64" {
				t.Fatalf("sku = %s", *vm.Properties.StorageProfile.ImageReference.SKU)
			}
		}, ""},
		"urn with version": {"Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:22.04.202601010", "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if *vm.Properties.StorageProfile.ImageReference.Version != "22.04.202601010" {
				t.Fatalf("version = %s", *vm.Properties.StorageProfile.ImageReference.Version)
			}
		}, ""},
		"urn with a plan": {"Canonical:ubuntu-24_04-lts:server:latest", "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if vm.Plan == nil || *vm.Plan.Product != "product" {
				t.Fatalf("plan = %+v, want the image's purchase plan", vm.Plan)
			}
		}, ""},
		"urn latest skips nothing but picks newest for properties": {"Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest", "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if *vm.Properties.StorageProfile.ImageReference.Version != "latest" {
				t.Fatal("latest should be left for Azure to resolve")
			}
		}, ""},
		"deprecated":    {"Deprecated:offer:sku:latest", "x64", nil, "deprecated"},
		"unknown urn":   {"Nobody:offer:sku:latest", "x64", nil, "not found"},
		"arch mismatch": {"Canonical:0001-com-ubuntu-server-jammy:22_04-lts-arm64:latest", "x64", nil, "is arm64 but"},
		"managed image": {managedID, "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if *vm.Properties.StorageProfile.ImageReference.ID != managedID || vm.Properties.OSProfile == nil {
				t.Fatalf("image = %+v", *vm.Properties.StorageProfile.ImageReference)
			}
		}, ""},
		"specialized managed image": {specializedID, "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if vm.Properties.OSProfile != nil {
				t.Fatal("a specialized image takes no OS profile")
			}
			if cmd := f.server.RunCommand(*vm.Name, runCommandName); cmd == nil || !strings.HasPrefix(*cmd.Properties.Source.Script, "#!/") {
				t.Fatalf("a specialized Linux image boots through a run command: %+v", cmd)
			}
		}, ""},
		"missing managed image": {"/subscriptions/" + subscription + "/resourceGroups/images/providers/Microsoft.Compute/images/nope", "x64", nil, "not found"},
		"other subscription":    {"/subscriptions/ffffffff-ffff-4fff-8fff-ffffffffffff/resourceGroups/images/providers/Microsoft.Compute/images/runner-2026", "x64", nil, "another subscription"},
		"gallery image": {galleryID, "arm64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if *vm.Properties.StorageProfile.ImageReference.ID != galleryID {
				t.Fatalf("image = %s", *vm.Properties.StorageProfile.ImageReference.ID)
			}
		}, ""},
		"gallery image version": {galleryID + "/versions/1.2.0", "arm64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if !strings.HasSuffix(*vm.Properties.StorageProfile.ImageReference.ID, "/versions/1.2.0") {
				t.Fatalf("image = %s", *vm.Properties.StorageProfile.ImageReference.ID)
			}
		}, ""},
		"missing gallery version": {galleryID + "/versions/9.9.9", "arm64", nil, "not found"},
		"gallery arch mismatch":   {galleryID, "x64", nil, "is arm64 but"},
		"specialized windows gallery image": {windowsGalleryID, "x64", func(t *testing.T, vm *armcompute.VirtualMachine) {
			if vm.Properties.OSProfile != nil || vm.Properties.SecurityProfile == nil {
				t.Fatalf("specialized Windows: os profile = %v, security = %v", vm.Properties.OSProfile, vm.Properties.SecurityProfile)
			}
			if cmd := f.server.RunCommand(*vm.Name, runCommandName); cmd == nil || strings.HasPrefix(*cmd.Properties.Source.Script, "#!/") {
				t.Fatalf("a Windows image boots through a PowerShell run command: %+v", cmd)
			}
		}, ""},
		"not an image": {"ubuntu", "x64", nil, "not an image URN"},
	}
	i := 0
	for tc, c := range cases {
		t.Run(tc, func(t *testing.T) {
			i++
			l := testLaunch()
			l.RunnerID = runnerID[:len(runnerID)-2] + string(rune('a'+i/10)) + string(rune('a'+i%10))
			l.RunnerName = name + "-" + string(rune('a'+i/10)) + string(rune('a'+i%10))
			l.Machine.Image, l.Machine.Arch = c.image, c.arch
			if c.arch == "arm64" {
				l.Machine.InstanceType = "Standard_D4ps_v5"
			}
			nicsBefore := len(f.server.NICs())
			id, err := f.launch(l)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) || cloud.IsRetryable(err) {
					t.Fatalf("err = %v, want a permanent error mentioning %q", err, c.wantErr)
				}
				if len(f.server.NICs()) != nicsBefore {
					t.Fatal("an image that cannot be resolved should make no resources")
				}

				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, f.server.VM(id))
		})
	}
}

func TestImageAndSetupCachesAreBounded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	for i := range cacheLimit + 10 {
		f.provider.cachePutImage(strings.Repeat("k", 3)+string(rune(i)), resolvedImage{})
		f.provider.cachePutSetup(strings.Repeat("s", 3) + string(rune(i)))
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.images) > cacheLimit || len(f.provider.setups) > cacheLimit {
		t.Fatalf("caches grew past the limit: %d images, %d setups", len(f.provider.images), len(f.provider.setups))
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
	raw, err := EncodeSecret(Secret{SubscriptionID: " " + strings.ToUpper(subscription) + " ", TenantID: tenant, ClientID: clientID, ClientSecret: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSecret(raw)
	if err != nil || s.SubscriptionID != subscription || s.ClientSecret != "abc" {
		t.Fatalf("round trip = %+v, %v", s, err)
	}
	bad := []Secret{
		{SubscriptionID: "not-a-guid", TenantID: tenant, ClientID: clientID, ClientSecret: "abc"},
		{SubscriptionID: subscription, TenantID: "", ClientID: clientID, ClientSecret: "abc"},
		{SubscriptionID: subscription, TenantID: tenant, ClientID: clientID, ClientSecret: ""},
		{SubscriptionID: subscription, TenantID: tenant, ClientID: clientID, ClientSecret: "with space"},
		{SubscriptionID: subscription, TenantID: tenant, ClientID: clientID, ClientSecret: strings.Repeat("x", maxClientSecretLength+1)},
	}
	for _, b := range bad {
		if _, err := EncodeSecret(b); err == nil {
			t.Fatalf("%+v was accepted", b)
		}
	}
	if _, err := ParseSecret([]byte(strings.Repeat("x", maxSecretBytes+1))); err == nil {
		t.Fatal("an oversized secret was accepted")
	}
}

func TestResourceGroupCreationNeedsContributor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.RefuseGroupCreate = true
	_, err := f.launch(testLaunch())
	if err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "AuthorizationFailed") {
		t.Fatalf("err = %v, want a permanent authorization error", err)
	}
}

func TestNames(t *testing.T) {
	t.Parallel()
	if got := computerName("airship-0a1b2c3d4e5f"); got != "air0a1b2c3d4e5f" {
		t.Fatalf("computer name = %s", got)
	}
	if got := computerName("a-very-long-runner-name-indeed"); len(got) != maxWindowsComputerName || !strings.HasPrefix(got, "air") {
		t.Fatalf("hashed computer name = %s", got)
	}
	if !inResourceGroup("/subscriptions/x/resourceGroups/Airship-Runners/providers/Microsoft.Compute/virtualMachines/a") || inResourceGroup("/subscriptions/x/resourceGroups/other/providers/Microsoft.Compute/virtualMachines/a") {
		t.Fatal("resource group membership")
	}
	for _, ok := range []string{"eastus", "uksouth", "germanywestcentral"} {
		if !IsRegion(ok) {
			t.Fatalf("%s should be a region", ok)
		}
	}
	for _, bad := range []string{"East US", "us", "eastus/", "EASTUS"} {
		if IsRegion(bad) {
			t.Fatalf("%q should not be a region", bad)
		}
	}
	for _, ref := range []string{"Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest", "a:b:c:1.2.3", "/subscriptions/" + subscription + "/resourceGroups/rg/providers/Microsoft.Compute/images/x", "/subscriptions/" + subscription + "/resourceGroups/rg/providers/Microsoft.Compute/galleries/g/images/i/versions/1.0.0", "ubuntu2204", "ubuntu2404"} {
		if !IsImageRef(ref) {
			t.Fatalf("%q should be an image reference", ref)
		}
	}
	for _, ref := range []string{"ubuntu", "a:b:c", "a:b:c:d", "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Compute/disks/x"} {
		if IsImageRef(ref) {
			t.Fatalf("%q should not be an image reference", ref)
		}
	}
}
