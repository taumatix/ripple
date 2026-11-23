package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
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

// Two replicas on one store each beat; either answers GET /replica with the count of both and
// the takeovers of both, and a replica that retires (clean shutdown) drops out at once.
func TestReplicaStatusCountsLiveReplicas(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	a, b := feedApp(st, "a", &fetchCounter{}), feedApp(st, "b", &fetchCounter{})
	a.pollFeed(ctx) // a becomes the feed poller
	b.pollFeed(ctx)
	a.beat(ctx)
	b.beat(ctx)
	read := func(x *App) (out struct {
		Replicas       int   `json:"replicas_live"`
		TakeoversTotal int64 `json:"lease_takeovers_all_replicas"`
		Pollers        int   `json:"feed_pollers"`
	}) {
		rec := httptest.NewRecorder()
		x.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/replica", nil))
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v: %s", err, rec.Body.String())
		}
		return out
	}
	if got := read(b); got.Replicas != 2 || got.Pollers != 1 {
		t.Fatalf("status from b = %+v, want 2 replicas and 1 poller", got)
	}
	a.retire(ctx)
	if got := read(b); got.Replicas != 1 || got.Pollers != 0 {
		t.Fatalf("after a retired = %+v, want 1 replica and no poller", got)
	}
}

// The same over real sockets and Run: two replicas serve HTTP on one store, each beats from
// Run, and when one is stopped the other soon reports one.
func TestRunBeatsAndRetiresOverHTTP(t *testing.T) {
	st := store.NewMemory()
	type srv struct {
		url  string
		stop context.CancelFunc
		done chan struct{}
	}
	start := func(id string) srv {
		a := feedApp(st, id, &fetchCounter{})
		a.PollEvery = 0
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = a.Run(ctx, ln) }()
		t.Cleanup(func() { cancel(); <-done })
		return srv{"http://" + ln.Addr().String(), cancel, done}
	}
	live := func(s srv) int {
		resp, err := http.Get(s.url + "/replica")
		if err != nil {
			return -1
		}
		defer resp.Body.Close()
		var out struct {
			Replicas *int `json:"replicas_live"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) != nil || out.Replicas == nil {
			return -1
		}
		return *out.Replicas
	}
	a, b := start("a"), start("b")
	waitFor := func(s srv, want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for live(s) != want {
			if time.Now().After(deadline) {
				t.Fatalf("replicas_live = %d, want %d", live(s), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor(b, 2)
	a.stop()
	<-a.done
	waitFor(b, 1)
}
