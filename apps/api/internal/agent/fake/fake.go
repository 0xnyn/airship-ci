// Package fake is an in-memory provider for tests and local demos. Its
// "instances" are records; nothing runs.
package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/wizenheimer/airship/apps/api/internal/agent"
	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// Instance is a fake runner machine.
type Instance struct {
	ID          string
	DataPlaneID string
	Launch      agentproto.Launch
	Stopping    bool
}

// Provider is the in-memory provider.
type Provider struct {
	mu        sync.Mutex
	instances map[string]*Instance
	next      int
	// FailLaunch, when set, decides launch failures.
	FailLaunch func(agentproto.Launch) error
	// FailList makes List fail.
	FailList bool
}

// New returns an empty Provider.
func New() *Provider {
	return &Provider{instances: map[string]*Instance{}}
}

// Name implements agent.Provider.
func (*Provider) Name() string { return "fake" }

// List implements agent.Provider.
func (p *Provider) List(_ context.Context, dataPlaneID string) ([]agent.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FailList {
		return nil, errors.New("fake list failure")
	}
	out := []agent.Instance{}
	for _, instance := range p.instances {
		if instance.DataPlaneID == dataPlaneID {
			out = append(out, agent.Instance{RunnerID: instance.Launch.RunnerID, InstanceID: instance.ID, Stopping: instance.Stopping})
		}
	}

	return out, nil
}

// Launch implements agent.Provider.
func (p *Provider) Launch(_ context.Context, dataPlaneID string, launch agentproto.Launch) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FailLaunch != nil {
		if err := p.FailLaunch(launch); err != nil {
			return "", err
		}
	}
	for _, instance := range p.instances {
		if instance.Launch.RunnerID == launch.RunnerID {
			return instance.ID, nil // idempotent, like a ClientToken
		}
	}
	p.next++
	id := fmt.Sprintf("fake-%d", p.next)
	p.instances[id] = &Instance{ID: id, DataPlaneID: dataPlaneID, Launch: launch}

	return id, nil
}

// Terminate implements agent.Provider.
func (p *Provider) Terminate(_ context.Context, instanceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.instances, instanceID)

	return nil
}

// Instances returns a snapshot.
func (p *Provider) Instances() []Instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Instance, 0, len(p.instances))
	for _, instance := range p.instances {
		out = append(out, *instance)
	}

	return out
}

// Exit removes an instance as if it shut itself down.
func (p *Provider) Exit(instanceID string) {
	_ = p.Terminate(context.Background(), instanceID)
}
