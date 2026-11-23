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
