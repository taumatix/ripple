package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/store"
)

// waitRepo is what every test calls before it acts on a repo's dependencies, so it must not
// return while any scan is still running: a repo is registered before its dependency
// backfill finishes (ROADMAP 138, 139). waitRegistered is the low-level wait that tests
// holding a checkout open need.
func TestWaitRepoReturnsOnlyOnceTheQueueIsIdle(t *testing.T) {
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{queue: func(q *app.Queue) { q.Workers, q.PerInstallation = 2, 1 }})
	held := e.addHeldRepos("busy", 1)
	e.gh.AddRepo("quiet/app", map[string]string{"go.mod": gomod("example.com/quiet", "example.com/lib v1.0.0")})
	busy := installBody(1, held...)
	if c := e.post("installation", "busy-1", sign(busy), busy); c != http.StatusAccepted {
		t.Fatalf("busy delivery: %d", c)
	}
	quiet := installBody(2, "quiet/app")
	if c := e.post("installation", "quiet-1", sign(quiet), quiet); c != http.StatusAccepted {
		t.Fatalf("quiet delivery: %d", c)
	}
	e.waitRegistered("quiet/app") // registered although the busy scan is still held

	done := make(chan struct{})
	go func() { e.waitRepo("quiet/app"); close(done) }()
	select {
	case <-done:
		t.Fatal("waitRepo returned while a scan was still running")
	case <-time.After(300 * time.Millisecond):
	}
	e.plat.Release(held[0])
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("waitRepo did not return once the queue drained")
	}
}
