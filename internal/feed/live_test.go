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

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
