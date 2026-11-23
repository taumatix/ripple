package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const repo = "acme/app"

// supersedeEnv registers one repo importing example.com/lib v1.0.0 and releases
// v1.1.0, so the first PR is open when a test starts.
func supersedeEnv(t *testing.T, st store.Store, cfg string) *env {
	t.Helper()
	e := newEnv(t, st, defaultLimits())
	stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
	files := map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")}
	if cfg != "" {
		files[".ripple.yaml"] = cfg
	}
	e.gh.AddRepo(repo, files)
	start(t, e, repo)
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	if n := len(e.gh.PRs(repo)); n != 1 {
		t.Fatalf("release v1.1.0: want 1 PR, got %d", n)
	}
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.2.0")
	return e
}

func prByTitle(t *testing.T, prs []testutil.PR, version string) testutil.PR {
	t.Helper()
	for _, p := range prs {
		if strings.HasSuffix(p.Title, " "+version) {
			return p
		}
	}
	t.Fatalf("no PR for %s in %+v", version, prs)
	return testutil.PR{}
}

func TestNewerReleaseClosesTheStalePRWithAComment(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 {
			t.Fatalf("want 2 PRs, got %d", len(prs))
		}
		old, cur := prByTitle(t, prs, "v1.1.0"), prByTitle(t, prs, "v1.2.0")
		if !old.Closed || cur.Closed {
			t.Fatalf("old closed=%v, new closed=%v; want old closed and new open", old.Closed, cur.Closed)
		}
		if len(old.Comments) != 1 || !strings.Contains(old.Comments[0], "/pull/2") || !strings.Contains(old.Comments[0], "v1.2.0") {
			t.Errorf("comment on the old PR must point at the new one: %q", old.Comments)
		}
		if len(cur.Comments) != 0 {
			t.Errorf("the new PR got comments: %q", cur.Comments)
		}
		if got := e.gh.Branches(repo); len(got) != 2 {
			t.Errorf("branches %v: the stale branch must be left in place", got)
		}
	})
}

func TestStalePRsATeamMemberTouchedAreLeftAlone(t *testing.T) {
	touches := map[string]func(e *env, pr testutil.PR){
		"comment": func(e *env, pr testutil.PR) { e.gh.HumanComment(repo, pr.Number, "hold off, we are testing this") },
		"review":  func(e *env, pr testutil.PR) { e.gh.HumanReview(repo, pr.Number) },
		"rename":  func(e *env, pr testutil.PR) { e.gh.HumanRename(repo, pr.Number, "My own title") },
		"commit":  func(e *env, pr testutil.PR) { e.gh.HumanCommitOnBranch(repo, pr.Head) },
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := supersedeEnv(t, st, "")
				touch(e, e.gh.PRs(repo)[0])
				e.tick()
				prs := e.gh.PRs(repo)
				if len(prs) != 2 {
					t.Fatalf("want 2 PRs, got %d", len(prs))
				}
				if prs[0].Closed {
					t.Errorf("a PR a human touched (%s) was closed", name)
				}
				for _, c := range prs[0].Comments {
					if strings.Contains(c, "pull/2") {
						t.Errorf("ripple commented on a PR it left alone: %q", c)
					}
				}
			})
		})
	}
}

func TestNothingIsClosedWhenTheTouchCheckFailsPartway(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.gh.FailPath = "/reviews"
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 || prs[0].Closed || len(prs[0].Comments) != 0 {
			t.Fatalf("a failing check must change nothing: %+v", prs)
		}
		if j := e.job(repo, "example.com/lib", "v1.2.0"); j.PRURL == "" {
			t.Errorf("the new PR's job must still be done: %+v", j)
		}
	})
}

func TestSupersedingCanBeSwitchedOff(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "close-superseded-pull-requests: false\n")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 || prs[0].Closed || len(prs[0].Comments) != 0 {
			t.Fatalf("opt-out ignored: %+v", prs)
		}
	})
}

func TestOnlyAnOlderVersionOfTheSameModuleIsSuperseded(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.2.0", "v1.3.0")
		stubModule(e, "example.com/lib-extra", "v1.0.0", "v1.5.0")
		e.gh.AddRepo(repo, map[string]string{
			"go.mod":  gomod("example.com/app", "example.com/lib v1.0.0", "example.com/lib-extra v1.0.0"),
			"main.go": blankImports("example.com/lib", "example.com/lib-extra")})
		start(t, e, repo)
		// A newer lib PR exists first, then an older release of a different module and
		// an older lib release arrive: neither may close the v1.3.0 PR.
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.3.0")
		e.publish("example.com/lib-extra", "v1.5.0")
		e.tick()
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.2.0")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 3 {
			t.Fatalf("want a PR each for lib v1.3.0, lib-extra v1.5.0 and lib v1.2.0, got %d", len(prs))
		}
		for _, p := range prs {
			if p.Closed {
				t.Errorf("PR %q was closed", p.Title)
			}
		}
	})
}
