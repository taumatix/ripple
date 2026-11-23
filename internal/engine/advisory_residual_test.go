package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/model"
)

const pseudoFix = `,{"fixed":"1.0.1-0.20260911233437-76452a2c8a2f"}`

func TestFanOutAdvisoryPseudoFixOnlyWhereTheEntryOptedIn(t *testing.T) {
	ctx := context.Background()
	e, st := advEngine(t, 0, "updates:\n  - ecosystem: gomod\n    security-pseudo-versions: true\n", "v1.0.0")
	n, err := e.FanOutAdvisory(ctx, advVuln(t, "GO-1", "example.com/lib", pseudoFix))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	key := model.JobKey("acme/app", "go", "example.com/lib", "v1.0.1-0.20260911233437-76452a2c8a2f")
	j, ok, _ := st.GetJob(ctx, key)
	if !ok {
		t.Fatal("no job for the pseudo-version")
	}
	if note := e.advisoryNoteFor(ctx, j); note == nil || !note.Pseudo {
		t.Fatalf("note %+v", note)
	}
	// A dependency that is itself a pseudo-version is never moved by this path.
	e, _ = advEngine(t, 0, "updates:\n  - ecosystem: gomod\n    security-pseudo-versions: true\n", "v1.0.1-0.20260101000000-aaaaaaaaaaaa")
	if n, _ := e.FanOutAdvisory(ctx, advVuln(t, "GO-1", "example.com/lib", pseudoFix)); n != 0 {
		t.Fatalf("a pseudo-version dependency got a job")
	}
}

func TestFanOutAdvisoryPrefersTheReleaseOverAPseudoVersion(t *testing.T) {
	ctx := context.Background()
	e, st := advEngine(t, 0, "", "v1.0.0")
	n, err := e.FanOutAdvisory(ctx, advVuln(t, "GO-1", "example.com/lib", pseudoFix+`,{"fixed":"1.0.5"}`))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, ok, _ := st.GetJob(ctx, model.JobKey("acme/app", "go", "example.com/lib", "v1.0.5")); !ok {
		t.Fatal("the release fix was not chosen")
	}
}

func TestOpenAdvisoriesAreListedOnTheDashboardAndClearedByAFix(t *testing.T) {
	ctx := context.Background()
	e, _ := advEngine(t, 0, "dependency-dashboard: true\n", "v1.0.0")
	if n, err := e.FanOutAdvisory(ctx, advVuln(t, "GO-1", "example.com/lib", ``)); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	list, err := e.currentOpenAdvisories(ctx, "acme/app")
	if err != nil || len(list) != 1 || list[0].ID != "GO-1" || !strings.Contains(list[0].Why, "no release") {
		t.Fatalf("list %+v err=%v", list, err)
	}
	body := renderDashboardWith(nil, list, advNow)
	if !strings.Contains(body, "https://osv.dev/vulnerability/GO-1") || strings.Contains(body, "Nothing is pending") {
		t.Fatalf("body:\n%s", body)
	}
	// Pseudo-only without opt-in says so.
	e, _ = advEngine(t, 0, "dependency-dashboard: true\n", "v1.0.0")
	_, _ = e.FanOutAdvisory(ctx, advVuln(t, "GO-2", "example.com/lib", pseudoFix))
	if list, _ := e.currentOpenAdvisories(ctx, "acme/app"); len(list) != 1 || !strings.Contains(list[0].Why, "security-pseudo-versions") {
		t.Fatalf("pseudo list %+v", list)
	}
	// The record gains a release: the entry goes and a job appears.
	if n, _ := e.FanOutAdvisory(ctx, advVuln(t, "GO-2", "example.com/lib", pseudoFix+`,{"fixed":"1.0.5"}`)); n != 1 {
		t.Fatalf("fix enqueued %d", n)
	}
	if list, _ := e.currentOpenAdvisories(ctx, "acme/app"); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
	// Withdrawal clears too.
	e, _ = advEngine(t, 0, "dependency-dashboard: true\n", "v1.0.0")
	_, _ = e.FanOutAdvisory(ctx, advVuln(t, "GO-3", "example.com/lib", ``))
	w := advVuln(t, "GO-3", "example.com/lib", ``)
	w.WithdrawnAt = "2026-11-22T00:00:00Z"
	_, _ = e.FanOutAdvisory(ctx, w)
	if list, _ := e.currentOpenAdvisories(ctx, "acme/app"); len(list) != 0 {
		t.Fatalf("withdrawn still listed: %+v", list)
	}
	// A repo without the dashboard keeps nothing.
	e, _ = advEngine(t, 0, "", "v1.0.0")
	_, _ = e.FanOutAdvisory(ctx, advVuln(t, "GO-4", "example.com/lib", ``))
	if raw, _ := e.Store.GetCursor(ctx, advisoryOpenPrefix+"acme/app"); raw != "" {
		t.Fatalf("kept %q without a dashboard", raw)
	}
}

func TestOpenAdvisoryGoesStaleWhenTheDependencyMoves(t *testing.T) {
	ctx := context.Background()
	e, st := advEngine(t, 0, "dependency-dashboard: true\n", "v1.0.0")
	_, _ = e.FanOutAdvisory(ctx, advVuln(t, "GO-1", "example.com/lib", ``))
	dep := model.Dependency{Repo: "acme/app", Manager: "gomod", File: "go.mod", Datasource: "go", Package: "example.com/lib", Version: "v1.0.9"}
	_ = st.ReplaceDependencies(ctx, "acme/app", []model.Dependency{dep})
	if list, _ := e.currentOpenAdvisories(ctx, "acme/app"); len(list) != 0 {
		t.Fatalf("stale entry kept: %+v", list)
	}
}

func toolchainEngine(t *testing.T, cfg, ver string) (*Engine, *advisory.Vuln) {
	t.Helper()
	e, st := advEngine(t, 72*time.Hour, cfg, "v1.0.0")
	tc := model.Dependency{Repo: "acme/app", Manager: "gomod", File: "go.mod", Datasource: model.DatasourceGoToolchain, Package: model.PackageGoToolchain, Version: ver}
	if err := st.ReplaceDependencies(context.Background(), "acme/app", []model.Dependency{tc}); err != nil {
		t.Fatal(err)
	}
	var v advisory.Vuln
	raw := `{"id":"GO-9","summary":"s","affected":[{"package":{"name":"stdlib","ecosystem":"Go"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.25.4"}]}]}]}`
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return e, &v
}

func TestFanOutAdvisoryMovesAToolchainLineToTheFixedPatch(t *testing.T) {
	ctx := context.Background()
	e, v := toolchainEngine(t, "updates:\n  - ecosystem: gomod\n    go-toolchain: toolchain\n", "v1.25.3")
	n, err := e.FanOutAdvisory(ctx, v)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	key := model.JobKey("acme/app", model.DatasourceGoToolchain, model.PackageGoToolchain, "v1.25.4")
	j, ok, _ := e.Store.GetJob(ctx, key)
	if !ok || j.NotBefore.After(advNow) {
		t.Fatalf("job %+v ok=%v (the cooldown must not apply)", j, ok)
	}
	if note := e.advisoryNoteFor(ctx, j); note == nil || note.ID != "GO-9" {
		t.Fatalf("note %+v", note)
	}
	// Already on the fix: nothing.
	e, v = toolchainEngine(t, "updates:\n  - ecosystem: gomod\n    go-toolchain: toolchain\n", "v1.25.4")
	if n, _ := e.FanOutAdvisory(ctx, v); n != 0 {
		t.Fatalf("fixed toolchain got a job")
	}
	// No opt-in, no toolchain dependency in the index: nothing, as before.
	e, st := advEngine(t, 0, "", "v1.0.0")
	_ = st.ReplaceDependencies(ctx, "acme/app", nil)
	if n, err := e.FanOutAdvisory(ctx, v); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
