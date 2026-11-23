package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Entry 51: a security PR skips only the cooldown unless the update entry opts in with
// security-bypass. "open-pull-requests-limit" opens it at the repo's cap, "schedule"
// opens it outside the window.

const limitOneBypass = "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit]\n"

func TestSecurityBypassOpensAPRPastTheOpenPullRequestLimit(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, _ := correctedEnv(t, st, limitOneBypass)
		if n := len(e.gh.PRs("acme/app")); n != 2 {
			t.Fatalf("both security PRs must open past the limit of one: %d", n)
		}
	})
}

func TestWithoutSecurityBypassTheLimitStillHoldsASecurityPR(t *testing.T) {
	e, _ := correctedEnv(t, store.NewMemory(), "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n")
	if n := len(e.gh.PRs("acme/app")); n != 1 {
		t.Fatalf("the limit must hold the second security PR: %d", n)
	}
}

// A non-security update still obeys the limit while a security PR is open past it.
func TestSecurityBypassDoesNotWidenTheLimitForOtherUpdates(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/one", "v1.0.0", "v1.1.0")
	stubModule(e, "example.com/plain", "v1.0.0", "v1.1.0")
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       gomod("example.com/app", "example.com/one v1.0.0", "example.com/plain v1.0.0"),
		"main.go":      blankImports("example.com/one", "example.com/plain"),
		".ripple.yaml": "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n    security-bypass: [open-pull-requests-limit]\n",
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.publish("example.com/plain", "v1.1.0")
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "0", "1.1.0"))
	e.tick()
	e.tick()
	var titles []string
	for _, p := range e.gh.PRs("acme/app") {
		titles = append(titles, p.Title)
	}
	if len(titles) != 1 || !strings.HasPrefix(titles[0], "[security]") {
		t.Fatalf("want the security PR alone at a limit of one: %q", titles)
	}
	if j := e.job("acme/app", "example.com/plain", "v1.1.0"); j.State != model.JobPending {
		t.Fatalf("the ordinary job must wait behind the limit: %+v", j)
	}
}

const closedWindow = "  - ecosystem: gomod\n    schedule:\n      days: [sat]\n"

func TestSecurityBypassOpensAPROutsideTheSchedule(t *testing.T) {
	e, _ := correctedEnv(t, store.NewMemory(), "updates:\n"+closedWindow+"    security-bypass: [schedule]\n")
	if n := len(e.gh.PRs("acme/app")); n != 2 {
		t.Fatalf("the security PRs must open on a Monday despite a Saturday-only schedule: %d", n)
	}
}

func TestWithoutSecurityBypassTheScheduleStillHoldsASecurityPR(t *testing.T) {
	e, _ := correctedEnv(t, store.NewMemory(), "updates:\n"+closedWindow)
	if n := len(e.gh.PRs("acme/app")); n != 0 {
		t.Fatalf("the schedule must hold the security PRs: %d", n)
	}
}
