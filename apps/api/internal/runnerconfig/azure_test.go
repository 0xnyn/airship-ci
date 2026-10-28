package runnerconfig

import (
	"strings"
	"testing"
)

// An Azure definition names VM sizes, regions and images as Cirun's docs
// spell them, with the OS disk and the public address in extra_config.
func TestAzureRunner(t *testing.T) {
	f := mustParse(t, `
runners:
  - name: vm
    cloud: azure
    instance_type: [Standard_D4s_v5, Standard_D2pls_v5, Standard_NC6s_v3]
    machine_image:
      - Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest
      - {publisher: Canonical, offer: 0001-com-ubuntu-server-jammy, sku: 22_04-lts-arm64}
      - /subscriptions/0f0e0d0c-0b0a-4908-8706-050403020100/resourceGroups/images/providers/Microsoft.Compute/galleries/ci/images/gpu/versions/1.2.0
    region: [eastus, uksouth, westeurope]
    preemptible: [false, true, false]
    gpu: nvidia-tesla-t4
    labels: [airship-vm]
    extra_config:
      cache: true
      enable_public_ip: false
      storageProfile:
        osDisk:
          diskSizeGB: 128
          managedDisk:
            storageAccountType: premium_lrs
      env_vars: {CI_TIER: fast}
`)
	r := f.Runners[0]
	if r.Cloud != CloudAzure || !r.GPU || r.GPUType != "" || !r.Cache || r.Env["CI_TIER"] != "fast" {
		t.Fatalf("runner = %+v", r)
	}
	if r.Azure == nil || !r.Azure.NoPublicIP || r.GCP != nil || r.Daytona != nil {
		t.Fatalf("azure = %+v", r.Azure)
	}
	if len(r.Variants) != 3 {
		t.Fatalf("variants = %+v", r.Variants)
	}
	x64, arm, gpu := r.Variants[0], r.Variants[1], r.Variants[2]
	if x64.InstanceType != "Standard_D4s_v5" || x64.Arch != "x64" || x64.Region != "eastus" || x64.Spot || x64.Zone != "" ||
		x64.Image != "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest" || x64.Disk.SizeGB != 128 || x64.Disk.Type != "Premium_LRS" {
		t.Errorf("x64 variant = %+v", x64)
	}
	if arm.Arch != "arm64" || !arm.Spot || arm.Image != "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-arm64:latest" {
		t.Errorf("arm variant = %+v", arm)
	}
	if gpu.Arch != "x64" || !HasGPU(CloudAzure, gpu.InstanceType) || !strings.HasSuffix(gpu.Image, "/versions/1.2.0") {
		t.Errorf("gpu variant = %+v", gpu)
	}

	plain := mustParse(t, `
runners:
  - {name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a]}
`).Runners[0]
	v := plain.Variants[0]
	if v.Region != "" || v.Image != "" || v.Disk != (Disk{}) || plain.Azure != nil || v.Arch != "x64" {
		t.Errorf("plain azure runner = %+v, variant %+v", plain, v)
	}
	withIP := mustParse(t, `
runners:
  - {name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a], extra_config: {enable_public_ip: true}}
`).Runners[0]
	if withIP.Azure != nil {
		t.Errorf("enable_public_ip: true is the default, got %+v", withIP.Azure)
	}
	single := mustParse(t, `
runners:
  - name: win
    cloud: azure
    instance_type: Standard_D2pls_v5
    machine_image: {publisher: MicrosoftWindowsDesktop, offer: windows-11, sku: win11-24h2-pro, version: 26100.1.260101}
    labels: [airship-win]
    extra_config: {run_as: interactive}
`).Runners[0]
	if single.Variants[0].Image != "MicrosoftWindowsDesktop:windows-11:win11-24h2-pro:26100.1.260101" || !single.Windows.Interactive {
		t.Errorf("mapping image = %+v", single.Variants[0])
	}
}

func TestAzureRunnerRejects(t *testing.T) {
	cases := map[string]struct{ config, want string }{
		"display name region":  {`{name: a, cloud: azure, instance_type: Standard_B2s, region: "East US", labels: [airship-a]}`, "not an Azure region"},
		"ec2 size":             {`{name: a, cloud: azure, instance_type: c7i.xlarge, labels: [airship-a]}`, "not an Azure VM size"},
		"lowercase size":       {`{name: a, cloud: azure, instance_type: standard_d4s_v5, labels: [airship-a]}`, "not an Azure VM size"},
		"ami":                  {`{name: a, cloud: azure, instance_type: Standard_B2s, machine_image: ami-0123456789abcdef0, labels: [airship-a]}`, "image URN"},
		"urn without version":  {`{name: a, cloud: azure, instance_type: Standard_B2s, machine_image: "Canonical:ubuntu:jammy", labels: [airship-a]}`, "image URN"},
		"mapping without sku":  {`{name: a, cloud: azure, instance_type: Standard_B2s, machine_image: {publisher: Canonical, offer: x}, labels: [airship-a]}`, "publisher, offer and sku"},
		"disk too small":       {`{name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a], extra_config: {storageProfile: {osDisk: {diskSizeGB: 8}}}}`, "diskSizeGB must be between"},
		"disk kind":            {`{name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a], extra_config: {storageProfile: {osDisk: {managedDisk: {storageAccountType: gp3}}}}}`, "storageAccountType must be one of"},
		"gcp key":              {`{name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a], extra_config: {project_id: acme}}`, "project_id is a GCP setting"},
		"aws key":              {`{name: a, cloud: azure, instance_type: Standard_B2s, labels: [airship-a], extra_config: {SubnetId: subnet-0123456789abcdef0}}`, "SubnetId is an AWS setting"},
		"azure key on aws":     {`{name: a, cloud: aws, instance_type: c7i.xlarge, labels: [airship-a], extra_config: {storageProfile: {osDisk: {diskSizeGB: 64}}}}`, "storageProfile is an Azure setting"},
		"azure key on gcp":     {`{name: a, cloud: gcp, instance_type: n2-standard-4, labels: [airship-a], extra_config: {storageProfile: {osDisk: {diskSizeGB: 64}}}}`, "storageProfile is an Azure setting"},
		"mapping image on aws": {`{name: a, cloud: aws, instance_type: c7i.xlarge, machine_image: {publisher: a, offer: b, sku: c}, labels: [airship-a]}`, "must be an AMI ID"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("airship.yaml", []byte("runners:\n  - "+c.config))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
	// enable_public_ip is GCP's and Azure's both.
	if _, err := Parse("airship.yaml", []byte("runners:\n  - {name: a, cloud: gcp, instance_type: n2-standard-4, labels: [airship-a], extra_config: {enable_public_ip: false}}")); err != nil {
		t.Fatal(err)
	}
}

func TestAzureSizes(t *testing.T) {
	for size, want := range map[string]struct {
		arch string
		gpu  bool
	}{
		"Standard_D4s_v5":          {"x64", false},
		"Standard_D2pls_v5":        {"arm64", false},
		"Standard_E4ps_v5":         {"arm64", false},
		"Standard_D2pds_v6":        {"arm64", false},
		"Standard_D4as_v5":         {"x64", false},
		"Standard_B2s":             {"x64", false},
		"Standard_NC6s_v3":         {"x64", true},
		"Standard_NV6ads_A10_v5":   {"x64", true},
		"Standard_ND96asr_v4":      {"x64", true},
		"Standard_NG32ads_V620_v1": {"x64", true},
		"Standard_M128ms":          {"x64", false},
		"Standard_HB120rs_v3":      {"x64", false},
		"Standard_DC2s_v3":         {"x64", false},
		"Standard_D2-64ps_v5":      {"arm64", false},
	} {
		if got := ArchForInstanceType(CloudAzure, size); got != want.arch {
			t.Errorf("arch(%s) = %s, want %s", size, got, want.arch)
		}
		if got := HasGPU(CloudAzure, size); got != want.gpu {
			t.Errorf("gpu(%s) = %v, want %v", size, got, want.gpu)
		}
		if !azureSizePattern.MatchString(size) {
			t.Errorf("%s should be a size", size)
		}
	}
}
