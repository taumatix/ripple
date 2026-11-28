//go:build live

package advisory

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/livetest"
)

// These tests read the public OSV API and bucket, anonymously. They run with
// `go test -tags live`. Fixtures are old Go advisories whose affected ranges cannot change
// meaning: GO-2021-0113 (golang.org/x/text, fixed 0.3.7) and GO-2022-0969 (net/http, fixed
// 1.18.6 and 1.19.1). Newer advisories are added to those packages over time, so id sets
// are asserted with "contains", never equality.

func liveClient() (*Client, *livetest.Transport) {
	c, tr := livetest.Client()
	return &Client{HTTP: c}, tr
}

func TestLiveVulnRecordParsesAndAssesses(t *testing.T) {
	c, _ := liveClient()
	ctx := context.Background()
	v, err := c.Vuln(ctx, "GO-2021-0113")
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "GO-2021-0113" || v.Withdrawn() || !slices.Contains(v.Modules(), "golang.org/x/text") || v.URL() != "https://osv.dev/vulnerability/GO-2021-0113" {
		t.Fatalf("%+v", v)
	}
	if got := v.Assess("golang.org/x/text", "v0.3.6"); !got.Affected || got.Fixed != "v0.3.7" {
		t.Errorf("v0.3.6: %+v", got)
	}
	if got := v.Assess("golang.org/x/text", "v0.3.7"); got.Affected {
		t.Errorf("v0.3.7 is the fix: %+v", got)
	}
	if got := v.Assess("golang.org/x/net", "v0.0.1"); got.Affected {
		t.Errorf("another module: %+v", got)
	}

	std, err := c.Vuln(ctx, "GO-2022-0969") // two ranges: backported fix in 1.18.6, fix in 1.19.1
	if err != nil {
		t.Fatal(err)
	}
	if !std.HasToolchain() {
		t.Fatal("a stdlib record must read as a toolchain advisory")
	}
	for cur, want := range map[string]Verdict{
		"v1.18.0": {Affected: true, Fixed: "v1.18.6"},
		"v1.19.0": {Affected: true, Fixed: "v1.19.1"},
		"v1.19.1": {},
		"v1.20.0": {},
	} {
		if got := std.AssessToolchain(cur); got != want {
			t.Errorf("toolchain %s: got %+v want %+v", cur, got, want)
		}
	}
}

func TestLiveVulnUnknownIDIsErrNotFound(t *testing.T) {
	c, _ := liveClient()
	if _, err := c.Vuln(context.Background(), "GO-1900-0000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// querybatch: the version goes without its v, an unaffected version is an empty result (no
// "vulns" key), stdlib answers by name, and what OSV calls affected is what ripple's own
// evaluator calls affected for every record it returns.
func TestLiveQueryBatchAgreesWithRipplesEvaluator(t *testing.T) {
	c, _ := liveClient()
	ctx := context.Background()
	got, err := c.QueryBatch(ctx, []Query{
		{Name: "golang.org/x/text", Version: "v0.3.6"},
		{Name: "golang.org/x/text", Version: "0.3.7"},
		{Name: "golang.org/x/text", Version: "v1.0.0"},
		{Name: "stdlib", Version: "v1.18.0"},
		{Name: "github.com/taumatix/ripple-live-test-no-such-module", Version: "v1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("one answer per query, got %d", len(got))
	}
	if !slices.Contains(got[0], "GO-2021-0113") || !slices.Contains(got[0], "GO-2022-1059") {
		t.Errorf("x/text v0.3.6: %v", got[0])
	}
	if slices.Contains(got[1], "GO-2021-0113") || !slices.Contains(got[1], "GO-2022-1059") {
		t.Errorf("x/text 0.3.7 (no v): %v", got[1])
	}
	if len(got[2]) != 0 || len(got[4]) != 0 {
		t.Errorf("nothing should affect x/text v1.0.0 or an unknown module: %v %v", got[2], got[4])
	}
	if !slices.Contains(got[3], "GO-2022-0969") {
		t.Errorf("stdlib v1.18.0: %d ids, no GO-2022-0969", len(got[3]))
	}
	// Ripple's evaluator must agree with OSV for the Go-database records (GO-*; the GHSA
	// twins are the same facts under another id).
	checked := 0
	for _, id := range got[0] {
		if !strings.HasPrefix(id, "GO-") {
			continue
		}
		v, err := c.Vuln(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if a := v.Assess("golang.org/x/text", "v0.3.6"); !a.Affected {
			t.Errorf("OSV lists %s for x/text v0.3.6 but ripple's evaluator says not affected", id)
		}
		if checked++; checked == 3 {
			break
		}
	}
	if checked == 0 {
		t.Fatal("no GO- record to compare")
	}
}

// The stdlib result for one version is large (100+ ids). It arrives in one result with no
// next_page_token, so the pagination loop was not exercised: see ROADMAP 52.
func TestLiveQueryBatchStdlibIsOneUnpagedResult(t *testing.T) {
	c, tr := liveClient()
	out, err := c.QueryBatch(context.Background(), []Query{{Name: "stdlib", Version: "v1.21.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0]) < 50 {
		t.Fatalf("stdlib 1.21.0 had 90 ids on 2026-11-23, got %d", len(out[0]))
	}
	if tr.Requests() != 1 {
		t.Fatalf("%d requests; a page token appeared, so pagination is now live-testable", tr.Requests())
	}
	seen := map[string]bool{}
	for _, id := range out[0] {
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
	}
}

// No Go package has more than 1000 records, so pagination is exercised through the ecosystem seam
// on Debian:12, whose packages do (ROADMAP 52). querybatch returned 1000 ids and a
// next_page_token; the token goes back in the query's page_token and the next page shares no id
// with the first (checked 2026-11-28). The client follows up to maxBatchPageRound pages.
func TestLiveQueryBatchFollowsNextPageTokenToTheEnd(t *testing.T) {
	c, tr := liveClient()
	c.ecosystem = "Debian:12"
	out, err := c.QueryBatch(context.Background(), []Query{{Name: "firefox-esr"}}) // 1562 ids, 2 pages, on 2026-11-28
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0]) < 1500 || tr.Requests() != 2 {
		t.Fatalf("%d ids in %d requests, want about 1562 ids in 2 (firefox-esr, Debian:12)", len(out[0]), tr.Requests())
	}
	seen := map[string]bool{}
	for _, id := range out[0] {
		if seen[id] {
			t.Fatalf("duplicate id %s across pages", id)
		}
		seen[id] = true
	}
}

// A query whose pages outlast the bound returns the ids it got and no error: chromium on Debian:12
// had 6279 ids over 7 pages, the client stops after 5 rounds with exactly 5000. Silent, by design
// (docs/osv.md); no Go package comes near it.
func TestLiveQueryBatchStopsAtItsPageBound(t *testing.T) {
	c, tr := liveClient()
	c.ecosystem = "Debian:12"
	out, err := c.QueryBatch(context.Background(), []Query{{Name: "chromium"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out[0]) != maxBatchPageRound*1000 || tr.Requests() != maxBatchPageRound {
		t.Fatalf("%d ids in %d requests, want %d in %d (the bound)", len(out[0]), tr.Requests(), maxBatchPageRound*1000, maxBatchPageRound)
	}
}

// A withdrawn record read from the real API: GO-2026-4505 was withdrawn on 2026-11-10 and the
// record is kept with its `withdrawn` time. Such a record proposes nothing.
func TestLiveWithdrawnRecordIsReadAsWithdrawn(t *testing.T) {
	c, _ := liveClient()
	v, err := c.Vuln(context.Background(), "GO-2026-4505")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Withdrawn() || v.WithdrawnAt != "2026-11-10T18:46:52Z" {
		t.Fatalf("withdrawn = %q, want the 2026-11-10 time", v.WithdrawnAt)
	}
}

// The delay between an id appearing in modified_id.csv and its record being readable from the
// API: every id the CSV listed at least 30 minutes ago resolves. Younger ones are not asserted
// (one run cannot show a delay, only that old entries have none); the youngest age checked is
// logged so the daily runs build a record of it.
func TestLiveRecentCSVIdsHaveRecords(t *testing.T) {
	hc, _ := livetest.Client()
	c := &Client{HTTP: hc}
	all, _, err := c.Modified(context.Background(), Position{})
	if err != nil || len(all) < 20 {
		t.Fatalf("%d entries, %v", len(all), err)
	}
	checked := 0
	youngest := time.Duration(1 << 62)
	for i := len(all) - 1; i >= 0 && checked < 8; i-- {
		age := time.Since(all[i].Time)
		if age < 30*time.Minute {
			continue
		}
		if _, err := c.Vuln(context.Background(), all[i].ID); err != nil {
			t.Errorf("%s was listed %s ago and is not readable: %v", all[i].ID, age.Round(time.Second), err)
		}
		youngest = min(youngest, age)
		checked++
	}
	if checked < 8 {
		t.Fatalf("only %d entries older than 30 minutes", checked)
	}
	t.Logf("8 newest ids older than 30 minutes all readable; youngest checked was %s old", youngest.Round(time.Second))
}

// modified_id.csv: newest first with nanosecond timestamps, the property Modified relies on to
// stop early; and Modified returns the entries after a position oldest first.
func TestLiveModifiedCSVOrderAndEarlyStop(t *testing.T) {
	hc, tr := livetest.Client()
	resp, err := hc.Get(DefaultDumpURL + "/" + Ecosystem + "/modified_id.csv")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%v %v", resp.Status, err)
	}
	var times []time.Time
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		ts, id, ok := strings.Cut(line, ",")
		at, terr := time.Parse(time.RFC3339Nano, ts)
		if !ok || terr != nil || !idRE.MatchString(id) {
			t.Fatalf("unparseable line %q", line)
		}
		times, ids = append(times, at), append(ids, id)
	}
	if len(times) < 5000 {
		t.Fatalf("only %d lines", len(times))
	}
	for i := 1; i < len(times); i++ {
		if times[i].After(times[i-1]) {
			t.Fatalf("not newest first at line %d: %s after %s", i, times[i], times[i-1])
		}
	}

	c := &Client{HTTP: hc}
	pos := Position{Time: times[4], ID: ids[4]} // the sixth newest line
	got, truncated, err := c.Modified(context.Background(), pos)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(got) < 4 {
		t.Fatalf("%d entries truncated=%v", len(got), truncated)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Time.Before(got[i-1].Time) {
			t.Fatalf("not oldest first: %+v", got)
		}
	}
	for _, e := range got {
		if !pos.before(e.Time, e.ID) {
			t.Fatalf("%+v is not after %+v", e, pos)
		}
	}
	// A full read with the zero position is bounded: it reaches the end of the file.
	if all, truncated, err := c.Modified(context.Background(), Position{}); err != nil || truncated || len(all) != len(times) {
		t.Fatalf("full read: %d of %d lines, truncated=%v err=%v", len(all), len(times), truncated, err)
	}
	t.Logf("modified_id.csv: %d bytes, %d lines; %d requests", len(raw), len(times), tr.Requests())
}
