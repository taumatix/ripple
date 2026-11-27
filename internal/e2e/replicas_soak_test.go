package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// The replica soak (ROADMAP 101): three replicas, each with its own handle on one shared
// store and its own clock, run many repos' jobs against the fake GitHub; every repo must end
// with exactly one PR and a done job whatever the clocks say. Postgres and DynamoDB Local
// run when CI provides them (ci.yml, soak.yml); RIPPLE_SOAK_REPOS and RIPPLE_SOAK_ROUNDS
// scale it (docs/replicas.md).

func soakInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// soakLeaseSkew is the DynamoDB takeover margin the soak handles use. Skews within it must
// not make a replica steal a live lease; skews beyond it may (docs/replicas.md "Clocks").
const soakLeaseSkew = 250 * time.Millisecond

func runSoak(t *testing.T, skews []time.Duration) {
	repos := soakInt("RIPPLE_SOAK_REPOS", 12)
	testutil.ForEachSharedStore(t, skews, soakLeaseSkew, func(t *testing.T, h testutil.Handles) {
		e := newEnv(t, h[0], defaultLimits())
		names := replicaRepos(t, e, repos)
		var rs []*replica
		for i, st := range h {
			r := e.newReplica(fmt.Sprintf("r%d", i), st, nil)
			off := skews[i]
			r.eng.Now = func() time.Time { return e.clock.Now().Add(off) }
			rs = append(rs, r)
		}
		for _, r := range rs {
			r.start()
			defer r.stop()
		}
		e.eventually("every job done", func() bool {
			for _, n := range names {
				if !e.jobDone(n) {
					return false
				}
			}
			return true
		})
		for _, r := range rs {
			r.stop()
		}
		for _, n := range names {
			if got := len(e.gh.PRs(n)); got != 1 {
				t.Errorf("%s: %d PRs, want exactly 1", n, got)
			}
			if j, _, _ := e.st.GetJob(context.Background(), model.JobKey(n, "go", "example.com/lib", "v1.1.0")); j.State != model.JobDone || j.PRURL == "" {
				t.Errorf("%s: job %+v, want done with a PR", n, j)
			}
		}
		calls := 0
		for _, r := range rs {
			calls += callsOf(r, names)
		}
		t.Logf("skews %v: %d repos, %d platform calls, %d lease takeovers", skews, repos, calls, e.logLines("lease taken over"))
	})
}

// Clocks that agree to within the lease margin: the ordinary multi-node case.
func TestReplicaSoakClocksWithinMargin(t *testing.T) {
	for i := 0; i < soakInt("RIPPLE_SOAK_ROUNDS", 1); i++ {
		t.Run(fmt.Sprintf("round%d", i), func(t *testing.T) {
			runSoak(t, []time.Duration{0, 40 * time.Millisecond, -40 * time.Millisecond})
		})
	}
}

// Clocks seconds apart, far beyond the margin and ten times the lease TTL: a replica ahead
// sees others' live leases as lapsed and may take them early. That is wasteful and must stay
// safe: one PR per repo, no job lost.
func TestReplicaSoakClocksBeyondMargin(t *testing.T) {
	for i := 0; i < soakInt("RIPPLE_SOAK_ROUNDS", 1); i++ {
		t.Run(fmt.Sprintf("round%d", i), func(t *testing.T) {
			runSoak(t, []time.Duration{0, 3 * time.Second, -6 * time.Second})
		})
	}
}

// The event queue's order across replicas (docs/replicas.md): events of one repo enqueued
// through handles whose clocks disagree by seconds get strictly increasing Seq in the order
// the store received them, from any handle, concurrently with other repos.
func TestReplicaSoakEventOrderIgnoresClocks(t *testing.T) {
	skews := []time.Duration{0, 5 * time.Second, -5 * time.Second}
	testutil.ForEachSharedStore(t, skews, soakLeaseSkew, func(t *testing.T, h testutil.Handles) {
		ctx := context.Background()
		const nRepos = 6
		per := soakInt("RIPPLE_SOAK_EVENTS", 20)
		order := make([][]model.Event, nRepos)
		var wg sync.WaitGroup
		for r := 0; r < nRepos; r++ {
			wg.Add(1)
			go func(r int) {
				defer wg.Done()
				repo := fmt.Sprintf("acme/r%d", r)
				for i := 0; i < per; i++ {
					st := h[(i+r)%len(h)].(store.EventSequencer)
					// Seq is the replica's clock when the store does not number: set it wildly
					// wrong so a store that trusted it would misorder the queue.
					ev, ok, err := st.EnqueueSequenced(ctx, model.Event{ID: fmt.Sprintf("%s-%03d", repo, i), Kind: "push", Repo: repo,
						Seq: time.Now().Add(skews[(i+r)%len(h)]).UnixNano()})
					if err != nil || !ok {
						t.Errorf("enqueue %s %d: ok=%v err=%v", repo, i, ok, err)
						return
					}
					order[r] = append(order[r], ev)
				}
			}(r)
		}
		wg.Wait()
		if t.Failed() {
			return
		}
		seen := map[int64]string{}
		for r := range order {
			for i, ev := range order[r] {
				if i > 0 && ev.Seq <= order[r][i-1].Seq {
					t.Errorf("%s: seq %d after %d: the queue is out of order", ev.ID, ev.Seq, order[r][i-1].Seq)
				}
				if other, dup := seen[ev.Seq]; dup {
					t.Errorf("%s and %s share seq %d", ev.ID, other, ev.Seq)
				}
				seen[ev.Seq] = ev.ID
			}
		}
		live, err := h[1].ListEvents(ctx)
		if err != nil || len(live) != nRepos*per {
			t.Fatalf("ListEvents: %d events, err %v; want %d", len(live), err, nRepos*per)
		}
		for i := 1; i < len(live); i++ {
			if live[i].Seq < live[i-1].Seq {
				t.Fatalf("ListEvents not ascending at %d: %d after %d", i, live[i].Seq, live[i-1].Seq)
			}
		}
		// Each repo's events drain oldest first: the newest has an older sibling until all
		// the older ones are gone, whichever handle asks.
		for r := range order {
			for i, ev := range order[r] {
				older, err := h[(i+1)%len(h)].HasOlderEvent(ctx, ev)
				if err != nil || older != (i > 0) {
					t.Fatalf("%s: HasOlderEvent = %v, %v; want %v", ev.ID, older, err, i > 0)
				}
			}
			for i, ev := range order[r] {
				if err := h[(i+2)%len(h)].DeleteEvent(ctx, ev.ID); err != nil {
					t.Fatal(err)
				}
				if i+1 < len(order[r]) {
					if older, err := h[i%len(h)].HasOlderEvent(ctx, order[r][i+1]); err != nil || older {
						t.Fatalf("%s after deleting %s: older = %v, %v", order[r][i+1].ID, ev.ID, older, err)
					}
				}
			}
		}
	})
}

// Three handles enqueue to one repo at the same moment: every event gets its own Seq.
func TestReplicaSoakConcurrentEnqueueOneRepo(t *testing.T) {
	testutil.ForEachSharedStore(t, []time.Duration{0, 5 * time.Second, -5 * time.Second}, soakLeaseSkew, func(t *testing.T, h testutil.Handles) {
		ctx := context.Background()
		per := soakInt("RIPPLE_SOAK_EVENTS", 20)
		var mu sync.Mutex
		seqs := map[int64]string{}
		var wg sync.WaitGroup
		for i, st := range h {
			wg.Add(1)
			go func(i int, st store.EventSequencer) {
				defer wg.Done()
				for n := 0; n < per; n++ {
					ev, ok, err := st.EnqueueSequenced(ctx, model.Event{ID: fmt.Sprintf("h%d-%03d", i, n), Kind: "push", Repo: "acme/one"})
					if err != nil || !ok {
						t.Errorf("enqueue: ok=%v err=%v", ok, err)
						return
					}
					mu.Lock()
					if other, dup := seqs[ev.Seq]; dup {
						t.Errorf("%s and %s share seq %d", ev.ID, other, ev.Seq)
					}
					seqs[ev.Seq] = ev.ID
					mu.Unlock()
				}
			}(i, st.(store.EventSequencer))
		}
		wg.Wait()
		live, err := h[0].ListEvents(ctx)
		if err != nil || len(live) != len(h)*per {
			t.Fatalf("ListEvents: %d, %v; want %d", len(live), err, len(h)*per)
		}
		for i := 1; i < len(live); i++ {
			if live[i].Seq <= live[i-1].Seq {
				t.Fatalf("seq not strictly ascending at %d", i)
			}
		}
	})
}

// racePlatform opens the branch's PR itself, as another replica that took the repo over
// would, just before this replica's own CreatePR.
type racePlatform struct {
	platform.Platform
	once sync.Once
}

func (p *racePlatform) CreatePR(ctx context.Context, r model.Repo, s platform.PRSpec) (platform.PR, error) {
	p.once.Do(func() { _, _ = p.Platform.CreatePR(ctx, r, s) })
	return p.Platform.CreatePR(ctx, r, s)
}

// The window the soak found (ROADMAP 101): between a run's listing of open PRs and its
// CreatePR another replica opens the branch's PR. The host refuses the second PR; the run
// adopts the one that exists instead of failing the job, and there is one PR.
func TestCreatePRRefusedBecauseAnotherReplicaOpenedItIsAdopted(t *testing.T) {
	testutil.ForEachStoreWithFake(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		repo := replicaRepos(t, e, 1)[0]
		a := e.newReplica("a", st, func(p platform.Platform) platform.Platform { return &racePlatform{Platform: p} })
		a.start()
		defer a.stop()
		e.eventually("job done", func() bool { return e.jobDone(repo) })
		a.stop()
		if n := len(e.gh.PRs(repo)); n != 1 {
			t.Fatalf("%d PRs, want exactly 1", n)
		}
		j, _, _ := st.GetJob(context.Background(), model.JobKey(repo, "go", "example.com/lib", "v1.1.0"))
		if j.PRURL == "" || e.logLines("opened by another replica") != 1 {
			t.Errorf("job %+v, adoption log lines %d", j, e.logLines("opened by another replica"))
		}
	})
}
