package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// These drive GitHub's `repository` webhook event through the real receiver, queue
// and engine against the fake GitHub. Payload shapes follow docs/vendor/github.

// repoEvent delivers a repository event for fullName with the given extra fields.
func (e *env) repoEvent(action, fullName, defaultBranch string, changes map[string]any) {
	e.t.Helper()
	p := map[string]any{
		"action":       action,
		"installation": map[string]any{"id": 7},
		"repository":   map[string]any{"name": fullName[strings.Index(fullName, "/")+1:], "full_name": fullName, "default_branch": defaultBranch},
	}
	if changes != nil {
		p["changes"] = changes
	}
	if code := e.deliver("repository", p, ""); code != http.StatusAccepted {
		e.t.Fatalf("repository %s webhook: %d", action, code)
	}
}

func (e *env) repoGone(name string) {
	e.t.Helper()
	e.eventually("repo "+name+" forgotten", func() bool {
		_, ok, err := e.st.GetRepo(context.Background(), name)
		return err == nil && !ok
	})
}

func (e *env) scans(repo string) int { return e.plat.Calls(repo) }

func TestRepositoryRenamed(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnv(t, st, lim)
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick() // the job exists but waits out the cooldown
		if j := e.job("acme/uses-lib", "example.com/lib", "v1.1.0"); j.State != model.JobPending {
			t.Fatalf("job state %s, want pending", j.State)
		}

		e.gh.RenameRepo("acme/uses-lib", "acme/renamed")
		before := e.scans("acme/renamed")
		e.repoEvent("renamed", "acme/renamed", "main", map[string]any{"repository": map[string]any{"name": map[string]any{"from": "uses-lib"}}})
		e.waitRepo("acme/renamed")
		e.repoGone("acme/uses-lib")
		if d := e.deps("acme/renamed"); len(d) != 1 || d[0].Repo != "acme/renamed" || d[0].Package != "example.com/lib" {
			t.Fatalf("dependencies did not follow the rename: %+v", d)
		}
		if d := e.deps("acme/uses-lib"); len(d) != 0 {
			t.Fatalf("old name still has dependencies: %+v", d)
		}
		if n := e.scans("acme/renamed") - before; n != 0 {
			t.Errorf("a rename cost %d platform calls (a re-scan), want 0", n)
		}

		e.clock.Advance(73 * time.Hour)
		e.tick()
		prs := e.gh.PRs("acme/renamed")
		if len(prs) != 1 {
			t.Fatalf("want exactly 1 PR in the renamed repo, got %d", len(prs))
		}
		if j := e.job("acme/renamed", "example.com/lib", "v1.1.0"); j.State != model.JobDone || j.PRURL == "" {
			t.Errorf("job under the new name: %+v", j)
		}
		if j := e.job("acme/uses-lib", "example.com/lib", "v1.1.0"); j.State != model.JobSkipped {
			t.Errorf("job under the old name is %s, want skipped", j.State)
		}
		// Redelivery and further ticks add nothing.
		e.tick()
		if n := len(e.gh.PRs("acme/renamed")); n != 1 {
			t.Errorf("PRs after another tick: %d", n)
		}
	})
}

func TestRepositoryTransferred(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		for name, from := range map[string]map[string]any{
			"user":         {"user": map[string]any{"login": "acme"}},
			"organization": {"organization": map[string]any{"login": "acme"}},
		} {
			t.Run(name, func(t *testing.T) {
				e := newEnv(t, st, defaultLimits())
				stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
				repo := "acme/moves-" + name
				e.gh.AddRepo(repo, map[string]string{"go.mod": gomod("example.com/m", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
				start(t, e, repo)
				e.gh.RenameRepo(repo, "newowner/moves-"+name)
				e.repoEvent("transferred", "newowner/moves-"+name, "main", map[string]any{"owner": map[string]any{"from": from}})
				e.waitRepo("newowner/moves-" + name)
				e.repoGone(repo)
				e.clock.Advance(time.Minute)
				e.publish("example.com/lib", "v1.1.0")
				e.tick()
				if n := len(e.gh.PRs("newowner/moves-" + name)); n != 1 {
					t.Fatalf("want 1 PR in the transferred repo, got %d", n)
				}
			})
		}
	})
}

func TestRepositoryTransferredWithoutOldOwnerRegistersNewName(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	baseRepos(e)
	e.gh.RenameRepo("acme/uses-lib", "elsewhere/uses-lib")
	e.repoEvent("transferred", "elsewhere/uses-lib", "main", map[string]any{"owner": map[string]any{"from": map[string]any{}}})
	e.waitRepo("elsewhere/uses-lib")
}

func TestRepositoryDefaultBranchChanged(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		stubModule(e, "example.com/lib", "v1.0.0", "v1.0.5", "v1.1.0", "v1.2.0")
		stubModule(e, "example.com/new", "v0.1.0")
		e.gh.AddRepo("acme/app", map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		old := e.gh.PRs("acme/app")
		if len(old) != 1 || old[0].Base != "main" {
			t.Fatalf("setup: PRs %+v", old)
		}

		// The default branch becomes trunk, and trunk differs from main in what it imports.
		e.gh.SetDefaultBranch("acme/app", "trunk")
		e.gh.CommitOn("acme/app", "trunk", map[string]string{
			"go.mod":  gomod("example.com/app", "example.com/lib v1.0.5", "example.com/new v0.1.0"),
			"main.go": blankImports("example.com/lib", "example.com/new")})
		e.repoEvent("edited", "acme/app", "trunk", map[string]any{"default_branch": map[string]any{"from": "main"}})
		e.eventually("rescan against trunk", func() bool { return len(e.deps("acme/app")) == 2 })
		if r, _, _ := e.st.GetRepo(context.Background(), "acme/app"); r.DefaultBranch != "trunk" {
			t.Fatalf("stored default branch %q, want trunk", r.DefaultBranch)
		}
		if n := e.logLines("using the deprecated Ref"); n != 0 {
			t.Fatalf("a typed event used the deprecated Ref (%d warnings)", n)
		}

		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.2.0")
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want the old PR plus one new, got %+v", prs)
		}
		if prs[0].Base != "main" || prs[0].Head != old[0].Head {
			t.Errorf("existing PR was touched: %+v", prs[0])
		}
		if prs[1].Base != "trunk" {
			t.Errorf("new PR base %q, want trunk", prs[1].Base)
		}
	})
}

func TestRepositoryEditedOtherFieldsCostNothing(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	baseRepos(e)
	start(t, e, "acme/uses-lib")
	before := e.scans("acme/uses-lib")
	e.repoEvent("edited", "acme/uses-lib", "main", map[string]any{"description": map[string]any{"from": "x"}})
	// A same-branch "change" must not rescan either.
	e.repoEvent("edited", "acme/uses-lib", "main", map[string]any{"default_branch": map[string]any{"from": "main"}})
	time.Sleep(300 * time.Millisecond)
	if n := e.scans("acme/uses-lib") - before; n != 0 {
		t.Errorf("irrelevant edits cost %d platform calls", n)
	}
}

func TestRepositoryDeleted(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		repo := "acme/uses-lib"
		start(t, e, repo)
		e.clock.Advance(time.Minute)
		e.repoEvent("deleted", repo, "main", nil)
		e.repoGone(repo)
		calls := e.gh.Calls(repo)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if n := len(e.gh.PRs(repo)); n != 0 || e.gh.Calls(repo) != calls {
			t.Fatalf("acted on a deleted repo: %d PRs, %d new calls", n, e.gh.Calls(repo)-calls)
		}
	})
}

// A name freed by a rename and taken by a new repository must not be acted on with the old
// repository's pending jobs: they moved to the renamed repository with the rename, so they
// are neither lost nor run against the newcomer.
func TestRenamedNameReusedDoesNotInheritJobs(t *testing.T) {
	lim := defaultLimits()
	lim.MinimumReleaseAge = 72 * time.Hour
	e := newEnv(t, store.NewMemory(), lim)
	baseRepos(e)
	start(t, e, "acme/uses-lib")
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	e.gh.RenameRepo("acme/uses-lib", "acme/renamed")
	e.repoEvent("renamed", "acme/renamed", "main", map[string]any{"repository": map[string]any{"name": map[string]any{"from": "uses-lib"}}})
	e.waitRepo("acme/renamed")
	e.repoGone("acme/uses-lib")
	// Someone creates a new acme/uses-lib that imports nothing.
	e.gh.AddRepo("acme/uses-lib", map[string]string{"go.mod": gomod("example.com/fresh", "example.com/other v1.0.0")})
	e.install("acme/uses-lib")
	e.clock.Advance(73 * time.Hour)
	e.tick()
	if n := len(e.gh.PRs("acme/uses-lib")); n != 0 {
		t.Errorf("the new repository got %d PRs from the old one's job", n)
	}
	if n := len(e.gh.PRs("acme/renamed")); n != 1 {
		t.Errorf("the renamed repository got %d PRs from its own pending job, want 1", n)
	}
}

// An event queued before the typed fields existed carries only the overloaded Ref. It is
// still handled, and each use of the fallback is logged, which is how an operator (and the
// removal of the overload, ROADMAP 76) learns that no queue holds old rows any more.
func TestQueuedEventWithOnlyTheDeprecatedRefIsHandledAndLogged(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/app", map[string]string{"go.mod": gomod("example.com/app"), "main.go": blankImports()})
		start(t, e, "acme/app")
		e.gh.SetDefaultBranch("acme/app", "trunk")
		e.gh.CommitOn("acme/app", "trunk", map[string]string{"NOTES.md": "trunk"})
		const warn = "using the deprecated Ref"
		if n := e.logLines(warn); n != 0 {
			t.Fatalf("%d fallback warnings before any old event", n)
		}
		old := model.Event{ID: "old-1", InstallationID: 7, Kind: "default_branch_changed", Repo: "acme/app", Ref: "refs/heads/trunk"}
		if err := e.app.Queue.Add(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		e.eventually("old event handled", func() bool {
			r, _, _ := e.st.GetRepo(context.Background(), "acme/app")
			return r.DefaultBranch == "trunk"
		})
		if n := e.logLines(warn, "old-1"); n != 1 {
			t.Fatalf("%d fallback warnings for the old event, want 1", n)
		}
	})
}
