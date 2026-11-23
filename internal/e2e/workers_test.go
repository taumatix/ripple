package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/store"
)

func (e *env) registered(name string) bool {
	_, ok, err := e.st.GetRepo(context.Background(), name)
	return err == nil && ok
}

func (e *env) queued() int {
	evs, err := e.st.ListEvents(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return len(evs)
}

func (e *env) addHeldRepos(prefix string, n int) []string {
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s/r%02d", prefix, i)
		e.gh.AddRepo(name, map[string]string{"go.mod": gomod("example.com/"+name, "example.com/lib v1.0.0")})
		e.plat.Hold(name)
		names = append(names, name)
	}
	return names
}

// A flood from one installation, every scan of it stuck, does not delay another
// installation's event; when the flood clears, every one of its events is handled
// exactly once.
func TestNoisyInstallationDoesNotDelayAnother(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{queue: func(q *app.Queue) { q.Workers, q.PerInstallation = 4, 2 }})
		noisy := e.addHeldRepos("noisy", 20)
		e.gh.AddRepo("quiet/app", map[string]string{"go.mod": gomod("example.com/quiet", "example.com/lib v1.0.0")})
		for i := 0; i < 4; i++ {
			body := installBody(1, noisy[i*5:(i+1)*5]...)
			if c := e.post("installation", fmt.Sprintf("noisy-%d", i), sign(body), body); c != http.StatusAccepted {
				t.Fatalf("noisy delivery %d: %d", i, c)
			}
		}
		body := installBody(2, "quiet/app")
		if c := e.post("installation", "quiet-1", sign(body), body); c != http.StatusAccepted {
			t.Fatalf("quiet delivery: %d", c)
		}
		e.waitRegistered("quiet/app")
		for _, n := range noisy {
			if e.registered(n) {
				t.Fatalf("%s registered while its checkout is held", n)
			}
		}
		for _, n := range noisy {
			e.plat.Release(n)
		}
		for _, n := range noisy {
			e.waitRegistered(n)
			if c := e.plat.Calls(n); c != 1 {
				t.Errorf("%s scanned %d times, want 1", n, c)
			}
		}
		e.eventually("queue drained", func() bool { return e.queued() == 0 })
	})
}

// Past an installation's persisted backlog the receiver answers 503 and keeps
// nothing; that cap is the only point an event is refused. Other installations are
// still admitted, and the refused delivery is accepted once there is room.
func TestEventBacklogCapAnswers503(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{queue: func(q *app.Queue) {
			q.Workers, q.PerInstallation, q.InstallationBacklog = 2, 1, 3
		}})
		held := e.addHeldRepos("busy", 4)
		e.gh.AddRepo("quiet/app", map[string]string{"go.mod": gomod("example.com/quiet", "example.com/lib v1.0.0")})
		body := installBody(1, held[:3]...)
		if c := e.post("installation", "busy-1", sign(body), body); c != http.StatusAccepted {
			t.Fatalf("within the cap: %d", c)
		}
		over := installBody(1, held[3])
		if c := e.post("installation", "busy-2", sign(over), over); c != http.StatusServiceUnavailable {
			t.Fatalf("over the cap: %d, want 503", c)
		}
		if n := e.queued(); n != 3 {
			t.Fatalf("%d events stored, want the 3 admitted", n)
		}
		quiet := installBody(2, "quiet/app")
		if c := e.post("installation", "quiet-1", sign(quiet), quiet); c != http.StatusAccepted {
			t.Fatalf("other installation: %d", c)
		}
		e.waitRegistered("quiet/app")
		for _, n := range held {
			e.plat.Release(n)
		}
		for _, n := range held[:3] {
			e.waitRegistered(n)
		}
		if e.registered(held[3]) {
			t.Fatal("the refused event was handled")
		}
		// The operator redelivers it from the App's Recent Deliveries page.
		e.eventually("room in the backlog", func() bool { return e.queued() == 0 })
		if c := e.post("installation", "busy-2", sign(over), over); c != http.StatusAccepted {
			t.Fatalf("redelivery: %d", c)
		}
		e.waitRegistered(held[3])
	})
}

// Stopping the server with an event in flight and two waiting loses none of them:
// the next process handles all three.
func TestRestartResumesQueuedEvents(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{queue: func(q *app.Queue) { q.Workers, q.PerInstallation = 1, 1 }})
		names := e.addHeldRepos("acme", 3)
		body := installBody(1, names...)
		if c := e.post("installation", "d-1", sign(body), body); c != http.StatusAccepted {
			t.Fatal(c)
		}
		e.eventually("first scan in flight", func() bool { return e.plat.Calls(names[0]) > 0 })
		e.stopApp()
		if n := e.queued(); n != 3 {
			t.Fatalf("after shutdown %d events stored, want 3", n)
		}
		for _, n := range names {
			if e.registered(n) {
				t.Fatalf("%s registered before the restart", n)
			}
			e.plat.Release(n)
		}
		e.startApp()
		for _, n := range names {
			e.waitRegistered(n)
		}
		e.eventually("queue drained", func() bool { return e.queued() == 0 })
	})
}

// With the app's own loops running, a job stuck in its checkout (a slow major
// verify) does not hold the feed poll: a later release is still fetched and its PR
// opened while the first job is stuck.
func TestSlowJobDoesNotStallFeedPoll(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{pollEvery: 20 * time.Millisecond})
		baseRepos(e)
		e.install("acme/uses-lib", "acme/unrelated")
		feedName := e.app.Watcher.Feed.Name()
		e.eventually("feed cursor recorded", func() bool {
			c, err := e.st.GetCursor(context.Background(), feedName)
			return err == nil && c != ""
		})
		before := e.plat.Calls("acme/uses-lib")
		e.plat.Hold("acme/uses-lib")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.eventually("lib job stuck in its checkout", func() bool { return e.plat.Calls("acme/uses-lib") > before })

		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.eventually("PR for the later release", func() bool { return len(e.gh.PRs("acme/unrelated")) == 1 })
		if n := len(e.gh.PRs("acme/uses-lib")); n != 0 {
			t.Fatalf("stuck job opened %d PRs", n)
		}
		e.plat.Release("acme/uses-lib")
		e.eventually("PR for the stuck job", func() bool { return len(e.gh.PRs("acme/uses-lib")) == 1 })
	})
}
