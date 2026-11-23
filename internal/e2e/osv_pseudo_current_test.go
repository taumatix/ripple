package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const pseudoCurrent = "v1.0.1-0.20260101000000-abcdefabcdef"

// Entry 83: a module required at a pseudo-version that an advisory covers is moved to
// the fixing release only where the entry opts in (security-from-pseudo-versions), is
// never automerged, and says so. Elsewhere the dashboard lists the advisory and no PR
// opens: the commit was chosen on purpose.
func TestAdvisoryMovesAPseudoVersionDependencyToItsFixOnlyWhereOptedIn(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		osv := testutil.NewOSV(t)
		e := newEnvOpts(t, st, defaultLimits(), withOSV(osv, 0))
		stubModule(e, "example.com/lib", pseudoCurrent, "v1.0.1", "v1.1.0")
		repo := func(cfg string) map[string]string {
			return map[string]string{
				"go.mod":  gomod("example.com/app", "example.com/lib "+pseudoCurrent),
				"main.go": blankImports("example.com/lib"), ".ripple.yaml": cfg,
			}
		}
		e.gh.AddRepo("acme/optin", repo("updates:\n  - ecosystem: gomod\n    automerge: major\n    security-from-pseudo-versions: true\n"))
		e.gh.AddRepo("acme/dash", repo("dependency-dashboard: true\nupdates:\n  - ecosystem: gomod\n"))
		start(t, e, "acme/optin", "acme/dash")

		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7300", e.clock.Now(), osvRecord("GO-2026-7300", "example.com/lib", `,{"fixed":"1.0.1"}`))
		e.tick()
		e.tick()

		_, body := onePR(t, e, "acme/optin", false)
		if !strings.Contains(body, "moves off the pseudo-version `"+pseudoCurrent+"`") {
			t.Errorf("body does not say the dependency leaves a pseudo-version:\n%s", body)
		}
		if p := e.gh.PRs("acme/optin")[0]; p.AutoMerge || p.Title != "[security] Update example.com/lib to v1.0.1" {
			t.Errorf("PR %q automerge=%v", p.Title, p.AutoMerge)
		}
		if n := len(e.gh.PRs("acme/dash")); n != 0 {
			t.Errorf("a repo that did not opt in got %d PRs", n)
		}
		is := e.gh.Issues("acme/dash")
		if len(is) != 1 || !strings.Contains(is[0].Body, "security-from-pseudo-versions") ||
			!strings.Contains(is[0].Body, "https://osv.dev/vulnerability/GO-2026-7300") {
			t.Errorf("dashboard: %+v", is)
		}
	})
}
