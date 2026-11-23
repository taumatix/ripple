package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// Pushes carry the repository's identity and default branch (docs/vendor/github, push).
// These drive them through the real receiver, queue and engine against the fake GitHub.

// pushPayload delivers a push with the given ref, claimed default branch, repository id
// and signature ("" signs it properly) and returns the HTTP status.
func (e *env) pushPayload(repo, ref, defaultBranch string, id int64, files []string, sig string) int {
	e.t.Helper()
	return e.deliver("push", map[string]any{
		"ref": ref, "installation": map[string]any{"id": 7},
		"repository": map[string]any{"id": id, "full_name": repo, "default_branch": defaultBranch},
		"commits":    []map[string]any{{"modified": files}},
	}, sig)
}

func (e *env) storedBranch(repo string) string {
	e.t.Helper()
	r, ok, err := e.st.GetRepo(context.Background(), repo)
	if err != nil || !ok {
		e.t.Fatalf("repo %s: found=%v err=%v", repo, ok, err)
	}
	return r.DefaultBranch
}

// moveToTrunk renames acme/app's default branch to trunk and gives trunk a second dependency.
func moveToTrunk(e *env) {
	e.gh.SetDefaultBranch("acme/app", "trunk")
	e.gh.CommitOn("acme/app", "trunk", map[string]string{
		"go.mod":  gomod("example.com/app", "example.com/lib v1.0.5", "example.com/new v0.1.0"),
		"main.go": blankImports("example.com/lib", "example.com/new")})
}

func appOnMain(t *testing.T, e *env) {
	stubModule(e, "example.com/lib", "v1.0.0", "v1.0.5", "v1.1.0")
	stubModule(e, "example.com/new", "v0.1.0")
	e.gh.AddRepo("acme/app", map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
	start(t, e, "acme/app")
}

// A missed `repository.edited` delivery is repaired by the next push.
func TestPushHealsDefaultBranch(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		for name, ref := range map[string]string{"push to the new default": "refs/heads/trunk", "push to another branch": "refs/heads/feature"} {
			t.Run(name, func(t *testing.T) {
				e := newEnv(t, st, defaultLimits())
				appOnMain(t, e)
				moveToTrunk(e)
				if code := e.pushPayload("acme/app", ref, "trunk", 11, []string{"README.md"}, ""); code != http.StatusAccepted {
					t.Fatalf("push: %d", code)
				}
				e.eventually("rescan against trunk", func() bool { return len(e.deps("acme/app")) == 2 })
				if b := e.storedBranch("acme/app"); b != "trunk" {
					t.Fatalf("stored default branch %q, want trunk", b)
				}
				// Healed once: the same claim again costs nothing.
				before := e.scans("acme/app")
				e.pushPayload("acme/app", ref, "trunk", 11, []string{"README.md"}, "")
				time.Sleep(300 * time.Millisecond)
				if n := e.scans("acme/app") - before; n != 0 {
					t.Errorf("an agreeing push cost %d platform calls", n)
				}
			})
		}
	})
}

// The payload only says that the stored branch may be wrong. The value stored is what
// the host reports, so a stale or false claim cannot point ripple at another branch.
func TestPushWithFalseDefaultBranchStoresWhatHostReports(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	appOnMain(t, e)
	e.pushPayload("acme/app", "refs/heads/feature", "gh-pages", 11, []string{"README.md"}, "")
	e.eventually("claim examined", func() bool { return e.scans("acme/app") > 0 && e.logLines("scanned", "acme/app") >= 2 })
	if b := e.storedBranch("acme/app"); b != "main" {
		t.Fatalf("stored default branch %q after a false claim, want main", b)
	}
	// The same false claim is not re-examined on every push.
	before := e.scans("acme/app")
	e.pushPayload("acme/app", "refs/heads/feature", "gh-pages", 11, []string{"README.md"}, "")
	time.Sleep(300 * time.Millisecond)
	if n := e.scans("acme/app") - before; n != 0 {
		t.Errorf("a repeated false claim cost %d platform calls", n)
	}
}

// An unsigned or wrongly signed push changes nothing.
func TestPushWithBadSignatureDoesNotHeal(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	appOnMain(t, e)
	moveToTrunk(e)
	if code := e.pushPayload("acme/app", "refs/heads/trunk", "trunk", 11, []string{"go.mod"}, "sha256=00"); code == http.StatusAccepted {
		t.Fatalf("a badly signed push was accepted")
	}
	time.Sleep(300 * time.Millisecond)
	if b := e.storedBranch("acme/app"); b != "main" || len(e.deps("acme/app")) != 1 {
		t.Fatalf("a forged push changed the repo: branch %q, %d deps", b, len(e.deps("acme/app")))
	}
}

// A push under the new name that beats the rename event registers the repo; the rename
// event arriving afterwards retires the old record without losing the new one.
func TestPushForRenamedRepoBeatsRenameEvent(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnv(t, st, lim)
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick() // a pending job waits out the cooldown under the old name

		e.gh.RenameRepo("acme/uses-lib", "acme/renamed")
		if code := e.pushPayload("acme/renamed", "refs/heads/main", "main", 11, []string{"go.mod"}, ""); code != http.StatusAccepted {
			t.Fatalf("push: %d", code)
		}
		e.waitRepo("acme/renamed")
		e.eventually("dependencies indexed under the new name", func() bool { return len(e.deps("acme/renamed")) == 1 })

		// Redelivery of the same push, and a push under the old name's alias, add nothing.
		scans := e.scans("acme/renamed")
		e.pushPayload("acme/renamed", "refs/heads/main", "main", 11, []string{"README.md"}, "")
		time.Sleep(300 * time.Millisecond)
		if n := e.scans("acme/renamed") - scans; n != 0 {
			t.Errorf("an irrelevant push to the reconciled repo cost %d platform calls", n)
		}

		// The late rename event.
		e.repoEvent("renamed", "acme/renamed", "main", map[string]any{"repository": map[string]any{"name": map[string]any{"from": "uses-lib"}}})
		e.repoGone("acme/uses-lib")
		if d := e.deps("acme/renamed"); len(d) != 1 || d[0].Repo != "acme/renamed" {
			t.Fatalf("new name lost its dependencies after the rename event: %+v", d)
		}
		e.clock.Advance(73 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/renamed")); n != 1 {
			t.Fatalf("want 1 PR under the new name, got %d", n)
		}
	})
}

// Without an immutable id from the host, a push for an unknown repo registers nothing.
func TestPushWithoutRepoIDDoesNotRegister(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	baseRepos(e)
	start(t, e, "acme/uses-lib")
	calls := e.gh.TotalCalls()
	e.pushPayload("acme/also-uses", "refs/heads/main", "main", 0, []string{"go.mod"}, "")
	time.Sleep(300 * time.Millisecond)
	if _, ok, _ := e.st.GetRepo(context.Background(), "acme/also-uses"); ok || e.gh.TotalCalls() != calls {
		t.Fatalf("an unknown repo was registered from a push with no id (registered=%v)", ok)
	}
}

// A late push still named after the old name of a repo already renamed goes to the repo.
func TestPushUnderOldNameAfterRenameFollowsAlias(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	baseRepos(e)
	start(t, e, "acme/uses-lib")
	e.gh.RenameRepo("acme/uses-lib", "acme/renamed")
	e.repoEvent("renamed", "acme/renamed", "main", map[string]any{"repository": map[string]any{"name": map[string]any{"from": "uses-lib"}}})
	e.waitRepo("acme/renamed")
	e.repoGone("acme/uses-lib")
	e.gh.Commit("acme/renamed", map[string]string{"go.mod": gomod("example.com/uses-lib", "example.com/lib v1.0.0", "example.com/other v1.0.0"), "main.go": blankImports("example.com/lib", "example.com/other")})
	e.pushPayload("acme/uses-lib", "refs/heads/main", "main", 11, []string{"go.mod"}, "")
	e.eventually("push applied to the renamed repo", func() bool { return len(e.deps("acme/renamed")) == 2 })
	if _, ok, _ := e.st.GetRepo(context.Background(), "acme/uses-lib"); ok {
		t.Fatalf("the old name was registered again")
	}
}
