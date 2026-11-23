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
