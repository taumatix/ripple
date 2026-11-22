package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
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

func TestFailedEventIsRetriedThenDropped(t *testing.T) {
	st, g := store.NewMemory(), newGate()
	g.fail["flaky"], g.fail["broken"] = 1, 100
	q := &Queue{Store: st, Process: g.process, MaxAttempts: 3, Backoff: func(int) time.Duration { return time.Millisecond }}
	startQueue(t, q)
	_ = q.Add(context.Background(), ev(1, "flaky", "acme/a"), ev(1, "broken", "acme/b"))
	eventually(t, "flaky handled on retry", func() bool { return g.has("flaky") })
	eventually(t, "broken given up after MaxAttempts", func() bool { return stored(t, st) == 0 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail["broken"] != 97 {
		t.Fatalf("broken attempted %d times, want 3", 100-g.fail["broken"])
	}
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
