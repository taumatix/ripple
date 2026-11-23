package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// DefaultLeaseTTL is how long a replica's claim on a repo lasts without renewal. A
// replica that dies mid-job is out of the way after this long, so it is also the delay
// before another replica retakes the work.
const DefaultLeaseTTL = 30 * time.Second

// ErrLeaseLost is the cause of a run's context being cancelled because another replica
// holds the repo's lease now.
var ErrLeaseLost = store.ErrLeaseLost

// LeaseName is the store lease that makes one replica at a time act on a repo.
func LeaseName(repo string) string { return "repo:" + repo }

// replicaID returns this engine's replica id: Replica when set, otherwise a random one chosen
// once. Two engines on one store must differ; the random default guarantees it.
func (e *Engine) replicaID() string {
	e.idOnce.Do(func() {
		if e.Replica != "" {
			e.id = e.Replica
			return
		}
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		e.id = hex.EncodeToString(b)
	})
	return e.id
}

func (e *Engine) leaseTTL() time.Duration {
	if e.LeaseTTL > 0 {
		return e.LeaseTTL
	}
	return DefaultLeaseTTL
}

// repoLease is this replica's hold on a repo (see store.Held).
type repoLease = store.Held

// acquireRepo claims the repo for this replica and keeps the claim renewed until Release.
// With wait it polls until the repo is free or ctx ends; without it a held repo returns
// (nil, nil) at once. Several engines in one process contend through the store like any
// other replicas, so no special case is needed for them.
func (e *Engine) acquireRepo(ctx context.Context, name string, wait bool) (*repoLease, error) {
	ttl := e.leaseTTL()
	for {
		h, err := store.Hold(ctx, e.Store, e.log(), LeaseName(name), e.replicaID(), ttl)
		if err != nil || h != nil || !wait {
			return h, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(max(ttl/10, 5*time.Millisecond), 2*time.Second)):
		}
	}
}
