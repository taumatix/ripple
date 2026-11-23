package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const pseudoFixed = "1.61.1-0.20260911233437-76452a2c8a2f"

// pushGoMod pushes a new go.mod and waits for the scan it causes.
func (e *env) pushGoMod(repo, goMod string) {
	e.t.Helper()
	scans := e.logLines("scanned", repo)
	e.push(repo, map[string]string{"go.mod": goMod})
	e.eventually("rescan of "+repo, func() bool { return e.logLines("scanned", repo) > scans })
}

// osvRecord is a minimal record in the shape OSV serves, for the cases the recorded
// samples do not cover (an unfixed advisory, a stdlib entry).
func osvRecord(id, pkg, events string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":"1.7.5","id":%q,"modified":"2026-11-20T00:00:00Z","summary":"A flaw in %s.",`+
		`"affected":[{"package":{"name":%q,"ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"}%s]}]}]}`, id, pkg, pkg, events))
}

// A repo that registers after an advisory was published gets its security PR from one
// querybatch, with no poller involved (the poller starts at "now"), and a rescan of an
// unchanged repo asks OSV nothing.
func TestRegistrationFindsAdvisoriesPublishedBefore(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		osv := testutil.NewOSV(t)
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnvOpts(t, st, lim, withOSV(osv, 0))
		osvRepos(e)
		e.publishAdvisory(osv, 6894)
		e.clock.Advance(time.Hour) // the poller's first cursor lies after the record

		start(t, e, "acme/affected", "acme/fixed", "acme/unrelated")
		e.tick()

		prs := e.gh.PRs("acme/affected")
		if len(prs) != 1 || prs[0].Title != "[security] Update example.com/lib to v1.1.0" {
			t.Fatalf("PRs %+v", prs)
		}
		for _, r := range []string{"acme/fixed", "acme/unrelated"} {
			if n := len(e.gh.PRs(r)); n != 0 {
				t.Errorf("%s got %d PRs", r, n)
			}
		}
		reqs, queries := osv.BatchRequests()
		if reqs != 3 || queries != 3 {
			t.Errorf("querybatch: %d requests, %d queries; want one query per repo's single dependency", reqs, queries)
		}
		// Unchanged repos are not asked about again.
		e.pushGoMod("acme/affected", gomod("example.com/affected", "example.com/lib v1.0.0")+"// touched\n")
		e.tick()
		if r2, _ := osv.BatchRequests(); r2 != reqs {
			t.Errorf("a rescan of an unchanged repo asked OSV again: %d -> %d", reqs, r2)
		}
	})
}

// A dependency bumped to a version an old advisory covers is a new pair: it is caught on the rescan.
func TestRescanChecksANewlyVulnerableVersion(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	osvRepos(e)
	e.gh.AddRepo("acme/clean", map[string]string{"go.mod": gomod("example.com/clean", "example.com/lib v1.1.0"), "main.go": blankImports("example.com/lib")})
	e.publishAdvisory(osv, 6894) // fixed in 1.1.0
	e.clock.Advance(time.Hour)
	start(t, e, "acme/clean")
	if n := len(e.gh.PRs("acme/clean")); n != 0 {
		t.Fatalf("a fixed repo got %d PRs", n)
	}
	e.pushGoMod("acme/clean", gomod("example.com/clean", "example.com/lib v1.0.0"))
	e.tick()
	e.tick()
	if prs := e.gh.PRs("acme/clean"); len(prs) != 1 || !strings.HasPrefix(prs[0].Title, "[security] ") {
		t.Fatalf("PRs %+v", prs)
	}
}

// An OSV outage during a scan loses nothing: the scan succeeds, and the next one asks again.
func TestOutageDuringScanIsRetriedByTheNextScan(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	osvRepos(e)
	e.publishAdvisory(osv, 6894)
	e.clock.Advance(time.Hour)
	osv.SetDown(true)
	start(t, e, "acme/affected")
	if len(e.deps("acme/affected")) == 0 {
		t.Fatal("the scan failed with OSV down: no dependencies were stored")
	}
	if n := len(e.gh.PRs("acme/affected")); n != 0 {
		t.Fatalf("PR while OSV was down: %d", n)
	}
	osv.SetDown(false)
	e.pushGoMod("acme/affected", gomod("example.com/affected", "example.com/lib v1.0.0")+"// touched\n")
	e.tick()
	if n := len(e.gh.PRs("acme/affected")); n != 1 {
		t.Fatalf("the retry did not find the advisory: %d PRs", n)
	}
}

// Entry 48: a pseudo-version fix is proposed only where the entry opts in, never
// automerged, and said to be a pseudo-version; elsewhere the dashboard lists it and
// no PR opens. An unfixed advisory is listed too.
func TestPseudoVersionFixIsOptInAndUnfixedGoesToTheDashboard(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	stubModule(e, "example.com/lib", "v1.0.0", "v"+pseudoFixed)
	stubModule(e, "example.com/gap", "v1.0.0")
	uses := func(mod string) map[string]string {
		return map[string]string{"go.mod": gomod("example.com/app", mod+" v1.0.0"), "main.go": blankImports(mod)}
	}
	optIn := uses("example.com/lib")
	optIn[".ripple.yaml"] = "updates:\n  - ecosystem: gomod\n    security-pseudo-versions: true\n    automerge: major\n"
	dash := uses("example.com/lib")
	dash[".ripple.yaml"] = "dependency-dashboard: true\nupdates:\n  - ecosystem: gomod\n"
	gap := uses("example.com/gap")
	gap[".ripple.yaml"] = "dependency-dashboard: true\nupdates:\n  - ecosystem: gomod\n"
	e.gh.AddRepo("acme/optin", optIn)
	e.gh.AddRepo("acme/dash", dash)
	e.gh.AddRepo("acme/gap", gap)
	start(t, e, "acme/optin", "acme/dash", "acme/gap")

	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-6894", e.clock.Now(), sampleFor(t, "GO-2026-6894.json", "GO-2026-6894", "example.com/lib", pseudoFixed))
	osv.Publish("GO-2026-7000", e.clock.Now(), osvRecord("GO-2026-7000", "example.com/gap", ""))
	e.tick()
	e.tick()

	head, body := onePR(t, e, "acme/optin", false)
	_ = head
	if !strings.Contains(body, "pseudo-version") || !strings.Contains(body, "v"+pseudoFixed) {
		t.Errorf("body does not say the fix is a pseudo-version:\n%s", body)
	}
	if e.gh.PRs("acme/optin")[0].AutoMerge {
		t.Errorf("a pseudo-version fix must never be auto-merged")
	}
	if n := len(e.gh.PRs("acme/dash")) + len(e.gh.PRs("acme/gap")); n != 0 {
		t.Errorf("PRs without a usable fix: %d", n)
	}
	is := e.gh.Issues("acme/dash")
	if len(is) != 1 || !strings.Contains(is[0].Body, "Advisories without a fix") || !strings.Contains(is[0].Body, "security-pseudo-versions") ||
		!strings.Contains(is[0].Body, "https://osv.dev/vulnerability/GO-2026-6894") {
		t.Errorf("dash dashboard: %+v", is)
	}
	is = e.gh.Issues("acme/gap")
	if len(is) != 1 || !strings.Contains(is[0].Body, "no release fixes it yet") {
		t.Errorf("gap dashboard: %+v", is)
	}
	if is := e.gh.Issues("acme/optin"); len(is) != 0 {
		t.Errorf("a repo without the dashboard got an issue: %+v", is)
	}
}

// Entry 49: a stdlib advisory moves a toolchain line, with no cooldown, in a repo that
// opted into go-toolchain; a repo that did not is left alone.
func TestStdlibAdvisoryMovesTheToolchainLineOnlyWhereOptedIn(t *testing.T) {
	n := localMinor(t)
	osv := testutil.NewOSV(t)
	lim := defaultLimits()
	lim.MinimumReleaseAge = 72 * time.Hour
	e := newEnvOpts(t, store.NewMemory(), lim, withOSV(osv, 0))
	e.gh.AddRepo("acme/in", withMode(tcRepo("1.22", goVer(n, 0)), "toolchain"))
	e.gh.AddRepo("acme/out", withMode(tcRepo("1.22", goVer(n, 0)), ""))
	start(t, e, "acme/in", "acme/out")

	e.clock.Advance(time.Minute)
	osv.Publish("GO-2026-7100", e.clock.Now(), osvRecord("GO-2026-7100", "stdlib", fmt.Sprintf(`,{"fixed":"1.%d.3"}`, n)))
	e.tick()

	head, body := onePR(t, e, "acme/in", false)
	mod, _ := e.gh.FileAt("acme/in", head, "go.mod")
	if want := "go 1.22\n\ntoolchain " + goVer(n, 3) + "\n"; !strings.Contains(mod, want) {
		t.Errorf("want the toolchain line on the fixed patch and the directive left alone:\n%s", mod)
	}
	if !strings.Contains(body, "https://osv.dev/vulnerability/GO-2026-7100") {
		t.Errorf("body lacks the advisory:\n%s", body)
	}
	if !strings.HasPrefix(e.gh.PRs("acme/in")[0].Title, "[security] ") {
		t.Errorf("title %q", e.gh.PRs("acme/in")[0].Title)
	}
	if prs := e.gh.PRs("acme/out"); len(prs) != 0 {
		t.Errorf("a repo without go-toolchain got %+v", prs)
	}
}

// Entry 50 for the toolchain: a repo registered after a stdlib advisory is found by the
// history check.
func TestRegistrationFindsAStdlibAdvisoryForTheToolchainLine(t *testing.T) {
	n := localMinor(t)
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	osv.Publish("GO-2026-7100", e.clock.Now(), osvRecord("GO-2026-7100", "stdlib", fmt.Sprintf(`,{"fixed":"1.%d.3"}`, n)))
	e.clock.Advance(time.Hour)
	e.gh.AddRepo("acme/in", withMode(tcRepo("1.22", goVer(n, 0)), "toolchain"))
	start(t, e, "acme/in")
	e.tick()
	if prs := e.gh.PRs("acme/in"); len(prs) != 1 {
		t.Fatalf("PRs %+v", prs)
	}
}
