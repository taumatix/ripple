package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

const (
	backfillApp   = "acme/app"
	backfillWatch = "acme/watcher"
)

// backfillEnv registers acme/app (importing only example.com/lib) and acme/watcher,
// which imports every module in mods, so the feed records their v1.1.0 releases
// before acme/app has ever heard of them. The watcher's own PRs are not under test.
func backfillEnv(t *testing.T, st store.Store, appCfg string, mods ...string) *env {
	t.Helper()
	e := newEnv(t, st, defaultLimits())
	stubModule(e, "example.com/lib", "v1.0.0")
	reqs := []string{}
	for _, m := range mods {
		stubModule(e, m, "v1.0.0", "v1.1.0")
		reqs = append(reqs, m+" v1.0.0")
	}
	appFiles := map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")}
	if appCfg != "" {
		appFiles[".ripple.yaml"] = appCfg
	}
	e.gh.AddRepo(backfillApp, appFiles)
	e.gh.AddRepo(backfillWatch, map[string]string{"go.mod": gomod("example.com/watcher", reqs...), "main.go": blankImports(mods...)})
	start(t, e, backfillApp, backfillWatch)
	e.clock.Advance(time.Minute)
	for _, m := range mods {
		e.publish(m, "v1.1.0")
	}
	e.tick()
	// Move the feed cursor past those releases: the index is polled inclusively, and
	// a replay of them would fan out to acme/app and prove nothing about backfill.
	e.clock.Advance(time.Minute)
	e.publish("example.com/nobody-imports-this", "v1.0.0")
	e.tick()
	return e
}

// importAll commits a go.mod and main.go for acme/app that also import mods at v1.0.0.
func (e *env) importAll(mods ...string) {
	e.t.Helper()
	scans := e.logLines("scanned", backfillApp)
	e.push(backfillApp, map[string]string{"go.mod": e.appGoMod(mods), "main.go": blankImports(append([]string{"example.com/lib"}, mods...)...)})
	e.eventually("push scanned", func() bool { return e.logLines("scanned", backfillApp) > scans })
}

func (e *env) appGoMod(mods []string) string {
	reqs := []string{"example.com/lib v1.0.0"}
	for _, m := range mods {
		reqs = append(reqs, m+" v1.0.0")
	}
	return gomod("example.com/app", reqs...)
}

// rescan pushes a commit that changes no dependency and waits for acme/app to be scanned again.
func (e *env) rescan(mods ...string) {
	e.t.Helper()
	scans := e.logLines("scanned", backfillApp)
	e.push(backfillApp, map[string]string{"go.mod": e.appGoMod(mods) + fmt.Sprintf("// rescan %d\n", scans)})
	e.eventually("rescan done", func() bool { return e.logLines("scanned", backfillApp) > scans })
}

func TestNewDependencyIsBroughtToTheReleaseAlreadyKnown(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "", "example.com/fresh")
		e.importAll("example.com/fresh")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/fresh") || !strings.HasSuffix(prs[0].Title, " v1.1.0") {
			t.Fatalf("want one PR moving example.com/fresh to v1.1.0, got %+v", prs)
		}

		// A rescan with nothing new, and a second tick, must not repeat it.
		e.rescan("example.com/fresh")
		e.tick()
		if got := len(e.gh.PRs(backfillApp)); got != 1 {
			t.Fatalf("rescan produced %d PRs, want still 1", got)
		}
	})
}

func TestBackfillIsBoundedPerScan(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		var mods []string
		for i := 0; i < 5; i++ {
			mods = append(mods, fmt.Sprintf("example.com/m%d", i))
		}
		e := backfillEnv(t, st, "", mods...)
		e.app.Engine.MaxBackfill = 2
		e.importAll(mods...)
		e.tick()
		if got := len(e.gh.PRs(backfillApp)); got != 2 {
			t.Fatalf("5 new dependencies behind with MaxBackfill 2: want 2 PRs, got %d", got)
		}
		e.rescan(mods...)
		e.tick()
		if got := len(e.gh.PRs(backfillApp)); got != 2 {
			t.Fatalf("a rescan backfilled the rest: %d PRs", got)
		}
	})
}

func TestBackfillCanBeTurnedOffAndRespectsIgnore(t *testing.T) {
	cases := map[string]string{
		"off":    "backfill-new-dependencies: false\n",
		"ignore": "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: example.com/fresh\n",
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := backfillEnv(t, st, cfg, "example.com/fresh")
				e.importAll("example.com/fresh")
				e.tick()
				if got := e.gh.PRs(backfillApp); len(got) != 0 {
					t.Fatalf("want no PR, got %+v", got)
				}
			})
		})
	}
}

func TestBackfillWaitsOutTheCooldown(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "updates:\n  - ecosystem: gomod\n    cooldown: 3d\n", "example.com/fresh")
		e.importAll("example.com/fresh")
		e.tick()
		if got := e.gh.PRs(backfillApp); len(got) != 0 {
			t.Fatalf("PR before the cooldown: %+v", got)
		}
		e.clock.Advance(4 * 24 * time.Hour)
		e.tick()
		if got := e.gh.PRs(backfillApp); len(got) != 1 {
			t.Fatalf("want 1 PR after the cooldown, got %+v", got)
		}
	})
}
