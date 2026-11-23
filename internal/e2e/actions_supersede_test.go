package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// actionsOpenCount counts the open PRs of acme/app.
func actionsOpenCount(e *env) int {
	n := 0
	for _, pr := range e.gh.PRs("acme/app") {
		if !pr.Closed {
			n++
		}
	}
	return n
}

func TestActionsNewerTagClosesTheStalePR(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5")
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("v5: want 1 PR, got %d", n)
		}
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v6")
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want 2 PRs, got %+v", prs)
		}
		old, cur := prByTitle(t, prs, "v5"), prByTitle(t, prs, "v6")
		if !old.Closed || cur.Closed {
			t.Fatalf("old closed=%v, new closed=%v; want old closed and new open", old.Closed, cur.Closed)
		}
		if len(old.Comments) != 1 || !strings.Contains(old.Comments[0], "/pull/") || !strings.Contains(old.Comments[0], "v6") {
			t.Errorf("comment on the old PR must point at the new one: %q", old.Comments)
		}
	})
}

func TestActionsStalePRIsLeftWhenSupersedeIsOff(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig + "close-superseded-pull-requests: false\n", ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5")
		e.tick()
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v6")
		e.tick()
		e.tick()
		if n := actionsOpenCount(e); n != 2 {
			t.Errorf("with close-superseded-pull-requests: false both PRs stay open, got %d open", n)
		}
	})
}

// A PR for one tag shape never closes the PR of another: v4 -> v5 and v4.1.2 -> v5.1.0
// are two dependencies, each with its own newest tag.
func TestActionsSupersedeKeepsTagShapesApart(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4", "v4.1.2")
		// Two files: the stores keep one dependency per package and file.
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig,
			".github/workflows/a.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n",
			".github/workflows/b.yml": "jobs:\n  b:\n    steps:\n      - uses: up/action@v4.1.2\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5", "v5.0.0")
		e.tick()
		e.tick()
		e.eventually("a PR per shape", func() bool { e.tick(); return len(e.gh.PRs("acme/app")) == 2 })
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5.1.0")
		e.tick()
		e.tick()
		e.eventually("the v5.0.0 PR superseded", func() bool { e.tick(); return len(e.gh.PRs("acme/app")) == 3 && actionsOpenCount(e) == 2 })
		if n := actionsOpenCount(e); n != 2 {
			t.Errorf("the v5 PR and the v5.1.0 PR are both wanted, got %d open: %+v", n, e.gh.PRs("acme/app"))
		}
		// The v5.0.0 PR is the stale one of its own shape.
		var closed []string
		for _, pr := range e.gh.PRs("acme/app") {
			if pr.Closed {
				closed = append(closed, pr.Title)
			}
		}
		if len(closed) != 1 || !strings.HasSuffix(closed[0], "v5.0.0") {
			t.Errorf("only the v5.0.0 PR is superseded, closed: %v", closed)
		}
	})
}

func TestActionsWithoutATagOfTheirShapeAreListedOnTheDashboard(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig + optIn, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5.0.0") // no v5: the floating v4 has nowhere to go
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("a PR was opened: %+v", e.gh.PRs("acme/app"))
		}
		is := e.gh.Issues("acme/app")
		if len(is) != 1 || !strings.Contains(is[0].Body, "## Actions left alone (1)") || !strings.Contains(is[0].Body, "`up/action` `v4`: newest tag `v5.0.0`") {
			t.Fatalf("the dashboard must say why v4 is not moved: %+v", is)
		}
		// Upstream publishes the shape: the entry goes away and the PR opens.
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5")
		e.tick()
		e.tick()
		onePR(t, e, "acme/app", false)
		for _, i := range e.gh.Issues("acme/app") {
			if strings.Contains(i.Body, "Actions left alone") {
				t.Errorf("resolved entry still listed:\n%s", i.Body)
			}
		}
	})
}

func TestActionsLeftAloneAreNotReportedWithoutADashboard(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5.0.0")
		e.tick()
		e.tick()
		if is := e.gh.Issues("acme/app"); len(is) != 0 {
			t.Errorf("issues without opting in: %+v", is)
		}
	})
}

// An action no repo uses any more leaves the polled set, so the watcher stops reading its tags.
func TestActionsUnusedPackageIsPruned(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4")
		actionsUpstream(e, "up/other", "v1")
		ci := "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: up/action@v4\n      - uses: up/other@v1\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": ci})
		start(t, e, "acme/app")
		cursor := func() string {
			c, err := e.st.GetCursor(context.Background(), "github-tags:packages")
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
		if got := cursor(); got != "up/action\nup/other" {
			t.Fatalf("polled set after the scan = %q", got)
		}
		e.push("acme/app", map[string]string{".github/workflows/ci.yml": "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: up/action@v4\n"})
		e.waitQueueIdle()
		e.tick()
		if got := cursor(); got != "up/action" {
			t.Errorf("polled set after up/other was dropped = %q, want only up/action", got)
		}
	})
}
