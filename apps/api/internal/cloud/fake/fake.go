// Package fake is an in-memory cloud for tests. Its "instances" are
// records; nothing runs.
package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// Instance is a fake runner machine.
type Instance struct {
	ID       string
	Region   string
	Launch   cloud.Launch
	Stopping bool
}

// Provider is the in-memory cloud.
type Provider struct {
	// Cloud is the name it answers to; tests register it as "aws".
	Cloud string

	mu        sync.Mutex
	instances map[string]*Instance
	next      int
	// FailLaunch, when set, decides launch failures.
	FailLaunch func(cloud.Launch) error
	// FailList makes List fail.
	FailList bool
	// BadSecret is a secret Verify rejects.
	BadSecret string
	launches  int
	buckets   []string
	grants    int
}

// New returns an empty Provider named cloud.
func New(name string) *Provider {
	return &Provider{Cloud: name, instances: map[string]*Instance{}}
}

// Name implements cloud.Provider.
func (p *Provider) Name() string { return p.Cloud }

// Verify implements cloud.Provider.
func (p *Provider) Verify(_ context.Context, secret []byte, _ string) (cloud.Identity, error) {
	if p.BadSecret != "" && string(secret) == p.BadSecret {
		return cloud.Identity{}, cloud.ErrBadCredentials
	}

	return cloud.Identity{AccountID: "123456789012", Principal: "arn:aws:iam::123456789012:user/fake"}, nil
}

// List implements cloud.Provider.
func (p *Provider) List(_ context.Context, _ []byte, region string, installationID int64) ([]cloud.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FailList {
		return nil, errors.New("fake list failure")
	}
	out := []cloud.Instance{}
	for _, instance := range p.instances {
		if instance.Region == region && instance.Launch.InstallationID == installationID {
			out = append(out, cloud.Instance{RunnerID: instance.Launch.RunnerID, InstanceID: instance.ID, Stopping: instance.Stopping})
		}
	}

	return out, nil
}

// Launch implements cloud.Provider.
func (p *Provider) Launch(_ context.Context, _ []byte, launch cloud.Launch) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.launches++
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
	id := fmt.Sprintf("i-fake%d", p.next)
	p.instances[id] = &Instance{ID: id, Region: launch.Region, Launch: launch}

	return id, nil
}

// Terminate implements cloud.Provider.
func (p *Provider) Terminate(_ context.Context, _ []byte, _, instanceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.instances, instanceID)

	return nil
}

// Instances returns a snapshot, oldest first.
func (p *Provider) Instances() []Instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Instance, 0, len(p.instances))
	for _, instance := range p.instances {
		out = append(out, *instance)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].ID < out[i].ID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}

	return out
}

// Launches returns how many launch calls were made.
func (p *Provider) Launches() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.launches
}

// Exit removes an instance as if it shut itself down.
func (p *Provider) Exit(instanceID string) {
	_ = p.Terminate(context.Background(), nil, "", instanceID)
}

// SetStopping marks an instance as shutting down.
func (p *Provider) SetStopping(instanceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if instance, ok := p.instances[instanceID]; ok {
		instance.Stopping = true
	}
}

// EnsureCacheBucket implements cloud.Cacher.
func (p *Provider) EnsureCacheBucket(_ context.Context, _ []byte, _, bucket string, _ int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buckets = append(p.buckets, bucket)

	return nil
}

// CacheGrant implements cloud.Cacher.
func (p *Provider) CacheGrant(_ context.Context, _ []byte, region, bucket, prefix, _ string, ttl time.Duration) (cloud.Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grants++

	return cloud.Grant{
		Bucket: bucket, Region: region, Prefix: prefix,
		AccessKeyID: "ASIAFAKE", SecretAccessKey: "fake-secret", SessionToken: "fake-session", ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// Buckets lists the buckets ensured so far.
func (p *Provider) Buckets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.buckets...)
}

// Grants returns how many cache grants were issued.
func (p *Provider) Grants() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.grants
}
