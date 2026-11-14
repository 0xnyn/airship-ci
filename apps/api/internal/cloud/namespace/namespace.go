// Package namespace launches each runner as its own Namespace instance (a
// microVM built for CI) in the customer's Namespace workspace, with the
// token they connected.
//
// Namespace's Compute API is Connect-RPC; its unary calls are plain JSON
// over HTTPS POST, which is all this provider uses
// (namespace.cloud.compute.v1beta, as published in
// namespacelabs.dev/integrations). An instance here is:
//
//   - One container, "runner", from the definition's image, whose
//     entrypoint is bootscript.Waiter: it holds no secret, waits for the
//     boot script and becomes it. Its workload type is JOB (the default),
//     so when the script exits the instance terminates: EC2's
//     shutdown-terminates.
//   - The boot script is written through CommandService.RunCommandSync with
//     the script in the command's environment, not its argv.
//   - Labels airship-installation and airship-runner-id, by which List
//     filters on the server and a repeated launch finds its instance.
//   - A deadline past the runner's own; the workspace's policy may shorten
//     it, and Namespace destroys the instance at its deadline.
//   - docker: true mounts the instance's managed dockerd socket into the
//     container, so jobs drive the VM's own Docker.
//
// Regions are Namespace's regional endpoints (us, eu).
package namespace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/restapi"
)

const (
	// LabelInstallation and LabelRunner mark every instance the control
	// plane creates. Listing and destroying are scoped to LabelInstallation.
	LabelInstallation = "airship-installation"
	LabelRunner       = "airship-runner-id"

	// DefaultImage is the container image a definition without one runs.
	DefaultImage = "ubuntu:22.04"
	// ContainerName is the runner's container in the instance.
	ContainerName = "runner"
	// DockerSocket is where docker: true mounts the VM's dockerd.
	DockerSocket = "/var/run/docker.sock"

	computeService = "/namespace.cloud.compute.v1beta.ComputeService/"
	commandService = "/namespace.cloud.compute.v1beta.CommandService/"

	requestTimeout = 30 * time.Second
	clientTimeout  = 2 * time.Minute
	// deadlineMargin is added to the runner's maximum lifetime for the
	// instance's deadline, so the script's own deadline fires first.
	deadlineMargin = 15 * time.Minute
	cleanupTimeout = 20 * time.Second
	pageSize       = 500
)

var (
	// startTimeout bounds waiting for a new instance to run.
	startTimeout = 4 * time.Minute
	// pollInterval is how often a starting instance is read.
	pollInterval = 2 * time.Second
	// listTimeout bounds one List call, all pages included.
	listTimeout = time.Minute

	namePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	runnerIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)
)

// Options configure the provider.
type Options struct {
	// Endpoint, when set, replaces the regional Compute API URL (tests).
	Endpoint string
	// HTTPClient overrides the default client (tests).
	HTTPClient *http.Client
}

// Provider is the Namespace provider.
type Provider struct {
	opts Options
	http *http.Client
}

// New returns a provider that talks to Namespace.
func New(opts Options) *Provider {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: clientTimeout}
	}

	return &Provider{opts: opts, http: client}
}

// Name implements cloud.Provider.
func (*Provider) Name() string { return cloud.Namespace }

// label is namespace.stdlib.Label.
type label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// instanceMetadata is InstanceMetadata, the fields Airship reads.
type instanceMetadata struct {
	InstanceID string  `json:"instanceId"`
	Status     string  `json:"status"`
	Labels     []label `json:"labels"`
	Deadline   string  `json:"deadline"`
}

func (m instanceMetadata) label(name string) string {
	for _, l := range m.Labels {
		if l.Name == name {
			return l.Value
		}
	}

	return ""
}

// describeResponse is DescribeInstanceResponse (and CreateInstance's
// answer, which has the same two fields).
type describeResponse struct {
	Metadata         instanceMetadata `json:"metadata"`
	ExtendedMetadata struct {
		CommandServiceEndpoint string `json:"commandServiceEndpoint"`
	} `json:"extendedMetadata"`
}

type client struct {
	api    *restapi.Client
	token  string
	region string
	p      *Provider
}

// endpoint is the regional Compute API.
func (p *Provider) endpoint(region string) string {
	if p.opts.Endpoint != "" {
		return p.opts.Endpoint
	}

	return "https://" + region + ".compute.namespaceapis.com"
}

func (p *Provider) clientFor(raw []byte, region string) (*client, error) {
	secret, err := ParseSecret(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, err.Error())
	}
	if !IsRegion(region) {
		return nil, fmt.Errorf("%q is not a Namespace region (%s)", region, strings.Join(regions, ", "))
	}

	return &client{
		api:    p.rpcClient(p.endpoint(region), secret.Token),
		token:  secret.Token,
		region: region,
		p:      p,
	}, nil
}

func (p *Provider) rpcClient(base, token string) *restapi.Client {
	return &restapi.Client{
		BaseURL: base,
		HTTP:    p.http,
		Header: http.Header{
			"Authorization":            {"Bearer " + token},
			"Connect-Protocol-Version": {"1"},
		},
	}
}

// call makes one Connect unary call with JSON.
func call(ctx context.Context, api *restapi.Client, op, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	_, err := api.Do(ctx, op, http.MethodPost, path, in, out)

	return err //nolint:wrapcheck // restapi errors name the operation
}

// Verify implements cloud.Provider: listing one instance in the region
// proves the token works there.
func (p *Provider) Verify(ctx context.Context, raw []byte, region string) (cloud.Identity, error) {
	c, err := p.clientFor(raw, region)
	if err != nil {
		return cloud.Identity{}, err
	}
	if err := call(ctx, c.api, "list instances", computeService+"ListInstances", map[string]any{"maxEntries": 1}, &struct{}{}); err != nil {
		if restapi.IsAuth(err) {
			return cloud.Identity{}, fmt.Errorf("%w: %s", cloud.ErrBadCredentials, restapi.Message(err))
		}

		return cloud.Identity{}, fmt.Errorf("verify credentials: %w", err)
	}

	return cloud.Identity{Principal: "token " + MaskKey(c.token)}, nil
}

// list returns the live instances whose label name equals value.
func (c *client) list(ctx context.Context, name, value string) ([]instanceMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	var out []instanceMetadata
	cursor := ""
	for {
		req := map[string]any{
			"maxEntries":  pageSize,
			"labelFilter": []map[string]string{{"name": name, "value": value, "op": "EQUAL"}},
		}
		if cursor != "" {
			req["paginationCursor"] = cursor
		}
		var page struct {
			Instances        []instanceMetadata `json:"instances"`
			PaginationCursor string             `json:"paginationCursor"`
		}
		if err := call(ctx, c.api, "list instances", computeService+"ListInstances", req, &page); err != nil {
			return nil, err
		}
		for _, inst := range page.Instances {
			if inst.label(name) == value { // the filter is a request; the label is the proof
				out = append(out, inst)
			}
		}
		if page.PaginationCursor == "" || len(page.Instances) == 0 {
			return out, nil
		}
		cursor = page.PaginationCursor
	}
}

// List implements cloud.Provider.
func (p *Provider) List(ctx context.Context, raw []byte, region string, installationID int64) ([]cloud.Instance, error) {
	c, err := p.clientFor(raw, region)
	if err != nil {
		return nil, err
	}
	instances, err := c.list(ctx, LabelInstallation, strconv.FormatInt(installationID, 10))
	if err != nil {
		return nil, fmt.Errorf("list instances in %s: %w", region, err)
	}
	out := make([]cloud.Instance, 0, len(instances))
	seen := map[string]bool{}
	for _, inst := range instances {
		if seen[inst.InstanceID] {
			continue
		}
		seen[inst.InstanceID] = true
		instance := cloud.Instance{RunnerID: inst.label(LabelRunner), InstanceID: inst.InstanceID}
		switch inst.Status {
		case "DESTROYED":
			continue
		case "DESTROYING":
			instance.Stopping = true
		case "SUSPENDING", "SUSPENDED", "ERROR":
			// Nothing runs in it and nothing will.
			instance.Stopped = true
		}
		out = append(out, instance)
	}

	return out, nil
}

// Terminate implements cloud.Provider. The instance is read first and
// destroyed only if its labels say the installation launched it.
func (p *Provider) Terminate(ctx context.Context, raw []byte, region, instanceID string, installationID int64) error {
	c, err := p.clientFor(raw, region)
	if err != nil {
		return err
	}
	inst, err := c.describe(ctx, instanceID)
	if restapi.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if inst.Metadata.label(LabelInstallation) != strconv.FormatInt(installationID, 10) {
		return fmt.Errorf("instance %s is not labelled for installation %d; not destroying it", instanceID, installationID)
	}

	return c.destroy(ctx, inst.Metadata)
}

func (c *client) describe(ctx context.Context, id string) (*describeResponse, error) {
	var out describeResponse
	if err := call(ctx, c.api, "describe instance "+id, computeService+"DescribeInstance", map[string]string{"instanceId": id}, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// destroy ends an instance; one already gone is not an error.
func (c *client) destroy(ctx context.Context, inst instanceMetadata) error {
	if inst.Status == "DESTROYED" || inst.Status == "DESTROYING" {
		return nil
	}
	err := call(ctx, c.api, "destroy instance "+inst.InstanceID, computeService+"DestroyInstance",
		map[string]string{"instanceId": inst.InstanceID, "reason": "Airship: the runner is finished"}, nil)
	if err == nil || restapi.IsNotFound(err) {
		return nil
	}

	return err
}

// createRequest is CreateInstanceRequest, the fields Airship sets.
type createRequest struct {
	Shape             shape       `json:"shape"`
	DocumentedPurpose string      `json:"documentedPurpose"`
	Labels            []label     `json:"labels"`
	Deadline          string      `json:"deadline"`
	Containers        []container `json:"containers"`
}

type shape struct {
	VirtualCPU      int    `json:"virtualCpu"`
	MemoryMegabytes int    `json:"memoryMegabytes"`
	MachineArch     string `json:"machineArch"`
	OS              string `json:"os"`
}

type container struct {
	Name           string   `json:"name"`
	ImageRef       string   `json:"imageRef"`
	Entrypoint     []string `json:"entrypoint"`
	DockerSockPath string   `json:"dockerSockPath,omitempty"`
	// WorkloadType JOB: the instance terminates with the container.
	WorkloadType string `json:"workloadType"`
}

// buildRequest is the instance to create for a launch.
func buildRequest(launch cloud.Launch, now time.Time) (createRequest, error) {
	m := launch.Machine
	opts := m.Sandbox
	if opts == nil || opts.CPU <= 0 || opts.MemoryGB <= 0 {
		return createRequest{}, errors.New("a Namespace instance needs a shape such as 4vcpu-8gb")
	}
	image := m.Image
	if image == "" {
		image = DefaultImage
	}
	arch := "amd64"
	if m.Arch == "arm64" {
		arch = "arm64"
	}
	lifetime := time.Duration(max(launch.Boot.MaxLifetimeSeconds, 60))*time.Second + deadlineMargin
	c := container{
		Name:         ContainerName,
		ImageRef:     image,
		Entrypoint:   []string{"/bin/sh", "-c", bootscript.Waiter(launch.RunnerID)},
		WorkloadType: "JOB",
	}
	if opts.Docker {
		c.DockerSockPath = DockerSocket
	}

	return createRequest{
		Shape:             shape{VirtualCPU: opts.CPU, MemoryMegabytes: opts.MemoryGB * 1024, MachineArch: arch, OS: "linux"},
		DocumentedPurpose: "Airship runner " + launch.RunnerName + " for " + launch.Repository,
		Labels: []label{
			{Name: LabelInstallation, Value: strconv.FormatInt(launch.InstallationID, 10)},
			{Name: LabelRunner, Value: launch.RunnerID},
		},
		Deadline:   now.Add(lifetime).UTC().Format(time.RFC3339),
		Containers: []container{c},
	}, nil
}

// check refuses launches Namespace cannot serve.
func check(launch cloud.Launch) error {
	switch {
	case !namePattern.MatchString(launch.RunnerName):
		return fmt.Errorf("runner name %q is not valid", launch.RunnerName)
	case !runnerIDPattern.MatchString(launch.RunnerID):
		return fmt.Errorf("runner id %q is not a UUID", launch.RunnerID)
	case launch.Boot.PullFromAPI:
		return errors.New("provision_via is not supported on namespace: an instance runs nothing of ours at boot")
	case launch.Boot.Windows.Interactive:
		return errors.New("windows runners are not supported on namespace")
	case launch.Machine.GPUType != "" || launch.Boot.GPU:
		return errors.New("GPU instances are not supported on namespace")
	case launch.Machine.Spot:
		return errors.New("namespace has no spot capacity")
	}

	return nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(ctx context.Context, raw []byte, launch cloud.Launch) (string, error) {
	c, err := p.clientFor(raw, launch.Region)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	if err := check(launch); err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	body, err := buildRequest(launch, time.Now())
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}
	docker := launch.Machine.Sandbox != nil && launch.Machine.Sandbox.Docker
	script, err := bootscript.Sandbox(launch.Boot, launch.Machine.Arch, docker, bootscript.StopEntrypoint)
	if err != nil {
		return "", &cloud.LaunchError{Err: err}
	}

	id, err := c.ensure(ctx, launch, body)
	if err != nil {
		return "", err
	}
	if err := c.boot(ctx, id, launch.RunnerID, script); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		// The instance has no runner; nothing is lost by destroying it, and a
		// retry makes a new one. One that stays ends when its waiter gives up.
		if destroyErr := c.destroy(cleanupCtx, instanceMetadata{InstanceID: id}); destroyErr != nil {
			err = fmt.Errorf("%w (and destroying the instance failed: %w)", err, destroyErr)
		}

		return "", classify(err)
	}

	return id, nil
}

// ensure returns the runner's instance ID: the live one an earlier launch
// made, or a new one.
func (c *client) ensure(ctx context.Context, launch cloud.Launch, body createRequest) (string, error) {
	existing, err := c.list(ctx, LabelRunner, launch.RunnerID)
	if err != nil {
		return "", classify(err)
	}
	for _, inst := range existing {
		if inst.label(LabelInstallation) == strconv.FormatInt(launch.InstallationID, 10) && inst.Status != "DESTROYING" && inst.Status != "DESTROYED" {
			return inst.InstanceID, nil
		}
	}
	var created describeResponse
	err = call(ctx, c.api, "create instance", computeService+"CreateInstance", body, &created)
	if err == nil && created.Metadata.InstanceID != "" {
		return created.Metadata.InstanceID, nil
	}
	if err == nil {
		err = errors.New("create instance: the answer named no instance")
	}
	if !restapi.IsTransport(err) && restapi.Status(err) < http.StatusInternalServerError {
		return "", classify(err)
	}
	// The answer was lost: the instance may exist.
	if found, lookupErr := c.list(context.WithoutCancel(ctx), LabelRunner, launch.RunnerID); lookupErr == nil && len(found) > 0 {
		return found[0].InstanceID, nil
	}

	return "", &cloud.LaunchError{Err: err, Retryable: true, Ambiguous: true}
}

// boot waits for the instance to run, then writes the script and its
// ready marker into the runner container. Repeating it is harmless: the
// waiter moved the first script aside, and the script's lock refuses a
// second runner.
func (c *client) boot(ctx context.Context, id, runnerID, script string) error {
	endpoint, err := c.waitRunning(ctx, id)
	if err != nil {
		return err
	}
	commands := c.p.rpcClient(endpoint, c.token)
	if c.p.opts.Endpoint != "" {
		commands = c.p.rpcClient(c.p.opts.Endpoint, c.token)
	}
	req := map[string]any{
		"instanceId":          id,
		"targetContainerName": ContainerName,
		"command": map[string]any{
			"command": []string{"/bin/sh", "-c", bootscript.WriteCommand(runnerID)},
			"envVars": []map[string]string{{"name": bootscript.BootEnv, "value": bootscript.EncodeScript(script)}},
		},
	}
	// The container may still be starting when the instance runs; a failed
	// write is tried again for a while.
	deadline := time.Now().Add(time.Minute)
	for {
		var result struct {
			Stderr   []byte `json:"stderr"`
			ExitCode int    `json:"exitCode"`
		}
		err = call(ctx, commands, "write boot script", commandService+"RunCommandSync", req, &result)
		if err == nil && result.ExitCode == 0 {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("write boot script: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		}
		if restapi.IsAuth(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("write boot script: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// waitRunning polls until the instance runs and returns its CommandService
// endpoint.
func (c *client) waitRunning(ctx context.Context, id string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	for {
		inst, err := c.describe(ctx, id)
		switch {
		case restapi.IsNotFound(err):
			return "", &cloud.LaunchError{Err: errors.New("instance disappeared while starting"), Retryable: true}
		case err != nil && !restapi.IsTransient(err):
			return "", err
		case err == nil:
			switch inst.Metadata.Status {
			case "RUNNING":
				endpoint := inst.ExtendedMetadata.CommandServiceEndpoint
				if endpoint == "" {
					endpoint = c.p.endpoint(c.region)
				}

				return endpoint, nil
			case "ERROR":
				return "", &cloud.LaunchError{Err: errors.New("instance failed to start"), Retryable: true}
			case "DESTROYED", "DESTROYING":
				return "", &cloud.LaunchError{Err: errors.New("instance was destroyed before it ran"), Retryable: true}
			case "SUSPENDING", "SUSPENDED":
				return "", fmt.Errorf("instance is %s instead of starting", strings.ToLower(inst.Metadata.Status))
			}
		}
		select {
		case <-ctx.Done():
			return "", &cloud.LaunchError{Err: fmt.Errorf("instance did not start in time: %w", ctx.Err()), Retryable: true}
		case <-time.After(pollInterval):
		}
	}
}

// classify turns an error into a LaunchError the scheduler understands.
func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}
	if restapi.IsAuth(err) {
		return &cloud.LaunchError{Err: fmt.Errorf("%w: %s", cloud.ErrBadCredentials, restapi.Message(err))}
	}

	return &cloud.LaunchError{Err: err, Retryable: restapi.IsTransient(err)}
}
