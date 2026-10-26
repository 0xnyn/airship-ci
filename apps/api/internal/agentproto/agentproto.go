// Package agentproto defines the wire protocol between the control plane and
// the data plane agent. It has no dependencies so the agent binary stays
// small, and the control plane registers these same types with Huma, so the
// protocol is also described in openapi.json.
//
// The protocol is level-triggered: on every sync the agent reports every
// instance its provider can see, and the control plane answers with what to
// launch and what to terminate. A lost request or response is repaired by
// the next sync, and agent restarts need no local state.
package agentproto

// ReceivedWindow is how long the agent keeps acknowledging a launch. It
// must outlast the control plane's lease on an assigned runner.
const ReceivedWindow = 15 * 60 // seconds

// SyncPath is the one endpoint the agent calls.
const SyncPath = "/v1/agent/sync"

// MaxWaitSeconds bounds the long poll; it stays under common 60s proxy
// idle timeouts.
const MaxWaitSeconds = 25

// SyncRequest is what the agent sends.
type SyncRequest struct {
	AgentVersion string `doc:"Agent build" json:"agentVersion" maxLength:"64"`
	Provider     string `doc:"Provider the agent launches runners with" enum:"aws,docker,fake" json:"provider"`
	// Instances must be the provider's complete view of live runner
	// instances for this data plane, or the control plane will conclude
	// unreported instances are gone.
	Instances []Instance `doc:"Every live runner instance the provider reports for this data plane" json:"instances" maxItems:"10000"`
	// InventoryUnknown says Instances is not a complete view (the agent has
	// just started, or listing failed). The control plane then leaves
	// instance state alone and hands out no work.
	InventoryUnknown bool `doc:"Instances is not a complete inventory; skip reconciliation" json:"inventoryUnknown,omitempty"`
	// Received lists every runner the agent got in a Launch within the last
	// ReceivedWindow, whatever became of it. A runner the control plane
	// handed out that is missing here was in a response the agent never
	// got, and is queued again instead of waiting for its lease to expire.
	// Agents that predate this field omit it, which turns the check off.
	Received []string `doc:"Runner IDs received in launches recently" json:"received" maxItems:"10000" required:"false"`
	// Failures are launches that failed since the last successful sync.
	Failures []LaunchFailure `doc:"Launches that failed since the last sync" json:"failures" maxItems:"1000"`
	// Capacity is how many new launches the agent accepts in this response.
	Capacity int `doc:"Launches the agent will accept in this response" json:"capacity" maximum:"100" minimum:"0"`
	// WaitSeconds makes the call a long poll when there is nothing to do.
	WaitSeconds int `doc:"Seconds to hold the request open when there is no work" json:"waitSeconds" maximum:"25" minimum:"0"`
}

// Instance is one runner instance the provider can see.
type Instance struct {
	RunnerID   string `doc:"Airship runner ID the instance was launched for" format:"uuid" json:"runnerId"`
	InstanceID string `doc:"Provider's instance ID" json:"instanceId" maxLength:"256" minLength:"1"`
	// Stopping marks instances already shutting down, so the agent is not
	// asked to terminate them again.
	Stopping bool `doc:"The instance is already shutting down" json:"stopping,omitempty"`
}

// LaunchFailure reports a launch the provider rejected.
type LaunchFailure struct {
	RunnerID string `doc:"Runner the launch was for" format:"uuid" json:"runnerId"`
	Error    string `doc:"Provider error, shown to the customer" json:"error" maxLength:"2000"`
	// Retryable failures (capacity, throttling) put the job back in line.
	Retryable bool `doc:"Whether launching again could succeed" json:"retryable"`
}

// SyncResponse is what the control plane wants the agent to do.
type SyncResponse struct {
	// DataPlaneID is what the agent tags instances with, so data planes
	// sharing a cloud account never touch each other's instances.
	DataPlaneID string      `doc:"Data plane the token belongs to" format:"uuid" json:"dataPlaneId"`
	Launch      []Launch    `doc:"Runners to launch" json:"launch"`
	Terminate   []Terminate `doc:"Instances to terminate" json:"terminate"`
}

// Launch asks the agent to start one ephemeral runner.
type Launch struct {
	RunnerID   string `doc:"Airship runner ID; tag the instance with it" format:"uuid" json:"runnerId"`
	RunnerName string `doc:"GitHub runner name" json:"runnerName"`
	Repository string `doc:"Repository the runner is registered to (owner/name)" json:"repository"`
	// JITConfig is a single-use GitHub runner credential. Agents must not
	// log or persist it.
	JITConfig     string        `doc:"Single-use GitHub runner registration. Treat as a secret." json:"jitConfig"`
	RunnerVersion string        `doc:"actions/runner version to run" json:"runnerVersion"`
	Profile       LaunchProfile `json:"profile"`
	// MaxLifetimeSeconds is when the instance must shut itself down even if
	// the control plane is unreachable.
	MaxLifetimeSeconds int `doc:"Hard deadline after which the instance powers itself off" json:"maxLifetimeSeconds"`
}

// LaunchProfile is the machine shape for a launch.
type LaunchProfile struct {
	Label        string `doc:"runs-on label" json:"label"`
	Arch         string `doc:"CPU architecture" enum:"x64,arm64" json:"arch"`
	InstanceType string `doc:"Provider machine type, such as c7i.xlarge" json:"instanceType"`
	Image        string `doc:"Machine image; empty means the agent's default for the arch" json:"image,omitempty"`
	DiskGB       int    `doc:"Root disk size in GiB" json:"diskGb"`
	Spot         bool   `doc:"Use spot or preemptible capacity" json:"spot"`
}

// Terminate asks the agent to destroy an instance.
type Terminate struct {
	RunnerID   string `doc:"Runner the instance belongs to" format:"uuid" json:"runnerId"`
	InstanceID string `doc:"Provider's instance ID" json:"instanceId"`
	Reason     string `doc:"Why the instance should go" json:"reason"`
}
