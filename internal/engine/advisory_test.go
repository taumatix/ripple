package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
)

var advNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func advVuln(t *testing.T, id, mod, fixedEvent string) *advisory.Vuln {
	t.Helper()
	raw := `{"id":"` + id + `","summary":"s","affected":[{"package":{"name":"` + mod + `","ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"}` + fixedEvent + `]}]}]}`
	var v advisory.Vuln
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func advEngine(t *testing.T, cooldown time.Duration, repoCfg string, ver string) (*Engine, store.Store) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.UpsertRepo(ctx, model.Repo{FullName: "acme/app", Config: []byte(repoCfg)}); err != nil {
		t.Fatal(err)
	}
	dep := model.Dependency{Repo: "acme/app", Manager: "gomod", File: "go.mod", Datasource: "go", Package: "example.com/lib", Version: ver}
	if err := st.ReplaceDependencies(ctx, "acme/app", []model.Dependency{dep}); err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: st, Advisories: true, Now: func() time.Time { return advNow },
		Limits: rules.Limits{MinimumReleaseAge: cooldown, OpenPRLimit: 5}}, st
}

func TestFanOutAdvisoryDueNowDespiteCooldown(t *testing.T) {
	e, st := advEngine(t, 72*time.Hour, "", "v1.0.0")
	n, err := e.FanOutAdvisory(context.Background(), advVuln(t, "GO-1", "example.com/lib", `,{"fixed":"1.0.2"}`))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	j, ok, _ := st.GetJob(context.Background(), model.JobKey("acme/app", "go", "example.com/lib", "v1.0.2"))
	if !ok || j.State != model.JobPending || j.NotBefore.After(advNow) {
		t.Fatalf("job %+v ok=%v", j, ok)
	}
	if note := e.advisoryNoteFor(context.Background(), j); note == nil || note.ID != "GO-1" {
		t.Fatalf("note %+v", note)
	}
	// Replaying the record changes nothing.
	if n, _ := e.FanOutAdvisory(context.Background(), advVuln(t, "GO-1", "example.com/lib", `,{"fixed":"1.0.2"}`)); n != 0 {
		t.Fatalf("replay enqueued %d", n)
	}
}

func TestFanOutAdvisoryProposesNothingWhenThereIsNoUsableFix(t *testing.T) {
	for name, c := range map[string]struct{ ver, fix string }{
		"unfixed":        {"v1.0.0", ``},
		"pseudo fix":     {"v1.0.0", `,{"fixed":"1.0.1-0.20260911233437-76452a2c8a2f"}`},
		"already fixed":  {"v1.0.2", `,{"fixed":"1.0.2"}`},
		"only a ceiling": {"v1.0.0", `,{"last_affected":"1.0.5"}`},
	} {
		e, st := advEngine(t, 0, "", c.ver)
		if n, err := e.FanOutAdvisory(context.Background(), advVuln(t, "GO-1", "example.com/lib", c.fix)); err != nil || n != 0 {
			t.Errorf("%s: n=%d err=%v", name, n, err)
		}
		if due, _ := st.ListDueJobs(context.Background(), advNow.Add(time.Hour), 10); len(due) != 0 {
			t.Errorf("%s: jobs %v", name, due)
		}
	}
}

func TestFanOutAdvisoryHonoursIgnoreRulesAndWithdrawn(t *testing.T) {
	ign := "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: example.com/lib\n"
	e, _ := advEngine(t, 0, ign, "v1.0.0")
	if n, _ := e.FanOutAdvisory(context.Background(), advVuln(t, "GO-1", "example.com/lib", `,{"fixed":"1.0.2"}`)); n != 0 {
		t.Fatalf("ignored dependency got a security job")
	}
	e, _ = advEngine(t, 0, "", "v1.0.0")
	v := advVuln(t, "GO-1", "example.com/lib", `,{"fixed":"1.0.2"}`)
	v.WithdrawnAt = "2026-11-22T00:00:00Z"
	if n, _ := e.FanOutAdvisory(context.Background(), v); n != 0 {
		t.Fatalf("withdrawn advisory produced a job")
	}
}

type fakeSource struct {
	entries []advisory.Entry
	vulns   map[string]*advisory.Vuln
	errs    map[string]error
	fetched []string
}

func (f *fakeSource) Modified(ctx context.Context, after advisory.Position) ([]advisory.Entry, bool, error) {
	return f.entries, false, nil
}

func (f *fakeSource) Vuln(ctx context.Context, id string) (*advisory.Vuln, error) {
	f.fetched = append(f.fetched, id)
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	if v, ok := f.vulns[id]; ok {
		return v, nil
	}
	return nil, advisory.ErrNotFound
}

// A record that keeps failing is retried, then skipped, so it cannot hold the
// cursor (and every advisory behind it) forever.
func TestAdvisoryWatcherGivesUpOnAPoisonRecord(t *testing.T) {
	ctx := context.Background()
	e, st := advEngine(t, 0, "", "v1.0.0")
	e.Log = nil
	src := &fakeSource{
		entries: []advisory.Entry{{Time: advNow.Add(time.Minute), ID: "GO-BAD"}, {Time: advNow.Add(2 * time.Minute), ID: "GO-GOOD"}},
		vulns:   map[string]*advisory.Vuln{"GO-GOOD": advVuln(t, "GO-GOOD", "example.com/lib", `,{"fixed":"1.0.2"}`)},
		errs:    map[string]error{"GO-BAD": errors.New("HTTP 500")},
	}
	w := &AdvisoryWatcher{Engine: e, Source: src}
	if err := st.SetCursor(ctx, advisoryCursor, advisory.Position{Time: advNow}.String()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < advisoryGiveUpAfter; i++ {
		if _, err := w.Poll(ctx); err == nil || !strings.Contains(err.Error(), "GO-BAD") {
			t.Fatalf("poll %d: err=%v", i, err)
		}
	}
	n, err := w.Poll(ctx)
	if err != nil || n != 1 {
		t.Fatalf("after giving up: n=%d err=%v", n, err)
	}
	cur, _ := st.GetCursor(ctx, advisoryCursor)
	if !strings.HasSuffix(cur, ",GO-GOOD") {
		t.Fatalf("cursor %q", cur)
	}
}

func TestAdvisoryWatcherFirstRunStartsNowAndFetchesNothing(t *testing.T) {
	ctx := context.Background()
	e, st := advEngine(t, 0, "", "v1.0.0")
	src := &fakeSource{entries: []advisory.Entry{{Time: advNow.Add(-time.Hour), ID: "GO-OLD"}}}
	if n, err := (&AdvisoryWatcher{Engine: e, Source: src}).Poll(ctx); err != nil || n != 0 || len(src.fetched) != 0 {
		t.Fatalf("n=%d err=%v fetched=%v", n, err, src.fetched)
	}
	if cur, _ := st.GetCursor(ctx, advisoryCursor); cur == "" {
		t.Fatal("no cursor stored")
	}
}
