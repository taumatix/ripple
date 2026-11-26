package e2e

import (
	"log/slog"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// pollGitHub serves the default GitHub through a platform.Host, which is what runs the
// per-host discovery (and so the head polling); the app has no Hosts otherwise.
func pollGitHub(a *app.App) {
	c := a.Engine.Platform.(*testutil.CountingPlatform).Inner.(*github.Client)
	a.Hosts = []*platform.Host{github.NewHost(c, []byte(secret), slog.New(slog.DiscardHandler))}
	a.DefaultHost = github.HostName
}

// ROADMAP entry 247 for GitHub: a push whose webhook never arrived is found by the next
// discovery, which reads every installed repository's default-branch head (one GraphQL
// request for up to 100 repositories) and re-scans only the ones that moved.
func TestHeadPollingFindsAPushWhoseWebhookNeverArrived(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{tweak: pollGitHub})
		stubModule(e, "example.com/lib", "v1.0.0", "v1.0.1")
		importer := func(mod string) map[string]string {
			return map[string]string{"go.mod": gomod(mod, "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")}
		}
		e.gh.AddRepo("acme/old", importer("example.com/old"))
		e.gh.AddRepo("acme/late", map[string]string{"go.mod": gomod("example.com/late"), "main.go": "package main\n\nfunc main() {}\n"})
		e.gh.AddRepo("acme/idle", map[string]string{"go.mod": gomod("example.com/idle"), "main.go": "package main\n\nfunc main() {}\n"})
		e.gh.Install(7, "acme/old", "acme/late", "acme/idle")
		start(t, e, "acme/old", "acme/late", "acme/idle")
		e.waitQueueIdle()
		e.tick() // the first discovery remembers every head; nothing moved yet
		e.waitQueueIdle()

		// acme/late starts importing the library, but its push webhook is lost.
		e.gh.Commit("acme/late", importer("example.com/late"))
		before := map[string]int{"acme/old": e.gh.Calls("acme/old"), "acme/idle": e.gh.Calls("acme/idle")}
		e.clock.Advance(11 * time.Minute) // past the client's head read interval
		e.tick()
		e.waitQueueIdle()
		e.eventually("acme/late's lost push to be found", func() bool {
			e.tick()
			e.waitQueueIdle()
			ds := e.deps("acme/late")
			for _, d := range ds {
				if d.Package == "example.com/lib" {
					return true
				}
			}
			return false
		})
		for n, c := range before {
			if got := e.gh.Calls(n); got != c {
				t.Errorf("%s: %d API calls during the poll, want none (its head did not move)", n, got-c)
			}
		}

		// The restart forgets nothing: a head that moved while the server was down is caught.
		e.stopApp()
		e.gh.Commit("acme/idle", importer("example.com/idle"))
		e.clock.Advance(11 * time.Minute)
		e.startApp()
		e.eventually("acme/idle's push during the restart to be found", func() bool {
			e.tick()
			e.waitQueueIdle()
			for _, d := range e.deps("acme/idle") {
				if d.Package == "example.com/lib" {
					return true
				}
			}
			return false
		})

		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		for _, r := range []string{"acme/old", "acme/late", "acme/idle"} {
			if n := len(e.gh.PRs(r)); n != 1 {
				t.Errorf("%s: %d PRs, want exactly 1", r, n)
			}
		}
	})
}

// A 429 ends the round; the next discovery after the limit lifts reads again.
func TestHeadPollingRetriesAfterARateLimit(t *testing.T) {
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{tweak: pollGitHub})
	stubModule(e, "example.com/lib", "v1.0.0")
	plain := map[string]string{"go.mod": gomod("example.com/late"), "main.go": "package main\n\nfunc main() {}\n"}
	e.gh.AddRepo("acme/late", plain)
	e.gh.Install(7, "acme/late")
	start(t, e, "acme/late")
	e.waitQueueIdle()
	e.tick()
	e.waitQueueIdle()

	e.gh.Commit("acme/late", map[string]string{"go.mod": gomod("example.com/late", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
	e.gh.Refuse("/graphql", 1, 429, "30")
	e.clock.Advance(11 * time.Minute)
	e.tick()
	e.waitQueueIdle()
	if e.gh.Refused() != 1 {
		t.Fatalf("the 429 was not met: refused %d", e.gh.Refused())
	}
	if len(e.deps("acme/late")) != 0 {
		t.Fatalf("a rate-limited round re-scanned the repository")
	}
	e.clock.Advance(31 * time.Second)
	e.eventually("the retry after the limit lifted", func() bool {
		e.tick()
		e.waitQueueIdle()
		return len(e.deps("acme/late")) == 1
	})
}
