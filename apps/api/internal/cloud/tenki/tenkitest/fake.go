// Package tenkitest is an in-memory Tenki for the provider's tests: it
// implements tenki.API the way Tenki's SDK behaves (create waits until
// running, tags filter lists, a closed sandbox terminates).
package tenkitest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/wizenheimer/airship/apps/api/internal/cloud/tenki"
)

// Sandbox is a fake sandbox.
type Sandbox struct {
	tenki.Session
	Spec tenki.Spec
	// Execs are every exec's argv and env.
	Execs []Exec
}

// Exec is one exec call.
type Exec struct {
	Argv []string
	Env  map[string]string
}

// Fake is the in-memory Tenki.
type Fake struct {
	// Key is the API key it accepts.
	Key string

	mu        sync.Mutex
	sandboxes map[string]*Sandbox
	order     []string
	next      int
	// LoseCreateAnswer makes the next create make the sandbox and fail
	// transiently.
	LoseCreateAnswer bool
	// FailCreate fails creates with this error.
	FailCreate error
	// ExitCode is what execs exit with.
	ExitCode int
	// Closed counts client closes.
	Closed int
}

// New returns an empty Fake that accepts key.
func New(key string) *Fake {
	return &Fake{Key: key, sandboxes: map[string]*Sandbox{}}
}

// Connect is tenki.Options.Connect for this fake.
func (f *Fake) Connect(secret tenki.Secret) (tenki.API, error) {
	return &client{f: f, key: secret.APIKey}, nil
}

// Sandboxes returns every sandbox not terminated, oldest first.
func (f *Fake) Sandboxes() []Sandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Sandbox
	for _, id := range f.order {
		if s := f.sandboxes[id]; s.State != "TERMINATED" {
			out = append(out, *s)
		}
	}

	return out
}

// Add puts a sandbox in the workspace directly.
func (f *Fake) Add(s tenki.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sandboxes[s.ID] = &Sandbox{Session: s}
	f.order = append(f.order, s.ID)
}

type client struct {
	f   *Fake
	key string
}

func (c *client) auth() error {
	if c.key != c.f.Key {
		return fmt.Errorf("%w: invalid key", tenki.ErrUnauthorized)
	}

	return nil
}

func (c *client) Ping(context.Context) error { return c.auth() }

func (c *client) Create(_ context.Context, spec tenki.Spec) (tenki.Session, error) {
	if err := c.auth(); err != nil {
		return tenki.Session{}, err
	}
	f := c.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailCreate != nil {
		return tenki.Session{}, f.FailCreate
	}
	f.next++
	s := &Sandbox{Session: tenki.Session{
		ID: fmt.Sprintf("sess-%04d", f.next), State: "RUNNING", Metadata: maps.Clone(spec.Metadata), Tags: slices.Clone(spec.Tags),
	}, Spec: spec}
	f.sandboxes[s.ID] = s
	f.order = append(f.order, s.ID)
	if f.LoseCreateAnswer {
		f.LoseCreateAnswer = false

		return tenki.Session{}, fmt.Errorf("%w: connection reset", tenki.ErrTransient)
	}

	return s.Session, nil
}

func (c *client) Get(_ context.Context, id string) (tenki.Session, error) {
	if err := c.auth(); err != nil {
		return tenki.Session{}, err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok {
		return tenki.Session{}, tenki.ErrNotFound
	}

	return s.Session, nil
}

func (c *client) ListByTag(_ context.Context, tag string) ([]tenki.Session, error) {
	if err := c.auth(); err != nil {
		return nil, err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	var out []tenki.Session
	for _, id := range c.f.order {
		s := c.f.sandboxes[id]
		if s.State != "TERMINATED" && slices.Contains(s.Tags, tag) {
			out = append(out, s.Session)
		}
	}

	return out, nil
}

func (c *client) Exec(_ context.Context, id string, argv []string, env map[string]string) (int, string, error) {
	if err := c.auth(); err != nil {
		return 0, "", err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok {
		return 0, "", tenki.ErrNotFound
	}
	s.Execs = append(s.Execs, Exec{Argv: slices.Clone(argv), Env: maps.Clone(env)})
	if c.f.ExitCode != 0 {
		return c.f.ExitCode, "boom", nil
	}

	return 0, "", nil
}

func (c *client) Terminate(_ context.Context, id string) error {
	if err := c.auth(); err != nil {
		return err
	}
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	s, ok := c.f.sandboxes[id]
	if !ok {
		return tenki.ErrNotFound
	}
	s.State = "TERMINATED"

	return nil
}

func (c *client) Close() error {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.Closed++

	return nil
}
