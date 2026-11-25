package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Entry 212: one advisory that affects several modules opens a PR for each past the limit
// under security-bypass, unless security-bypass-limit-per-advisory caps it.

// capEnv has one repo on three modules, all fixed by one advisory, at a limit of one.
func capEnv(t *testing.T, st store.Store, extra string) *env {
	t.Helper()
	mods := []string{"example.com/a", "example.com/b", "example.com/c"}
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, st, defaultLimits(), withOSV(osv, 0))
	var reqs []string
	affected := ""
	for i, m := range mods {
		stubModule(e, m, "v1.0.0", "v1.1.0")
		reqs = append(reqs, m+" v1.0.0")
		if i > 0 {
			affected += ","
		}
		affected += fmt.Sprintf(`{"package":{"name":%q,"ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.1.0"}]}]}`, m)
	}
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       gomod("example.com/app", reqs...),
		"main.go":      blankImports(mods...),
		".ripple.yaml": "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit]\n" + extra,
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7600", e.clock.Now(), []byte(`{"schema_version":"1.7.5","id":"GO-2026-7600","modified":"2026-11-20T00:00:00Z","summary":"A flaw in three modules.","affected":[`+affected+`]}`))
	e.tick()
	e.tick()
	return e
}

func TestWithoutACapOneAdvisoryOpensAPRPerModulePastTheLimit(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := capEnv(t, st, "")
		if n := len(e.gh.PRs("acme/app")); n != 3 {
			t.Fatalf("want a PR per affected module: %d", n)
		}
	})
}

func TestTheCapStopsOneAdvisoryBypassingTheLimitMoreThanNTimes(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := capEnv(t, st, "    security-bypass-limit-per-advisory: 2\n")
		if n := len(e.gh.PRs("acme/app")); n != 2 {
			t.Fatalf("want two PRs for the advisory, then the limit: %d", n)
		}
		pending := 0
		for _, m := range []string{"a", "b", "c"} {
			if e.job("acme/app", "example.com/"+m, "v1.1.0").State == model.JobPending {
				pending++
			}
		}
		if pending != 1 {
			t.Fatalf("the third job must wait behind the limit: %d pending", pending)
		}
	})
}

func TestACapOfZeroIsNoCap(t *testing.T) {
	e := capEnv(t, store.NewMemory(), "    security-bypass-limit-per-advisory: 0\n")
	if n := len(e.gh.PRs("acme/app")); n != 3 {
		t.Fatalf("a cap of 0 is no cap: %d", n)
	}
}

func TestACapNeedsTheLimitBypass(t *testing.T) {
	for _, bad := range []string{
		"updates:\n  - ecosystem: gomod\n    security-bypass-limit-per-advisory: 2\n",
		"updates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit]\n    security-bypass-limit-per-advisory: -1\n",
	} {
		if _, err := config.Parse([]byte(bad)); err == nil || !strings.Contains(err.Error(), "security-bypass-limit-per-advisory") {
			t.Errorf("%q must be refused: %v", bad, err)
		}
	}
}
