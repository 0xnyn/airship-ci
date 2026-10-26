package fleet_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/fleet"
	"github.com/wizenheimer/airship/apps/api/internal/github"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/fakegithub"
	"github.com/wizenheimer/airship/apps/api/internal/testsupport/pgtest"
)

func TestRedeliverer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := pgtest.New(t)
	gh := fakegithub.New(t)
	now := time.Now()
	// Oldest first: the fake serves them newest first, like GitHub.
	gh.AddDelivery(github.HookDelivery{ID: 1, GUID: "ancient", DeliveredAt: now.Add(-48 * time.Hour), StatusCode: 500})
	gh.AddDelivery(github.HookDelivery{ID: 2, GUID: "recovered", DeliveredAt: now.Add(-3 * time.Minute), StatusCode: 502})
	gh.AddDelivery(github.HookDelivery{ID: 3, GUID: "failed", DeliveredAt: now.Add(-2 * time.Minute), StatusCode: 500, Event: "workflow_job"})
	gh.AddDelivery(github.HookDelivery{ID: 4, GUID: "ok", DeliveredAt: now.Add(-time.Minute), StatusCode: 204})
	gh.AddDelivery(github.HookDelivery{ID: 5, GUID: "recovered", DeliveredAt: now.Add(-time.Minute), StatusCode: 204, Redelivery: true})

	newRedeliverer := func() *fleet.Redeliverer {
		return fleet.NewRedeliverer(pool, gh.App(t), slog.New(slog.DiscardHandler))
	}
	once := func(r *fleet.Redeliverer) int {
		t.Helper()
		n, err := r.Once(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return n
	}

	r := newRedeliverer()
	if n := once(r); n != 1 || !slices.Equal(gh.Redelivered(), []int64{3}) {
		t.Fatalf("redelivered %v (count %d), want only delivery 3", gh.Redelivered(), n)
	}
	// It is not asked for again until its backoff passes.
	if n := once(r); n != 0 {
		t.Fatal("the same failure was redelivered without backing off")
	}

	// The resend failed too. The failure is kept in the database, so a
	// restarted API (a new Redeliverer) still retries it when due.
	gh.AddDelivery(github.HookDelivery{ID: 6, GUID: "failed", DeliveredAt: time.Now(), StatusCode: 503, Redelivery: true})
	if _, err := pool.Exec(ctx, "update failed_deliveries set next_attempt_at = now()"); err != nil {
		t.Fatal(err)
	}
	if n := once(newRedeliverer()); n != 1 || !slices.Equal(gh.Redelivered(), []int64{3, 6}) {
		t.Fatalf("redelivered %v, want the newest failed attempt (6) resent", gh.Redelivered())
	}

	// Once an attempt succeeds the failure is resolved for good.
	gh.AddDelivery(github.HookDelivery{ID: 7, GUID: "failed", DeliveredAt: time.Now(), StatusCode: 204, Redelivery: true})
	if _, err := pool.Exec(ctx, "update failed_deliveries set next_attempt_at = now()"); err != nil {
		t.Fatal(err)
	}
	if n := once(r); n != 0 {
		t.Fatal("a delivery that succeeded was resent")
	}
	var left int
	if err := pool.QueryRow(ctx, "select count(*) from failed_deliveries").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d failures still tracked, want 0", left)
	}
}
