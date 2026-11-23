package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

type fetchCounter struct{ n atomic.Int64 }

func (f *fetchCounter) Name() string       { return "countfeed" }
func (f *fetchCounter) Datasource() string { return "go" }
func (f *fetchCounter) Fetch(context.Context, string) ([]model.Release, bool, error) {
	f.n.Add(1)
	return nil, false, nil
}

func feedApp(st store.Store, id string, f *fetchCounter) *App {
	eng := &engine.Engine{Store: st, Replica: id, LeaseTTL: time.Second}
	return &App{Engine: eng, Watcher: &engine.Watcher{Engine: eng, Feed: f}, Log: slog.New(slog.DiscardHandler), PollEvery: time.Second}
}

// Replicas that share a store poll the release feed one at a time: the poll loop of the
// replica that is not elected skips it.
func TestOnlyOneReplicaPollsTheFeed(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	fa, fb := &fetchCounter{}, &fetchCounter{}
	a, b := feedApp(st, "a", fa), feedApp(st, "b", fb)
	for i := 0; i < 3; i++ { // the first poll only seeds the cursor
		a.pollFeed(ctx)
		b.pollFeed(ctx)
	}
	if fa.n.Load() != 2 || fb.n.Load() != 0 {
		t.Fatalf("fetches: a=%d b=%d, want 2 and 0", fa.n.Load(), fb.n.Load())
	}
	a.stepDownFeed(ctx)
	b.pollFeed(ctx)
	if fb.n.Load() != 1 {
		t.Fatalf("b did not take over after a stepped down: %d fetches", fb.n.Load())
	}
}

// GET /replica says which replica answered, whether it polls the feed, and how many leases
// it has taken over: the service has no metrics endpoint, so this is what an operator can scrape.
func TestReplicaStatusEndpoint(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	a := feedApp(st, "ripple-1", &fetchCounter{})
	a.pollFeed(ctx)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/replica", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got struct {
		Replica         string `json:"replica"`
		FeedPoller      bool   `json:"feed_poller"`
		LeaseTakeovers  int64  `json:"lease_takeovers"`
		LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if got.Replica != "ripple-1" || !got.FeedPoller || got.LeaseTTLSeconds != 1 || got.LeaseTakeovers < 0 {
		t.Fatalf("status = %+v", got)
	}
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/replica", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /replica = %d", rec.Code)
	}
}
