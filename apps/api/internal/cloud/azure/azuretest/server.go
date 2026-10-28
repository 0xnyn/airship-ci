// Package azuretest is a fake Azure Resource Manager for tests: enough of
// subscriptions, resource groups, virtual machines, run commands, images,
// galleries and networking for the provider to run against through the
// real SDK, the way moto stands in for EC2. It models the asynchronous
// creation (an operation that can fail after the request was accepted and
// leave a Failed resource behind), the resources a machine is made of and
// how they cascade on delete, and the ways a response can go missing.
package azuretest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions/v2"
)

// Token is the bearer token the fake accepts.
const Token = "azuretest-token"

// Credential is a token credential the fake accepts.
type Credential struct{}

// GetToken implements azcore.TokenCredential.
func (Credential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: Token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// HTTPError is an API error the fake returns in place of an answer.
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

// Failure fails an accepted operation, as Azure does when it cannot
// allocate the machine after all.
type Failure struct {
	Code    string
	Message string
}

type operation struct {
	name    string
	status  string
	failure *Failure
}

type runCommandKey struct{ vm, name string }

// Server is the fake API. Fields are protected by mu; the knobs are read
// at request time, so tests may flip them between calls.
type Server struct {
	// URL is the endpoint to give the provider.
	URL string

	mu           sync.Mutex
	closing      chan struct{}
	mux          *http.ServeMux
	subscription string
	groups       map[string]*armresources.ResourceGroup
	vms          map[string]*armcompute.VirtualMachine
	bodies       map[string][]byte // vm name -> the request body that made it
	power        map[string]string // vm name -> power state
	runCommands  map[runCommandKey]*armcompute.VirtualMachineRunCommand
	nics         map[string]*armnetwork.Interface
	ips          map[string]*armnetwork.PublicIPAddress
	vnets        map[string]*armnetwork.VirtualNetwork
	nsgs         map[string]*armnetwork.SecurityGroup
	nats         map[string]*armnetwork.NatGateway
	platform     map[string]*armcompute.VirtualMachineImage // location/publisher/offer/sku/version
	managed      map[string]*armcompute.Image               // rg/name
	galleries    map[string]*armcompute.GalleryImage        // rg/gallery/name
	versions     map[string]bool                            // rg/gallery/name/version
	operations   map[string]*operation
	nextOp       int

	// Locations the subscription offers.
	Locations []string
	// SubscriptionState is what the subscription reports (Enabled).
	SubscriptionState string

	// VMCreates, VMDeletes, RunCommandCreates, NICCreates, IPCreates and
	// Lists count the requests received; RunCommandRuns the times a script
	// was started (every PUT of a run command runs it again).
	VMCreates, VMDeletes, RunCommandCreates, RunCommandRuns, NICCreates, IPCreates, Lists int

	// RejectVMCreate, when set, is returned by the next VM creation
	// instead of accepting it.
	RejectVMCreate *HTTPError
	// FailVMCreate fails the next VM creation after it was accepted,
	// leaving the VM as a Failed resource.
	FailVMCreate *Failure
	// DropVMCreates accepts that many creations but closes the connection
	// without answering, as a timed-out request looks to the client. The
	// SDK retries a lost request on its own, so the launch sees the loss
	// only when every attempt is dropped.
	DropVMCreates int
	// SlowVMCreates leaves created VMs in the Creating state, with their
	// operations in progress, until FinishVMCreates is called.
	SlowVMCreates bool
	// HangOperations keeps every operation in progress, so waits time
	// out, while the resources themselves are made.
	HangOperations bool
	// FailOperationPolls makes that many operation reads fail with a 503.
	FailOperationPolls int
	// RejectRunCommand, FailRunCommand and DropRunCommands do for the Run
	// Command what RejectVMCreate, FailVMCreate and DropVMCreates do for
	// the VM.
	RejectRunCommand *HTTPError
	FailRunCommand   *Failure
	DropRunCommands  int
	// RejectIPCreate, when set, is returned by the next public address
	// creation.
	RejectIPCreate *HTTPError
	// UnreachableList fails the virtual machine list with a 500.
	UnreachableList bool
	// HangList never answers the virtual machine list until the client
	// gives up.
	HangList bool
	// SlowDeletes leaves deleted VMs in the Deleting state until
	// FinishDeletes is called.
	SlowDeletes bool
	// RefuseGroupCreate makes creating the resource group fail with
	// AuthorizationFailed, as a credential without Contributor would.
	RefuseGroupCreate bool
}

// New starts a fake with one subscription and closes it when the test
// ends.
func New(t *testing.T, subscription string) *Server {
	t.Helper()
	s := &Server{
		closing:           make(chan struct{}),
		subscription:      strings.ToLower(subscription),
		groups:            map[string]*armresources.ResourceGroup{},
		vms:               map[string]*armcompute.VirtualMachine{},
		bodies:            map[string][]byte{},
		power:             map[string]string{},
		runCommands:       map[runCommandKey]*armcompute.VirtualMachineRunCommand{},
		nics:              map[string]*armnetwork.Interface{},
		ips:               map[string]*armnetwork.PublicIPAddress{},
		vnets:             map[string]*armnetwork.VirtualNetwork{},
		nsgs:              map[string]*armnetwork.SecurityGroup{},
		nats:              map[string]*armnetwork.NatGateway{},
		platform:          map[string]*armcompute.VirtualMachineImage{},
		managed:           map[string]*armcompute.Image{},
		galleries:         map[string]*armcompute.GalleryImage{},
		versions:          map[string]bool{},
		operations:        map[string]*operation{},
		Locations:         []string{"eastus", "westeurope", "uksouth"},
		SubscriptionState: "Enabled",
	}
	mux := http.NewServeMux()
	const rg = "/subscriptions/{sub}/resourcegroups/{rg}"
	mux.HandleFunc("GET /subscriptions/{sub}", s.getSubscription)
	mux.HandleFunc("GET /subscriptions/{sub}/locations", s.listLocations)
	mux.HandleFunc("GET "+rg, s.getGroup)
	mux.HandleFunc("HEAD "+rg, s.getGroup)
	mux.HandleFunc("PUT "+rg, s.putGroup)
	mux.HandleFunc("GET /subscriptions/{sub}/providers/microsoft.compute/virtualmachines", s.listVMs)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.compute/virtualmachines/{name}", s.getVM)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.compute/virtualmachines/{name}", s.putVM)
	mux.HandleFunc("DELETE "+rg+"/providers/microsoft.compute/virtualmachines/{name}", s.deleteVM)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.compute/virtualmachines/{name}/runcommands/{cmd}", s.getRunCommand)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.compute/virtualmachines/{name}/runcommands/{cmd}", s.putRunCommand)
	mux.HandleFunc("GET /subscriptions/{sub}/providers/microsoft.compute/locations/{loc}/publishers/{pub}/artifacttypes/vmimage/offers/{offer}/skus/{sku}/versions", s.listVersions)
	mux.HandleFunc("GET /subscriptions/{sub}/providers/microsoft.compute/locations/{loc}/publishers/{pub}/artifacttypes/vmimage/offers/{offer}/skus/{sku}/versions/{version}", s.getVersion)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.compute/images/{name}", s.getManagedImage)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.compute/galleries/{gallery}/images/{name}", s.getGalleryImage)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.compute/galleries/{gallery}/images/{name}/versions/{version}", s.getGalleryVersion)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/virtualnetworks/{name}", s.getVNet)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/virtualnetworks/{name}", s.putVNet)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/virtualnetworks/{name}/subnets/{subnet}", s.getSubnet)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/virtualnetworks/{name}/subnets/{subnet}", s.putSubnet)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/networksecuritygroups/{name}", s.getNSG)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/networksecuritygroups/{name}", s.putNSG)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/publicipaddresses", s.listIPs)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/publicipaddresses/{name}", s.getIP)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/publicipaddresses/{name}", s.putIP)
	mux.HandleFunc("DELETE "+rg+"/providers/microsoft.network/publicipaddresses/{name}", s.deleteIP)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/networkinterfaces", s.listNICs)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/networkinterfaces/{name}", s.getNIC)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/networkinterfaces/{name}", s.putNIC)
	mux.HandleFunc("DELETE "+rg+"/providers/microsoft.network/networkinterfaces/{name}", s.deleteNIC)
	mux.HandleFunc("GET "+rg+"/providers/microsoft.network/natgateways/{name}", s.getNAT)
	mux.HandleFunc("PUT "+rg+"/providers/microsoft.network/natgateways/{name}", s.putNAT)
	mux.HandleFunc("GET /azuretest/operations/{id}", s.getOperation)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "azuretest: "+r.Method+" "+r.URL.Path)
	})
	s.mux = mux
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	// Close waits for handlers; a hung one (HangList) is released first,
	// since cleanups run last in, first out.
	t.Cleanup(func() { close(s.closing) })
	s.URL = srv.URL

	return s
}

// ServeHTTP checks the token and routes without regard to case, as ARM
// does.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+Token {
		writeError(w, http.StatusUnauthorized, "InvalidAuthenticationToken", "The access token is invalid.")

		return
	}
	lowered := r.Clone(r.Context())
	lowered.URL.Path = strings.ToLower(r.URL.Path)
	lowered.URL.RawPath = ""
	s.mux.ServeHTTP(w, lowered)
}

// Resource IDs as ARM spells them.
func (s *Server) vmID(name string) string {
	return "/subscriptions/" + s.subscription + "/resourceGroups/airship-runners/providers/Microsoft.Compute/virtualMachines/" + name
}

func (s *Server) networkID(kind, name string) string {
	return "/subscriptions/" + s.subscription + "/resourceGroups/airship-runners/providers/Microsoft.Network/" + kind + "/" + name
}

// AddPlatformImage publishes a marketplace image version in a location.
func (s *Server) AddPlatformImage(location, publisher, offer, sku, version string, props armcompute.VirtualMachineImageProperties) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(location + "/" + publisher + "/" + offer + "/" + sku + "/" + version)
	s.platform[key] = &armcompute.VirtualMachineImage{
		Name: to.Ptr(version), Location: to.Ptr(location),
		ID:         to.Ptr("/Subscriptions/" + s.subscription + "/Providers/Microsoft.Compute/Locations/" + location + "/Publishers/" + publisher + "/ArtifactTypes/VMImage/Offers/" + offer + "/Skus/" + sku + "/Versions/" + version),
		Properties: &props,
	}
}

// AddManagedImage places a managed image in a resource group. Its ID is
// filled in.
func (s *Server) AddManagedImage(resourceGroup, name string, image armcompute.Image) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "/subscriptions/" + s.subscription + "/resourceGroups/" + resourceGroup + "/providers/Microsoft.Compute/images/" + name
	image.ID, image.Name = to.Ptr(id), to.Ptr(name)
	s.managed[strings.ToLower(resourceGroup+"/"+name)] = &image

	return id
}

// AddGalleryImage places a gallery image definition with the given
// versions. It returns the definition's ID.
func (s *Server) AddGalleryImage(resourceGroup, gallery, name string, image armcompute.GalleryImage, versions ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "/subscriptions/" + s.subscription + "/resourceGroups/" + resourceGroup + "/providers/Microsoft.Compute/galleries/" + gallery + "/images/" + name
	image.ID, image.Name = to.Ptr(id), to.Ptr(name)
	s.galleries[strings.ToLower(resourceGroup+"/"+gallery+"/"+name)] = &image
	for _, v := range versions {
		s.versions[strings.ToLower(resourceGroup+"/"+gallery+"/"+name+"/"+v)] = true
	}

	return id
}

// VM returns a copy of a virtual machine, or nil.
func (s *Server) VM(name string) *armcompute.VirtualMachine {
	s.mu.Lock()
	defer s.mu.Unlock()
	vm, ok := s.vms[strings.ToLower(name)]
	if !ok {
		return nil
	}

	return clone(vm)
}

// VMs returns copies of every virtual machine, by name.
func (s *Server) VMs() map[string]*armcompute.VirtualMachine {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*armcompute.VirtualMachine{}
	for name, vm := range s.vms {
		out[name] = clone(vm)
	}

	return out
}

// RunCommand returns a copy of a VM's run command, or nil.
func (s *Server) RunCommand(vm, name string) *armcompute.VirtualMachineRunCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.runCommands[runCommandKey{strings.ToLower(vm), strings.ToLower(name)}]
	if !ok {
		return nil
	}

	return clone(cmd)
}

// NICs returns copies of every network interface, by name.
func (s *Server) NICs() map[string]*armnetwork.Interface {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*armnetwork.Interface{}
	for name, nic := range s.nics {
		out[name] = clone(nic)
	}

	return out
}

// PublicIPs returns copies of every public address, by name.
func (s *Server) PublicIPs() map[string]*armnetwork.PublicIPAddress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*armnetwork.PublicIPAddress{}
	for name, ip := range s.ips {
		out[name] = clone(ip)
	}

	return out
}

// VNets returns copies of every virtual network, by name.
func (s *Server) VNets() map[string]*armnetwork.VirtualNetwork {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*armnetwork.VirtualNetwork{}
	for name, vnet := range s.vnets {
		out[name] = clone(vnet)
	}

	return out
}

// NATs returns copies of every NAT gateway, by name.
func (s *Server) NATs() map[string]*armnetwork.NatGateway {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*armnetwork.NatGateway{}
	for name, nat := range s.nats {
		out[name] = clone(nat)
	}

	return out
}

// ResourceGroups returns the names of the resource groups created.
func (s *Server) ResourceGroups() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for name := range s.groups {
		out = append(out, name)
	}
	sort.Strings(out)

	return out
}

// SetPowerState changes a VM's power state (running, stopped,
// deallocated), as a shutdown from inside or a user would.
func (s *Server) SetPowerState(name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vms[strings.ToLower(name)]; ok {
		s.power[strings.ToLower(name)] = state
	}
}

// PutVM places a virtual machine Airship did not create, in the
// provider's resource group.
func (s *Server) PutVM(name string, vm armcompute.VirtualMachine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.ToLower(name)
	vm.ID, vm.Name = to.Ptr(s.vmID(name)), to.Ptr(name)
	if vm.Properties == nil {
		vm.Properties = &armcompute.VirtualMachineProperties{}
	}
	if vm.Properties.ProvisioningState == nil {
		vm.Properties.ProvisioningState = to.Ptr("Succeeded")
	}
	s.vms[name] = &vm
	s.power[name] = "running"
}

// PutNIC and PutPublicIP place network resources Airship did not create
// (or left behind), for the sweep to judge.
func (s *Server) PutNIC(name string, nic armnetwork.Interface) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.ToLower(name)
	nic.ID, nic.Name = to.Ptr(s.networkID("networkInterfaces", name)), to.Ptr(name)
	if nic.Properties == nil {
		nic.Properties = &armnetwork.InterfacePropertiesFormat{}
	}
	s.nics[name] = &nic
}

// PutPublicIP places a public address.
func (s *Server) PutPublicIP(name string, ip armnetwork.PublicIPAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.ToLower(name)
	ip.ID, ip.Name = to.Ptr(s.networkID("publicIPAddresses", name)), to.Ptr(name)
	if ip.Properties == nil {
		ip.Properties = &armnetwork.PublicIPAddressPropertiesFormat{}
	}
	s.ips[name] = &ip
}

// PutVNet replaces a virtual network, as a customer editing it would.
func (s *Server) PutVNet(vnet armnetwork.VirtualNetwork) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vnets[strings.ToLower(*vnet.Name)] = &vnet
}

// Evict removes a VM as a spot eviction (or a deletion from the portal)
// would, with its interface and address.
func (s *Server) Evict(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeVM(strings.ToLower(name))
}

// FinishVMCreates moves every Creating VM to Succeeded and completes its
// operation.
func (s *Server) FinishVMCreates() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, vm := range s.vms {
		if vm.Properties != nil && vm.Properties.ProvisioningState != nil && *vm.Properties.ProvisioningState == "Creating" {
			vm.Properties.ProvisioningState = to.Ptr("Succeeded")
		}
	}
	for _, op := range s.operations {
		if op.status == "InProgress" && op.failure == nil {
			op.status = "Succeeded"
		}
	}
}

// FinishDeletes removes every VM in the Deleting state.
func (s *Server) FinishDeletes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, vm := range s.vms {
		if vm.Properties != nil && vm.Properties.ProvisioningState != nil && *vm.Properties.ProvisioningState == "Deleting" {
			s.removeVM(name)
		}
	}
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := r.PathValue("sub")
	if sub != s.subscription {
		writeError(w, http.StatusNotFound, "SubscriptionNotFound", "The subscription '"+sub+"' could not be found.")

		return
	}
	state := armsubscriptions.SubscriptionState(s.SubscriptionState)
	writeJSON(w, http.StatusOK, armsubscriptions.Subscription{
		ID: to.Ptr("/subscriptions/" + sub), SubscriptionID: to.Ptr(sub), DisplayName: to.Ptr("Test"), State: &state,
	})
}

func (s *Server) listLocations(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := r.PathValue("sub")
	if sub != s.subscription {
		writeError(w, http.StatusNotFound, "SubscriptionNotFound", "The subscription '"+sub+"' could not be found.")

		return
	}
	out := armsubscriptions.LocationListResult{}
	for _, loc := range s.Locations {
		out.Value = append(out.Value, &armsubscriptions.Location{Name: to.Ptr(loc), ID: to.Ptr("/subscriptions/" + sub + "/locations/" + loc)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := s.groups[r.PathValue("rg")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceGroupNotFound", "Resource group '"+r.PathValue("rg")+"' could not be found.")

		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) putGroup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.RefuseGroupCreate {
		writeError(w, http.StatusForbidden, "AuthorizationFailed", "The client does not have authorization to perform action 'Microsoft.Resources/subscriptions/resourcegroups/write'.")

		return
	}
	var group armresources.ResourceGroup
	if err := readJSON(r, &group); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("rg")
	group.ID, group.Name = to.Ptr("/subscriptions/"+s.subscription+"/resourceGroups/"+name), to.Ptr(name)
	group.Properties = &armresources.ResourceGroupProperties{ProvisioningState: to.Ptr("Succeeded")}
	s.groups[name] = &group
	writeJSON(w, http.StatusCreated, group)
}

func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Lists++
	if s.UnreachableList {
		writeError(w, http.StatusInternalServerError, "InternalServerError", "An unexpected error occurred.")

		return
	}
	if s.HangList {
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-s.closing:
		}
		s.mu.Lock()

		return
	}
	statusOnly := r.URL.Query().Get("statusonly") == "true" || r.URL.Query().Get("statusOnly") == "true"
	out := armcompute.VirtualMachineListResult{Value: []*armcompute.VirtualMachine{}}
	names := make([]string, 0, len(s.vms))
	for name := range s.vms {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		vm := clone(s.vms[name])
		if statusOnly {
			vm.Properties.InstanceView = &armcompute.VirtualMachineInstanceView{}
			if power := s.power[name]; power != "" {
				vm.Properties.InstanceView.Statuses = []*armcompute.InstanceViewStatus{
					{Code: to.Ptr("ProvisioningState/" + *vm.Properties.ProvisioningState)},
					{Code: to.Ptr("PowerState/" + power), DisplayStatus: to.Ptr("VM " + power)},
				}
			}
		}
		out.Value = append(out.Value, vm)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getVM(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vm, ok := s.vms[r.PathValue("name")]
	if !ok || r.PathValue("rg") != "airship-runners" {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Compute/virtualMachines/"+r.PathValue("name")+"' under resource group '"+r.PathValue("rg")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, vm)
}

func (s *Server) putVM(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.VMCreates++
	name := r.PathValue("name")
	if s.RejectVMCreate != nil {
		failure := *s.RejectVMCreate
		s.RejectVMCreate = nil
		writeError(w, failure.Status, failure.Code, failure.Message)

		return
	}
	if _, ok := s.groups[r.PathValue("rg")]; !ok {
		writeError(w, http.StatusNotFound, "ResourceGroupNotFound", "Resource group '"+r.PathValue("rg")+"' could not be found.")

		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	var vm armcompute.VirtualMachine
	if err := json.Unmarshal(body, &vm); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	if existing, exists := s.vms[name]; exists {
		// A PUT is an upsert: the same request again (the SDK retrying a
		// lost one) changes nothing; a different boot script is refused.
		if !bytes.Equal(s.bodies[name], body) {
			writeError(w, http.StatusConflict, "PropertyChangeNotAllowed", "Changing property 'osProfile.customData' is not allowed.")

			return
		}
		if s.DropVMCreates > 0 {
			s.DropVMCreates--
			drop(w)

			return
		}
		writeJSON(w, http.StatusOK, existing)

		return
	}
	if vm.Properties == nil || vm.Properties.HardwareProfile == nil || vm.Properties.HardwareProfile.VMSize == nil {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "The value of parameter hardwareProfile.vmSize is invalid.")

		return
	}
	if vm.Properties.StorageProfile == nil || vm.Properties.StorageProfile.ImageReference == nil {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "The value of parameter storageProfile.imageReference is invalid.")

		return
	}
	if vm.Properties.NetworkProfile == nil || len(vm.Properties.NetworkProfile.NetworkInterfaces) == 0 || vm.Properties.NetworkProfile.NetworkInterfaces[0].ID == nil {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "The value of parameter networkProfile.networkInterfaces is invalid.")

		return
	}
	nicName := lastSegment(*vm.Properties.NetworkProfile.NetworkInterfaces[0].ID)
	nic, ok := s.nics[nicName]
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "Referenced resource "+*vm.Properties.NetworkProfile.NetworkInterfaces[0].ID+" was not found.")

		return
	}
	if vm.Properties.OSProfile != nil && vm.Properties.OSProfile.CustomData != nil && len(*vm.Properties.OSProfile.CustomData) > 87380 {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "Custom data in OSProfile must be in Base64 encoding and with a maximum length of 65535 bytes.")

		return
	}
	vm.ID, vm.Name, vm.Type = to.Ptr(s.vmID(name)), to.Ptr(name), to.Ptr("Microsoft.Compute/virtualMachines")
	nic.Properties.VirtualMachine = &armnetwork.SubResource{ID: vm.ID}
	op := s.newOperation()
	switch {
	case s.FailVMCreate != nil:
		vm.Properties.ProvisioningState = to.Ptr("Failed")
		op.failure = s.FailVMCreate
		op.status = "Failed"
		s.FailVMCreate = nil
	case s.SlowVMCreates:
		vm.Properties.ProvisioningState = to.Ptr("Creating")
		op.status = "InProgress"
	default:
		vm.Properties.ProvisioningState = to.Ptr("Succeeded")
		s.power[name] = "running"
	}
	s.vms[name] = &vm
	s.bodies[name] = body
	if s.DropVMCreates > 0 {
		s.DropVMCreates--
		drop(w)

		return
	}
	w.Header().Set("Azure-AsyncOperation", s.URL+"/azuretest/operations/"+op.id())
	pending := clone(&vm)
	pending.Properties.ProvisioningState = to.Ptr("Creating")
	writeJSON(w, http.StatusCreated, pending)
}

func (s *Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.VMDeletes++
	name := r.PathValue("name")
	vm, ok := s.vms[name]
	if !ok {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	if s.SlowDeletes {
		vm.Properties.ProvisioningState = to.Ptr("Deleting")
	} else {
		s.removeVM(name)
	}
	op := s.newOperation()
	w.Header().Set("Azure-AsyncOperation", s.URL+"/azuretest/operations/"+op.id())
	w.WriteHeader(http.StatusAccepted)
}

// removeVM deletes a VM and, as Azure does for resources marked so, its
// interface, that interface's address and its OS disk.
func (s *Server) removeVM(name string) {
	vm, ok := s.vms[name]
	if !ok {
		return
	}
	delete(s.vms, name)
	delete(s.bodies, name)
	delete(s.power, name)
	for key := range s.runCommands {
		if key.vm == name {
			delete(s.runCommands, key)
		}
	}
	if vm.Properties == nil || vm.Properties.NetworkProfile == nil {
		return
	}
	for _, ref := range vm.Properties.NetworkProfile.NetworkInterfaces {
		if ref == nil || ref.ID == nil {
			continue
		}
		nicName := lastSegment(*ref.ID)
		nic, ok := s.nics[nicName]
		if !ok {
			continue
		}
		if ref.Properties == nil || ref.Properties.DeleteOption == nil || *ref.Properties.DeleteOption != armcompute.DeleteOptionsDelete {
			nic.Properties.VirtualMachine = nil

			continue
		}
		s.removeNIC(nicName)
	}
}

func (s *Server) removeNIC(name string) {
	nic, ok := s.nics[name]
	if !ok {
		return
	}
	delete(s.nics, name)
	for _, cfg := range nic.Properties.IPConfigurations {
		if cfg == nil || cfg.Properties == nil || cfg.Properties.PublicIPAddress == nil || cfg.Properties.PublicIPAddress.ID == nil {
			continue
		}
		ipName := lastSegment(*cfg.Properties.PublicIPAddress.ID)
		ip, ok := s.ips[ipName]
		if !ok {
			continue
		}
		ref := cfg.Properties.PublicIPAddress
		if ref.Properties != nil && ref.Properties.DeleteOption != nil && *ref.Properties.DeleteOption == armnetwork.DeleteOptionsDelete {
			delete(s.ips, ipName)
		} else {
			ip.Properties.IPConfiguration = nil
		}
	}
}

func (s *Server) getRunCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.runCommands[runCommandKey{r.PathValue("name"), r.PathValue("cmd")}]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Compute/virtualMachines/"+r.PathValue("name")+"/runCommands/"+r.PathValue("cmd")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

func (s *Server) putRunCommand(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RunCommandCreates++
	vmName, cmdName := r.PathValue("name"), r.PathValue("cmd")
	if s.RejectRunCommand != nil {
		failure := *s.RejectRunCommand
		s.RejectRunCommand = nil
		writeError(w, failure.Status, failure.Code, failure.Message)

		return
	}
	vm, ok := s.vms[vmName]
	if !ok {
		writeError(w, http.StatusNotFound, "ParentResourceNotFound", "Can not perform requested operation on nested resource. Parent resource '"+vmName+"' not found.")

		return
	}
	var cmd armcompute.VirtualMachineRunCommand
	if err := readJSON(r, &cmd); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	if existing, ok := s.runCommands[runCommandKey{vmName, cmdName}]; ok {
		// An upsert, as for the VM, except that Azure runs the script again.
		existing.Properties.Source = cmd.Properties.Source
		s.RunCommandRuns++
		if s.DropRunCommands > 0 {
			s.DropRunCommands--
			drop(w)

			return
		}
		writeJSON(w, http.StatusOK, existing)

		return
	}
	s.RunCommandRuns++
	if cmd.Properties == nil || cmd.Properties.Source == nil || cmd.Properties.Source.Script == nil {
		writeError(w, http.StatusBadRequest, "InvalidParameter", "The value of parameter source is invalid.")

		return
	}
	cmd.ID, cmd.Name = to.Ptr(*vm.ID+"/runCommands/"+cmdName), to.Ptr(cmdName)
	op := s.newOperation()
	switch {
	case s.FailRunCommand != nil:
		cmd.Properties.ProvisioningState = to.Ptr("Failed")
		op.failure = s.FailRunCommand
		op.status = "Failed"
		s.FailRunCommand = nil
	default:
		cmd.Properties.ProvisioningState = to.Ptr("Succeeded")
	}
	s.runCommands[runCommandKey{vmName, cmdName}] = &cmd
	if s.DropRunCommands > 0 {
		s.DropRunCommands--
		drop(w)

		return
	}
	w.Header().Set("Azure-AsyncOperation", s.URL+"/azuretest/operations/"+op.id())
	pending := clone(&cmd)
	pending.Properties.ProvisioningState = to.Ptr("Creating")
	writeJSON(w, http.StatusCreated, pending)
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := r.PathValue("loc") + "/" + r.PathValue("pub") + "/" + r.PathValue("offer") + "/" + r.PathValue("sku") + "/"
	out := []*armcompute.VirtualMachineImageResource{}
	for key, img := range s.platform {
		if strings.HasPrefix(key, prefix) {
			out = append(out, &armcompute.VirtualMachineImageResource{Name: img.Name, Location: img.Location, ID: img.ID})
		}
	}
	if len(out) == 0 {
		writeError(w, http.StatusNotFound, "NotFound", "Artifact: VMImage was not found.")

		return
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].Name < *out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("loc") + "/" + r.PathValue("pub") + "/" + r.PathValue("offer") + "/" + r.PathValue("sku") + "/" + r.PathValue("version")
	img, ok := s.platform[key]
	if !ok {
		writeError(w, http.StatusNotFound, "NotFound", "Artifact: VMImage was not found.")

		return
	}
	writeJSON(w, http.StatusOK, img)
}

func (s *Server) getManagedImage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img, ok := s.managed[r.PathValue("rg")+"/"+r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Compute/images/"+r.PathValue("name")+"' under resource group '"+r.PathValue("rg")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, img)
}

func (s *Server) getGalleryImage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img, ok := s.galleries[r.PathValue("rg")+"/"+r.PathValue("gallery")+"/"+r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Compute/galleries/"+r.PathValue("gallery")+"/images/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, img)
}

func (s *Server) getGalleryVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("rg") + "/" + r.PathValue("gallery") + "/" + r.PathValue("name")
	img, ok := s.galleries[key]
	if !ok || !s.versions[key+"/"+r.PathValue("version")] {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Compute/galleries/"+r.PathValue("gallery")+"/images/"+r.PathValue("name")+"/versions/"+r.PathValue("version")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, armcompute.GalleryImageVersion{
		ID: to.Ptr(*img.ID + "/versions/" + r.PathValue("version")), Name: to.Ptr(r.PathValue("version")), Location: img.Location,
		Properties: &armcompute.GalleryImageVersionProperties{ProvisioningState: to.Ptr(armcompute.GalleryProvisioningStateSucceeded)},
	})
}

func (s *Server) getVNet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vnet, ok := s.vnets[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/virtualNetworks/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, vnet)
}

func (s *Server) putVNet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var vnet armnetwork.VirtualNetwork
	if err := readJSON(r, &vnet); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("name")
	vnet.ID, vnet.Name = to.Ptr(s.networkID("virtualNetworks", name)), to.Ptr(name)
	if vnet.Properties == nil {
		vnet.Properties = &armnetwork.VirtualNetworkPropertiesFormat{}
	}
	vnet.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	for _, subnet := range vnet.Properties.Subnets {
		if subnet != nil && subnet.Name != nil {
			subnet.ID = to.Ptr(*vnet.ID + "/subnets/" + *subnet.Name)
			if subnet.Properties != nil {
				subnet.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
			}
		}
	}
	s.vnets[name] = &vnet
	writeJSON(w, http.StatusOK, vnet)
}

func (s *Server) findSubnet(vnetName, subnetName string) (*armnetwork.VirtualNetwork, int) {
	vnet, ok := s.vnets[vnetName]
	if !ok || vnet.Properties == nil {
		return nil, -1
	}
	for i, subnet := range vnet.Properties.Subnets {
		if subnet != nil && subnet.Name != nil && strings.EqualFold(*subnet.Name, subnetName) {
			return vnet, i
		}
	}

	return vnet, -1
}

func (s *Server) getSubnet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vnet, i := s.findSubnet(r.PathValue("name"), r.PathValue("subnet"))
	if i < 0 {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/virtualNetworks/"+r.PathValue("name")+"/subnets/"+r.PathValue("subnet")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, vnet.Properties.Subnets[i])
}

func (s *Server) putSubnet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vnet, i := s.findSubnet(r.PathValue("name"), r.PathValue("subnet"))
	if vnet == nil {
		writeError(w, http.StatusNotFound, "ParentResourceNotFound", "Virtual network '"+r.PathValue("name")+"' was not found.")

		return
	}
	var subnet armnetwork.Subnet
	if err := readJSON(r, &subnet); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	subnet.ID, subnet.Name = to.Ptr(*vnet.ID+"/subnets/"+r.PathValue("subnet")), to.Ptr(r.PathValue("subnet"))
	if subnet.Properties == nil {
		subnet.Properties = &armnetwork.SubnetPropertiesFormat{}
	}
	subnet.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	if i < 0 {
		vnet.Properties.Subnets = append(vnet.Properties.Subnets, &subnet)
	} else {
		vnet.Properties.Subnets[i] = &subnet
	}
	writeJSON(w, http.StatusOK, subnet)
}

func (s *Server) getNSG(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nsg, ok := s.nsgs[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/networkSecurityGroups/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, nsg)
}

func (s *Server) putNSG(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var nsg armnetwork.SecurityGroup
	if err := readJSON(r, &nsg); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("name")
	nsg.ID, nsg.Name = to.Ptr(s.networkID("networkSecurityGroups", name)), to.Ptr(name)
	if nsg.Properties == nil {
		nsg.Properties = &armnetwork.SecurityGroupPropertiesFormat{}
	}
	nsg.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	s.nsgs[name] = &nsg
	writeJSON(w, http.StatusOK, nsg)
}

func (s *Server) listIPs(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := armnetwork.PublicIPAddressListResult{Value: []*armnetwork.PublicIPAddress{}}
	for _, ip := range s.ips {
		out.Value = append(out.Value, ip)
	}
	sort.Slice(out.Value, func(i, j int) bool { return *out.Value[i].Name < *out.Value[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getIP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ip, ok := s.ips[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/publicIPAddresses/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, ip)
}

func (s *Server) putIP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IPCreates++
	if s.RejectIPCreate != nil {
		failure := *s.RejectIPCreate
		s.RejectIPCreate = nil
		writeError(w, failure.Status, failure.Code, failure.Message)

		return
	}
	var ip armnetwork.PublicIPAddress
	if err := readJSON(r, &ip); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("name")
	ip.ID, ip.Name = to.Ptr(s.networkID("publicIPAddresses", name)), to.Ptr(name)
	if ip.Properties == nil {
		ip.Properties = &armnetwork.PublicIPAddressPropertiesFormat{}
	}
	if existing, ok := s.ips[name]; ok {
		ip.Properties.IPConfiguration = existing.Properties.IPConfiguration
		ip.Properties.NatGateway = existing.Properties.NatGateway
	}
	ip.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	ip.Properties.IPAddress = to.Ptr(fmt.Sprintf("203.0.113.%d", len(s.ips)%250+1))
	s.ips[name] = &ip
	writeJSON(w, http.StatusOK, ip)
}

func (s *Server) deleteIP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.PathValue("name")
	ip, ok := s.ips[name]
	if !ok {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	if ip.Properties.IPConfiguration != nil || ip.Properties.NatGateway != nil {
		writeError(w, http.StatusBadRequest, "PublicIPAddressCannotBeDeleted", "Public IP address "+name+" can not be deleted since it is still allocated.")

		return
	}
	delete(s.ips, name)
	op := s.newOperation()
	w.Header().Set("Azure-AsyncOperation", s.URL+"/azuretest/operations/"+op.id())
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) listNICs(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := armnetwork.InterfaceListResult{Value: []*armnetwork.Interface{}}
	for _, nic := range s.nics {
		out.Value = append(out.Value, nic)
	}
	sort.Slice(out.Value, func(i, j int) bool { return *out.Value[i].Name < *out.Value[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getNIC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nic, ok := s.nics[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/networkInterfaces/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, nic)
}

func (s *Server) putNIC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NICCreates++
	var nic armnetwork.Interface
	if err := readJSON(r, &nic); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("name")
	nic.ID, nic.Name = to.Ptr(s.networkID("networkInterfaces", name)), to.Ptr(name)
	if nic.Properties == nil {
		nic.Properties = &armnetwork.InterfacePropertiesFormat{}
	}
	for _, cfg := range nic.Properties.IPConfigurations {
		if cfg == nil || cfg.Properties == nil {
			continue
		}
		if cfg.Properties.Subnet == nil || cfg.Properties.Subnet.ID == nil {
			writeError(w, http.StatusBadRequest, "InvalidParameter", "An IP configuration needs a subnet.")

			return
		}
		subnetID := *cfg.Properties.Subnet.ID
		parts := strings.Split(strings.ToLower(subnetID), "/")
		if len(parts) < 3 {
			writeError(w, http.StatusBadRequest, "InvalidResourceReference", "Resource "+subnetID+" is not a subnet.")

			return
		}
		if _, i := s.findSubnet(parts[len(parts)-3], parts[len(parts)-1]); i < 0 {
			writeError(w, http.StatusBadRequest, "InvalidResourceReference", "Resource "+subnetID+" referenced by resource "+*nic.ID+" was not found.")

			return
		}
		cfg.ID = to.Ptr(*nic.ID + "/ipConfigurations/" + *cfg.Name)
		cfg.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
		cfg.Properties.PrivateIPAddress = to.Ptr(fmt.Sprintf("10.0.%d.%d", len(s.nics)/250, len(s.nics)%250+4))
		if cfg.Properties.PublicIPAddress != nil && cfg.Properties.PublicIPAddress.ID != nil {
			ip, ok := s.ips[lastSegment(*cfg.Properties.PublicIPAddress.ID)]
			if !ok {
				writeError(w, http.StatusBadRequest, "InvalidResourceReference", "Resource "+*cfg.Properties.PublicIPAddress.ID+" referenced by resource "+*nic.ID+" was not found.")

				return
			}
			ip.Properties.IPConfiguration = &armnetwork.IPConfiguration{ID: cfg.ID}
		}
	}
	if existing, ok := s.nics[name]; ok {
		nic.Properties.VirtualMachine = existing.Properties.VirtualMachine
	}
	nic.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	s.nics[name] = &nic
	writeJSON(w, http.StatusOK, nic)
}

func (s *Server) deleteNIC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.PathValue("name")
	nic, ok := s.nics[name]
	if !ok {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	if nic.Properties.VirtualMachine != nil {
		writeError(w, http.StatusBadRequest, "NicInUse", "Network Interface "+name+" is used by existing resource "+*nic.Properties.VirtualMachine.ID+".")

		return
	}
	// A deleted interface frees its address rather than deleting it: the
	// address's delete option applies to the VM's deletion.
	for _, cfg := range nic.Properties.IPConfigurations {
		if cfg != nil && cfg.Properties != nil && cfg.Properties.PublicIPAddress != nil && cfg.Properties.PublicIPAddress.ID != nil {
			if ip, ok := s.ips[lastSegment(*cfg.Properties.PublicIPAddress.ID)]; ok {
				ip.Properties.IPConfiguration = nil
			}
		}
	}
	delete(s.nics, name)
	op := s.newOperation()
	w.Header().Set("Azure-AsyncOperation", s.URL+"/azuretest/operations/"+op.id())
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) getNAT(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nat, ok := s.nats[r.PathValue("name")]
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "The Resource 'Microsoft.Network/natGateways/"+r.PathValue("name")+"' was not found.")

		return
	}
	writeJSON(w, http.StatusOK, nat)
}

func (s *Server) putNAT(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var nat armnetwork.NatGateway
	if err := readJSON(r, &nat); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())

		return
	}
	name := r.PathValue("name")
	nat.ID, nat.Name = to.Ptr(s.networkID("natGateways", name)), to.Ptr(name)
	if nat.Properties == nil {
		nat.Properties = &armnetwork.NatGatewayPropertiesFormat{}
	}
	for _, ref := range nat.Properties.PublicIPAddresses {
		if ref == nil || ref.ID == nil {
			continue
		}
		ip, ok := s.ips[lastSegment(*ref.ID)]
		if !ok {
			writeError(w, http.StatusBadRequest, "InvalidResourceReference", "Resource "+*ref.ID+" referenced by resource "+*nat.ID+" was not found.")

			return
		}
		ip.Properties.NatGateway = &armnetwork.NatGateway{ID: nat.ID}
	}
	nat.Properties.ProvisioningState = to.Ptr(armnetwork.ProvisioningStateSucceeded)
	s.nats[name] = &nat
	writeJSON(w, http.StatusOK, nat)
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailOperationPolls > 0 {
		s.FailOperationPolls--
		writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "The service is unavailable.")

		return
	}
	op, ok := s.operations[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "NotFound", "operation not found")

		return
	}
	status := op.status
	if s.HangOperations {
		status = "InProgress"
	}
	body := map[string]any{"status": status, "name": r.PathValue("id")}
	if status == "Failed" && op.failure != nil {
		body["error"] = map[string]string{"code": op.failure.Code, "message": op.failure.Message}
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) newOperation() *operation {
	s.nextOp++
	op := &operation{status: "Succeeded"}
	s.operations[fmt.Sprintf("op-%d", s.nextOp)] = op
	op.name = fmt.Sprintf("op-%d", s.nextOp)

	return op
}

func (o *operation) id() string { return o.name }

func lastSegment(id string) string {
	return strings.ToLower(id[strings.LastIndex(id, "/")+1:])
}

// clone copies an SDK model through its JSON form.
func clone[T any](v *T) *T {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	out := new(T)
	if err := json.Unmarshal(raw, out); err != nil {
		panic(err)
	}

	return out
}

// drop closes the connection without answering.
func drop(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()

			return
		}
	}
	panic(http.ErrAbortHandler)
}

func readJSON(r *http.Request, out any) error {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "InternalServerError", err.Error())

		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
