package gcp

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/gcp/gcptest"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	project  = "acme-ci-123456"
	region   = "us-central1"
	runnerID = "0192f3a4-5b6c-7d8e-9f00-112233445566"
	name     = "airship-0a1b2c3d4e5f"
)

var zones = []string{"us-central1-a", "us-central1-b", "us-central1-c"}

var testSecret = []byte(`{
  "type": "service_account",
  "project_id": "` + project + `",
  "private_key_id": "abc123",
  "private_key": "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----\n",
  "client_email": "airship@` + project + `.iam.gserviceaccount.com",
  "client_id": "1234567890",
  "token_uri": "https://oauth2.googleapis.com/token"
}`)

type fixture struct {
	t        *testing.T
	server   *gcptest.Server
	provider *Provider
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	server := gcptest.New(t, project)
	server.AddRegion(project, region, zones...)
	server.AddRegion(project, "europe-west1", "europe-west1-b", "europe-west1-c")
	server.AddProject("ubuntu-os-cloud")
	server.AddImage("ubuntu-os-cloud", &computepb.Image{
		Name: proto.String("ubuntu-2204-jammy-v20260101"), Family: proto.String("ubuntu-2204-lts"),
		Architecture: proto.String(computepb.Image_X86_64.String()),
	})
	server.AddImage("ubuntu-os-cloud", &computepb.Image{
		Name: proto.String("ubuntu-2204-jammy-arm64-v20260101"), Family: proto.String("ubuntu-2204-lts-arm64"),
		Architecture: proto.String(computepb.Image_ARM64.String()),
	})
	provider := New(Options{Endpoint: server.URL, NoAuth: true})

	return &fixture{t: t, server: server, provider: provider}
}

func testLaunch() cloud.Launch {
	return cloud.Launch{
		RunnerID:       runnerID,
		RunnerName:     name,
		InstallationID: 42,
		Repository:     "acme/widgets",
		Region:         region,
		Machine:        cloud.Machine{InstanceType: "n2-standard-4", Arch: "x64", Disk: cloud.Disk{SizeGB: 75}},
		Boot: cloud.Boot{
			JITConfig:          base64.StdEncoding.EncodeToString([]byte(`{"jit":"config"}`)),
			RunnerVersion:      "2.338.2",
			MaxLifetimeSeconds: 3600,
		},
	}
}

func (f *fixture) launch(l cloud.Launch) (string, error) {
	return f.provider.Launch(context.Background(), testSecret, l)
}

func (f *fixture) mustLaunch(l cloud.Launch) *computepb.Instance {
	f.t.Helper()
	id, err := f.launch(l)
	if err != nil {
		f.t.Fatalf("launch: %v", err)
	}
	zone, instanceName, err := ParseInstanceID(id)
	if err != nil {
		f.t.Fatal(err)
	}
	instance := f.server.Instance(project, zone, instanceName)
	if instance == nil {
		f.t.Fatalf("launch returned %s but no such instance exists", id)
	}

	return instance
}

func metadataValue(instance *computepb.Instance, key string) (string, bool) {
	for _, item := range instance.GetMetadata().GetItems() {
		if item.GetKey() == key {
			return item.GetValue(), true
		}
	}

	return "", false
}

func TestVerify(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	identity, err := f.provider.Verify(context.Background(), testSecret, "us-central1-b")
	if err != nil || identity.AccountID != project || !strings.HasPrefix(identity.Principal, "airship@") {
		t.Fatalf("verify = %+v, %v", identity, err)
	}
	if _, err := f.provider.Verify(context.Background(), testSecret, "RegionOne"); err == nil {
		t.Fatal("a non-GCP location was accepted")
	}
	if _, err := f.provider.Verify(context.Background(), []byte(`{"type":"authorized_user"}`), region); !errors.Is(err, cloud.ErrBadCredentials) {
		t.Fatalf("user credentials: err = %v, want bad credentials", err)
	}
	f.server.APIDisabled = true
	_, err = f.provider.Verify(context.Background(), testSecret, region)
	if !errors.Is(err, cloud.ErrBadCredentials) || !strings.Contains(err.Error(), "enable the Compute Engine API") {
		t.Fatalf("disabled API: err = %v", err)
	}
}

func TestLaunchRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	instance := f.mustLaunch(testLaunch())

	if instance.GetLabels()[LabelInstallation] != "42" || instance.GetLabels()[LabelRunner] != runnerID {
		t.Errorf("labels = %v", instance.GetLabels())
	}
	if !strings.HasSuffix(instance.GetMachineType(), "/machineTypes/n2-standard-4") || !strings.HasPrefix(instance.GetMachineType(), "zones/us-central1-") {
		t.Errorf("machine type = %s", instance.GetMachineType())
	}
	script, ok := metadataValue(instance, "startup-script")
	if !ok || !strings.HasPrefix(script, "#!/bin/bash") || !strings.Contains(script, "ACTIONS_RUNNER_INPUT_JITCONFIG") {
		t.Errorf("startup script missing or wrong: %.60q", script)
	}
	if repo, _ := metadataValue(instance, MetadataRepository); repo != "acme/widgets" {
		t.Errorf("repository metadata = %q", repo)
	}
	disk := instance.GetDisks()[0]
	if !disk.GetBoot() || !disk.GetAutoDelete() || disk.GetInitializeParams().GetDiskSizeGb() != 75 ||
		!strings.HasSuffix(disk.GetInitializeParams().GetDiskType(), "/diskTypes/pd-balanced") ||
		!strings.Contains(disk.GetInitializeParams().GetSourceImage(), "ubuntu-2204-jammy-v20260101") {
		t.Errorf("boot disk = %+v", disk)
	}
	nic := instance.GetNetworkInterfaces()[0]
	if nic.GetNetwork() != "global/networks/default" || len(nic.GetAccessConfigs()) != 1 || nic.GetAccessConfigs()[0].GetType() != "ONE_TO_ONE_NAT" {
		t.Errorf("network interface = %+v", nic)
	}
	sched := instance.GetScheduling()
	if sched.GetAutomaticRestart() || sched.GetOnHostMaintenance() != "MIGRATE" || sched.GetProvisioningModel() != "STANDARD" {
		t.Errorf("scheduling = %+v", sched)
	}
	if len(instance.GetServiceAccounts()) != 0 || len(instance.GetGuestAccelerators()) != 0 {
		t.Errorf("unexpected service accounts or accelerators: %+v", instance)
	}
	if instance.GetShieldedInstanceConfig().GetEnableSecureBoot() {
		t.Error("secure boot must stay off for the GPU driver path")
	}
	if items := instance.GetTags().GetItems(); len(items) != 1 || items[0] != NetworkTag {
		t.Errorf("network tags = %v", items)
	}
}

// Launching the same runner again finds the instance by name and does not
// insert another, whether the first call answered or not.
func TestRepeatedLaunchIsIdempotent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first, err := f.launch(testLaunch())
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.launch(testLaunch())
	if err != nil || second != first {
		t.Fatalf("second launch = %s, %v; want %s", second, err, first)
	}
	if f.server.Inserts != 1 || len(f.server.Instances()) != 1 {
		t.Fatalf("inserts = %d, instances = %d", f.server.Inserts, len(f.server.Instances()))
	}
	// Another runner may not take the name.
	other := testLaunch()
	other.RunnerID = "ffffffff-0000-0000-0000-000000000000"
	_, err = f.launch(other)
	if err == nil || cloud.IsRetryable(err) {
		t.Fatalf("name collision: err = %v, want a permanent error", err)
	}
}

func TestLaunchTriesAnotherZoneWhenOneIsExhausted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.ExhaustedZones = map[string]bool{"us-central1-a": true, "us-central1-b": true}
	f.server.MissingMachineTypeZones = map[string]bool{}
	instance := f.mustLaunch(testLaunch())
	if !strings.HasSuffix(instance.GetZone(), "/us-central1-c") {
		t.Fatalf("zone = %s, want the one with capacity", instance.GetZone())
	}
	if f.server.Inserts > 3 {
		t.Fatalf("inserts = %d", f.server.Inserts)
	}

	// A zone without the machine type is also skipped.
	g := newFixture(t)
	g.server.MissingMachineTypeZones = map[string]bool{"us-central1-a": true, "us-central1-b": true, "us-central1-c": false}
	instance = g.mustLaunch(testLaunch())
	if !strings.HasSuffix(instance.GetZone(), "/us-central1-c") {
		t.Fatalf("zone = %s, want the one offering the type", instance.GetZone())
	}

	// Everything exhausted: retryable, not ambiguous (the operation said so).
	h := newFixture(t)
	h.server.ExhaustedZones = map[string]bool{"us-central1-a": true, "us-central1-b": true, "us-central1-c": true}
	_, err := h.launch(testLaunch())
	if err == nil || !cloud.IsRetryable(err) || cloud.IsAmbiguous(err) || !strings.Contains(err.Error(), "ZONE_RESOURCE_POOL_EXHAUSTED") {
		t.Fatalf("all exhausted: err = %v", err)
	}
	if n := len(h.server.Instances()); n != 0 {
		t.Fatalf("%d instances left behind by failed inserts", n)
	}
}

func TestPinnedZoneDoesNotFallBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.ExhaustedZones = map[string]bool{"us-central1-b": true}
	l := testLaunch()
	l.Machine.Zone = "us-central1-b"
	_, err := f.launch(l)
	if err == nil || !cloud.IsRetryable(err) || f.server.Inserts != 1 {
		t.Fatalf("pinned zone: err = %v, inserts = %d", err, f.server.Inserts)
	}
	l.Machine.Zone = "europe-west1-b"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) {
		t.Fatalf("zone outside the region: err = %v, want permanent", err)
	}
}

func TestLaunchErrorsAreClassified(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		arrange   func(*gcptest.Server)
		retryable bool
	}{
		"quota after accept": {func(s *gcptest.Server) { s.OperationQuotaError = true }, true},
		"rate limited": {func(s *gcptest.Server) {
			s.InsertFailure = &gcptest.HTTPError{Status: 429, Reason: "rateLimitExceeded", Message: "slow down"}
		}, true},
		"quota 403": {func(s *gcptest.Server) {
			s.InsertFailure = &gcptest.HTTPError{Status: 403, Reason: "quotaExceeded", Message: "Quota 'CPUS' exceeded"}
		}, true},
		"forbidden": {func(s *gcptest.Server) {
			s.InsertFailure = &gcptest.HTTPError{Status: 403, Reason: "forbidden", Message: "Required 'compute.instances.create' permission"}
		}, false},
		"bad request": {func(s *gcptest.Server) {
			s.InsertFailure = &gcptest.HTTPError{Status: 400, Reason: "invalid", Message: "Invalid value for field 'resource.disks[0].initializeParams.diskSizeGb'"}
		}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			tc.arrange(f.server)
			_, err := f.launch(testLaunch())
			if err == nil || cloud.IsRetryable(err) != tc.retryable || cloud.IsAmbiguous(err) {
				t.Fatalf("err = %v, retryable = %v, want %v and not ambiguous", err, cloud.IsRetryable(err), tc.retryable)
			}
		})
	}
}

// A lost answer is settled by looking the instance up; only when even
// that fails is the launch reported ambiguous.
func TestLostAnswersAreSettledOrAmbiguous(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.DropNextInsert = true
	id, err := f.launch(testLaunch())
	if err != nil || f.server.Instance(project, id[:strings.Index(id, "/")], name) == nil {
		t.Fatalf("dropped answer, instance exists: id = %s, err = %v", id, err)
	}

	g := newFixture(t)
	g.server.DropNextInsert = true
	g.server.FailGetsAfterInsert = 1
	_, err = g.launch(testLaunch())
	if err == nil || !cloud.IsAmbiguous(err) || !cloud.IsRetryable(err) {
		t.Fatalf("dropped answer, lookup failed: err = %v, want ambiguous", err)
	}
	if n := len(g.server.Instances()); n != 1 {
		t.Fatalf("the fake should hold the instance the lost insert made, has %d", n)
	}
	// The retry finds it.
	id, err = g.launch(testLaunch())
	if err != nil || id == "" || g.server.Inserts != 1 {
		t.Fatalf("retry after ambiguity: id = %s, err = %v, inserts = %d", id, err, g.server.Inserts)
	}

	// A poll that fails is settled the same way.
	h := newFixture(t)
	h.server.FailNextOperationPoll = true
	if _, err := h.launch(testLaunch()); err != nil {
		t.Fatalf("failed poll: %v", err)
	}
}

func TestOperationTimeoutIsSettledByLookup(t *testing.T) {
	saved := operationTimeout
	operationTimeout = 300 * time.Millisecond
	t.Cleanup(func() { operationTimeout = saved })
	f := newFixture(t)
	f.server.HangOperations = true
	id, err := f.launch(testLaunch())
	if err != nil || id == "" {
		t.Fatalf("hung operation with a visible instance: id = %s, err = %v", id, err)
	}
}

func TestSpotAndGPU(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.Spot = true
	l.Machine.GPUType = "nvidia-tesla-t4"
	l.Machine.InstanceType = "n1-standard-4"
	l.Boot.GPU = true
	instance := f.mustLaunch(l)
	sched := instance.GetScheduling()
	if sched.GetProvisioningModel() != "SPOT" || sched.GetInstanceTerminationAction() != "DELETE" || sched.GetOnHostMaintenance() != "TERMINATE" || sched.GetAutomaticRestart() {
		t.Errorf("spot scheduling = %+v", sched)
	}
	acc := instance.GetGuestAccelerators()
	if len(acc) != 1 || !strings.HasSuffix(acc[0].GetAcceleratorType(), "/acceleratorTypes/nvidia-tesla-t4") || acc[0].GetAcceleratorCount() != 1 {
		t.Errorf("accelerators = %+v", acc)
	}
	if script, _ := metadataValue(instance, "startup-script"); !strings.Contains(script, "nvidia-smi") {
		t.Error("GPU launch did not ask the boot script to install drivers")
	}

	// A2 machines carry their GPUs; adding accelerators would be refused.
	g := newFixture(t)
	l = testLaunch()
	l.Machine.InstanceType = "a2-highgpu-1g"
	l.Machine.GPUType = "nvidia-tesla-a100"
	instance = g.mustLaunch(l)
	if len(instance.GetGuestAccelerators()) != 0 || instance.GetScheduling().GetOnHostMaintenance() != "TERMINATE" {
		t.Errorf("a2 instance = %+v", instance.GetScheduling())
	}

	// An explicit maintenance policy is honored.
	h := newFixture(t)
	l = testLaunch()
	l.Machine.GCP = &cloud.GCPOptions{OnHostMaintenance: "TERMINATE"}
	if instance = h.mustLaunch(l); instance.GetScheduling().GetOnHostMaintenance() != "TERMINATE" {
		t.Errorf("scheduling = %+v", instance.GetScheduling())
	}
}

func TestExtraConfigOptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.Disk = cloud.Disk{SizeGB: 120, Type: "pd-ssd"}
	l.Machine.GCP = &cloud.GCPOptions{
		AdditionalDisk: &cloud.GCPDisk{SizeGB: 42, Type: "projects/" + project + "/zones/us-central1-a/diskTypes/pd-balanced"},
		NetworkInterfaces: []cloud.GCPNetworkInterface{{
			NicType: "GVNIC", Network: "global/networks/ci", Subnetwork: "projects/" + project + "/regions/us-central1/subnetworks/ci", StackType: "IPV4_ONLY",
			AccessConfigs: []cloud.GCPAccessConfig{{Type: "ONE_TO_ONE_NAT", Name: "External NAT", NetworkTier: "PREMIUM"}},
		}},
		ServiceAccounts: []cloud.GCPServiceAccount{{Email: "runner@" + project + ".iam.gserviceaccount.com", Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}}},
	}
	instance := f.mustLaunch(l)
	boot := instance.GetDisks()[0].GetInitializeParams()
	if boot.GetDiskSizeGb() != 120 || !strings.HasSuffix(boot.GetDiskType(), "/diskTypes/pd-ssd") {
		t.Errorf("boot disk = %+v", boot)
	}
	extra := instance.GetDisks()[1]
	// Cirun documents a zonal diskType path; the zone in it is whatever the
	// user copied, so only the type survives and the launch zone is used.
	zone := strings.TrimPrefix(instance.GetZone(), f.server.URL+"/compute/v1/projects/"+project+"/zones/")
	if extra.GetBoot() || !extra.GetAutoDelete() || extra.GetInitializeParams().GetDiskSizeGb() != 42 || extra.GetInitializeParams().GetDiskType() != "zones/"+zone+"/diskTypes/pd-balanced" {
		t.Errorf("additional disk = %+v (zone %s)", extra, zone)
	}
	nic := instance.GetNetworkInterfaces()[0]
	if nic.GetNicType() != "GVNIC" || nic.GetNetwork() != "global/networks/ci" || !strings.HasSuffix(nic.GetSubnetwork(), "/subnetworks/ci") || nic.GetStackType() != "IPV4_ONLY" || len(nic.GetAccessConfigs()) != 1 {
		t.Errorf("nic = %+v", nic)
	}
	if sa := instance.GetServiceAccounts(); len(sa) != 1 || !strings.HasPrefix(sa[0].GetEmail(), "runner@") || len(sa[0].GetScopes()) != 1 {
		t.Errorf("service accounts = %+v", sa)
	}
}

// A customer's disk is attached by reference, never auto-deleted, in its
// own zone whatever the region's other zones offer; a disk in use by
// another runner is a retryable failure, and a disk elsewhere a permanent
// one.
func TestExistingDisk(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.ExhaustedZones = map[string]bool{"us-central1-a": true, "us-central1-c": true}
	l := testLaunch()
	l.Machine.GCP = &cloud.GCPOptions{ExistingDisk: &cloud.GCPExistingDisk{Source: "zones/us-central1-b/disks/ccache", Mode: "READ_WRITE", Interface: "NVME"}}
	instance := f.mustLaunch(l)
	if !strings.HasSuffix(instance.GetZone(), "/us-central1-b") {
		t.Fatalf("zone = %s, want the disk's", instance.GetZone())
	}
	disks := instance.GetDisks()
	if len(disks) != 2 || disks[1].GetAutoDelete() || disks[1].GetBoot() || disks[1].GetSource() != "projects/"+project+"/zones/us-central1-b/disks/ccache" ||
		disks[1].GetMode() != "READ_WRITE" || disks[1].GetInterface() != "NVME" || disks[1].GetDeviceName() != "ccache" {
		t.Fatalf("disks = %+v", disks)
	}
	if f.server.Inserts != 1 {
		t.Fatalf("inserts = %d; a zonal disk must pin the zone, not try others", f.server.Inserts)
	}

	// The disk's zone is exhausted: no other zone is tried.
	g := newFixture(t)
	g.server.ExhaustedZones = map[string]bool{"us-central1-b": true}
	if _, err := g.launch(l); err == nil || !cloud.IsRetryable(err) || g.server.Inserts != 1 {
		t.Fatalf("exhausted disk zone: err = %v, inserts = %d", err, g.server.Inserts)
	}

	// Attached read-write elsewhere: retry later.
	h := newFixture(t)
	h.server.DisksInUse = map[string]bool{"ccache": true}
	if _, err := h.launch(l); err == nil || !cloud.IsRetryable(err) || cloud.IsAmbiguous(err) || !strings.Contains(err.Error(), "RESOURCE_IN_USE_BY_ANOTHER_RESOURCE") {
		t.Fatalf("disk in use: err = %v", err)
	}
	readOnly := testLaunch()
	readOnly.RunnerID, readOnly.RunnerName = "44444444-0000-0000-0000-000000000000", "airship-readonly"
	readOnly.Machine.GCP = &cloud.GCPOptions{ExistingDisk: &cloud.GCPExistingDisk{Source: "projects/" + project + "/zones/us-central1-b/disks/ccache", Mode: "READ_ONLY"}}
	if _, err := h.launch(readOnly); err != nil {
		t.Fatalf("read-only attach of a disk in use: %v", err)
	}

	// A regional disk leaves the zone choice free; disks elsewhere and a
	// pinned zone that differs from the disk's are refused before any call.
	k := newFixture(t)
	regional := testLaunch()
	regional.Machine.GCP = &cloud.GCPOptions{ExistingDisk: &cloud.GCPExistingDisk{Source: "regions/us-central1/disks/shared"}}
	if instance := k.mustLaunch(regional); instance.GetDisks()[1].GetSource() != "projects/"+project+"/regions/us-central1/disks/shared" {
		t.Fatalf("regional disk = %+v", instance.GetDisks()[1])
	}
	for name, source := range map[string]string{"other region": "zones/europe-west1-b/disks/d", "regional elsewhere": "regions/europe-west1/disks/d", "garbage": "my-disk"} {
		bad := testLaunch()
		bad.Machine.GCP = &cloud.GCPOptions{ExistingDisk: &cloud.GCPExistingDisk{Source: source}}
		if _, err := k.launch(bad); err == nil || cloud.IsRetryable(err) {
			t.Errorf("%s: err = %v, want permanent", name, err)
		}
	}
	pinnedElsewhere := testLaunch()
	pinnedElsewhere.Machine.Zone = "us-central1-a"
	pinnedElsewhere.Machine.GCP = l.Machine.GCP
	if _, err := k.launch(pinnedElsewhere); err == nil || cloud.IsRetryable(err) {
		t.Errorf("pinned zone differs from the disk's: err = %v, want permanent", err)
	}
	if k.server.Inserts != 1 {
		t.Errorf("refused disks reached the API: inserts = %d", k.server.Inserts)
	}

	// Deleting the machine leaves the disk: the fake only knows instances,
	// so the contract is the autoDelete flag checked above.
}

// Without a public IP the interface gets no access config and the region
// gets one Cloud NAT, created once.
func TestNoPublicIPCreatesOneNAT(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.GCP = &cloud.GCPOptions{NoPublicIP: true}
	instance := f.mustLaunch(l)
	if len(instance.GetNetworkInterfaces()[0].GetAccessConfigs()) != 0 {
		t.Errorf("interface has an access config: %+v", instance.GetNetworkInterfaces()[0])
	}
	routers := f.server.Routers()
	router, ok := routers[project+"/"+region+"/"+NATName]
	if !ok || len(router.GetNats()) != 1 || router.GetNats()[0].GetNatIpAllocateOption() != "AUTO_ONLY" || router.GetNetwork() != "global/networks/default" {
		t.Fatalf("routers = %+v", routers)
	}
	second := l
	second.RunnerID, second.RunnerName = "11111111-2222-3333-4444-555555555555", "airship-second"
	f.mustLaunch(second)
	if len(f.server.Routers()) != 1 {
		t.Fatalf("a second launch made another router: %+v", f.server.Routers())
	}

	// A definition that names only a subnetwork launches on that
	// subnetwork's network, so that is where its NAT goes, not the default.
	f.server.AddSubnetwork(project, region, "private", "ci")
	third := l
	third.RunnerID, third.RunnerName = "22222222-2222-3333-4444-555555555555", "airship-third"
	third.Machine.GCP = &cloud.GCPOptions{NoPublicIP: true, NetworkInterfaces: []cloud.GCPNetworkInterface{{Subnetwork: "regions/" + region + "/subnetworks/private"}}}
	f.mustLaunch(third)
	routers = f.server.Routers()
	ci, ok := routers[project+"/"+region+"/"+NATName+"-ci"]
	if len(routers) != 2 || !ok || !strings.HasSuffix(ci.GetNetwork(), "/global/networks/ci") {
		t.Fatalf("a runner on another network needs a NAT of its own; routers = %+v", routers)
	}
	if got := natName("global/networks/a-very-long-network-name-that-goes-on-and-on-and-on-past-the-limit"); len(got) != 63 || !strings.HasPrefix(got, NATName+"-a-very-long") {
		t.Errorf("long network NAT name = %q (%d)", got, len(got))
	}
	fourth := third
	fourth.RunnerID, fourth.RunnerName = "33333333-2222-3333-4444-555555555555", "airship-fourth"
	fourth.Machine.GCP = &cloud.GCPOptions{NoPublicIP: true, NetworkInterfaces: []cloud.GCPNetworkInterface{{Subnetwork: "regions/" + region + "/subnetworks/missing"}}}
	if _, err := f.launch(fourth); err == nil || !strings.Contains(err.Error(), "subnetwork") {
		t.Fatalf("a missing subnetwork must fail the launch, got %v", err)
	}
}

// A router with Airship's name that it did not make is read, not
// trusted: on another network, or without a NAT, the launch fails and
// says so, whether the router was found by the first read or only by
// the conflict that a racing create reports.
func TestNATRefusesARouterItCannotUse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := testLaunch()
	l.Machine.GCP = &cloud.GCPOptions{NoPublicIP: true}

	f.server.PutRouter(project, region, &computepb.Router{Name: proto.String(NATName), Network: proto.String("global/networks/other"), Nats: []*computepb.RouterNat{{Name: proto.String("x")}}})
	if _, err := f.launch(l); err == nil || !strings.Contains(err.Error(), "move or rename") {
		t.Fatalf("a router on another network was accepted: %v", err)
	}
	// The same, found only through the conflicting create.
	f.server.MissRouterGets = 1
	if _, err := f.launch(l); err == nil || !strings.Contains(err.Error(), "move or rename") {
		t.Fatalf("after a conflicting create, a router on another network was accepted: %v", err)
	}
	if len(f.server.Instances()) != 0 {
		t.Fatal("an instance was launched without egress")
	}

	f.server.PutRouter(project, region, &computepb.Router{Name: proto.String(NATName), Network: proto.String("global/networks/default")})
	if _, err := f.launch(l); err == nil || !strings.Contains(err.Error(), "has no NAT") {
		t.Fatalf("a router without a NAT was accepted: %v", err)
	}

	// A concurrent launch's router is the one case the conflict stands
	// for; it is accepted after reading it back.
	f.server.PutRouter(project, region, &computepb.Router{Name: proto.String(NATName), Network: proto.String("global/networks/default"), Nats: []*computepb.RouterNat{{Name: proto.String(NATName)}}})
	f.server.MissRouterGets = 1
	f.mustLaunch(l)
}

// A zone whose list never answers fails the scope's list in bounded time
// rather than stalling the reconciler; nothing is decided about the
// zone's instances, as with any list error.
func TestListIsBounded(t *testing.T) {
	saved := listTimeout
	listTimeout = 300 * time.Millisecond
	t.Cleanup(func() { listTimeout = saved })
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	f.server.HangListZones = map[string]bool{zones[0]: true, zones[1]: true, zones[2]: true}
	start := time.Now()
	_, err := f.provider.List(context.Background(), testSecret, region, 42)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung list returned %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("hung list took %s", time.Since(start))
	}
}

func TestImages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.AddImage(project, &computepb.Image{Name: proto.String("ci-base-v3"), Family: proto.String("ci-base"), Architecture: proto.String("X86_64")})
	f.server.AddImage(project, &computepb.Image{
		Name: proto.String("win-runner"), Architecture: proto.String("X86_64"),
		GuestOsFeatures: []*computepb.GuestOsFeature{{Type: proto.String("WINDOWS")}},
	})
	f.server.AddMachineImage(project, &computepb.MachineImage{Name: proto.String("golden")})
	f.server.AddProject("airship-labs")
	f.server.AddImage("airship-labs", &computepb.Image{Name: proto.String("airship-nvidia"), Architecture: proto.String("X86_64")})

	cases := map[string]struct {
		image, wantSource string
		machineImage      bool
		windows           bool
	}{
		"default":       {"", "ubuntu-2204-jammy-v20260101", false, false},
		"alias":         {"ubuntu2204", "ubuntu-2204-jammy-v20260101", false, false},
		"public family": {"ubuntu-2204-lts", "ubuntu-2204-jammy-v20260101", false, false},
		"own family":    {"ci-base", "ci-base-v3", false, false},
		"own image":     {"ci-base-v3", "ci-base-v3", false, false},
		"scoped":        {"airship-labs:airship-nvidia", "airship-nvidia", false, false},
		"path":          {"projects/airship-labs/global/images/airship-nvidia", "airship-nvidia", false, false},
		"self link":     {"https://www.googleapis.com/compute/v1/projects/airship-labs/global/images/airship-nvidia", "airship-nvidia", false, false},
		"windows":       {"win-runner", "win-runner", false, true},
		"machine image": {project + ":golden", "golden", true, false},
		"machine path":  {"projects/" + project + "/global/machineImages/golden", "golden", true, false},
		"machine beta":  {"https://www.googleapis.com/compute/beta/projects/" + project + "/global/machineImages/golden", "golden", true, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l := testLaunch()
			l.RunnerName = "airship-" + strings.ReplaceAll(name, " ", "-")
			l.RunnerID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
			l.Machine.Image = tc.image
			instance := f.mustLaunch(l)
			if tc.machineImage {
				if len(instance.GetDisks()) != 0 {
					t.Errorf("machine image launch set disks: %+v", instance.GetDisks())
				}

				return
			}
			if src := instance.GetDisks()[0].GetInitializeParams().GetSourceImage(); !strings.HasSuffix(src, "/"+tc.wantSource) {
				t.Errorf("source image = %s, want %s", src, tc.wantSource)
			}
			_, windows := metadataValue(instance, "windows-startup-script-ps1")
			if windows != tc.windows {
				t.Errorf("windows script present = %v, want %v", windows, tc.windows)
			}
			if tc.windows {
				script, _ := metadataValue(instance, "windows-startup-script-ps1")
				if strings.Contains(script, "<powershell>") || !strings.Contains(script, "actions-runner-win-x64") {
					t.Errorf("windows script = %.80q", script)
				}
			}
		})
	}

	// Mismatched architecture and missing images are permanent.
	l := testLaunch()
	l.Machine.InstanceType, l.Machine.Arch = "t2a-standard-4", "arm64"
	l.Machine.Image = "ci-base"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "arm64") {
		t.Errorf("arch mismatch: err = %v", err)
	}
	l = testLaunch()
	l.Machine.Image = "no-such-image"
	if _, err := f.launch(l); err == nil || cloud.IsRetryable(err) || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing image: err = %v", err)
	}
	if n := f.server.Inserts; n != len(cases) {
		t.Errorf("inserts = %d, want one per case and none for refused images", n)
	}
}

func TestListScopesByRegionAndInstallation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustLaunch(testLaunch())
	other := testLaunch()
	other.RunnerID, other.RunnerName, other.InstallationID = "22222222-0000-0000-0000-000000000000", "airship-other", 7
	f.mustLaunch(other)
	elsewhere := testLaunch()
	elsewhere.RunnerID, elsewhere.RunnerName, elsewhere.Region = "33333333-0000-0000-0000-000000000000", "airship-eu", "europe-west1"
	f.mustLaunch(elsewhere)
	f.server.PutInstance(project, "us-central1-a", &computepb.Instance{Name: proto.String("not-ours")})

	list, err := f.provider.List(context.Background(), testSecret, region, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].RunnerID != runnerID || !strings.HasSuffix(list[0].InstanceID, "/"+name) || list[0].Stopped || list[0].Stopping {
		t.Fatalf("list = %+v", list)
	}
	zone, _, _ := ParseInstanceID(list[0].InstanceID)
	f.server.SetStatus(project, zone, name, "TERMINATED")
	list, _ = f.provider.List(context.Background(), testSecret, region, 42)
	if len(list) != 1 || !list[0].Stopped {
		t.Fatalf("terminated instance not reported stopped: %+v", list)
	}
	f.server.SetStatus(project, zone, name, "STOPPING")
	list, _ = f.provider.List(context.Background(), testSecret, region, 42)
	if len(list) != 1 || !list[0].Stopping || list[0].Stopped {
		t.Fatalf("stopping instance: %+v", list)
	}

	f.server.UnreachableZones = map[string]bool{"us-central1-b": true}
	if _, err := f.provider.List(context.Background(), testSecret, region, 42); err == nil {
		t.Fatal("an unreachable zone in the region must fail the list")
	}
	f.server.UnreachableZones = map[string]bool{"europe-west1-b": true}
	if _, err := f.provider.List(context.Background(), testSecret, region, 42); err != nil {
		t.Fatalf("an unreachable zone elsewhere failed the list: %v", err)
	}
}

func TestProjectOverrideScope(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.AddProject("acme-runners")
	f.server.AddRegion("acme-runners", region, zones...)
	l := testLaunch()
	l.Region = EncodeScope(region, "acme-runners", project)
	l.Machine.GCP = &cloud.GCPOptions{Project: "acme-runners"}
	id, err := f.launch(l)
	if err != nil {
		t.Fatal(err)
	}
	zone, _, _ := ParseInstanceID(id)
	if f.server.Instance("acme-runners", zone, name) == nil || f.server.Instance(project, zone, name) != nil {
		t.Fatal("instance was not created in the overriding project")
	}
	list, err := f.provider.List(context.Background(), testSecret, l.Region, 42)
	if err != nil || len(list) != 1 {
		t.Fatalf("list in the override project = %+v, %v", list, err)
	}
	if list, _ := f.provider.List(context.Background(), testSecret, region, 42); len(list) != 0 {
		t.Fatalf("the credential's own project lists the override's instance: %+v", list)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, l.Region, id, 42); err != nil || f.server.Instance("acme-runners", zone, name) != nil {
		t.Fatalf("terminate in the override project: %v", err)
	}
}

func TestTerminateChecksOwnership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	instance := f.mustLaunch(testLaunch())
	zone := instance.GetZone()[strings.LastIndex(instance.GetZone(), "/")+1:]
	id := InstanceID(zone, name)

	if err := f.provider.Terminate(context.Background(), testSecret, region, id, 7); err == nil || f.server.Instance(project, zone, name) == nil {
		t.Fatalf("another installation deleted our instance: err = %v", err)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, id, 42); err != nil || f.server.Instance(project, zone, name) != nil {
		t.Fatalf("terminate: %v", err)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, id, 42); err != nil {
		t.Fatalf("terminating a gone instance: %v", err)
	}
	if err := f.provider.Terminate(context.Background(), testSecret, region, "i-0123", 42); err == nil {
		t.Fatal("an EC2-style ID was accepted")
	}
}

func TestLocations(t *testing.T) {
	t.Parallel()
	for input, want := range map[string][2]string{
		"us-central1-b":   {"us-central1", "us-central1-b"},
		"europe-west1":    {"europe-west1", ""},
		"asia-south1-c":   {"asia-south1", "asia-south1-c"},
		"europe-west10-a": {"europe-west10", "europe-west10-a"},
		"europe-west12":   {"europe-west12", ""},
		"us-east-1":       {"", ""},
		"us-central1-bb":  {"", ""},
	} {
		region, zone, ok := SplitLocation(input)
		if ok != (want[0] != "") || region != want[0] || zone != want[1] {
			t.Errorf("SplitLocation(%q) = %q, %q, %v", input, region, zone, ok)
		}
	}
	if got := EncodeScope("us-central1", project, project); got != "us-central1" {
		t.Errorf("own project scope = %s", got)
	}
	scope, err := ParseScope("us-central1@acme-runners", project)
	if err != nil || scope.Project != "acme-runners" || scope.Region != "us-central1" {
		t.Errorf("ParseScope = %+v, %v", scope, err)
	}
	if _, err := ParseScope("us-central1-b", project); err == nil {
		t.Error("a zone was accepted as a scope")
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
	compact, err := EncodeSecret(testSecret)
	if err != nil || strings.Contains(string(compact), "\n  ") {
		t.Fatalf("encode = %q, %v", compact, err)
	}
	for name, raw := range map[string]string{
		"empty":       "",
		"not json":    "{",
		"user key":    `{"type":"authorized_user","client_id":"x"}`,
		"no project":  `{"type":"service_account","client_email":"a@b.co","private_key":"-----BEGIN PRIVATE KEY-----"}`,
		"no key":      `{"type":"service_account","client_email":"a@b.co","project_id":"acme-ci-123456"}`,
		"bad project": `{"type":"service_account","client_email":"a@b.co","project_id":"ACME","private_key":"-----BEGIN PRIVATE KEY-----"}`,
	} {
		if _, err := ParseSecret([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Shielded VM options need a UEFI image: they are set when the image says
// it is one and left out otherwise, where Compute Engine would refuse them.
func TestShieldedConfigFollowsTheImage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.server.AddImage(project, &computepb.Image{
		Name: proto.String("ci-uefi"), Architecture: proto.String(computepb.Image_X86_64.String()),
		GuestOsFeatures: []*computepb.GuestOsFeature{{Type: proto.String(computepb.GuestOsFeature_UEFI_COMPATIBLE.String())}},
	})
	f.server.AddImage(project, &computepb.Image{Name: proto.String("ci-bios"), Architecture: proto.String(computepb.Image_X86_64.String())})

	uefi := testLaunch()
	uefi.Machine.Image = "ci-uefi"
	instance := f.mustLaunch(uefi)
	if cfg := instance.GetShieldedInstanceConfig(); cfg == nil || !cfg.GetEnableVtpm() || !cfg.GetEnableIntegrityMonitoring() || cfg.GetEnableSecureBoot() {
		t.Errorf("UEFI image shielded config = %+v", cfg)
	}

	bios := testLaunch()
	bios.RunnerID, bios.RunnerName, bios.Machine.Image = "44444444-2222-3333-4444-555555555555", "airship-bios", "ci-bios"
	if instance := f.mustLaunch(bios); instance.ShieldedInstanceConfig != nil {
		t.Errorf("BIOS image got a shielded config: %+v", instance.GetShieldedInstanceConfig())
	}
}

// A set of clients handed to a caller stays open until it is released,
// however long the call takes and whatever other credentials come through.
func TestClientsAreNotClosedWhileBorrowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	c, release, err := f.provider.clientsFor(ctx, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.mu.Lock()
	for _, entry := range f.provider.clients {
		entry.lastUsed = time.Now().Add(-2 * clientTTL) // long idle by the clock, still in use
	}
	f.provider.mu.Unlock()
	other := []byte(strings.Replace(string(testSecret), project, "other-project-654321", 1))
	_, releaseOther, err := f.provider.clientsFor(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	releaseOther()
	if _, err := c.Projects.Get(ctx, &computepb.GetProjectRequest{Project: project}); err != nil {
		t.Fatalf("borrowed clients were closed under the caller: %v", err)
	}
	release()
	release() // releasing twice is harmless
	f.provider.mu.Lock()
	for _, entry := range f.provider.clients {
		entry.lastUsed = time.Now().Add(-2 * clientTTL)
	}
	f.provider.mu.Unlock()
	if _, r, err := f.provider.clientsFor(ctx, other); err == nil {
		r()
	}
	f.provider.mu.Lock()
	n := len(f.provider.clients)
	f.provider.mu.Unlock()
	if n != 1 {
		t.Fatalf("released idle clients were not evicted: %d sets kept", n)
	}
}
