package e2e

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// laterRunEnv has one repo on a module with three releases and one advisory fixed in
// the first, whose security PR the first tick opens.
func laterRunEnv(t *testing.T, st store.Store) (*env, *testutil.OSV) {
	t.Helper()
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, st, defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/one", "v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0")
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":  gomod("example.com/app", "example.com/one v1.0.0"),
		"main.go": blankImports("example.com/one"),
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7501", e.clock.Now(), osvRange("GO-2026-7501", "example.com/one", "0", "1.1.0"))
	e.tick()
	return e, osv
}

func openTitles(e *env) (open []string) {
	for _, p := range e.gh.PRs("acme/app") {
		if !p.Closed {
			open = append(open, p.Title)
		}
	}
	return open
}

// Entry 179: a security PR opened by a later run replaces the lower one an earlier run
// opened.
func TestALaterRunsSecurityPRClosesTheLowerOneOfAnEarlierRun(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, osv := laterRunEnv(t, st)
		if got := openTitles(e); len(got) != 1 {
			t.Fatalf("want the first advisory's PR: %v", got)
		}
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7502", e.clock.Now(), osvRange("GO-2026-7502", "example.com/one", "0", "1.2.0"))
		e.tick()
		e.tick()
		if got := openTitles(e); len(got) != 1 {
			t.Fatalf("want only the v1.2.0 PR open: %v", got)
		}
	})
}

// Entry 179: when the host refused that close, a rerun and a tick closes the lower PR.
func TestARefusedCloseOfALowerSecurityPRIsRetriedAfterARerun(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, osv := laterRunEnv(t, st)
		e.gh.FailClose = true
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7502", e.clock.Now(), osvRange("GO-2026-7502", "example.com/one", "0", "1.2.0"))
		e.tick()
		e.tick()
		if got := openTitles(e); len(got) != 2 {
			t.Fatalf("the close was meant to be refused: %v", got)
		}
		e.gh.FailClose = false
		e.rerun("acme/app", "example.com/one", "v1.2.0")
		e.tick()
		e.tick()
		if got := openTitles(e); len(got) != 1 {
			t.Fatalf("a rerun must retry the close: %v", got)
		}
		if j := e.job("acme/app", "example.com/one", "v1.2.0"); j.State != model.JobDone {
			t.Fatalf("%+v", j)
		}
	})
}

// Entry 179: rerunning the LOWER job (its recorded PR cleared) while a higher PR is open
// must not strand the lower PR.
func TestARerunOfTheLowerSecurityJobDoesNotStrandItsPR(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, osv := laterRunEnv(t, st)
		e.gh.FailClose = true
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7502", e.clock.Now(), osvRange("GO-2026-7502", "example.com/one", "0", "1.2.0"))
		e.tick()
		e.tick()
		e.gh.FailClose = false
		e.rerun("acme/app", "example.com/one", "v1.1.0")
		e.tick()
		e.tick()
		if got := openTitles(e); len(got) != 1 {
			t.Fatalf("want only the v1.2.0 PR open: %v; lower job %+v", got, e.job("acme/app", "example.com/one", "v1.1.0"))
		}
	})
}
