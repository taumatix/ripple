package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/taumatix/ripple/internal/model"
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

// NewReplicaID returns a random replica id. A process that names itself neither through
// Engine.Replica nor RIPPLE_REPLICA uses one, shared by its engine and event queue.
func NewReplicaID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// replicaID returns this engine's replica id: Replica when set, otherwise a random one chosen
// once. Two engines on one store must differ; the random default guarantees it.
func (e *Engine) replicaID() string {
	e.idOnce.Do(func() {
		if e.Replica != "" {
			e.id = e.Replica
			return
		}
		e.id = NewReplicaID()
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

// updateJob writes j under the repo lease: while hold is this replica's live claim the write
// is fenced on its token, so a replica that lapsed and was replaced cannot overwrite the
// job row the new holder owns. A nil hold is an unleased caller and writes plainly.
func (e *Engine) updateJob(ctx context.Context, hold *repoLease, j model.Job) error {
	if hold == nil {
		return e.Store.UpdateJob(ctx, j)
	}
	return e.Store.UpdateJobFenced(ctx, j, hold.Lease())
}

// leaseRepo takes the in-process lock of the repo and then its lease, waiting for both, for a
// mutation of the repo's state (pause, resume, rename, remove): another replica's scan or
// run of the repo finishes first, and its writes cannot interleave with this one. The
// returned context ends if the lease is lost; release gives both back.
func (e *Engine) leaseRepo(ctx context.Context, name string) (context.Context, *repoLease, func(), error) {
	unlock := e.lockRepo(name)
	hold, err := e.acquireRepo(ctx, name, true)
	if err != nil {
		unlock()
		return ctx, nil, nil, err
	}
	return hold.Ctx, hold, func() { hold.Release(); unlock() }, nil
}

// leaseRepos is leaseRepo for two repos, taken in sorted order so two crossing renames, here
// or on different replicas, cannot deadlock. The second lease is held on the first's context,
// so losing either ends the work.
func (e *Engine) leaseRepos(ctx context.Context, a, b string) (context.Context, map[string]*repoLease, func(), error) {
	names := []string{a, b}
	sort.Strings(names)
	ctx1, h1, rel1, err := e.leaseRepo(ctx, names[0])
	if err != nil {
		return ctx, nil, nil, err
	}
	ctx2, h2, rel2, err := e.leaseRepo(ctx1, names[1])
	if err != nil {
		rel1()
		return ctx, nil, nil, err
	}
	return ctx2, map[string]*repoLease{names[0]: h1, names[1]: h2}, func() { rel2(); rel1() }, nil
}
