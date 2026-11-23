package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

type countingFeed struct{ fetches atomic.Int64 }

func (f *countingFeed) Name() string       { return "countfeed" }
func (f *countingFeed) Datasource() string { return "go" }
func (f *countingFeed) Fetch(context.Context, string) ([]model.Release, bool, error) {
	f.fetches.Add(1)
	return nil, false, nil
}

// Replicas that all poll the release feed waste requests. PollElected makes one of them the
// poller: the others skip until its claim lapses, and a graceful stop hands it over at once.
func TestOnlyTheElectedReplicaPollsTheFeed(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mk := func(id string) (*Watcher, *countingFeed) {
		f := &countingFeed{}
		return &Watcher{Engine: &Engine{Store: st, Replica: id, Now: func() time.Time { return now }}, Feed: f}, f
	}
	a, fa := mk("a")
	b, fb := mk("b")
	const ttl = 400 * time.Millisecond

	// The first poll seeds the cursor without fetching, so poll twice to reach Fetch.
	for i := 0; i < 2; i++ {
		if _, polled, err := a.PollElected(ctx, ttl); err != nil || !polled {
			t.Fatalf("a poll %d: polled=%v err=%v", i, polled, err)
		}
	}
	if fa.fetches.Load() != 1 {
		t.Fatalf("a fetched %d times", fa.fetches.Load())
	}
	if _, polled, err := b.PollElected(ctx, ttl); err != nil || polled {
		t.Fatalf("b polled while a holds the feed: polled=%v err=%v", polled, err)
	}
	if fb.fetches.Load() != 0 {
		t.Fatal("b fetched the feed while a was elected")
	}
	if !a.Elected() || b.Elected() {
		t.Fatalf("Elected: a=%v b=%v", a.Elected(), b.Elected())
	}

	// a stops gracefully: b takes over at once, without waiting for the claim to lapse.
	a.StepDown(ctx)
	if _, polled, err := b.PollElected(ctx, ttl); err != nil || !polled {
		t.Fatalf("b after a stepped down: polled=%v err=%v", polled, err)
	}
	if a.Elected() || !b.Elected() {
		t.Fatalf("Elected after hand-over: a=%v b=%v", a.Elected(), b.Elected())
	}

	// b dies without stepping down: its claim lapses and a takes over, which counts.
	before := store.LeaseTakeovers()
	time.Sleep(ttl + 100*time.Millisecond)
	if _, polled, err := a.PollElected(ctx, ttl); err != nil || !polled {
		t.Fatalf("a after b's claim lapsed: polled=%v err=%v", polled, err)
	}
	if got := store.LeaseTakeovers() - before; got != 1 {
		t.Fatalf("takeovers = %d, want 1", got)
	}
}
