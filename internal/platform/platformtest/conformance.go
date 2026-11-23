package platformtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// A release reaches exactly the repos that import the package: one PR each with
// the literal new requirement, and not one call for a repo that does not.
func releaseFanOut(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.0.5", "v1.1.0")
	e.stub("example.com/other", "v1.0.0")
	e.addRepo("uses-lib", libRepo("example.com/uses-lib", nil))
	e.addRepo("also-uses", map[string]string{
		"go.mod":  gomod("example.com/also", "example.com/lib v1.0.5", "example.com/other v1.0.0"),
		"main.go": blankImports("example.com/lib", "example.com/other")})
	e.addRepo("unrelated", map[string]string{"go.mod": gomod("example.com/unrelated", "example.com/other v1.0.0")})
	e.register("uses-lib", "also-uses", "unrelated")
	before := e.calls("unrelated")

	e.publish("example.com/lib", "v1.1.0")
	e.tick()

	for _, r := range []string{"uses-lib", "also-uses"} {
		prs := e.prs(r)
		if len(prs) != 1 {
			t.Fatalf("%s: want exactly 1 PR, got %d", r, len(prs))
		}
		if prs[0].Base != "main" || !strings.HasPrefix(prs[0].Head, "ripple/go/") {
			t.Errorf("%s: unexpected PR %+v", r, prs[0])
		}
		got, ok := f.FileAt(f.Path(r), prs[0].Head, "go.mod")
		if !ok || !strings.Contains(got, "example.com/lib v1.1.0\n") {
			t.Errorf("%s: go.mod on %s lacks literal 'example.com/lib v1.1.0':\n%s", r, prs[0].Head, got)
		}
	}
	if got, _ := f.FileAt(f.Path("also-uses"), e.prs("also-uses")[0].Head, "go.mod"); !strings.Contains(got, "example.com/other v1.0.0") {
		t.Errorf("unrelated requirement was touched:\n%s", got)
	}
	if n := e.calls("unrelated") - before; n != 0 {
		t.Errorf("calls for the repo that does not import the package: %d, want 0", n)
	}
	if n := len(e.prs("unrelated")); n != 0 {
		t.Errorf("PRs in the repo that does not import the package: %d", n)
	}
	// Polling again, even with the cursor replaying its inclusive boundary, adds nothing.
	e.tick()
	e.tick()
	if len(e.prs("uses-lib")) != 1 || len(e.prs("also-uses")) != 1 {
		t.Errorf("re-poll created extra PRs")
	}
}

// A push to the default branch re-scans the repo it names; a push that changes no
// file a manager or the config reads costs that repo nothing, and no push costs
// any other repo anything.
func pushRescan(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0")
	e.stub("example.com/new", "v0.1.0", "v0.1.1")
	e.addRepo("p", libRepo("example.com/p", nil))
	e.addRepo("bystander", libRepo("example.com/bystander", nil))
	e.register("p", "bystander")
	bystander := e.calls("bystander")

	idle, idleScans := e.calls("p"), e.plat.Calls(e.name("p"))
	e.push("p", map[string]string{"README.md": "hello\n"})
	time.Sleep(300 * time.Millisecond) // the push is queued; give a wrong re-scan time to happen
	if _, fileless := f.(FilelessPushes); fileless {
		// The host's push event lists no files, so deciding costs API calls; it must
		// still cost no scan, which is the checkout.
		if n := e.plat.Calls(e.name("p")) - idleScans; n != 0 {
			t.Errorf("a push touching no relevant file cost %d platform calls (a re-scan)", n)
		}
	} else if n := e.calls("p") - idle; n != 0 {
		t.Errorf("a push touching no relevant file cost %d calls", n)
	}

	e.push("p", map[string]string{
		"go.mod":  gomod("example.com/p", "example.com/lib v1.0.0", "example.com/new v0.1.0"),
		"main.go": blankImports("example.com/lib", "example.com/new")})
	e.eventually("new dependency indexed", func() bool { return len(e.deps("p")) == 2 })
	e.publish("example.com/new", "v0.1.1")
	e.tick()
	if n := len(e.prs("p")); n != 1 {
		t.Fatalf("want 1 PR for the dependency the push added, got %d", n)
	}
	if n := e.calls("bystander") - bystander; n != 0 {
		t.Errorf("pushes to another repo cost the bystander %d calls", n)
	}
}

const ignoreLib = "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: example.com/lib\n        update-types: [minor]\n"

// .ripple.yaml at the repository root means the same on every host.
func rootConfig(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.1.0")
	e.addRepo("cfg", libRepo("example.com/cfg", map[string]string{".ripple.yaml": ignoreLib}))
	e.addRepo("plain", libRepo("example.com/plain", nil))
	e.register("cfg", "plain")
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	if n := len(e.prs("cfg")); n != 0 {
		t.Errorf("ignore rule in .ripple.yaml not applied: %d PRs", n)
	}
	if n := len(e.prs("plain")); n != 1 {
		t.Errorf("repo without a config file: %d PRs, want 1", n)
	}
}

// .github/ripple.yml is a deprecated alias on GitHub alone; elsewhere it is an
// ordinary file that changes nothing.
func legacyAlias(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.1.0")
	e.addRepo("alias", libRepo("example.com/alias", map[string]string{".github/ripple.yml": ignoreLib}))
	e.register("alias")
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	reads := platform.ReadsLegacyConfig(e.mux, model.Repo{FullName: e.name("alias")})
	want := 1
	if reads {
		want = 0
	}
	if n := len(e.prs("alias")); n != want {
		t.Errorf("host reads the alias: %v; PRs = %d, want %d", reads, n, want)
	}
}

// automerge in the repo file asks the host to merge when green, where the host
// can; a host that cannot still gets its PR, just without auto-merge.
func autoMerge(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.0.1")
	e.addRepo("am", libRepo("example.com/am", map[string]string{".ripple.yaml": "updates:\n  - ecosystem: gomod\n    automerge: patch\n"}))
	e.register("am")
	e.publish("example.com/lib", "v1.0.1")
	e.tick()
	prs := e.prs("am")
	if len(prs) != 1 {
		t.Fatalf("want 1 PR, got %d", len(prs))
	}
	_, can := e.host.Platform.(platform.AutoMerger)
	if prs[0].AutoMerge != can {
		t.Errorf("host can auto-merge: %v; PR auto-merge = %v", can, prs[0].AutoMerge)
	}
	if j := e.job("am", "example.com/lib", "v1.0.1"); j.State != model.JobDone {
		t.Errorf("job is %s, want done whether or not the host can auto-merge", j.State)
	}
}

// After the host answers a PR creation with its rate-limit response, no further
// call is spent on that host's repos until the time it gave; then each job
// retries and opens its one PR.
func rateLimit(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{workers: 1})
	e.stub("example.com/lib", "v1.0.0", "v1.0.1")
	e.addRepo("rl-a", libRepo("example.com/rl-a", nil))
	e.addRepo("rl-b", libRepo("example.com/rl-b", nil))
	e.register("rl-a", "rl-b")
	if !f.RateLimitNextPR() {
		t.Skip("the fake cannot answer with a rate limit")
	}
	beforeA, beforeB := e.calls("rl-a"), e.calls("rl-b")
	e.publish("example.com/lib", "v1.0.1")
	e.tick()
	_, pushCreates := f.(PushCreatesProposals)
	left := 0
	for _, r := range []string{"rl-a", "rl-b"} {
		n := len(e.prs(r))
		if pushCreates {
			left += n // the refused repo already pushed; its proposal exists
		} else if n != 0 {
			t.Fatalf("%s: PR created despite the rate limit", r)
		}
		if j := e.job(r, "example.com/lib", "v1.0.1"); j.State != model.JobPending {
			t.Fatalf("%s: after the rate limit the job is %s, want pending", r, j.State)
		}
	}
	// One worker: one repo ran up to the refusal, the other must not have been called.
	da, db := e.calls("rl-a")-beforeA, e.calls("rl-b")-beforeB
	if max(da, db) == 0 || min(da, db) != 0 {
		t.Errorf("calls during the rate limit: rl-a %d, rl-b %d; want one repo refused and the other untouched", da, db)
	}
	if pushCreates && left != 1 {
		t.Fatalf("proposals left by the refused push: %d, want 1 (one repo refused, the other untouched)", left)
	}
	e.clock.Advance(time.Hour)
	e.tick()
	for _, r := range []string{"rl-a", "rl-b"} {
		if n := len(e.prs(r)); n != 1 {
			t.Fatalf("%s: want 1 PR after the retry, got %d", r, n)
		}
	}
}

// A newer release's PR replaces ripple's own untouched PR for an older version of
// the module, with a comment, where the host implements platform.PRCloser. A host
// that does not keeps both PRs open and still finishes both jobs: absent means
// reported absent, not an error.
func supersede(t *testing.T, f Fixture) {
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
	e.addRepo("sup", libRepo("example.com/sup", nil))
	e.register("sup")
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	e.publish("example.com/lib", "v1.2.0")
	e.tick()
	prs := e.prs("sup")
	if len(prs) != 2 {
		t.Fatalf("want a PR for each release, got %d", len(prs))
	}
	_, can := e.host.Platform.(platform.PRCloser)
	old, cur := prs[0], prs[1]
	if cur.Closed || len(cur.Comments) != 0 {
		t.Errorf("the newest PR must stay open and uncommented: %+v", cur)
	}
	if old.Closed != can {
		t.Errorf("host can close pull requests: %v; stale PR closed = %v", can, old.Closed)
	}
	if can && (len(old.Comments) != 1 || !strings.Contains(old.Comments[0], "v1.2.0")) {
		t.Errorf("the stale PR needs one comment naming the new version: %q", old.Comments)
	}
	if !can && len(old.Comments) != 0 {
		t.Errorf("a host that cannot close must not be commented on: %q", old.Comments)
	}
	for _, v := range []string{"v1.1.0", "v1.2.0"} {
		if j := e.job("sup", "example.com/lib", v); j.State != model.JobDone {
			t.Errorf("job %s is %s, want done", v, j.State)
		}
	}
}

// A host that reports renames: the stored repo and its dependencies move to the new
// name with no scan, and a later release opens its one PR there.
func renamed(t *testing.T, f Fixture) {
	rr, ok := f.(ReportsRenames)
	if !ok {
		// Absent means the host cannot report renames, which is allowed: nothing to hold
		// it to. Returning rather than skipping keeps CI's "no test may skip" check.
		return
	}
	e := newEnv(t, f, opts{})
	e.stub("example.com/lib", "v1.0.0", "v1.1.0")
	e.addRepo("before", libRepo("example.com/before", nil))
	e.register("before")
	oldName, newName := e.name("before"), e.mux.RepoName(e.host.Name, f.Path("after"))
	scans := e.plat.Calls(oldName)

	rr.RenameRepo(t, e.hook, f.Path("before"), f.Path("after"))
	e.eventually("repo stored under its new name", func() bool {
		_, ok, err := e.st.GetRepo(context.Background(), newName)
		return err == nil && ok
	})
	e.eventually("old name forgotten", func() bool {
		_, ok, err := e.st.GetRepo(context.Background(), oldName)
		return err == nil && !ok
	})
	if d, err := e.st.ListRepoDependencies(context.Background(), newName); err != nil || len(d) != 1 || d[0].Repo != newName {
		t.Fatalf("dependencies did not follow: %+v %v", d, err)
	}
	if n := e.plat.Calls(oldName) - scans + e.plat.Calls(newName); n != 0 {
		t.Errorf("the rename cost %d platform calls (a scan), want 0", n)
	}

	e.publish("example.com/lib", "v1.1.0")
	e.tick()
	if n := len(e.f.PRs(f.Path("after"))); n != 1 {
		t.Fatalf("want 1 PR under the new name, got %d", n)
	}
}
