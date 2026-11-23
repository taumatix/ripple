package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/webhook"
)

// gate blocks Process for the repos it holds until they are released, and records
// the order events were handled in.
type gate struct {
	mu      sync.Mutex
	held    map[string]chan struct{}
	done    []string
	running map[string]int
	overlap bool
	fail    map[string]int
}

func newGate() *gate {
	return &gate{held: map[string]chan struct{}{}, running: map[string]int{}, fail: map[string]int{}}
}

func (g *gate) hold(repo string) { g.mu.Lock(); g.held[repo] = make(chan struct{}); g.mu.Unlock() }
func (g *gate) release(repo string) {
	g.mu.Lock()
	c := g.held[repo]
	delete(g.held, repo)
	g.mu.Unlock()
	if c != nil {
		close(c)
	}
}

func (g *gate) process(ctx context.Context, ev model.Event) error {
	g.mu.Lock()
	c := g.held[ev.Repo]
	g.running[ev.Repo]++
	if g.running[ev.Repo] > 1 {
		g.overlap = true
	}
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.running[ev.Repo]--; g.mu.Unlock() }()
	if c != nil {
		select {
		case <-c:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail[ev.ID] > 0 {
		g.fail[ev.ID]--
		return errors.New("transient")
	}
	g.done = append(g.done, ev.ID)
	return nil
}

func (g *gate) handled() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.done...)
}

func (g *gate) has(id string) bool {
	for _, d := range g.handled() {
		if d == id {
			return true
		}
	}
	return false
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func ev(inst int64, id, repo string) model.Event {
	return model.Event{ID: id, InstallationID: inst, Kind: "push", Repo: repo}
}

// startQueue runs q until the test ends and returns a stop function that cancels
// it and waits for Run to return.
func startQueue(t *testing.T, q *Queue) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("queue.Run: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	eventually(t, "queue loaded", q.loaded)
	return stop
}

func stored(t *testing.T, st store.Store) int {
	t.Helper()
	evs, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

func TestNoisyInstallationDoesNotStarveAnother(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 4, PerInstallation: 2}
	startQueue(t, q)
	var flood []model.Event
	for i := 0; i < 50; i++ {
		repo := fmt.Sprintf("noisy/r%02d", i)
		g.hold(repo)
		flood = append(flood, ev(1, fmt.Sprintf("n%02d", i), repo))
	}
	if err := q.Add(context.Background(), flood...); err != nil {
		t.Fatal(err)
	}
	if err := q.Add(context.Background(), ev(2, "quiet", "quiet/app")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the quiet installation's event handled while the noisy one is stuck", func() bool { return g.has("quiet") })
	for i := 0; i < 50; i++ {
		g.release(fmt.Sprintf("noisy/r%02d", i))
	}
	eventually(t, "every noisy event handled", func() bool { return len(g.handled()) == 51 })
	eventually(t, "store drained", func() bool { return stored(t, st) == 0 })
}

func TestEventsOfOneRepoRunInOrderAndNeverTogether(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 4, PerInstallation: 4}
	startQueue(t, q)
	g.hold("acme/a")
	_ = q.Add(context.Background(), ev(1, "a1", "acme/a"), ev(1, "a2", "acme/a"), ev(1, "b1", "acme/b"), ev(1, "a3", "acme/a"))
	eventually(t, "b handled past the busy repo", func() bool { return g.has("b1") })
	if g.has("a2") || g.has("a3") {
		t.Fatalf("a later event of a busy repo ran early: %v", g.handled())
	}
	g.release("acme/a")
	eventually(t, "all handled", func() bool { return len(g.handled()) == 4 })
	var order []string
	for _, id := range g.handled() {
		if id[0] == 'a' {
			order = append(order, id)
		}
	}
	if fmt.Sprint(order) != "[a1 a2 a3]" || g.overlap {
		t.Fatalf("repo order %v overlap=%v", order, g.overlap)
	}
}

func TestBacklogIsBoundedPerInstallationAndOverall(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 1, PerInstallation: 1, Backlog: 5, InstallationBacklog: 3}
	startQueue(t, q)
	g.hold("acme/x")
	ctx := context.Background()
	if err := q.Add(ctx, ev(1, "1", "acme/x"), ev(1, "2", "acme/x"), ev(1, "3", "acme/x")); err != nil {
		t.Fatal(err)
	}
	if err := q.Add(ctx, ev(1, "4", "acme/x")); !errors.Is(err, ErrBacklogFull) {
		t.Fatalf("installation over its backlog: %v", err)
	}
	if err := q.Add(ctx, ev(2, "5", "other/x"), ev(3, "6", "third/x")); err != nil {
		t.Fatalf("other installations still admitted: %v", err)
	}
	if err := q.Add(ctx, ev(4, "7", "fourth/x")); !errors.Is(err, ErrBacklogFull) {
		t.Fatalf("overall backlog: %v", err)
	}
	if n := stored(t, st); n > 5 {
		t.Fatalf("refused events were persisted: %d", n)
	}
	g.release("acme/x")
	eventually(t, "admitted events handled", func() bool { return len(g.handled()) == 5 })
	if err := q.Add(ctx, ev(1, "8", "acme/x")); err != nil {
		t.Fatalf("room again after draining: %v", err)
	}
}

func TestFailedEventIsRetriedThenDeadLettered(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["flaky"], g.fail["broken"] = 1, 100
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 3, Backoff: func(int) time.Duration { return time.Millisecond }}
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "flaky", "acme/a"), ev(1, "broken", "acme/b"))
	eventually(t, "flaky handled on retry", func() bool { return g.has("flaky") })
	eventually(t, "broken dead-lettered after MaxAttempts", func() bool { return len(dead(t, st)) == 1 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail["broken"] != 97 {
		t.Fatalf("broken attempted %d times, want 3", 100-g.fail["broken"])
	}
	d := dead(t, st)[0]
	if d.ID != "broken" || d.Attempts != 3 || d.LastError != "transient" || d.DeadAt.IsZero() || d.LastAttemptAt.IsZero() {
		t.Fatalf("dead event keeps its attempts, last error and times: %+v", d)
	}
	if n := stored(t, st); n != 0 {
		t.Fatalf("%d live events left, want none", n)
	}
	if q.Len() != 0 {
		t.Fatalf("queue still holds %d", q.Len())
	}
}

func dead(t *testing.T, st store.Store) []model.Event {
	t.Helper()
	d, err := st.ListDeadEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A dead event no longer counts against the backlog caps: it is not work, and a burst of
// poison events must not turn genuine deliveries away with 503.
func TestDeadEventsDoNotCountAgainstTheBacklog(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["bad"] = 100
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 1, Backlog: 1, InstallationBacklog: 1, Backoff: func(int) time.Duration { return time.Millisecond }}
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "bad", "acme/a"))
	eventually(t, "dead", func() bool { return len(dead(t, st)) == 1 })
	if err := q.Add(context.Background(), ev(1, "good", "acme/b")); err != nil {
		t.Fatalf("a dead event filled the backlog of 1: %v", err)
	}
	eventually(t, "good handled", func() bool { return g.has("good") })
}

// An operator requeues a dead event through the store, possibly from another process;
// the running queue notices on its next rescan and handles it once.
func TestRequeuedDeadEventIsHandledOnce(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["bad"] = 3
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 3, Rescan: 10 * time.Millisecond, Backoff: func(int) time.Duration { return time.Millisecond }}
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "bad", "acme/a"))
	eventually(t, "dead", func() bool { return len(dead(t, st)) == 1 })
	time.Sleep(50 * time.Millisecond) // several rescans: a dead event must stay dead
	if g.has("bad") || len(dead(t, st)) != 1 {
		t.Fatal("a dead event was handled without a requeue")
	}
	if ok, err := st.RequeueDeadEvent(context.Background(), "bad"); err != nil || !ok {
		t.Fatalf("requeue: %v %v", ok, err)
	}
	eventually(t, "handled after requeue", func() bool { return g.has("bad") })
	eventually(t, "store drained", func() bool { return stored(t, st) == 0 && len(dead(t, st)) == 0 })
	time.Sleep(50 * time.Millisecond)
	if n := len(g.handled()); n != 1 {
		t.Fatalf("handled %d times, want once", n)
	}
	if q.Len() != 0 {
		t.Fatalf("queue holds %d", q.Len())
	}
}

// Rescanning must not hand an event to a worker that already has it.
func TestRescanDoesNotDuplicateQueuedEvents(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 1, PerInstallation: 1, Rescan: 5 * time.Millisecond}
	startQueue(t, q)
	g.hold("acme/a")
	_ = q.Add(context.Background(), ev(1, "1", "acme/a"), ev(1, "2", "acme/b"))
	time.Sleep(60 * time.Millisecond)
	g.release("acme/a")
	eventually(t, "both handled", func() bool { return len(g.handled()) == 2 })
	time.Sleep(30 * time.Millisecond)
	if fmt.Sprint(g.handled()) != "[1 2]" {
		t.Fatalf("handled %v", g.handled())
	}
}

func TestDeadEventsArePurgedAfterRetention(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["bad"], g.fail["bad2"] = 100, 100
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 1, Rescan: 5 * time.Millisecond, DeadRetention: 48 * time.Hour, Now: clock}
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "bad", "acme/a"))
	eventually(t, "dead", func() bool { return len(dead(t, st)) == 1 })
	mu.Lock()
	now = now.Add(24 * time.Hour)
	mu.Unlock()
	_ = q.Add(context.Background(), ev(1, "bad2", "acme/b"))
	eventually(t, "second dead", func() bool { return len(dead(t, st)) == 2 })
	mu.Lock()
	now = now.Add(25 * time.Hour) // the first is now 49h old, the second 25h
	mu.Unlock()
	eventually(t, "only the old one purged", func() bool { d := dead(t, st); return len(d) == 1 && d[0].ID == "bad2" })
}

func TestRestartResumesQueuedEvents(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 1, PerInstallation: 1}
	stop := startQueue(t, q)
	g.hold("acme/a")
	_ = q.Add(context.Background(), ev(1, "1", "acme/a"), ev(1, "2", "acme/b"), ev(1, "3", "acme/c"))
	stop() // shuts down with "1" in flight and two waiting
	if n := stored(t, st); n != 3 {
		t.Fatalf("after shutdown %d events stored, want 3", n)
	}
	g.release("acme/a")
	g2 := newGate()
	startQueue(t, &Queue{Store: st, Process: g2.process, Workers: 1, PerInstallation: 1})
	eventually(t, "resumed events handled", func() bool { return len(g2.handled()) == 3 })
	if fmt.Sprint(g2.handled()) != "[1 2 3]" {
		t.Fatalf("resumed out of order: %v", g2.handled())
	}
	eventually(t, "store drained", func() bool { return stored(t, st) == 0 })
}

func TestHandleSplitsADeliveryIntoOneEventPerRepo(t *testing.T) {
	st := store.NewMemory()
	a := &App{Engine: nil, Queue: &Queue{Store: st, Process: newGate().process}}
	long := strings.Repeat("x", 5000)
	if err := a.Handle(context.Background(), webhook.Event{Kind: "repos_added", InstallationID: 7, Delivery: "abc", Repos: []string{"acme/a", "acme/b"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Handle(context.Background(), webhook.Event{Kind: "push", InstallationID: 7, Delivery: long, Repos: []string{"acme/a"}, Ref: "refs/heads/main", Files: []string{"go.mod"}}); err != nil {
		t.Fatal(err)
	}
	// The same delivery again, as a manual redelivery while it is still queued.
	_ = a.Handle(context.Background(), webhook.Event{Kind: "repos_added", InstallationID: 7, Delivery: "abc", Repos: []string{"acme/a", "acme/b"}})
	evs, _ := st.ListEvents(context.Background())
	if len(evs) != 3 || evs[0].ID != "abc/0" || evs[0].Repo != "acme/a" || evs[1].ID != "abc/1" || evs[1].Repo != "acme/b" {
		t.Fatalf("events: %+v", evs)
	}
	if p := evs[2]; len(p.ID) > 80 || p.Kind != "push" || p.Ref != "refs/heads/main" || fmt.Sprint(p.Files) != "[go.mod]" {
		t.Fatalf("push event: %+v", p)
	}
}

// Two replicas load the same stored events and race for them: each event is handled
// exactly once, by whichever replica claims it first.
func TestTwoReplicasHandleEachEventOnce(t *testing.T) {
	st := store.NewMemory()
	var evs []model.Event
	for i := 0; i < 20; i++ {
		evs = append(evs, ev(1, fmt.Sprint("e", i), fmt.Sprint("acme/r", i)))
	}
	if err := (&Queue{Store: st}).Add(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	process := func(ctx context.Context, e model.Event) error {
		mu.Lock()
		seen[e.ID]++
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		return nil
	}
	for _, owner := range []string{"a", "b"} {
		startQueue(t, &Queue{Store: st, Process: process, Workers: 3, PerInstallation: 3, Owner: owner, LeaseTTL: 500 * time.Millisecond})
	}
	eventually(t, "store drained", func() bool { return stored(t, st) == 0 })
	mu.Lock()
	defer mu.Unlock()
	for _, e := range evs {
		if seen[e.ID] != 1 {
			t.Errorf("event %s handled %d times, want 1", e.ID, seen[e.ID])
		}
	}
}

// An event whose lease another replica holds is left alone while the lease lives, and
// taken once its holder has gone: a replica that died mid-event does not strand it.
func TestEventHeldByAnotherReplicaIsTakenAfterItsLeaseLapses(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	if err := (&Queue{Store: st}).Add(context.Background(), ev(1, "1", "acme/a")); err != nil {
		t.Fatal(err)
	}
	// The dead replica's claim: acquired and never renewed or released.
	if _, ok, err := st.AcquireLease(context.Background(), eventLease("1"), "dead", 400*time.Millisecond); err != nil || !ok {
		t.Fatalf("seed lease: ok=%v err=%v", ok, err)
	}
	start := time.Now()
	startQueue(t, &Queue{Store: st, Process: g.process, Workers: 1, PerInstallation: 1, Owner: "live", LeaseTTL: 300 * time.Millisecond})
	eventually(t, "event taken over", func() bool { return g.has("1") })
	if d := time.Since(start); d < 350*time.Millisecond {
		t.Errorf("handled after %v, while the dead replica's lease was live", d)
	}
}

// An event another replica finished since this one loaded it is dropped, not run again.
func TestEventHandledElsewhereIsNotRunAgain(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 1, PerInstallation: 1, Owner: "a"}
	g.hold("acme/blocker")
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "1", "acme/blocker"), ev(1, "2", "acme/a"))
	// Replica b handled "2" meanwhile: it is gone from the store.
	if err := st.DeleteEvent(context.Background(), "2"); err != nil {
		t.Fatal(err)
	}
	g.release("acme/blocker")
	eventually(t, "blocker handled", func() bool { return g.has("1") })
	time.Sleep(50 * time.Millisecond)
	if g.has("2") {
		t.Fatal("event deleted by another replica was run again")
	}
}

// Another replica's older event for a repo is not visible here until the rescan. A newer event
// of that repo claimed here meanwhile must wait for it: the store is asked, at claim time,
// whether an older live event of the repo exists.
func TestNewerEventWaitsForAnOlderOneOnAnotherReplica(t *testing.T) {
	ctx := context.Background()
	st, g := store.NewMemory(), newGate()
	q := &Queue{Store: st, Process: g.process, Workers: 2, PerInstallation: 2, Owner: "a", LeaseTTL: 300 * time.Millisecond, Rescan: time.Hour}
	startQueue(t, q)
	// Replica b received "old" first and is handling it; this replica has not loaded it.
	older := ev(1, "old", "acme/a")
	older.Seq = 1
	if _, err := st.EnqueueEvent(ctx, older); err != nil {
		t.Fatal(err)
	}
	bHold, err := store.Hold(ctx, st, nil, eventLease("old"), "b", 300*time.Millisecond)
	if err != nil || bHold == nil {
		t.Fatalf("b's claim: %v %v", bHold, err)
	}
	if err := q.Add(ctx, ev(1, "new", "acme/a")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if g.has("new") {
		t.Fatal("a newer event ran while an older event of the repo was still queued on another replica")
	}
	// b finishes: the event is deleted and its lease released.
	if err := st.DeleteEvent(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	bHold.Release()
	eventually(t, "newer event runs once the older is gone", func() bool { return g.has("new") })
}
