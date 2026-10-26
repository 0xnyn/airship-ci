package fleet

import (
	"sync"

	"github.com/google/uuid"
)

// Notifier wakes long-polling agents of one data plane when new work is
// queued for it. It is an in-process hint only: agents' long polls also
// recheck the database every few seconds, so a notification lost to another
// replica only costs latency, never correctness.
type Notifier struct {
	mu      sync.Mutex
	waiters map[uuid.UUID]chan struct{}
}

// NewNotifier returns an empty Notifier.
func NewNotifier() *Notifier {
	return &Notifier{waiters: map[uuid.UUID]chan struct{}{}}
}

// Wait returns a channel closed by the next Notify for dataPlane.
func (n *Notifier) Wait(dataPlane uuid.UUID) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()

	ch, ok := n.waiters[dataPlane]
	if !ok {
		ch = make(chan struct{})
		n.waiters[dataPlane] = ch
	}

	return ch
}

// Notify wakes everyone waiting on dataPlane.
func (n *Notifier) Notify(dataPlane uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if ch, ok := n.waiters[dataPlane]; ok {
		close(ch)
		delete(n.waiters, dataPlane)
	}
}
