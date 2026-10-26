package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/jackc/pgx/v5/pgxpool"
)

// redeliverLockKey elects one API replica to scan and redeliver.
const redeliverLockKey = 0x41495244 // "AIRD"

// HookDeliveries is the app-level webhook delivery log.
type HookDeliveries interface {
	ListHookDeliveries(ctx context.Context, cursor string) ([]github.HookDelivery, string, error)
	Redeliver(ctx context.Context, deliveryID int64) error
}

// Redeliverer replays webhook deliveries that failed, because GitHub never
// retries them on its own. Without it, a control plane outage would silently
// drop every job queued during it.
//
// Each pass reads the delivery log (newest first) down to the newest
// delivery the previous pass saw, records failures in Postgres, and asks
// GitHub to resend those that are due, with exponential backoff, until an
// attempt succeeds. State lives in the database, so failures survive
// restarts and one replica does the work.
type Redeliverer struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	hooks HookDeliveries
	log   *slog.Logger

	// FirstScan bounds how far back the very first pass looks.
	FirstScan time.Duration
	// MaxPages bounds the delivery log pages (100 deliveries each) read per
	// pass. At a pass every two minutes this covers far more webhooks than
	// one app receives.
	MaxPages int
	// MaxAttempts is how many times one delivery is resent.
	MaxAttempts int32
	// Batch is how many deliveries are resent per pass.
	Batch int32
}

// NewRedeliverer returns a Redeliverer with production defaults.
func NewRedeliverer(pool *pgxpool.Pool, hooks HookDeliveries, logger *slog.Logger) *Redeliverer {
	return &Redeliverer{
		pool: pool, q: db.New(pool), hooks: hooks, log: logger,
		FirstScan: 6 * time.Hour, MaxPages: 100, MaxAttempts: 8, Batch: 100,
	}
}

// Run replays failures every interval until ctx is done.
func (r *Redeliverer) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if n, err := r.Once(ctx); err != nil && ctx.Err() == nil {
			r.log.WarnContext(ctx, "redeliver webhooks", slog.Any("error", err))
		} else if n > 0 {
			r.log.InfoContext(ctx, "redelivered failed webhooks", slog.Int("count", n))
		}
	}
}

// Once runs one pass and returns how many deliveries it asked GitHub to
// resend. It does nothing if another replica holds the lock. Redelivery
// reuses the X-GitHub-Delivery GUID and Airship's handlers are idempotent,
// so a duplicate is harmless.
func (r *Redeliverer) Once(ctx context.Context) (int, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "select pg_try_advisory_lock($1)", redeliverLockKey).Scan(&locked); err != nil {
		return 0, fmt.Errorf("take redeliver lock: %w", err)
	}
	if !locked {
		return 0, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", redeliverLockKey)
	}()

	if err := r.scan(ctx); err != nil {
		return 0, err
	}

	return r.resend(ctx)
}

// scan records failed deliveries newer than the watermark and resolves
// those that have since succeeded.
func (r *Redeliverer) scan(ctx context.Context) error {
	watermark, err := r.q.GetWebhookWatermark(ctx)
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("get watermark: %w", err)
	}
	since := time.Now().Add(-r.FirstScan)

	var (
		newest    = watermark
		failed    = map[string]github.HookDelivery{}
		succeeded = map[string]bool{}
		complete  bool
		cursor    string
	)
	for range r.MaxPages {
		page, next, err := r.hooks.ListHookDeliveries(ctx, cursor)
		if err != nil {
			return fmt.Errorf("list deliveries: %w", err)
		}
		for _, delivery := range page {
			if delivery.ID <= watermark || (watermark == 0 && delivery.DeliveredAt.Before(since)) {
				complete = true

				break
			}
			newest = max(newest, delivery.ID)
			if delivery.StatusCode >= 200 && delivery.StatusCode < 300 {
				succeeded[delivery.GUID] = true
			} else if _, seen := failed[delivery.GUID]; !seen {
				failed[delivery.GUID] = delivery // newest attempt first
			}
		}
		if complete || next == "" {
			complete = true

			break
		}
		cursor = next
	}
	if !complete {
		// Failures older than what was read this pass are not recorded.
		r.log.ErrorContext(ctx, "webhook delivery log has more new deliveries than one pass reads; older failures may be missed",
			slog.Int("pages", r.MaxPages))
	}

	for guid, delivery := range failed {
		if succeeded[guid] {
			continue
		}
		if err := r.q.RecordFailedDelivery(ctx, db.RecordFailedDeliveryParams{Guid: guid, DeliveryID: delivery.ID, Event: delivery.Event}); err != nil {
			return fmt.Errorf("record failed delivery: %w", err)
		}
	}
	resolved := make([]string, 0, len(succeeded))
	for guid := range succeeded {
		resolved = append(resolved, guid)
	}
	if err := r.q.ResolveDeliveries(ctx, resolved); err != nil {
		return fmt.Errorf("resolve deliveries: %w", err)
	}
	if err := r.q.DeleteOldDeliveries(ctx, time.Now().Add(-githubDeliveryRetention)); err != nil {
		return fmt.Errorf("delete old deliveries: %w", err)
	}
	if newest > watermark {
		if err := r.q.SetWebhookWatermark(ctx, newest); err != nil {
			return fmt.Errorf("set watermark: %w", err)
		}
	}

	return nil
}

// githubDeliveryRetention is how long GitHub keeps deliveries; older ones
// cannot be redelivered.
const githubDeliveryRetention = 72 * time.Hour

// resend asks GitHub to redeliver every failure that is due.
func (r *Redeliverer) resend(ctx context.Context) (int, error) {
	due, err := r.q.ClaimDueDeliveries(ctx, db.ClaimDueDeliveriesParams{MaxAttempts: r.MaxAttempts, MaxResults: r.Batch})
	if err != nil {
		return 0, fmt.Errorf("claim due deliveries: %w", err)
	}
	count := 0
	for _, delivery := range due {
		log := r.log.With(slog.String("guid", delivery.Guid), slog.String("event", delivery.Event), slog.Int("attempt", int(delivery.Attempts)))
		if delivery.Attempts == r.MaxAttempts {
			log.ErrorContext(ctx, "last redelivery attempt for a webhook that keeps failing")
		}
		if err := r.hooks.Redeliver(ctx, delivery.DeliveryID); err != nil {
			log.WarnContext(ctx, "redeliver", slog.Any("error", err))

			continue
		}
		count++
	}

	return count, nil
}
