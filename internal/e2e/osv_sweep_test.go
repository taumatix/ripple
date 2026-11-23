package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// withOSVSweep is withOSV with a modified-list cap small enough to overflow and a sweep
// that checks `batch` repos per poll.
func withOSVSweep(osv *testutil.OSV, maxLines, batch int) opts {
	return opts{tweak: func(a *app.App) {
		a.Engine.Advisories = true
		c := &advisory.Client{DumpURL: osv.DumpURL(), APIURL: osv.APIURL(), MaxLines: maxLines}
		a.Engine.AdvisoryHistory = c
		a.Advisories = &engine.AdvisoryWatcher{Engine: a.Engine, Source: c, SweepBatch: batch}
	}}
}

// Entry 80: more records change during a gap than one poll reads, so the poller skips
// the oldest. A repo whose go.mod did not change is not re-checked by the history check,
// so the sweep re-asks OSV about every repo, a few per poll, and finds what was skipped.
func TestASweepFindsAnAdvisoryTheGapSkipped(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		if _, ok := st.(store.RepoLister); !ok {
			t.Skip("this store cannot list repos, so it cannot sweep (ROADMAP)")
		}
		osv := testutil.NewOSV(t)
		e := newEnvOpts(t, st, defaultLimits(), withOSVSweep(osv, 3, 1))
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
		stubModule(e, "example.com/other", "v1.0.0")
		e.gh.AddRepo("acme/a-clean", map[string]string{"go.mod": gomod("example.com/clean", "example.com/other v1.0.0"), "main.go": blankImports("example.com/other")})
		e.gh.AddRepo("acme/b-affected", map[string]string{"go.mod": gomod("example.com/affected", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		e.gh.AddRepo("acme/c-clean", map[string]string{"go.mod": gomod("example.com/clean2", "example.com/other v1.0.0"), "main.go": blankImports("example.com/other")})
		start(t, e, "acme/a-clean", "acme/b-affected", "acme/c-clean")

		// The real advisory is the oldest of six changes; the poll reads the newest three.
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7500", e.clock.Now(), osvRange("GO-2026-7500", "example.com/lib", "0", "1.1.0"))
		for i := 0; i < 5; i++ {
			e.clock.Advance(time.Minute)
			id := fmt.Sprintf("GO-2026-76%02d", i)
			osv.Publish(id, e.clock.Now(), osvRange(id, "example.com/filler", "0", "1.0.0"))
		}
		e.tick() // the poll truncates and the sweep checks acme/a-clean
		if n := len(e.gh.PRs("acme/b-affected")); n != 0 {
			t.Fatalf("PR before the sweep reached the repo: %d", n)
		}
		e.tick() // acme/b-affected
		if n := len(e.gh.PRs("acme/b-affected")); n != 1 {
			t.Fatalf("the sweep did not find the skipped advisory: %d PRs", n)
		}
		e.tick() // acme/c-clean: the sweep ends
		before, _ := osv.BatchRequests()
		e.tick()
		e.tick()
		if after, _ := osv.BatchRequests(); after != before {
			t.Errorf("the sweep kept asking OSV after it ended: %d -> %d querybatch requests", before, after)
		}
		if raw, err := e.st.GetCursor(context.Background(), "osv-sweep"); err != nil || raw != "" {
			t.Errorf("the sweep marker is still set: %q %v", raw, err)
		}
	})
}
