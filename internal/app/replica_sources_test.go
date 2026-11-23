package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/cratesindex"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/npmreg"
	"github.com/taumatix/ripple/internal/store"
)

// sourceStore counts, per replica, the reads each poller starts with: the package-list cursor
// of the Actions, Docker, npm and Cargo pollers and the dependents of the Go toolchain poller.
// A replica that does not poll a source never reads it.
type sourceStore struct {
	store.Store
	rt store.ReplicaTable
	mu sync.Mutex
	n  map[string]int
}

func (s *sourceStore) hit(k string) { s.mu.Lock(); s.n[k]++; s.mu.Unlock() }
func (s *sourceStore) hits(k string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n[k]
}

func (s *sourceStore) GetCursor(ctx context.Context, k string) (string, error) {
	switch k {
	case "npm:packages", "docker:packages", "github-tags:packages", "cargo:packages":
		s.hit(k)
	}
	return s.Store.GetCursor(ctx, k)
}

func (s *sourceStore) ListDependents(ctx context.Context, ds, pkg string) ([]model.Dependency, error) {
	if ds == model.DatasourceGoToolchain && pkg == model.PackageGoDirective {
		s.hit(ds)
	}
	return s.Store.ListDependents(ctx, ds, pkg)
}

func (s *sourceStore) Heartbeat(ctx context.Context, r store.ReplicaInfo, ttl time.Duration) error {
	return s.rt.Heartbeat(ctx, r, ttl)
}
func (s *sourceStore) LiveReplicas(ctx context.Context) ([]store.ReplicaInfo, error) {
	return s.rt.LiveReplicas(ctx)
}
func (s *sourceStore) RetireReplica(ctx context.Context, id string) error {
	return s.rt.RetireReplica(ctx, id)
}

type noToolchain struct{}

func (noToolchain) Supported(context.Context) ([]string, error) { return nil, nil }

type noImages struct{}

func (noImages) Tags(context.Context, string) ([]string, error)         { return nil, nil }
func (noImages) Digest(context.Context, string, string) (string, error) { return "", nil }

type noNpm struct{}

func (noNpm) Packument(context.Context, string) (npmreg.Packument, error) {
	return npmreg.Packument{}, npmreg.ErrNotFound
}

type noCrates struct{}

func (noCrates) Crate(context.Context, string) (cratesindex.Crate, error) {
	return cratesindex.Crate{}, nil
}

var allSources = []string{"npm:packages", "docker:packages", "github-tags:packages", "cargo:packages", model.DatasourceGoToolchain}

func sourcesApp(base store.Store, id string) (*App, *sourceStore) {
	st := &sourceStore{Store: base, rt: base.(store.ReplicaTable), n: map[string]int{}}
	eng := &engine.Engine{Store: st, Replica: id, LeaseTTL: time.Second,
		Registry: noImages{}, Npm: noNpm{}, Crates: noCrates{}}
	return &App{Engine: eng, Log: slog.New(slog.DiscardHandler), PollEvery: 50 * time.Millisecond,
		Watcher:   &engine.Watcher{Engine: eng, Feed: &fetchCounter{}},
		Toolchain: &engine.ToolchainWatcher{Engine: eng, Source: noToolchain{}},
		Actions:   &engine.ActionsWatcher{Engine: eng},
		Docker:    &engine.DockerWatcher{Engine: eng},
		Npm:       &engine.NpmWatcher{Engine: eng},
		Cargo:     &engine.CargoWatcher{Engine: eng}}, st
}

func (a *App) pollAllSources(ctx context.Context) {
	a.pollToolchain(ctx)
	a.pollActions(ctx)
	a.pollDocker(ctx)
	a.pollNpm(ctx)
	a.pollCargo(ctx)
}

func replicaJSON(t *testing.T, a *App) (out struct {
	Pollers    []string `json:"pollers"`
	FeedPoller bool     `json:"feed_poller"`
	Fleet      int      `json:"feed_pollers"`
}) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/replica", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /replica = %d %v: %s", rec.Code, err, rec.Body.String())
	}
	return out
}

// Two replicas over one store: only the first polls the toolchain, Actions, Docker, npm and
// Cargo sources (and the feed); GET /replica says so; when it dies without stepping down the
// other takes every source over once the claims lapse; and a clean step-down hands them back at once.
func TestOnlyOneReplicaPollsEachSourceAndTheNextTakesOver(t *testing.T) {
	ctx := context.Background()
	base := store.NewMemory()
	a, sa := sourcesApp(base, "a")
	b, sb := sourcesApp(base, "b")
	for i := 0; i < 3; i++ {
		a.pollFeed(ctx)
		a.pollAllSources(ctx)
		b.pollFeed(ctx)
		b.pollAllSources(ctx)
	}
	for _, k := range allSources {
		if sa.hits(k) != 3 || sb.hits(k) != 0 {
			t.Fatalf("%s polled a=%d b=%d times, want 3 and 0", k, sa.hits(k), sb.hits(k))
		}
	}
	a.beat(ctx)
	b.beat(ctx)
	want := []string{"cargo", "docker", "feed", "github-actions", "go-toolchain", "npm"}
	if got := replicaJSON(t, b); len(got.Pollers) != 0 || got.FeedPoller || got.Fleet != 1 {
		t.Fatalf("b reports %+v, want no pollers and one in the fleet", got)
	}
	if got := replicaJSON(t, a); !equalStrings(got.Pollers, want) || !got.FeedPoller {
		t.Fatalf("a reports %+v, want pollers %v", got, want)
	}

	// a dies: it stops polling and never steps down. Its claims lapse after 2*PollEvery.
	time.Sleep(3 * a.PollEvery)
	b.pollFeed(ctx)
	b.pollAllSources(ctx)
	for _, k := range allSources {
		if sb.hits(k) != 1 {
			t.Fatalf("%s: b polled %d times after a died, want it to take over", k, sb.hits(k))
		}
	}
	if got := replicaJSON(t, b); !equalStrings(got.Pollers, want) {
		t.Fatalf("b reports %v after takeover, want %v", got.Pollers, want)
	}

	// A clean stop hands every claim over without waiting for the lapse.
	b.stepDownFeed(ctx)
	a.pollAllSources(ctx)
	for _, k := range allSources {
		if sa.hits(k) != 4 {
			t.Fatalf("%s: a polled %d times after b stepped down, want 4", k, sa.hits(k))
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The same over Run and real sockets: of two serving replicas exactly one reports the sources
// as its pollers, and when it is stopped the other reports them.
func TestRunElectsSourcePollersOverHTTP(t *testing.T) {
	base := store.NewMemory()
	type srv struct {
		url  string
		stop context.CancelFunc
		done chan struct{}
	}
	start := func(id string) srv {
		a, _ := sourcesApp(base, id)
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
	pollers := func(s srv) []string {
		resp, err := http.Get(s.url + "/replica")
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		var out struct {
			Pollers []string `json:"pollers"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.Pollers
	}
	// Each source is elected on its own, so two replicas starting together may split them;
	// what holds is that every source has exactly one poller.
	a, b := start("a"), start("b")
	deadline := time.Now().Add(5 * time.Second)
	for {
		pa, pb := pollers(a), pollers(b)
		if len(pa)+len(pb) == 6 && len(unionOf(pa, pb)) == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sources not each polled once: a=%v b=%v", pa, pb)
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.stop()
	<-a.done
	deadline = time.Now().Add(5 * time.Second)
	for len(pollers(b)) != 6 {
		if time.Now().After(deadline) {
			t.Fatalf("b polls %v after a stopped, want all six", pollers(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func unionOf(a, b []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range append(append([]string{}, a...), b...) {
		m[x] = true
	}
	return m
}
