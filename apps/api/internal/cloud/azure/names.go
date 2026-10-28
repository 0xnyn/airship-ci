package azure

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// Azure places machines in regions (eastus, westeurope); availability
// zones within them are left to Azure, which spreads single machines on its
// own. The region is what the control plane tracks runners by.
var (
	// RegionPattern is a region's name as the API spells it: lowercase
	// letters and digits, no spaces (eastus, not "East US").
	RegionPattern = regexp.MustCompile(`^[a-z][a-z0-9]{2,39}$`)
	// namePattern is what the provider names its resources after a runner:
	// the runner name, which is also the virtual machine's name and, on
	// Linux, its host name.
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

// DefaultRegion is where runners launch when neither the config nor the
// credentials say.
const DefaultRegion = "eastus"

const (
	// ResourceGroup holds every resource the provider creates in the
	// subscription: runner machines and their interfaces and addresses, and
	// the per-region networks they attach to.
	ResourceGroup = "airship-runners"
	// networkPrefix starts the per-region virtual network and security
	// group names; natPrefix the NAT gateway's.
	networkPrefix = "airship-runners-"
	natPrefix     = "airship-nat-"
	// subnetName is the one subnet of each virtual network.
	subnetName = "runners"
	// runCommandName is the Run Command that boots Windows and specialized
	// images, which cannot take a script at creation.
	runCommandName = "airship-boot"
	// osDiskSuffix, nicSuffix and ipSuffix name a machine's own resources
	// after it.
	osDiskSuffix = "-osdisk"
	nicSuffix    = "-nic"
	ipSuffix     = "-ip"
	// adminUsername is the account the image is provisioned with. Nobody
	// has its credentials: the SSH key's private half and the Windows
	// password are discarded once the machine is created.
	adminUsername = "airship"
	// maxWindowsComputerName is Windows' limit on a host name.
	maxWindowsComputerName = 15
)

// IsRegion reports whether s is spelled like a region.
func IsRegion(s string) bool { return RegionPattern.MatchString(s) }

// ParseInstanceID checks a runner's instance ID, which is its virtual
// machine's name.
func ParseInstanceID(id string) (string, error) {
	if !namePattern.MatchString(id) {
		return "", fmt.Errorf("%q is not an Azure virtual machine name", id)
	}

	return id, nil
}

func networkName(region string) string { return networkPrefix + region }

func natName(region string) string { return natPrefix + region }

// computerName fits a runner name into Windows' 15-character host name:
// the hex part of airship-<hex> after "air", or a hash of anything else.
func computerName(name string) string {
	if hex, ok := strings.CutPrefix(name, "airship-"); ok && len("air"+hex) <= maxWindowsComputerName {
		return "air" + hex
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))

	return fmt.Sprintf("air%012x", h.Sum64())[:maxWindowsComputerName]
}

// resourceID is the ARM ID of a resource in the provider's resource group.
func resourceID(subscription, provider, kind, name string) string {
	return "/subscriptions/" + subscription + "/resourceGroups/" + ResourceGroup + "/providers/" + provider + "/" + kind + "/" + name
}

// inResourceGroup reports whether an ARM ID is in the provider's resource
// group. ARM compares names without regard to case and echoes IDs in
// whatever case they were created with.
func inResourceGroup(id string) bool {
	return strings.Contains(strings.ToLower(id), "/resourcegroups/"+ResourceGroup+"/")
}
