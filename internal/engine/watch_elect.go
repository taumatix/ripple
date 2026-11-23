package engine

import (
	"context"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// feedLease names the claim that makes one replica poll a feed.
func feedLease(feedName string) string { return "feed:" + feedName }

// election is a replica's standing claim on the feed's poll.
type election struct {
	mu    sync.Mutex
	lease store.Lease
	held  bool
}

// PollElected is Poll for replicas that share a store: only the replica that holds the feed's
// claim polls, the others skip and report polled=false. The claim lasts ttl from the last
// poll, so give it a few poll intervals: the holder extends it on each poll, and when it dies
// another replica takes over once the claim lapses. Two replicas polling would be safe (job
// enqueue is insert-if-absent) but each release would be fetched twice.
func (w *Watcher) PollElected(ctx context.Context, ttl time.Duration) (jobs int, polled bool, err error) {
	e := w.Engine
	l, ok, err := e.Store.AcquireLease(ctx, feedLease(w.Feed.Name()), e.replicaID(), ttl)
	w.elect.mu.Lock()
	switch {
	case err != nil || !ok:
		w.elect.held = false
	default:
		if l.Token > 1 && l.Token != w.elect.lease.Token {
			// The same owner re-acquiring a live claim keeps its token; a new token above 1 is a
			// takeover from a replica that died or stalled.
			store.NoteTakeover(e.log(), l, e.replicaID())
		}
		w.elect.lease, w.elect.held = l, true
	}
	w.elect.mu.Unlock()
	if err != nil || !ok {
		return 0, false, err
	}
	jobs, err = w.Poll(ctx)
	return jobs, true, err
}

// Elected reports whether this replica held the feed's claim at its last PollElected.
func (w *Watcher) Elected() bool {
	w.elect.mu.Lock()
	defer w.elect.mu.Unlock()
	return w.elect.held
}

// StepDown gives the claim up so another replica can poll at once; call it when stopping.
func (w *Watcher) StepDown(ctx context.Context) {
	w.elect.mu.Lock()
	l, held := w.elect.lease, w.elect.held
	w.elect.held = false
	w.elect.mu.Unlock()
	if held {
		_ = w.Engine.Store.ReleaseLease(ctx, l)
	}
}
