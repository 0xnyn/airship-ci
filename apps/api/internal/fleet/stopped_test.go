package fleet_test

import "testing"

// A machine someone stopped by hand runs no job and still costs its
// disk: the inventory scan terminates it, writes the runner off as lost
// and queues a replacement for its job.
func TestStoppedInstanceIsTerminatedAndReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	launched := h.launch()
	if len(launched) != 1 {
		t.Fatalf("launched = %d", len(launched))
	}
	h.aws.Stop(launched[0].ID)
	h.exec("update runners set running_at = now() - interval '10 minutes'")
	h.reconcile()
	if left := h.aws.Instances(); len(left) != 0 {
		t.Fatalf("stopped instance still exists: %+v", left)
	}
	wantStates(t, h, "finished:lost", "pending")
}
