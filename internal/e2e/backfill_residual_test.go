package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// A newer major path on the same line that the feed already recorded is the backfill target:
// the repo gets the PR to the new path, not only a bump inside its own major.
func TestBackfillLooksAcrossTheLineForANewerMajor(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "", "example.com/maj")
		stubModule(e, "example.com/maj/v2", "v2.0.0")
		if _, err := e.st.RecordRelease(context.Background(), model.Release{
			Datasource: model.DatasourceGo, Package: "example.com/maj/v2", Version: "v2.0.0", Time: e.clock.Now()}); err != nil {
			t.Fatal(err)
		}
		e.importAll("example.com/maj")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/maj/v2") || !strings.HasSuffix(prs[0].Title, " v2.0.0") {
			t.Fatalf("want one PR to example.com/maj/v2 v2.0.0, got %+v", prs)
		}
	})
}

// A module nobody else imports has no stored release; the module proxy is asked, boundedly.
func TestBackfillAsksTheProxyForAModuleWithNoStoredRelease(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "")
		e.app.Engine.Lookup = &feed.GoProxyLookup{BaseURL: e.proxy.URL}
		stubModule(e, "example.com/lonely", "v1.0.0", "v1.2.0")
		e.importAll("example.com/lonely")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/lonely") || !strings.HasSuffix(prs[0].Title, " v1.2.0") {
			t.Fatalf("want one PR to example.com/lonely v1.2.0, got %+v", prs)
		}
	})
}

// Without a lookup configured nothing is asked and nothing is invented.
func TestBackfillWithoutLookupLeavesAnUnrecordedModuleAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "")
		stubModule(e, "example.com/lonely", "v1.0.0", "v1.2.0")
		e.importAll("example.com/lonely")
		e.tick()
		if prs := e.gh.PRs(backfillApp); len(prs) != 0 {
			t.Fatalf("want no PR, got %+v", prs)
		}
	})
}

// A module whose newest release retracts itself is brought to the newest version that is not
// retracted, not to the retracted one.
func TestBackfillLookupSkipsARetractedLatest(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "")
		e.app.Engine.Lookup = &feed.GoProxyLookup{BaseURL: e.proxy.URL}
		stubModule(e, "example.com/retr", "v1.0.0", "v1.1.0")
		e.proxy.Publish("example.com/retr", "v1.2.0", map[string]string{
			"x.go":   "package x\n",
			"go.mod": "module example.com/retr\n\ngo 1.21\n\nretract v1.2.0 // published by mistake\n",
		})
		e.importAll("example.com/retr")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/retr") || !strings.HasSuffix(prs[0].Title, " v1.1.0") {
			t.Fatalf("want one PR to example.com/retr v1.1.0, got %+v", prs)
		}
	})
}

// A module that skipped past the three near majors is found when RIPPLE_BACKFILL_MAJORS reaches
// it: the proxy is asked path by path, and the PR goes to the newest major it knows.
func TestBackfillFindsAFarMajorWhenTheProbeIsRaised(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "", "example.com/far")
		e.app.Engine.Lookup = &feed.GoProxyLookup{BaseURL: e.proxy.URL}
		stubModule(e, "example.com/far/v6", "v6.1.0")
		e.app.Engine.BackfillMajors = 8
		e.importAll("example.com/far")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || !strings.Contains(prs[0].Title, "example.com/far/v6") || !strings.HasSuffix(prs[0].Title, " v6.1.0") {
			t.Fatalf("want one PR to example.com/far/v6 v6.1.0, got %+v", prs)
		}
	})
}

// Left at the default the probe stops at three majors, so the same far major is not reached.
func TestBackfillDefaultProbeStopsAtThreeMajors(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := backfillEnv(t, st, "", "example.com/far")
		e.app.Engine.Lookup = &feed.GoProxyLookup{BaseURL: e.proxy.URL}
		stubModule(e, "example.com/far/v6", "v6.1.0")
		e.importAll("example.com/far")
		e.tick()
		prs := e.gh.PRs(backfillApp)
		if len(prs) != 1 || strings.Contains(prs[0].Title, "/v6") {
			t.Fatalf("want one in-major PR, got %+v", prs)
		}
	})
}
