package e2e

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Found by the live run against real OSV: golang.org/x/text v0.3.6 is covered by advisories fixed in
// four different releases, and ripple opened four security PRs for the one go.mod line, none
// closed, because the supersede step only looked at the PRs open before the run started
// and not at the ones the run opened a moment earlier. The advisory's own fix is the lowest
// release; the highest one a run opens must replace the lower ones it just opened.
func TestTwoAdvisoriesFixedInDifferentReleasesLeaveOnePR(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		osv := testutil.NewOSV(t)
		e := newEnvOpts(t, st, defaultLimits(), withOSV(osv, 0))
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
		e.gh.AddRepo("acme/app", map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7700", e.clock.Now(), osvRange("GO-2026-7700", "example.com/lib", "0", "1.1.0"))
		osv.Publish("GO-2026-7701", e.clock.Now(), osvRange("GO-2026-7701", "example.com/lib", "0", "1.2.0"))
		start(t, e, "acme/app") // the history check finds both
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want the two PRs, one of them closed: %+v", prs)
		}
		if lower := prByTitle(t, prs, "v1.1.0"); !lower.Closed || len(lower.Comments) != 1 {
			t.Errorf("the PR for v1.1.0 must be closed with one comment once v1.2.0 opens: %+v", lower)
		}
		if prByTitle(t, prs, "v1.2.0").Closed {
			t.Error("the PR for the highest fix was closed")
		}
	})
}
