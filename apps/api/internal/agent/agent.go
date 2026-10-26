// Package agent is the data plane: a small process in the customer's own
// infrastructure that launches and destroys runner machines on the control
// plane's behalf.
//
// It only ever connects out to the control plane, so it needs no inbound
// ports, and the control plane never holds credentials to the customer's
// cloud. Its loop is level-triggered: list every instance it owns, report
// the full list, do what the answer says, repeat. Nothing is kept on disk;
// a restarted agent rebuilds its view from the provider.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// Instance is a runner machine the provider can see.
type Instance struct {
	RunnerID   string
	InstanceID string
	// Stopping is true once the machine is shutting down.
	Stopping bool
}

// Provider launches runner machines. Implementations must only list and
// terminate machines they tagged for this data plane.
type Provider interface {
	// Name identifies the provider to the control plane ("aws", "docker").
	Name() string
	// List returns every live machine tagged for dataPlaneID. It must
	// return an error rather than a partial list: an instance missing from
	// a successful list is presumed gone.
	List(ctx context.Context, dataPlaneID string) ([]Instance, error)
	// Launch starts a machine for launch, tagged for dataPlaneID, and
	// returns its instance ID. Launching the same runner twice must not
	// create two machines.
	Launch(ctx context.Context, dataPlaneID string, launch agentproto.Launch) (string, error)
	// Terminate destroys a machine. A machine that is already gone is not
	// an error.
	Terminate(ctx context.Context, instanceID string) error
}

// LaunchError is a failed launch. Retryable failures (capacity, throttling)
// let the control plane try again with a fresh runner.
type LaunchError struct {
	Err       error
	Retryable bool
}

func (e *LaunchError) Error() string { return e.Err.Error() }

func (e *LaunchError) Unwrap() error { return e.Err }

// Config tunes the agent.
type Config struct {
	// Capacity is how many launches the agent accepts per sync.
	Capacity int
	// Parallelism bounds concurrent provider calls.
	Parallelism int
	// Version is reported to the control plane.
	Version string
	// Region is where the provider launches, reported so runner configs
	// can pick this data plane by region.
	Region string
}

// Agent runs the sync loop.
type Agent struct {
	client   *Client
	provider Provider
	cfg      Config
	log      *slog.Logger

	dataPlaneID string
	failures    []agentproto.LaunchFailure
	// received maps runner IDs from recent launches to when they arrived.
	received map[string]time.Time
}

// New returns an Agent.
func New(client *Client, provider Provider, cfg Config, logger *slog.Logger) *Agent {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 10
	}
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 8
	}

	return &Agent{client: client, provider: provider, cfg: cfg, log: logger, received: map[string]time.Time{}}
}

const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Run syncs until ctx is done. Errors are retried with backoff; the agent
// never stops on its own, because its runners depend on it.
func (a *Agent) Run(ctx context.Context) error {
	backoff := minBackoff
	wait := 0
	for ctx.Err() == nil {
		busy, err := a.Step(ctx, wait)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			a.log.WarnContext(ctx, "sync failed", slog.Any("error", err), slog.Duration("retry_in", backoff))
			sleep(ctx, jitter(backoff))
			backoff = min(backoff*2, maxBackoff)

			continue
		}
		backoff = minBackoff
		// After acting, sync again right away so the control plane sees the
		// new instances; otherwise long-poll for work.
		wait = agentproto.MaxWaitSeconds
		if busy {
			wait = 0
		}
	}

	return fmt.Errorf("agent stopped: %w", context.Cause(ctx))
}

// Step runs one sync round and reports whether it launched or terminated
// anything.
func (a *Agent) Step(ctx context.Context, wait int) (bool, error) {
	req := agentproto.SyncRequest{
		AgentVersion: a.cfg.Version,
		Provider:     a.provider.Name(),
		Region:       a.cfg.Region,
		Instances:    []agentproto.Instance{},
		Failures:     a.failures,
		Capacity:     a.cfg.Capacity,
		WaitSeconds:  wait,
	}
	if req.Failures == nil {
		req.Failures = []agentproto.LaunchFailure{}
	}
	req.Received = a.acknowledge(time.Now())

	if a.dataPlaneID == "" {
		// First contact: learn which data plane this is before listing.
		req.InventoryUnknown, req.Capacity, req.WaitSeconds = true, 0, 0
	} else {
		instances, err := a.provider.List(ctx, a.dataPlaneID)
		if err != nil {
			// Never report a partial view. Heartbeat and try again.
			a.log.WarnContext(ctx, "could not list instances", slog.Any("error", err))
			req.InventoryUnknown, req.Capacity, req.WaitSeconds = true, 0, 0
		}
		for _, instance := range instances {
			req.Instances = append(req.Instances, agentproto.Instance{
				RunnerID: instance.RunnerID, InstanceID: instance.InstanceID, Stopping: instance.Stopping,
			})
		}
	}

	resp, err := a.client.Sync(ctx, req)
	if err != nil {
		return false, err
	}
	a.failures = nil
	if a.dataPlaneID == "" {
		a.dataPlaneID = resp.DataPlaneID
		a.log.InfoContext(ctx, "connected to control plane", slog.String("data_plane", resp.DataPlaneID))

		return true, nil
	}

	now := time.Now()
	for _, launch := range resp.Launch {
		a.received[launch.RunnerID] = now
	}
	a.apply(ctx, resp)

	return len(resp.Launch) > 0 || len(resp.Terminate) > 0, nil
}

// acknowledge returns the runners received within the window, forgetting
// older ones.
func (a *Agent) acknowledge(now time.Time) []string {
	out := make([]string, 0, len(a.received))
	for id, at := range a.received {
		if now.Sub(at) > agentproto.ReceivedWindow*time.Second {
			delete(a.received, id)

			continue
		}
		out = append(out, id)
	}

	return out
}

// apply terminates and launches in parallel. Terminate errors are only
// logged: the control plane repeats the request on the next sync.
func (a *Agent) apply(ctx context.Context, resp agentproto.SyncResponse) {
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		sem = make(chan struct{}, a.cfg.Parallelism)
	)
	for _, term := range resp.Terminate {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			log := a.log.With(slog.String("runner", term.RunnerID), slog.String("instance", term.InstanceID))
			if err := a.provider.Terminate(ctx, term.InstanceID); err != nil {
				log.WarnContext(ctx, "terminate failed", slog.Any("error", err))

				return
			}
			log.InfoContext(ctx, "terminated", slog.String("reason", term.Reason))
		})
	}
	for _, launch := range resp.Launch {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			log := a.log.With(slog.String("runner", launch.RunnerID), slog.String("profile", launch.Profile.Label))
			instanceID, err := a.provider.Launch(ctx, a.dataPlaneID, launch)
			if err != nil {
				retryable := false
				var launchErr *LaunchError
				if errors.As(err, &launchErr) {
					retryable = launchErr.Retryable
				}
				log.WarnContext(ctx, "launch failed", slog.Any("error", err), slog.Bool("retryable", retryable))
				mu.Lock()
				a.failures = append(a.failures, agentproto.LaunchFailure{RunnerID: launch.RunnerID, Error: truncate(err.Error(), 2000), Retryable: retryable})
				mu.Unlock()

				return
			}
			log.InfoContext(ctx, "launched", slog.String("instance", instanceID))
		})
	}
	wg.Wait()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}

func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1) //nolint:gosec // jitter, not security
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
