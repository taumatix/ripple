package app

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
)

type countingOSV struct {
	mu sync.Mutex
	n  int
}

func (c *countingOSV) Modified(context.Context, advisory.Position) ([]advisory.Entry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return nil, false, nil
}
func (c *countingOSV) Vuln(context.Context, string) (*advisory.Vuln, error) {
	return nil, advisory.ErrNotFound
}
func (c *countingOSV) calls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.n }

func osvApp(base store.Store, id string) (*App, *countingOSV) {
	src := &countingOSV{}
	eng := &engine.Engine{Store: base, Replica: id, LeaseTTL: time.Second}
	return &App{Engine: eng, Log: slog.New(slog.DiscardHandler), PollEvery: 50 * time.Millisecond,
		AdvisoryEvery: 50 * time.Millisecond,
		Advisories:    &engine.AdvisoryWatcher{Engine: eng, Source: src}}, src
}

// The OSV poll is elected like the other sources: of two replicas over one store the first
// reads OSV and the second does not, GET /replica names osv among the first's pollers, and
// when the first steps down the second takes the poll over.
func TestOnlyOneReplicaPollsOSVAndTheNextTakesOver(t *testing.T) {
	ctx := context.Background()
	base := store.NewMemory()
	a, sa := osvApp(base, "a")
	b, sb := osvApp(base, "b")
	for i := 0; i < 4; i++ { // the first poll only sets the cursor
		a.pollAdvisories(ctx)
		b.pollAdvisories(ctx)
	}
	if sa.calls() != 3 || sb.calls() != 0 {
		t.Fatalf("OSV read a=%d b=%d times, want 3 and 0", sa.calls(), sb.calls())
	}
	a.beat(ctx)
	b.beat(ctx)
	if got := replicaJSON(t, a); !equalStrings(got.Pollers, []string{"osv"}) {
		t.Fatalf("a reports pollers %v, want [osv]", got.Pollers)
	}
	if got := replicaJSON(t, b); len(got.Pollers) != 0 {
		t.Fatalf("b reports pollers %v, want none", got.Pollers)
	}
	a.stepDownFeed(ctx)
	b.pollAdvisories(ctx)
	if sb.calls() != 1 {
		t.Fatalf("b read OSV %d times after a stepped down, want 1", sb.calls())
	}
}

// A source a replica has configured and nobody polls is named by GET /replica. Here a dead
// replica's claim on osv has not lapsed yet, so the live one cannot poll it; once the claim
// is released the live one takes it and the list is empty.
func TestReplicaStatusNamesASourceNobodyPolls(t *testing.T) {
	ctx := context.Background()
	base := store.NewMemory()
	a, _ := osvApp(base, "a")
	if _, ok, err := base.AcquireLease(ctx, "feed:osv", "ghost", time.Hour); err != nil || !ok {
		t.Fatalf("ghost claim: %v %v", ok, err)
	}
	a.pollAdvisories(ctx)
	a.beat(ctx)
	got := replicaStatusJSON(t, a)
	if !equalStrings(got.Unpolled, []string{"osv"}) || got.Fleet != 0 {
		t.Fatalf("status = %+v, want osv unpolled and no poller", got)
	}
	l, _, _ := base.AcquireLease(ctx, "feed:osv", "ghost", time.Hour)
	if err := base.ReleaseLease(ctx, l); err != nil {
		t.Fatal(err)
	}
	a.pollAdvisories(ctx)
	a.beat(ctx)
	if got := replicaStatusJSON(t, a); len(got.Unpolled) != 0 || got.Fleet != 1 {
		t.Fatalf("status = %+v, want everything polled", got)
	}
}

func TestUnpolledSourcesIsConfiguredMinusPolledAcrossReplicas(t *testing.T) {
	live := []store.ReplicaInfo{
		{ID: "a", Configured: []string{"feed", "npm", "osv"}, Polled: []string{"feed"}},
		{ID: "b", Configured: []string{"feed", "npm"}, Polled: []string{"npm"}},
	}
	if got := unpolledSources(live); !equalStrings(got, []string{"osv"}) {
		t.Fatalf("got %v", got)
	}
	if got := unpolledSources(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
