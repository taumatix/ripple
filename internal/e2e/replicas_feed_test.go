package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Two replicas sharing a store poll the release feed through the election: one polls, the
// other skips, every repo gets exactly one job, and when the poller steps down the other
// takes the next release.
func TestOnlyOneReplicaPollsTheFeedAndTheNextTakesOver(t *testing.T) {
	testutil.ForEachStoreWithFake(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		repos := replicaRepos(t, e, 4)
		a, b := e.newReplica("a", st, nil), e.newReplica("b", st, nil)
		wa := &engine.Watcher{Engine: a.eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}}
		wb := &engine.Watcher{Engine: b.eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}}
		ctx := context.Background()

		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.2.0")
		_, pa, err := wa.PollElected(ctx, time.Minute)
		if err != nil || !pa {
			t.Fatalf("first replica polled=%v err=%v; want it elected", pa, err)
		}
		if _, pb, err := wb.PollElected(ctx, time.Minute); err != nil || pb {
			t.Fatalf("second replica polled=%v err=%v; want it to skip", pb, err)
		}
		if !wa.Elected() || wb.Elected() {
			t.Fatalf("elected a=%v b=%v; want only a", wa.Elected(), wb.Elected())
		}
		for _, r := range repos {
			if j := e.job(r, "example.com/lib", "v1.2.0"); j.Key == "" {
				t.Fatalf("%s has no job for v1.2.0", r)
			}
		}

		wa.StepDown(ctx)
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.3.0")
		n, pb, err := wb.PollElected(ctx, time.Minute)
		if err != nil || !pb || n != len(repos) {
			t.Fatalf("after step-down b polled=%v jobs=%d err=%v; want polled with %d jobs", pb, n, err, len(repos))
		}
	})
}
