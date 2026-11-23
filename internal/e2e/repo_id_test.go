package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// The repository id on a signed push is stored on the repo record (roadmap entry 73),
// ties a push under a new name to the record held under the old one, and, with the
// tombstone a removal leaves, stops a late push from bringing a removed repo back (75).

func (e *env) storedRepoID(repo string) int64 {
	e.t.Helper()
	r, ok, err := e.st.GetRepo(context.Background(), repo)
	if err != nil || !ok {
		e.t.Fatalf("repo %s: found=%v err=%v", repo, ok, err)
	}
	return r.RepoID
}

func (e *env) removeRepos(names ...string) {
	e.t.Helper()
	if code := e.deliver("installation_repositories", map[string]any{"action": "removed", "installation": map[string]any{"id": 7},
		"repositories_removed": repoList(names...)}, ""); code != http.StatusAccepted {
		e.t.Fatalf("installation_repositories webhook: %d", code)
	}
	for _, n := range names {
		e.repoGone(n)
	}
}

func TestPushStoresRepoIDOnce(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		if id := e.storedRepoID("acme/uses-lib"); id != 0 {
			t.Fatalf("id before any push: %d", id)
		}
		e.pushPayload("acme/uses-lib", "refs/heads/feature", "main", 11, []string{"README.md"}, "")
		e.eventually("id adopted", func() bool { return e.storedRepoID("acme/uses-lib") == 11 })

		// A push under the same name with another id is a different repository (or a replay
		// from before the name was reused): it changes nothing and costs no platform call.
		calls := e.scans("acme/uses-lib")
		e.pushPayload("acme/uses-lib", "refs/heads/main", "main", 12, []string{"go.mod"}, "")
		e.eventually("mismatch logged", func() bool { return e.logLines("repository id mismatch", "acme/uses-lib") == 1 })
		if id := e.storedRepoID("acme/uses-lib"); id != 11 || e.scans("acme/uses-lib") != calls {
			t.Fatalf("id %d, %d new platform calls after a push with another id", id, e.scans("acme/uses-lib")-calls)
		}
	})
}

// The rename event never arrives: the first signed push under the new name is tied to the
// old record by id, so its jobs and index move instead of a second record being registered.
func TestPushUnderNewNameFollowsRepoID(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnv(t, st, lim)
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.pushPayload("acme/uses-lib", "refs/heads/feature", "main", 11, []string{"README.md"}, "")
		e.eventually("id adopted", func() bool { return e.storedRepoID("acme/uses-lib") == 11 })
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()

		e.gh.RenameRepo("acme/uses-lib", "acme/renamed")
		e.pushPayload("acme/renamed", "refs/heads/feature", "main", 11, []string{"README.md"}, "")
		e.waitRepo("acme/renamed")
		e.repoGone("acme/uses-lib")
		if d := e.deps("acme/renamed"); len(d) != 1 || d[0].Repo != "acme/renamed" {
			t.Fatalf("index after the id-tied rename: %+v", d)
		}
		if id := e.storedRepoID("acme/renamed"); id != 11 {
			t.Fatalf("id after rename %d", id)
		}
		e.clock.Advance(73 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/renamed")); n != 1 {
			t.Fatalf("want the pending job to open 1 PR under the new name, got %d", n)
		}
	})
}

// A delivery that arrives after repos_removed must not register the repo again, but a
// new repository that takes the name (another id), or an explicit re-add, still can.
func TestLatePushAfterRemovalDoesNotReRegister(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.pushPayload("acme/uses-lib", "refs/heads/feature", "main", 11, []string{"README.md"}, "")
		e.eventually("id adopted", func() bool { return e.storedRepoID("acme/uses-lib") == 11 })
		e.removeRepos("acme/uses-lib")

		calls := e.gh.TotalCalls()
		e.pushPayload("acme/uses-lib", "refs/heads/main", "main", 11, []string{"go.mod"}, "")
		e.eventually("late push refused", func() bool { return e.logLines("push after removal ignored", "acme/uses-lib") == 1 })
		if _, ok, _ := e.st.GetRepo(context.Background(), "acme/uses-lib"); ok || e.gh.TotalCalls() != calls {
			t.Fatalf("a late push re-registered the removed repo (registered=%v)", ok)
		}

		// Another repository under the same name has another id: it is not the removed one.
		e.pushPayload("acme/uses-lib", "refs/heads/main", "main", 99, []string{"go.mod"}, "")
		e.waitRepo("acme/uses-lib")
		if id := e.storedRepoID("acme/uses-lib"); id != 99 {
			t.Fatalf("id of the new repository %d, want 99", id)
		}

		// An explicit re-add clears the tombstone for the removed one as well.
		e.removeRepos("acme/uses-lib")
		e.install("acme/uses-lib")
		e.pushPayload("acme/uses-lib", "refs/heads/feature", "main", 99, []string{"README.md"}, "")
		time.Sleep(300 * time.Millisecond)
		if e.logLines("push after removal ignored", "acme/uses-lib") != 1 {
			t.Fatalf("a push after an explicit re-add was treated as late")
		}
	})
}
