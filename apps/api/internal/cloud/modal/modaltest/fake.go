// Package modaltest is an in-memory Modal for the provider's tests: it
// implements modal.API the way Modal behaves (names unique among running
// Sandboxes, lists filtered by tags, a terminated Sandbox gone from both).
package modaltest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/wizenheimer/airship/apps/api/internal/cloud/modal"
)

// Sandbox is a fake Sandbox.
type Sandbox struct {
	ID         string
	Spec       modal.Spec
	Terminated bool
	Execs      []Exec
}

// Exec is one exec call.
type Exec struct {
	Argv []string
	Env  map[string]string
}

// Fake is the in-memory Modal.
type Fake struct {
	TokenID string

	mu        sync.Mutex
	sandboxes map[string]*Sandbox
	order     []string
	next      int
	// Apps counts App lookups.
	Apps int
	// LoseCreateAnswer makes the next create make the Sandbox and fail
	// transiently.
	LoseCreateAnswer bool
	// FailCreate fails creates with this error.
	FailCreate error
	// ExitCode is what execs exit with.
	ExitCode int
}

// New returns an empty Fake that accepts tokenID.
func New(tokenID string) *Fake {
	return &Fake{TokenID: tokenID, sandboxes: map[string]*Sandbox{}}
}

// Connect is modal.Options.Connect for this fake.
func (f *Fake) Connect(secret modal.Secret) (modal.API, error) {
	if secret.TokenID != f.TokenID {
		return nil, fmt.Errorf("%w: unknown token", modal.ErrUnauthorized)
	}

	return &client{f: f}, nil
}

// Sandboxes returns the running Sandboxes, oldest first.
func (f *Fake) Sandboxes() []Sandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Sandbox
	for _, id := range f.order {
		if s := f.sandboxes[id]; !s.Terminated {
			out = append(out, *s)
		}
	}

	return out
}

// Add puts a running Sandbox in the App directly.
func (f *Fake) Add(id string, spec modal.Spec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sandboxes[id] = &Sandbox{ID: id, Spec: spec}
	f.order = append(f.order, id)
}

type client struct{ f *Fake }

func (c *client) App(context.Context) (string, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.Apps++

	return "ap-airship", nil
}

func (c *client) Create(_ context.Context, spec modal.Spec) (string, error) {
	f := c.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailCreate != nil {
		return "", f.FailCreate
	}
	for _, s := range f.sandboxes {
		if !s.Terminated && s.Spec.Name == spec.Name {
			return "", fmt.Errorf("%w: sandbox %s", modal.ErrAlreadyExists, spec.Name)
		}
	}
	f.next++
	s := &Sandbox{ID: fmt.Sprintf("sb-%04d", f.next), Spec: spec}
	s.Spec.Tags = maps.Clone(spec.Tags)
	f.sandboxes[s.ID] = s
	f.order = append(f.order, s.ID)
	if f.LoseCreateAnswer {
		f.LoseCreateAnswer = false

		return "", fmt.Errorf("%w: stream reset", modal.ErrTransient)
	}

	return s.ID, nil
}

func (c *client) FromName(_ context.Context, name string) (string, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	for _, id := range c.f.order {
		if s := c.f.sandboxes[id]; !s.Terminated && s.Spec.Name == name {
			return id, nil
		}
	}

	return "", modal.ErrNotFound
}

func (c *client) Tags(_ context.Context, id string) (map[string]string, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok {
		return nil, modal.ErrNotFound
	}

	return maps.Clone(s.Spec.Tags), nil
}

func (c *client) List(_ context.Context, _ string, tags map[string]string) ([]string, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	var out []string
	for _, id := range c.f.order {
		s := c.f.sandboxes[id]
		if s.Terminated {
			continue
		}
		ok := true
		for k, v := range tags {
			ok = ok && s.Spec.Tags[k] == v
		}
		if ok {
			out = append(out, id)
		}
	}

	return out, nil
}

func (c *client) Exec(_ context.Context, id string, argv []string, env map[string]string) (int, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok || s.Terminated {
		return 0, modal.ErrNotFound
	}
	s.Execs = append(s.Execs, Exec{Argv: slices.Clone(argv), Env: maps.Clone(env)})

	return c.f.ExitCode, nil
}

func (c *client) Terminate(_ context.Context, id string) error {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok {
		return modal.ErrNotFound
	}
	s.Terminated = true

	return nil
}

func (c *client) Close() {}
