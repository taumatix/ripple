package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// sampleFor reads a record OSV served (docs/vendor/osv/samples) and points it at a
// module and fixed version the fake proxy knows, leaving every other byte as served.
func sampleFor(t *testing.T, name, id, mod, fixed string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "vendor", "osv", "samples", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.NewReplacer(
		"github.com/anacrolix/torrent", mod,
		"1.61.1-0.20260911233437-76452a2c8a2f", fixed,
		"GO-2026-6894", id, // the record's own id; the GHSA alias keeps its text
	).Replace(string(b)))
}

func withOSV(osv *testutil.OSV, maxFetch int) opts {
	return opts{tweak: func(a *app.App) {
		a.Engine.Advisories = true
		c := &advisory.Client{DumpURL: osv.DumpURL(), APIURL: osv.APIURL()}
		a.Engine.AdvisoryHistory = c // as wire.go does
		a.Advisories = &engine.AdvisoryWatcher{Engine: a.Engine, MaxFetch: maxFetch, Source: c}
	}}
}

func osvRepos(e *env) {
	stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
	stubModule(e, "example.com/other", "v1.0.0")
	e.gh.AddRepo("acme/affected", map[string]string{"go.mod": gomod("example.com/affected", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
	e.gh.AddRepo("acme/fixed", map[string]string{"go.mod": gomod("example.com/fixed", "example.com/lib v1.1.0"), "main.go": blankImports("example.com/lib")})
	e.gh.AddRepo("acme/unrelated", map[string]string{"go.mod": gomod("example.com/unrelated", "example.com/other v1.0.0"), "main.go": blankImports("example.com/other")})
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func (e *env) publishAdvisory(osv *testutil.OSV, n int) {
	// A GHSA record and its Go-database twin share a timestamp, as recorded.
	at := e.clock.Now()
	osv.Publish("GHSA-2wrx-84qj-4pcg", at, sampleFor(e.t, "GHSA-2wrx-84qj-4pcg.trimmed.json", "", "example.com/lib", "1.1.0"))
	osv.Publish(fmt.Sprintf("GO-2026-%04d", n), at, sampleFor(e.t, "GO-2026-6894.json", fmt.Sprintf("GO-2026-%04d", n), "example.com/lib", "1.1.0"))
}

// An advisory for a module in go.mod becomes a PR for the fixed version inside the
// cooldown, labelled and titled as security; a repo already at the fix and a repo
// that does not use the module are untouched; and a newer release of the same
// module still waits for its cooldown.
func TestAdvisoryOpensSecurityPRInsideCooldown(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		osv := testutil.NewOSV(t)
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnvOpts(t, st, lim, withOSV(osv, 0))
		osvRepos(e)
		start(t, e, "acme/affected", "acme/fixed", "acme/unrelated")

		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.2.0") // an ordinary release, 1 minute old
		e.publishAdvisory(osv, 6894)
		e.tick()

		prs := e.gh.PRs("acme/affected")
		if len(prs) != 1 {
			t.Fatalf("want 1 PR for the fixed version, got %d", len(prs))
		}
		pr := prs[0]
		if pr.Title != "[security] Update example.com/lib to v1.1.0" {
			t.Errorf("title %q", pr.Title)
		}
		if !hasLabel(pr.Labels, "security") {
			t.Errorf("labels %v", pr.Labels)
		}
		for _, want := range []string{"https://osv.dev/vulnerability/GHSA-2wrx-84qj-4pcg", "MODERATE", "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:N/I:N/A:H", "unbounded recursion"} {
			if !strings.Contains(pr.Body, want) {
				t.Errorf("PR body lacks %q:\n%s", want, pr.Body)
			}
		}
		// The cooldown was skipped for the fix only.
		if j := e.job("acme/affected", "example.com/lib", "v1.2.0"); j.State != model.JobPending {
			t.Errorf("v1.2.0 job is %s, want pending behind its cooldown", j.State)
		}
		for _, r := range []string{"acme/fixed", "acme/unrelated"} {
			if n := len(e.gh.PRs(r)); n != 0 {
				t.Errorf("%s got %d PRs", r, n)
			}
		}
	})
}

// The release feed queued the fixed version first, behind its cooldown; the advisory
// makes that same job due.
func TestAdvisoryMakesAFeedJobDue(t *testing.T) {
	osv := testutil.NewOSV(t)
	lim := defaultLimits()
	lim.MinimumReleaseAge = 72 * time.Hour
	e := newEnvOpts(t, store.NewMemory(), lim, withOSV(osv, 0))
	osvRepos(e)
	start(t, e, "acme/affected")
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	if n := len(e.gh.PRs("acme/affected")); n != 0 {
		t.Fatalf("PR inside the cooldown: %d", n)
	}
	e.clock.Advance(time.Minute)
	e.publishAdvisory(osv, 6894)
	e.tick()
	prs := e.gh.PRs("acme/affected")
	if len(prs) != 1 || !strings.HasPrefix(prs[0].Title, "[security] ") {
		t.Fatalf("PRs %+v", prs)
	}
}

// The cursor is persisted: a restart does not fetch or fan out the same records again.
func TestAdvisoryCursorSurvivesRestart(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	osvRepos(e)
	start(t, e, "acme/affected")
	e.clock.Advance(time.Minute)
	e.publishAdvisory(osv, 6894)
	e.tick()
	_, before := osv.Requests()
	if before != 2 || len(e.gh.PRs("acme/affected")) != 1 {
		t.Fatalf("record requests %d, PRs %d", before, len(e.gh.PRs("acme/affected")))
	}
	e.stopApp()
	e.startApp()
	e.tick()
	if _, after := osv.Requests(); after != before {
		t.Fatalf("restart refetched records: %d -> %d", before, after)
	}
	if n := len(e.gh.PRs("acme/affected")); n != 1 {
		t.Fatalf("restart opened another PR: %d", n)
	}
}

// OSV being down never blocks ordinary updates, and the advisory is picked up once
// OSV is back.
func TestAdvisoryOutageDoesNotBlockUpdates(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 0))
	osvRepos(e)
	start(t, e, "acme/affected")
	e.clock.Advance(time.Minute)
	osv.SetDown(true)
	e.publishAdvisory(osv, 6894)
	e.publish("example.com/lib", "v1.2.0")
	e.tick()
	if n := len(e.gh.PRs("acme/affected")); n != 1 {
		t.Fatalf("ordinary update blocked by the OSV outage: %d PRs", n)
	}
	osv.SetDown(false)
	e.clock.Advance(time.Minute)
	e.tick()
	if j := e.job("acme/affected", "example.com/lib", "v1.1.0"); j.Key == "" {
		t.Fatal("advisory not processed after OSV came back")
	}
}

// One poll makes one list request and at most MaxFetch record requests; the backlog
// drains over later polls with nothing lost.
func TestAdvisoryRequestVolumeIsBounded(t *testing.T) {
	osv := testutil.NewOSV(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), withOSV(osv, 3))
	osvRepos(e)
	start(t, e, "acme/unrelated")
	e.clock.Advance(time.Minute)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("GO-2026-%04d", 9000+i)
		osv.Publish(id, e.clock.Now().Add(time.Duration(i)*time.Second), sampleFor(t, "GO-2026-6894.json", id, "github.com/nobody/uses-this", "1.0.1"))
	}
	e.clock.Advance(time.Minute)
	e.tick()
	if csv, vulns := osv.Requests(); vulns != 3 || csv != 1 {
		t.Fatalf("one poll made %d list and %d record requests, want 1 and 3", csv, vulns)
	}
	for i := 0; i < 5; i++ {
		e.tick()
	}
	if _, vulns := osv.Requests(); vulns != 10 {
		t.Fatalf("backlog drained with %d record requests, want exactly 10", vulns)
	}
}
