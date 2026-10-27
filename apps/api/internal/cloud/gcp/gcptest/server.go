// Package gcptest is a fake Compute Engine REST API for tests: enough of
// instances, images, regions, routers and operations for the provider to
// run against through the real SDK, the way moto stands in for EC2. It
// models the asynchronous insert (an operation that can fail after the
// request was accepted), request IDs, name uniqueness and the ways a
// response can go missing.
package gcptest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Server is the fake API. Fields are protected by mu; the knobs are read
// at request time, so tests may flip them between calls.
type Server struct {
	// URL is the endpoint to give the provider.
	URL string

	mu            sync.Mutex
	closing       chan struct{}
	projects      map[string]bool
	regions       map[string][]string // project/region -> zones
	images        map[string][]*computepb.Image
	machineImages map[string]*computepb.MachineImage
	instances     map[string]*computepb.Instance   // project/zone/name
	routers       map[string]*computepb.Router     // project/region/name
	subnetworks   map[string]*computepb.Subnetwork // project/region/name
	operations    map[string]*computepb.Operation
	requestIDs    map[string]string // requestId -> operation name
	nextOp        int

	// Inserts counts instance insert requests received.
	Inserts int
	// Deletes counts instance delete requests received.
	Deletes int

	// ExhaustedZones fail inserts with ZONE_RESOURCE_POOL_EXHAUSTED after
	// the operation was accepted.
	ExhaustedZones map[string]bool
	// MissingMachineTypeZones reject inserts with the 400 Compute Engine
	// gives when the zone does not offer the machine type.
	MissingMachineTypeZones map[string]bool
	// UnreachableZones fail the instance list of those zones.
	UnreachableZones map[string]bool
	// HangListZones never answer the instance list of those zones until
	// the client gives up.
	HangListZones map[string]bool
	// MissRouterGets makes that many router reads answer 404 although the
	// router exists, as a read racing a concurrent create would.
	MissRouterGets int
	// InsertFailure, when set, is returned by the next insert instead of
	// an operation.
	InsertFailure *HTTPError
	// DropNextInsert creates the instance but closes the connection
	// without answering, as a timed-out request would look to the client.
	DropNextInsert bool
	// FailNextOperationPoll makes the next operation read fail with a 503.
	FailNextOperationPoll bool
	// FailGetsAfterInsert makes instance reads that follow an insert fail
	// with a 500 (which the SDK does not retry), so a lost answer cannot
	// be settled by looking the instance up.
	FailGetsAfterInsert int
	// HangOperations keeps every operation RUNNING, so waits time out.
	HangOperations bool
	// APIDisabled makes projects.get fail as it does before the Compute
	// Engine API is enabled.
	APIDisabled bool
	// OperationQuotaError fails inserts with QUOTA_EXCEEDED after they
	// were accepted.
	OperationQuotaError bool
	// DisksInUse names existing disks (by name) attached read-write
	// elsewhere; attaching one read-write fails the operation with
	// RESOURCE_IN_USE_BY_ANOTHER_RESOURCE.
	DisksInUse map[string]bool
}

// HTTPError is an API error the fake returns.
type HTTPError struct {
	Status  int
	Reason  string
	Message string
}

// New starts a fake with one project and closes it when the test ends.
func New(t *testing.T, project string) *Server {
	t.Helper()
	s := &Server{
		closing:       make(chan struct{}),
		projects:      map[string]bool{project: true},
		regions:       map[string][]string{},
		images:        map[string][]*computepb.Image{},
		machineImages: map[string]*computepb.MachineImage{},
		instances:     map[string]*computepb.Instance{},
		routers:       map[string]*computepb.Router{},
		subnetworks:   map[string]*computepb.Subnetwork{},
		operations:    map[string]*computepb.Operation{},
		requestIDs:    map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /compute/v1/projects/{project}", s.getProject)
	mux.HandleFunc("GET /compute/v1/projects/{project}/regions/{region}", s.getRegion)
	mux.HandleFunc("GET /compute/v1/projects/{project}/zones/{zone}/instances", s.listInstances)
	mux.HandleFunc("GET /compute/v1/projects/{project}/zones/{zone}/instances/{name}", s.getInstance)
	mux.HandleFunc("POST /compute/v1/projects/{project}/zones/{zone}/instances", s.insertInstance)
	mux.HandleFunc("DELETE /compute/v1/projects/{project}/zones/{zone}/instances/{name}", s.deleteInstance)
	mux.HandleFunc("GET /compute/v1/projects/{project}/zones/{zone}/operations/{op}", s.getOperation)
	mux.HandleFunc("POST /compute/v1/projects/{project}/zones/{zone}/operations/{op}/wait", s.getOperation)
	mux.HandleFunc("GET /compute/v1/projects/{project}/regions/{region}/operations/{op}", s.getOperation)
	mux.HandleFunc("POST /compute/v1/projects/{project}/regions/{region}/operations/{op}/wait", s.getOperation)
	mux.HandleFunc("GET /compute/v1/projects/{project}/global/images/family/{family}", s.getImageFromFamily)
	mux.HandleFunc("GET /compute/v1/projects/{project}/global/images/{image}", s.getImage)
	mux.HandleFunc("GET /compute/v1/projects/{project}/global/machineImages/{name}", s.getMachineImage)
	mux.HandleFunc("GET /compute/v1/projects/{project}/regions/{region}/routers/{name}", s.getRouter)
	mux.HandleFunc("POST /compute/v1/projects/{project}/regions/{region}/routers", s.insertRouter)
	mux.HandleFunc("GET /compute/v1/projects/{project}/regions/{region}/subnetworks/{name}", s.getSubnetwork)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "notImplemented", "gcptest: "+r.Method+" "+r.URL.Path)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Close waits for handlers; a hung one (HangListZones) is released
	// first, since cleanups run last in, first out.
	t.Cleanup(func() { close(s.closing) })
	s.URL = srv.URL

	return s
}

// AddProject makes another project known.
func (s *Server) AddProject(project string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[project] = true
}

// AddRegion registers a region and its zones in a project.
func (s *Server) AddRegion(project, region string, zones ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regions[project+"/"+region] = zones
}

// AddImage publishes an image in a project. Family lookups return the
// image added last for the family.
func (s *Server) AddImage(project string, image *computepb.Image) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if image.SelfLink == nil {
		image.SelfLink = proto.String(s.URL + "/compute/v1/projects/" + project + "/global/images/" + image.GetName())
	}
	s.images[project] = append(s.images[project], image)
}

// AddMachineImage publishes a machine image in a project.
func (s *Server) AddMachineImage(project string, image *computepb.MachineImage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if image.SelfLink == nil {
		image.SelfLink = proto.String(s.URL + "/compute/v1/projects/" + project + "/global/machineImages/" + image.GetName())
	}
	s.machineImages[project+"/"+image.GetName()] = image
}

// Instance returns a copy of an instance, or nil.
func (s *Server) Instance(project, zone, name string) *computepb.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, ok := s.instances[project+"/"+zone+"/"+name]
	if !ok {
		return nil
	}

	return proto.Clone(instance).(*computepb.Instance) //nolint:forcetypeassert // Clone preserves the type
}

// Instances returns copies of every instance, by project/zone/name.
func (s *Server) Instances() map[string]*computepb.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*computepb.Instance{}
	for key, instance := range s.instances {
		out[key] = proto.Clone(instance).(*computepb.Instance) //nolint:forcetypeassert // Clone preserves the type
	}

	return out
}

// SetStatus changes an instance's status, as a user stopping it would.
func (s *Server) SetStatus(project, zone, name, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if instance, ok := s.instances[project+"/"+zone+"/"+name]; ok {
		instance.Status = proto.String(status)
	}
}

// PutInstance places an instance that Airship did not create.
func (s *Server) PutInstance(project, zone string, instance *computepb.Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance.Zone = proto.String(s.URL + "/compute/v1/projects/" + project + "/zones/" + zone)
	if instance.Status == nil {
		instance.Status = proto.String(computepb.Instance_RUNNING.String())
	}
	s.instances[project+"/"+zone+"/"+instance.GetName()] = instance
}

// AddSubnetwork places a subnetwork on a network in a project's region.
func (s *Server) AddSubnetwork(project, region, name, network string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subnetworks[project+"/"+region+"/"+name] = &computepb.Subnetwork{
		Name:     proto.String(name),
		Region:   proto.String(s.URL + "/compute/v1/projects/" + project + "/regions/" + region),
		Network:  proto.String(s.URL + "/compute/v1/projects/" + project + "/global/networks/" + network),
		SelfLink: proto.String(s.URL + "/compute/v1/projects/" + project + "/regions/" + region + "/subnetworks/" + name),
	}
}

// PutRouter places a router Airship did not create.
func (s *Server) PutRouter(project, region string, router *computepb.Router) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routers[project+"/"+region+"/"+router.GetName()] = router
}

// Routers returns the routers created, by project/region/name.
func (s *Server) Routers() map[string]*computepb.Router {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*computepb.Router{}
	for key, router := range s.routers {
		out[key] = proto.Clone(router).(*computepb.Router) //nolint:forcetypeassert // Clone preserves the type
	}

	return out
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project := r.PathValue("project")
	if s.APIDisabled {
		writeError(w, http.StatusForbidden, "accessNotConfigured", "Compute Engine API has not been used in project "+project+" before or it is disabled.")

		return
	}
	if !s.projects[project] {
		writeError(w, http.StatusNotFound, "notFound", "The resource 'projects/"+project+"' was not found")

		return
	}
	writeProto(w, &computepb.Project{Name: proto.String(project)})
}

func (s *Server) getRegion(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, region := r.PathValue("project"), r.PathValue("region")
	zones, ok := s.regions[project+"/"+region]
	if !s.projects[project] || !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource 'projects/"+project+"/regions/"+region+"' was not found")

		return
	}
	out := &computepb.Region{Name: proto.String(region), Status: proto.String("UP")}
	for _, zone := range zones {
		out.Zones = append(out.Zones, s.URL+"/compute/v1/projects/"+project+"/zones/"+zone)
	}
	writeProto(w, out)
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, zone := r.PathValue("project"), r.PathValue("zone")
	if !s.projects[project] {
		writeError(w, http.StatusNotFound, "notFound", "project not found")

		return
	}
	if s.UnreachableZones[zone] {
		writeError(w, http.StatusInternalServerError, "backendError", "zone "+zone+" is unreachable")

		return
	}
	if s.HangListZones[zone] {
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-s.closing:
		}
		s.mu.Lock()

		return
	}
	filter := r.URL.Query().Get("filter")
	var wantLabel, wantValue string
	if k, v, ok := strings.Cut(filter, "="); ok && strings.HasPrefix(k, "labels.") {
		wantLabel, wantValue = strings.TrimPrefix(k, "labels."), strings.Trim(v, `"`)
	}
	out := &computepb.InstanceList{}
	for key, instance := range s.instances {
		parts := strings.SplitN(key, "/", 3)
		if parts[0] != project || parts[1] != zone {
			continue
		}
		if wantLabel != "" && instance.GetLabels()[wantLabel] != wantValue {
			continue
		}
		out.Items = append(out.Items, instance)
	}
	// Deterministic order, as the API's.
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].GetName() < out.Items[j].GetName() })
	writeProto(w, out)
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailGetsAfterInsert > 0 && s.Inserts > 0 {
		s.FailGetsAfterInsert--
		writeError(w, http.StatusInternalServerError, "backendError", "try again")

		return
	}
	key := r.PathValue("project") + "/" + r.PathValue("zone") + "/" + r.PathValue("name")
	instance, ok := s.instances[key]
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource '"+key+"' was not found")

		return
	}
	writeProto(w, instance)
}

func (s *Server) insertInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, zone := r.PathValue("project"), r.PathValue("zone")
	s.Inserts++
	if s.InsertFailure != nil {
		failure := *s.InsertFailure
		s.InsertFailure = nil
		writeError(w, failure.Status, failure.Reason, failure.Message)

		return
	}
	if !s.projects[project] {
		writeError(w, http.StatusNotFound, "notFound", "project not found")

		return
	}
	var instance computepb.Instance
	if err := readProto(r, &instance); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())

		return
	}
	if instance.GetName() == "" {
		writeError(w, http.StatusBadRequest, "invalid", "Invalid value for field 'resource.name': ''")

		return
	}
	requestID := r.URL.Query().Get("requestId")
	if requestID != "" {
		if opName, ok := s.requestIDs[requestID]; ok {
			writeProto(w, s.operations[opName]) // the retried request's first answer

			return
		}
	}
	if s.MissingMachineTypeZones[zone] {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("Invalid value for field 'resource.machineType': 'zones/%s/machineTypes/%s'. Machine type not found in zone", zone, baseName(instance.GetMachineType())))

		return
	}
	key := project + "/" + zone + "/" + instance.GetName()
	if _, exists := s.instances[key]; exists {
		writeError(w, http.StatusConflict, "alreadyExists", "The resource '"+key+"' already exists")

		return
	}
	op := s.newOperation("insert", zone)
	if requestID != "" {
		s.requestIDs[requestID] = op.GetName()
	}
	switch {
	case s.ExhaustedZones[zone]:
		op.Error = &computepb.Error{Errors: []*computepb.Errors{{
			Code:    proto.String("ZONE_RESOURCE_POOL_EXHAUSTED"),
			Message: proto.String("The zone '" + zone + "' does not have enough resources available to fulfill the request."),
		}}}
		op.HttpErrorStatusCode = proto.Int32(http.StatusServiceUnavailable)
		op.HttpErrorMessage = proto.String("SERVICE UNAVAILABLE")
	case s.OperationQuotaError:
		op.Error = &computepb.Error{Errors: []*computepb.Errors{{Code: proto.String("QUOTA_EXCEEDED"), Message: proto.String("Quota 'CPUS' exceeded.")}}}
		op.HttpErrorStatusCode = proto.Int32(http.StatusForbidden)
		op.HttpErrorMessage = proto.String("FORBIDDEN")
	case s.diskInUse(&instance):
		op.Error = &computepb.Error{Errors: []*computepb.Errors{{Code: proto.String("RESOURCE_IN_USE_BY_ANOTHER_RESOURCE"), Message: proto.String("The disk resource is already being used by another instance")}}}
		op.HttpErrorStatusCode = proto.Int32(http.StatusBadRequest)
		op.HttpErrorMessage = proto.String("BAD REQUEST")
	default:
		stored := proto.Clone(&instance).(*computepb.Instance) //nolint:forcetypeassert // Clone preserves the type
		stored.Zone = proto.String(s.URL + "/compute/v1/projects/" + project + "/zones/" + zone)
		stored.Status = proto.String(computepb.Instance_RUNNING.String())
		stored.SelfLink = proto.String(s.URL + "/compute/v1/" + key)
		s.instances[key] = stored
	}
	if s.DropNextInsert {
		s.DropNextInsert = false
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()

				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	writeProto(w, op)
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Deletes++
	key := r.PathValue("project") + "/" + r.PathValue("zone") + "/" + r.PathValue("name")
	if _, ok := s.instances[key]; !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource '"+key+"' was not found")

		return
	}
	delete(s.instances, key)
	writeProto(w, s.newOperation("delete", r.PathValue("zone")))
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailNextOperationPoll {
		s.FailNextOperationPoll = false
		writeError(w, http.StatusServiceUnavailable, "backendError", "try again")

		return
	}
	op, ok := s.operations[r.PathValue("op")]
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "operation not found")

		return
	}
	if s.HangOperations {
		pending := proto.Clone(op).(*computepb.Operation) //nolint:forcetypeassert // Clone preserves the type
		pending.Status = computepb.Operation_RUNNING.Enum()
		writeProto(w, pending)

		return
	}
	writeProto(w, op)
}

func (s *Server) getImageFromFamily(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, family := r.PathValue("project"), r.PathValue("family")
	images := s.images[project]
	for i := len(images) - 1; i >= 0; i-- {
		if images[i].GetFamily() == family {
			writeProto(w, images[i])

			return
		}
	}
	writeError(w, http.StatusNotFound, "notFound", "The resource 'projects/"+project+"/global/images/family/"+family+"' was not found")
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, name := r.PathValue("project"), r.PathValue("image")
	for _, image := range s.images[project] {
		if image.GetName() == name {
			writeProto(w, image)

			return
		}
	}
	writeError(w, http.StatusNotFound, "notFound", "The resource 'projects/"+project+"/global/images/"+name+"' was not found")
}

func (s *Server) getMachineImage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("project") + "/" + r.PathValue("name")
	image, ok := s.machineImages[key]
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource 'projects/"+r.PathValue("project")+"/global/machineImages/"+r.PathValue("name")+"' was not found")

		return
	}
	writeProto(w, image)
}

func (s *Server) getSubnetwork(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("project") + "/" + r.PathValue("region") + "/" + r.PathValue("name")
	subnet, ok := s.subnetworks[key]
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource '"+key+"' was not found")

		return
	}
	writeProto(w, subnet)
}

func (s *Server) getRouter(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.PathValue("project") + "/" + r.PathValue("region") + "/" + r.PathValue("name")
	router, ok := s.routers[key]
	if s.MissRouterGets > 0 {
		s.MissRouterGets--
		ok = false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "The resource '"+key+"' was not found")

		return
	}
	writeProto(w, router)
}

func (s *Server) insertRouter(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var router computepb.Router
	if err := readProto(r, &router); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())

		return
	}
	key := r.PathValue("project") + "/" + r.PathValue("region") + "/" + router.GetName()
	if _, exists := s.routers[key]; exists {
		writeError(w, http.StatusConflict, "alreadyExists", "The resource '"+key+"' already exists")

		return
	}
	s.routers[key] = &router
	writeProto(w, s.newOperation("insert", ""))
}

func (s *Server) newOperation(kind, zone string) *computepb.Operation {
	s.nextOp++
	name := fmt.Sprintf("operation-%d", s.nextOp)
	op := &computepb.Operation{
		Name:          proto.String(name),
		OperationType: proto.String(kind),
		Status:        computepb.Operation_DONE.Enum(),
		Progress:      proto.Int32(100),
	}
	if zone != "" {
		op.Zone = proto.String(s.URL + "/compute/v1/zones/" + zone)
	}
	s.operations[name] = op

	return op
}

func baseName(link string) string {
	return link[strings.LastIndex(link, "/")+1:]
}

func readProto(r *http.Request, msg proto.Message) error {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, msg); err != nil {
		return fmt.Errorf("decode resource: %w", err)
	}

	return nil
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	raw, err := protojson.Marshal(msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backendError", err.Error())

		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw) //nolint:gosec // protojson output to a test client, not a browser
}

// diskInUse reports whether the instance attaches a DisksInUse disk
// read-write.
func (s *Server) diskInUse(instance *computepb.Instance) bool {
	for _, disk := range instance.GetDisks() {
		if disk.GetSource() != "" && s.DisksInUse[baseName(disk.GetSource())] && disk.GetMode() != computepb.AttachedDisk_READ_ONLY.String() {
			return true
		}
	}

	return false
}

func writeError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]any{
		"code":    status,
		"message": message,
		"errors":  []map[string]string{{"domain": "global", "reason": reason, "message": message}},
		"status":  strconv.Itoa(status),
	}}
	_ = json.NewEncoder(w).Encode(body)
}
