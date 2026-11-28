//go:build live

package feed

import (
	"context"
	"testing"
	"time"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/livetest"
	"github.com/taumatix/ripple/internal/model"
)

// These tests read the public Go module index and proxy, anonymously and read only. They
// run with `go test -tags live` (the live workflow), never in the normal test job. The
// fixtures are old, immutable facts: the first entry of the index, long-dead v1 modules,
// a module whose go.mod retracts versions.

const firstIndexTimestamp = "2019-04-10T19:08:52.997264Z" // golang.org/x/text v0.3.0, the index's first entry

func liveIndex() (*GoIndex, *livetest.Transport) {
	c, tr := livetest.Client()
	return &GoIndex{BaseURL: DefaultGoIndexURL, HTTP: c, PageSize: 3, UserAgent: livetest.UserAgent}, tr
}

// since is inclusive on the real index, entries come oldest first with the original path
// case, and a page that comes back full reports more. The fake never replayed the boundary
// entry; the watcher tolerates it because recording a release and fanning it out are idempotent.
func TestLiveGoIndexPagesOldestFirstAndSinceIsInclusive(t *testing.T) {
	g, _ := liveIndex()
	page1, more, err := g.Fetch(context.Background(), firstIndexTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 3 || !more {
		t.Fatalf("want a full page with more, got %d more=%v", len(page1), more)
	}
	first := page1[0]
	if first.Package != "golang.org/x/text" || first.Version != "v0.3.0" || first.Datasource != model.DatasourceGo ||
		first.Time.Format(time.RFC3339Nano) != firstIndexTimestamp {
		t.Fatalf("since is not inclusive or the first entry changed: %+v", first)
	}
	for i := 1; i < len(page1); i++ {
		if page1[i].Time.Before(page1[i-1].Time) {
			t.Fatalf("not oldest first: %+v", page1)
		}
	}
	// What the watcher does next: since = the last timestamp seen.
	page2, _, err := g.Fetch(context.Background(), page1[2].Time.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) == 0 || page2[0] != page1[2] {
		t.Fatalf("the boundary entry should come back first: page1[2]=%+v page2=%+v", page1[2], page2)
	}
	for _, r := range page2 {
		if r.Time.Before(page1[2].Time) {
			t.Fatalf("an entry before the cursor: %+v", r)
		}
	}
}

// The index is live: something was published in the last two days, in order, none older than since.
func TestLiveGoIndexServesRecentReleases(t *testing.T) {
	g, _ := liveIndex()
	since := time.Now().Add(-48 * time.Hour).UTC()
	rels, _, err := g.Fetch(context.Background(), since.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("no module was published in 48 hours")
	}
	for i, r := range rels {
		if r.Time.Before(since.Truncate(time.Second)) || r.Package == "" || !semver.IsValid(r.Version) {
			t.Fatalf("entry %d: %+v", i, r)
		}
	}
}

func liveLookup() (*GoProxyLookup, *livetest.Transport) {
	c, tr := livetest.Client()
	return &GoProxyLookup{BaseURL: "https://proxy.golang.org", HTTP: c, UserAgent: livetest.UserAgent}, tr
}

func TestLiveGoProxyLookupFindsTheLatest(t *testing.T) {
	for _, tc := range []struct{ pkg, atLeast string }{
		{"github.com/pkg/errors", "v0.9.1"},
		{"github.com/Masterminds/semver", "v1.5.0"}, // upper case in the path: the proxy wants !masterminds
		{"github.com/go-redis/redis/v8", "v8.11.5"}, // a major version suffix is part of the path
	} {
		g, _ := liveLookup()
		rel, ok, err := g.Latest(context.Background(), tc.pkg)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", tc.pkg, ok, err)
		}
		if rel.Package != tc.pkg || rel.Datasource != model.DatasourceGo || semver.Compare(rel.Version, tc.atLeast) < 0 || rel.Time.IsZero() {
			t.Errorf("%s: %+v", tc.pkg, rel)
		}
	}
}

func TestLiveGoProxyLookupCachesAnAnswer(t *testing.T) {
	g, tr := liveLookup()
	if _, ok, err := g.Latest(context.Background(), "github.com/pkg/errors"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	n := tr.Requests()
	if _, ok, _ := g.Latest(context.Background(), "github.com/pkg/errors"); !ok || tr.Requests() != n {
		t.Fatalf("second answer was not cached: %d -> %d requests", n, tr.Requests())
	}
}

func TestLiveGoProxyLookupUnknownModuleIsNotAnError(t *testing.T) {
	g, _ := liveLookup()
	_, ok, err := g.Latest(context.Background(), "github.com/taumatix/ripple-live-test-no-such-module")
	if err != nil || ok {
		t.Fatalf("an unknown module should be (false, nil): ok=%v err=%v", ok, err)
	}
}

// go.mongodb.org/mongo-driver retracts v1.13.0 (a moved tag) and ranges below it in the
// go.mod of its newest v1. The real list is unsorted and still names the retracted versions.
func TestLiveGoProxyRetractionsAndUnsortedList(t *testing.T) {
	g, _ := liveLookup()
	ctx := context.Background()
	const pkg = "go.mongodb.org/mongo-driver"
	base := "https://proxy.golang.org/" + pkg
	list, err := g.versions(ctx, base, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(list, "v1.13.0") || !contains(list, "v1.11.1") {
		t.Fatalf("the proxy list should still name retracted versions: %v", list)
	}
	rel, ok, err := g.Latest(ctx, pkg)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	retracted := g.retractions(ctx, base, rel.Version)
	for v, want := range map[string]bool{"v1.13.0": true, "v1.11.1": true, "v1.11.0": true, "v1.11.2": true, "v1.11.3": false, "v1.13.1": false, "v1.12.0": false} {
		if retracted(v) != want {
			t.Errorf("retracted(%s) = %v, want %v", v, !want, want)
		}
	}
	if retracted(rel.Version) {
		t.Errorf("Latest answered a retracted version %s", rel.Version)
	}
}

// github.com/prometheus/common tagged v1.20.99 (its highest stable version) only to retract
// the accidental v1.x tags below it, and retracted v1.20.99 itself; its go.mod at v0.74.0 (the
// proxy's @latest when this was read, 2026-11-28) carries the retraction list. So the proxy's
// own @latest skips the retracted v1.x line, and Latest must too. If the module's maintainers
// move on, the assertions on the v1 versions still hold (retractions are permanent); the
// floor on the answer is the only thing that can need a bump.
func TestLiveGoProxyHighestStableVersionRetractsItself(t *testing.T) {
	g, _ := liveLookup()
	ctx := context.Background()
	const pkg = "github.com/prometheus/common"
	base := "https://proxy.golang.org/" + pkg
	list, err := g.versions(ctx, base, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(list, "v1.20.99") || !contains(list, "v1.20.3") {
		t.Fatalf("the proxy list should still name the retracted v1 tags: %v", list)
	}
	rel, ok, err := g.Latest(ctx, pkg)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if semver.Major(rel.Version) != "v0" || semver.Compare(rel.Version, "v0.74.0") < 0 {
		t.Errorf("Latest = %s, want a v0 version at or above v0.74.0 (every v1 tag is retracted)", rel.Version)
	}
	retracted := g.retractions(ctx, base, rel.Version)
	for _, v := range []string{"v1.20.99", "v1.20.3", "v1.0.0"} {
		if !retracted(v) {
			t.Errorf("retracted(%s) = false, want true", v)
		}
	}
	if retracted(rel.Version) {
		t.Errorf("Latest answered a retracted version %s", rel.Version)
	}
}

// github.com/go-git/go-git/v6 had only prerelease tags (alpha and beta) when this was read,
// 2026-11-28: the proxy's @latest names the newest prerelease, and so must Latest rather than
// answer "no release". When v6 goes stable this test fails on purpose: pin another
// prerelease-only module (the check is for the proxy's behaviour, not for go-git).
func TestLiveGoProxyPrereleaseOnlyModuleIsStillAnswered(t *testing.T) {
	g, _ := liveLookup()
	ctx := context.Background()
	const pkg = "github.com/go-git/go-git/v6"
	list, err := g.versions(ctx, "https://proxy.golang.org/"+pkg, pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if semver.Prerelease(v) == "" {
			t.Fatalf("%s now has a stable version %s: pin another prerelease-only module", pkg, v)
		}
	}
	rel, ok, err := g.Latest(ctx, pkg)
	if err != nil || !ok {
		t.Fatalf("a prerelease-only module must still be answered: ok=%v err=%v", ok, err)
	}
	if semver.Prerelease(rel.Version) == "" || semver.Compare(rel.Version, "v6.0.0-beta.1") < 0 {
		t.Errorf("Latest = %s, want the newest prerelease, at least v6.0.0-beta.1", rel.Version)
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// The backfill far-major probe asks the proxy path by path: a skipped major is a clean miss and
// the next path is a hit. Masterminds/semver went v1 -> v3 and never published v2. Each path
// costs the proxy requests counted here, which is what a raised RIPPLE_BACKFILL_MAJORS spends.
func TestLiveGoProxyFarMajorProbeSkipsAnUnpublishedMajor(t *testing.T) {
	g, tr := liveLookup()
	ctx := context.Background()
	const base = "github.com/Masterminds/semver"
	var perPath []int
	got := map[string]bool{}
	for _, p := range []string{base + "/v2", base + "/v3", base + "/v4", base + "/v5"} {
		n := tr.Requests()
		rel, ok, err := g.Latest(ctx, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		got[p] = ok
		perPath = append(perPath, tr.Requests()-n)
		if ok && (rel.Package != p || semver.Major(rel.Version) != "v3" || semver.Compare(rel.Version, "v3.3.1") < 0) {
			t.Errorf("%s: %+v", p, rel)
		}
	}
	if got[base+"/v2"] || !got[base+"/v3"] || got[base+"/v4"] || got[base+"/v5"] {
		t.Errorf("want only v3 known, got %v", got)
	}
	for i, n := range perPath {
		if n < 1 || n > 3 {
			t.Errorf("path %d cost %d proxy requests, want 1 to 3 (%v)", i, n, perPath)
		}
	}
	t.Logf("proxy requests per probed path (v2, v3, v4, v5): %v", perPath)
}
