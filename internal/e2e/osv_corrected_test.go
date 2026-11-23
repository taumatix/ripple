package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// osvRange is a one-module record affecting [introduced, fixed).
func osvRange(id, pkg, introduced, fixed string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"1.7.5","id":%q,"modified":"2026-11-20T00:00:00Z","summary":"A flaw in %s.",`+
		`"affected":[{"package":{"name":%q,"ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":%q},{"fixed":%q}]}]}]}`, id, pkg, pkg, introduced, fixed))
}

// correctedEnv has one repo on two vulnerable modules with room for one PR: the first
// advisory's job opens a PR, the second waits as a pending job behind the limit.
func correctedEnv(t *testing.T, st store.Store, cfg string) (*env, *testutil.OSV) {
	t.Helper()
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, st, defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/one", "v1.0.0", "v1.1.0")
	stubModule(e, "example.com/two", "v1.0.0", "v1.1.0")
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       gomod("example.com/app", "example.com/one v1.0.0", "example.com/two v1.0.0"),
		"main.go":      blankImports("example.com/one", "example.com/two"),
		".ripple.yaml": cfg,
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "0", "1.1.0"))
	osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "0", "1.1.0"))
	e.tick()
	return e, osv
}

const limitOne = "dependency-dashboard: true\nopen-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n"

// Entry 82: a record corrected so that it no longer affects the version in use finishes
// the job that waited for it and closes the PR ripple opened for it, with a comment that
// says why.
func TestACorrectedAdvisoryClosesItsPRAndSkipsItsPendingJob(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, osv := correctedEnv(t, st, limitOne)
		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 {
			t.Fatalf("want one PR and one waiting job: %+v", prs)
		}
		opened, waiting := "example.com/one", "example.com/two"
		if strings.Contains(prs[0].Title, "two") {
			opened, waiting = waiting, opened
		}
		if j := e.job("acme/app", waiting, "v1.1.0"); j.State != model.JobPending {
			t.Fatalf("the second job must wait behind the limit: %+v", j)
		}

		// Both records are corrected: v1.0.0 was never affected.
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
		osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "1.0.5", "1.1.0"))
		e.tick()

		pr := e.gh.PRs("acme/app")[0]
		if !pr.Closed || len(pr.Comments) == 0 || !strings.Contains(strings.Join(pr.Comments, "\n"), "corrected") {
			t.Errorf("the PR for %s must be closed with a reason: closed=%v comments=%q", opened, pr.Closed, pr.Comments)
		}
		j := e.job("acme/app", waiting, "v1.1.0")
		if j.State != model.JobSkipped || !strings.Contains(j.Reason, "corrected") {
			t.Errorf("the waiting job must be skipped with a reason: %+v", j)
		}
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Errorf("no new PR may open for a corrected record: %d", n)
		}
	})
}

// A PR a person touched is theirs: the correction leaves it open.
func TestACorrectedAdvisoryLeavesATouchedPRAlone(t *testing.T) {
	e, osv := correctedEnv(t, store.NewMemory(), limitOne)
	pr := e.gh.PRs("acme/app")[0]
	e.gh.HumanCommitOnBranch("acme/app", pr.Head)
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
	osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "1.0.5", "1.1.0"))
	e.tick()
	if got := e.gh.PRs("acme/app")[0]; got.Closed {
		t.Fatalf("a PR with a person's commit was closed: %+v", got)
	}
}

// A record modified without changing what it says about the version in use closes nothing.
func TestAnUnchangedVerdictLeavesThePRAndTheJobAlone(t *testing.T) {
	e, osv := correctedEnv(t, store.NewMemory(), limitOne)
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "0", "1.1.0"))
	osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "0", "1.1.0"))
	e.tick()
	if pr := e.gh.PRs("acme/app")[0]; pr.Closed {
		t.Fatalf("PR closed though the record still affects the dependency: %+v", pr)
	}
}

// The dashboard lists an advisory ripple has no fix for; once the record is corrected so
// that the version in use is not affected, the entry goes.
func TestACorrectedAdvisoryLeavesTheDashboard(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/one", "v1.0.0")
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       gomod("example.com/app", "example.com/one v1.0.0"),
		"main.go":      blankImports("example.com/one"),
		".ripple.yaml": "dependency-dashboard: true\nupdates:\n  - ecosystem: gomod\n",
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7402", e.clock.Now(), osvRecord("GO-2026-7402", "example.com/one", ""))
	e.tick()
	is := e.gh.Issues("acme/app")
	if len(is) != 1 || !strings.Contains(is[0].Body, "GO-2026-7402") {
		t.Fatalf("dashboard: %+v", is)
	}
	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7402", e.clock.Now(), osvRange("GO-2026-7402", "example.com/one", "0", "0.9.0"))
	e.tick()
	is = e.gh.Issues("acme/app")
	if len(is) != 1 || strings.Contains(is[0].Body, "GO-2026-7402") {
		t.Fatalf("the corrected advisory is still listed: %+v", is)
	}
}

// Entry 105: a repo that stays busy while its record is corrected keeps the stale job and
// PR only until the repo is free, not until the record changes again.
func TestACorrectionWaitsForABusyRepoAndIsAppliedWhenItIsFree(t *testing.T) {
	forEachFake(t, func(t *testing.T, st store.Store) {
		leases, ok := st.(store.Leases)
		if !ok {
			t.Fatal("store has no leases")
		}
		e, osv := correctedEnv(t, st, limitOne)
		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 {
			t.Fatalf("want one PR and one waiting job: %+v", prs)
		}
		waiting := "example.com/two"
		if strings.Contains(prs[0].Title, "two") {
			waiting = "example.com/one"
		}
		// Another replica is running the repo.
		other, held, err := leases.AcquireLease(context.Background(), engine.LeaseName("acme/app"), "another-replica", time.Hour)
		if err != nil || !held {
			t.Fatalf("lease: %v %v", held, err)
		}
		e.clock.Advance(time.Minute)
		osv.Publish("GO-2026-7400", e.clock.Now(), osvRange("GO-2026-7400", "example.com/one", "1.0.5", "1.1.0"))
		osv.Publish("GO-2026-7401", e.clock.Now(), osvRange("GO-2026-7401", "example.com/two", "1.0.5", "1.1.0"))
		e.tick()
		if j := e.job("acme/app", waiting, "v1.1.0"); j.State != model.JobPending {
			t.Fatalf("the job moved while the repo was busy: %+v", j)
		}
		if err := leases.ReleaseLease(context.Background(), other); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Minute)
		e.tick() // the record did not change again
		if j := e.job("acme/app", waiting, "v1.1.0"); j.State != model.JobSkipped {
			t.Errorf("the correction was not applied once the repo was free: %+v", j)
		}
		if pr := e.gh.PRs("acme/app")[0]; !pr.Closed {
			t.Errorf("the PR of the corrected record is still open")
		}
	})
}
