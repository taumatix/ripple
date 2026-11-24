package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
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

// A repo whose limit is one open PR: the stale PR holds the slot the newer PR needs.
func TestAStalePRDoesNotHoldTheSlotItsReplacementNeeds(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 {
			t.Fatalf("want the new PR opened at the limit, got %d PRs", len(prs))
		}
		old, cur := prByTitle(t, prs, "v1.1.0"), prByTitle(t, prs, "v1.2.0")
		if !old.Closed || cur.Closed {
			t.Fatalf("old closed=%v, new closed=%v; want old closed and new open", old.Closed, cur.Closed)
		}
		if len(old.Comments) != 1 || !strings.Contains(old.Comments[0], "v1.2.0") {
			t.Errorf("the stale PR needs one comment naming the new version: %q", old.Comments)
		}
	})
}

func TestAtTheLimitATouchedStalePRStillHoldsItsSlot(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n")
		e.gh.HumanComment(repo, e.gh.PRs(repo)[0].Number, "we are testing this one")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 1 || prs[0].Closed {
			t.Fatalf("a PR a person touched must stay open and keep the slot: %+v", prs)
		}
		if j := e.job(repo, "example.com/lib", "v1.2.0"); j.State != model.JobPending {
			t.Errorf("the new release must be deferred, not lost: %+v", j)
		}
	})
}

func TestAtTheLimitOptingOutKeepsTheStalePRAndDefers(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "close-superseded-pull-requests: false\nopen-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 1 || prs[0].Closed || len(prs[0].Comments) != 0 {
			t.Fatalf("opt-out ignored at the limit: %+v", prs)
		}
	})
}

// A close that failed is retried the next time ripple works on the repo, however
// old the newer PR is and whatever that run is for.
func TestAFailedCloseIsRetriedOnTheNextRunOfTheRepo(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
		stubModule(e, "example.com/other", "v1.0.0", "v1.1.0")
		e.gh.AddRepo(repo, map[string]string{
			"go.mod":  gomod("example.com/app", "example.com/lib v1.0.0", "example.com/other v1.0.0"),
			"main.go": blankImports("example.com/lib", "example.com/other")})
		start(t, e, repo)
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		e.gh.FailClose = true
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.2.0")
		e.tick()
		if prs := e.gh.PRs(repo); len(prs) != 2 || prByTitle(t, prs, "v1.1.0").Closed {
			t.Fatalf("the close was meant to fail: %+v", prs)
		}
		e.gh.FailClose = false
		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 3 {
			t.Fatalf("want 3 PRs, got %d", len(prs))
		}
		old := prByTitle(t, prs, "v1.1.0")
		if len(prs) == 3 && !prByTitle(t, prs, "v1.1.0").Closed || len(old.Comments) != 1 {
			t.Errorf("the next run of the repo must close the stale PR once: %+v", old)
		}
		if prByTitle(t, prs, "v1.2.0").Closed {
			t.Error("the newest PR was closed")
		}
	})
}

func TestACloseIsRetriedForAPRThatWasAlreadyOpenForTheNewVersion(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.gh.FailClose = true
		e.tick()
		e.gh.FailClose = false
		// The v1.2.0 job is done; a re-run of it finds its PR already open.
		e.rerun(repo, "example.com/lib", "v1.2.0")
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 || !prByTitle(t, prs, "v1.1.0").Closed {
			t.Fatalf("the already-open PR must still supersede the old one: %+v", prs)
		}
	})
}

// What a person can do to a PR that the commit, review and comment reads miss.
func TestStalePRsEditedOrForcePushedAreLeftAlone(t *testing.T) {
	touches := map[string]func(e *env, pr testutil.PR){
		"description edit": func(e *env, pr testutil.PR) { e.gh.HumanEditBody(repo, pr.Number) },
		"force push":       func(e *env, pr testutil.PR) { e.gh.HumanForcePush(repo, pr.Number) },
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := supersedeEnv(t, st, "")
				touch(e, e.gh.PRs(repo)[0])
				e.tick()
				prs := e.gh.PRs(repo)
				if len(prs) != 2 || prs[0].Closed || len(prs[0].Comments) != 0 {
					t.Fatalf("a PR a person touched (%s) was closed or commented: %+v", name, prs)
				}
			})
		})
	}
}

// Ownership comes from the job ripple recorded for the branch, not from a title or
// a branch name alone: a branch shaped like ripple's that no job opened is not ripple's.
func TestAPRNoRippleJobOpenedIsNeverClosed(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.gh.AddForeignPR(repo, "ripple/go/example.com-lib-v1.0.5", "Bump example.com/lib to v1.0.5")
		e.tick()
		for _, p := range e.gh.PRs(repo) {
			if p.Head == "ripple/go/example.com-lib-v1.0.5" && (p.Closed || len(p.Comments) != 0) {
				t.Errorf("closed a PR no ripple job opened: %+v", p)
			}
		}
	})
}

// A stale PR a person touched is read once: the next runs of the repo remember it.
func TestATouchedStalePRIsNotReReadOnEveryRun(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.gh.HumanComment(repo, e.gh.PRs(repo)[0].Number, "we are testing this one")
		e.tick() // opens v1.2.0 and finds v1.1.0 touched
		// Any later read of the PR's reviews would fail now, and a failed check is logged.
		e.gh.FailPath = "/reviews"
		e.rerun(repo, "example.com/lib", "v1.2.0")
		e.tick()
		if n := e.logLines("stale pull request not closed"); n != 0 {
			t.Errorf("the touched PR was checked again: %d failed checks logged", n)
		}
		if prs := e.gh.PRs(repo); len(prs) != 2 || prs[0].Closed {
			t.Errorf("the touched PR must stay open: %+v", prs)
		}
	})
}

// The memory ends with the PR: when the person closes it, nothing stale is kept, and
// a later PR on a reused branch is read afresh.
func TestTheTouchedMemoryIsDroppedWhenThePRCloses(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.gh.HumanComment(repo, e.gh.PRs(repo)[0].Number, "we are testing this one")
		e.tick()
		e.gh.HumanClosePR(repo, e.gh.PRs(repo)[0].Number)
		e.rerun(repo, "example.com/lib", "v1.2.0")
		e.tick()
		if raw, err := e.st.GetCursor(context.Background(), "supersede-touched:"+repo); err != nil || raw != "" && raw != "[]" && raw != "{}" {
			t.Errorf("memory %q (err %v) must be empty once the PR is closed", raw, err)
		}
	})
}

// A person's commit makes the stale PR touched; when they take it back (the head moves to
// a commit only ripple wrote) the host is asked again and the PR is superseded.
func TestATouchedStalePRIsJudgedAgainWhenItsHeadMoves(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		old := e.gh.PRs(repo)[0]
		e.gh.HumanCommitOnBranch(repo, old.Head)
		e.tick() // opens v1.2.0 and finds v1.1.0 touched
		if prs := e.gh.PRs(repo); len(prs) != 2 || prs[0].Closed {
			t.Fatalf("the touched PR must stay open: %+v", prs)
		}
		e.rerun(repo, "example.com/lib", "v1.2.0")
		e.tick() // head unchanged: remembered, not read again
		if prs := e.gh.PRs(repo); prs[0].Closed {
			t.Fatalf("closed while still touched: %+v", prs)
		}
		e.gh.HumanResetBranch(repo, old.Head, 1)
		e.rerun(repo, "example.com/lib", "v1.2.0")
		e.tick()
		if prs := e.gh.PRs(repo); !prs[0].Closed {
			t.Errorf("the head moved back to ripple's own commit but the stale PR stayed open: %+v", prs)
		}
	})
}
