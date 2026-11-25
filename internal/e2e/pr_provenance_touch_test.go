package e2e

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// Entry 94, second half: a stale PR whose description or head differs from what ripple
// recorded when it opened it was touched by a person, even where the host's own edit and
// event feeds say nothing (the fake leaves no trace of these two edits but the new value).
func TestStalePRWhoseBodyOrHeadChangedSinceItOpenedIsLeftAlone(t *testing.T) {
	changes := map[string]func(e *env){
		"description": func(e *env) { e.gh.HumanEditBodyQuietly(repo, e.gh.PRs(repo)[0].Number, "rewritten by a person") },
		"head":        func(e *env) { e.gh.HumanResetBranch(repo, e.gh.PRs(repo)[0].Head, 1) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := supersedeEnv(t, st, "")
				change(e)
				e.tick()
				prs := e.gh.PRs(repo)
				if len(prs) != 2 || prs[0].Closed || len(prs[0].Comments) != 0 {
					t.Fatalf("a PR whose %s changed was closed or commented: %+v", name, prs)
				}
			})
		})
	}
}

// A job from before the values were recorded has none to compare: that reads as "not
// recorded", so the stale PR is superseded as before, never treated as edited.
func TestJobsWithoutRecordedValuesAreNotTreatedAsEdited(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		j := e.job(repo, "example.com/lib", "v1.1.0")
		j.HeadSHA, j.BodyHash = "", ""
		if err := e.st.UpdateJob(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		e.tick()
		prs := e.gh.PRs(repo)
		if len(prs) != 2 || !prs[0].Closed {
			t.Fatalf("a job with nothing recorded must read as unedited and be superseded: %+v", prs)
		}
	})
}

// Unchanged PRs are still superseded (the normal path is not disturbed by the comparison).
func TestStalePRUnchangedSinceItOpenedIsStillSuperseded(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := supersedeEnv(t, st, "")
		e.tick()
		if prs := e.gh.PRs(repo); len(prs) != 2 || !prs[0].Closed {
			t.Fatalf("%+v", prs)
		}
	})
}
