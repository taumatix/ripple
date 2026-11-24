package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Entry 172: security-bypass-severity restricts security-bypass to advisories whose
// GitHub rating (database_specific.severity) is listed.

func osvRated(id, pkg, severity string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"1.7.5","id":%q,"modified":"2026-11-20T00:00:00Z","summary":"A flaw in %s.",`+
		`"database_specific":{"severity":%q},`+
		`"affected":[{"package":{"name":%q,"ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.1.0"}]}]}]}`, id, pkg, severity, pkg))
}

// severityEnv is correctedEnv with the first module's advisory rated sev1 and the second's
// sev2 ("" leaves it unrated).
func severityEnv(t *testing.T, cfg, sev1, sev2 string) *env {
	t.Helper()
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/one", "v1.0.0", "v1.1.0")
	stubModule(e, "example.com/two", "v1.0.0", "v1.1.0")
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       gomod("example.com/app", "example.com/one v1.0.0", "example.com/two v1.0.0"),
		"main.go":      blankImports("example.com/one", "example.com/two"),
		".ripple.yaml": cfg,
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	for _, r := range []struct{ id, pkg, sev string }{{"GO-2026-7400", "example.com/one", sev1}, {"GO-2026-7401", "example.com/two", sev2}} {
		rec := osvRange(r.id, r.pkg, "0", "1.1.0")
		if r.sev != "" {
			rec = osvRated(r.id, r.pkg, r.sev)
		}
		osv.Publish(r.id, e.clock.Now(), rec)
	}
	e.tick()
	return e
}

const severityLimit = "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit]\n    security-bypass-severity: [high, critical]\n"

func TestSeverityBypassOpensHighAdvisoriesPastTheLimit(t *testing.T) {
	e := severityEnv(t, severityLimit, "HIGH", "CRITICAL")
	if n := len(e.gh.PRs("acme/app")); n != 2 {
		t.Fatalf("both serious advisories must open past the limit of one: %d", n)
	}
}

func TestSeverityBypassHoldsAModerateOrUnratedAdvisoryAtTheLimit(t *testing.T) {
	for _, sev := range []string{"MODERATE", ""} {
		e := severityEnv(t, severityLimit, sev, sev)
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("severity %q must not bypass the limit: %d PRs", sev, n)
		}
	}
}

const severitySchedule = "updates:\n" + closedWindow + "    security-bypass: [schedule]\n    security-bypass-severity: [critical]\n"

func TestSeverityBypassOpensOnlyACriticalAdvisoryOutsideTheSchedule(t *testing.T) {
	e := severityEnv(t, severitySchedule, "CRITICAL", "HIGH")
	if n := len(e.gh.PRs("acme/app")); n != 1 {
		t.Fatalf("only the critical advisory may bypass the Saturday-only schedule: %d PRs", n)
	}
}
