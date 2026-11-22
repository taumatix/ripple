package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

func forEach(t *testing.T, fn func(t *testing.T, st store.Store)) { testutil.ForEachStore(t, fn) }

func baseRepos(e *env) {
	e.gh.AddRepo("acme/uses-lib", map[string]string{"go.mod": gomod("example.com/uses-lib", "example.com/lib v1.0.0")})
	e.gh.AddRepo("acme/also-uses", map[string]string{"go.mod": gomod("example.com/also", "example.com/lib v1.0.5", "example.com/other v1.0.0")})
	e.gh.AddRepo("acme/unrelated", map[string]string{"go.mod": gomod("example.com/unrelated", "example.com/other v1.0.0")})
}

// start registers the repos and lets the watcher record its starting cursor.
func start(t *testing.T, e *env, repos ...string) {
	t.Helper()
	e.install(repos...)
	e.tick()
}

func TestFanOutOnlyImportingRepos(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		start(t, e, "acme/uses-lib", "acme/also-uses", "acme/unrelated")
		platBefore := e.plat.Calls("acme/unrelated")
		before := e.gh.Calls("acme/unrelated")

		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()

		for _, r := range []string{"acme/uses-lib", "acme/also-uses"} {
			prs := e.gh.PRs(r)
			if len(prs) != 1 {
				t.Fatalf("%s: want exactly 1 PR, got %d", r, len(prs))
			}
			if prs[0].Base != "main" || !strings.HasPrefix(prs[0].Head, "ripple/go/") {
				t.Errorf("%s: unexpected PR %+v", r, prs[0])
			}
			got, ok := e.gh.FileAt(r, prs[0].Head, "go.mod")
			if !ok || !strings.Contains(got, "\texample.com/lib v1.1.0\n") {
				t.Errorf("%s: go.mod on %s lacks literal 'example.com/lib v1.1.0':\n%s", r, prs[0].Head, got)
			}
		}
		if got, _ := e.gh.FileAt("acme/also-uses", e.gh.PRs("acme/also-uses")[0].Head, "go.mod"); !strings.Contains(got, "example.com/other v1.0.0") {
			t.Errorf("unrelated requirement was touched:\n%s", got)
		}
		if n := e.plat.Calls("acme/unrelated") - platBefore; n != 0 {
			t.Errorf("platform calls for non-importing repo: %d, want 0", n)
		}
		if n := e.gh.Calls("acme/unrelated") - before; n != 0 {
			t.Errorf("GitHub API calls for non-importing repo: %d, want 0", n)
		}
		if n := len(e.gh.PRs("acme/unrelated")); n != 0 {
			t.Errorf("PRs in non-importing repo: %d", n)
		}

		// Polling again, even with the cursor replaying the inclusive boundary, adds nothing.
		e.tick()
		e.tick()
		if len(e.gh.PRs("acme/uses-lib")) != 1 || len(e.gh.PRs("acme/also-uses")) != 1 {
			t.Errorf("re-poll created extra PRs")
		}
	})
}

func TestIndexRequestFormat(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.tick()
		if e.idx.Requests() == 0 {
			t.Fatal("watcher never called the index")
		}
	})
}

func TestMinimumReleaseAge(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.MinimumReleaseAge = 72 * time.Hour
		e := newEnv(t, st, lim)
		baseRepos(e)
		start(t, e, "acme/uses-lib")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if n := len(e.gh.PRs("acme/uses-lib")); n != 0 {
			t.Fatalf("PR opened inside the cooldown: %d", n)
		}
		j := e.job("acme/uses-lib", "example.com/lib", "v1.1.0")
		if j.State != model.JobPending {
			t.Fatalf("job state %s, want pending", j.State)
		}
		e.clock.Advance(71 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/uses-lib")); n != 0 {
			t.Fatalf("PR opened before the cooldown ended: %d", n)
		}
		e.clock.Advance(2 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/uses-lib")); n != 1 {
			t.Fatalf("want 1 PR after the cooldown, got %d", n)
		}
	})
}

func TestRepoFileCooldownTightensServerDefault(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.MinimumReleaseAge = time.Hour
		e := newEnv(t, st, lim)
		e.gh.AddRepo("acme/slow", map[string]string{
			"go.mod":             gomod("example.com/slow", "example.com/lib v1.0.0"),
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    cooldown: 5d\n",
		})
		e.gh.AddRepo("acme/greedy", map[string]string{
			"go.mod":             gomod("example.com/greedy", "example.com/lib v1.0.0"),
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    cooldown: 1m\n",
		})
		start(t, e, "acme/slow", "acme/greedy")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if n := len(e.gh.PRs("acme/slow")); n != 0 {
			t.Errorf("acme/slow: PR inside its 5d cooldown")
		}
		if n := len(e.gh.PRs("acme/greedy")); n != 0 {
			t.Errorf("acme/greedy widened the server cooldown (1m < 1h): got a PR")
		}
		e.clock.Advance(2 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/greedy")); n != 1 {
			t.Errorf("acme/greedy: want 1 PR after the server minimum, got %d", n)
		}
		if n := len(e.gh.PRs("acme/slow")); n != 0 {
			t.Errorf("acme/slow: PR before 5d")
		}
		e.clock.Advance(5 * 24 * time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/slow")); n != 1 {
			t.Errorf("acme/slow: want 1 PR after 5d, got %d", n)
		}
	})
}

func TestRepoFileIgnoreGroupsAutomerge(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/cfg", map[string]string{
			"go.mod": gomod("example.com/cfg",
				"github.com/aws/a v1.0.0", "github.com/aws/b v1.0.0", "github.com/legacy/x v1.0.0",
				"example.com/lib v1.0.0", "example.com/big v1.0.0"),
			".github/ripple.yml": `updates:
  - ecosystem: gomod
    ignore:
      - dependency: github.com/legacy/*
      - dependency: example.com/big
        update-types: [major]
    groups:
      aws:
        patterns: ["github.com/aws/*"]
    automerge: patch
`,
		})
		start(t, e, "acme/cfg")
		e.clock.Advance(time.Minute)
		e.publish("github.com/legacy/x", "v1.1.0")
		e.publish("github.com/aws/a", "v1.0.1")
		e.publish("github.com/aws/b", "v1.0.1")
		e.publish("example.com/lib", "v1.1.0") // minor: no automerge
		e.publish("example.com/big", "v2.0.0") // ignored major (module path would differ in reality)
		e.tick()

		prs := e.gh.PRs("acme/cfg")
		heads := map[string]testutil.PR{}
		for _, p := range prs {
			heads[p.Head] = p
		}
		g, ok := heads["ripple/group/aws"]
		if !ok {
			t.Fatalf("no group PR; branches: %v", e.gh.Branches("acme/cfg"))
		}
		if !g.AutoMerge {
			t.Errorf("patch-only group PR should have auto-merge enabled")
		}
		gm, _ := e.gh.FileAt("acme/cfg", g.Head, "go.mod")
		if !strings.Contains(gm, "github.com/aws/a v1.0.1") || !strings.Contains(gm, "github.com/aws/b v1.0.1") {
			t.Errorf("group PR go.mod:\n%s", gm)
		}
		for h := range heads {
			if strings.Contains(h, "legacy") || strings.Contains(h, "big") {
				t.Errorf("ignored dependency produced PR %s", h)
			}
		}
		var lib *testutil.PR
		for _, p := range prs {
			if strings.Contains(p.Head, "example-com-lib") || strings.Contains(p.Head, "lib") {
				p := p
				lib = &p
			}
		}
		if lib == nil {
			t.Fatalf("no PR for example.com/lib among %v", e.gh.Branches("acme/cfg"))
		}
		if lib.AutoMerge {
			t.Errorf("minor update must not auto-merge under automerge: patch")
		}
		if len(prs) != 2 {
			t.Errorf("want 2 PRs (group + lib), got %d", len(prs))
		}
	})
}

func TestAutomergeNeedsServerPermission(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.AllowAutomerge = false
		e := newEnv(t, st, lim)
		e.gh.AddRepo("acme/am", map[string]string{
			"go.mod":             gomod("example.com/am", "example.com/lib v1.0.0"),
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    automerge: minor\n",
		})
		start(t, e, "acme/am")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		prs := e.gh.PRs("acme/am")
		if len(prs) != 1 || prs[0].AutoMerge {
			t.Fatalf("want one PR without auto-merge, got %+v", prs)
		}
	})
}

func TestOpenPRLimitTightenedNotWidened(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		lim.OpenPRLimit = 2
		e := newEnv(t, st, lim)
		mods := []string{"example.com/a v1.0.0", "example.com/b v1.0.0", "example.com/c v1.0.0"}
		e.gh.AddRepo("acme/server-limit", map[string]string{"go.mod": gomod("example.com/s", mods...),
			".github/ripple.yml": "open-pull-requests-limit: 10\nupdates:\n  - ecosystem: gomod\n"})
		e.gh.AddRepo("acme/tight", map[string]string{"go.mod": gomod("example.com/t", mods...),
			".github/ripple.yml": "open-pull-requests-limit: 1\nupdates:\n  - ecosystem: gomod\n"})
		start(t, e, "acme/server-limit", "acme/tight")
		e.clock.Advance(time.Minute)
		for _, p := range []string{"a", "b", "c"} {
			e.publish("example.com/"+p, "v1.0.1")
		}
		e.tick()
		if n := len(e.gh.PRs("acme/server-limit")); n != 2 {
			t.Errorf("repo asked for 10, server allows 2: got %d PRs", n)
		}
		if n := len(e.gh.PRs("acme/tight")); n != 1 {
			t.Errorf("repo asked for 1: got %d PRs", n)
		}
	})
}

func TestNoFileAndEmptyFileGetDefaultGoUpdates(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/nofile", map[string]string{"go.mod": gomod("example.com/n", "example.com/lib v1.0.0")})
		e.gh.AddRepo("acme/empty", map[string]string{"go.mod": gomod("example.com/e", "example.com/lib v1.0.0"), ".github/ripple.yml": ""})
		start(t, e, "acme/nofile", "acme/empty")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		for _, r := range []string{"acme/nofile", "acme/empty"} {
			if n := len(e.gh.PRs(r)); n != 1 {
				t.Errorf("%s: want 1 PR, got %d", r, n)
			}
		}
	})
}

func TestInvalidFileMeansNoUpdatesUntilFixed(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/bad", map[string]string{"go.mod": gomod("example.com/b", "example.com/lib v1.0.0"),
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    cooldwn: 3d\n"})
		start(t, e, "acme/bad")
		if d := e.deps("acme/bad"); len(d) != 0 {
			t.Fatalf("invalid config registered dependencies: %v", d)
		}
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		if n := len(e.gh.PRs("acme/bad")); n != 0 {
			t.Fatalf("PR opened for repo with invalid config")
		}
		e.push("acme/bad", map[string]string{".github/ripple.yml": "updates:\n  - ecosystem: gomod\n"})
		e.eventually("deps registered after fix", func() bool { return len(e.deps("acme/bad")) == 1 })
	})
}

func TestPushRescansDependencies(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/p", map[string]string{"go.mod": gomod("example.com/p", "example.com/lib v1.0.0")})
		start(t, e, "acme/p")
		e.push("acme/p", map[string]string{"go.mod": gomod("example.com/p", "example.com/lib v1.0.0", "example.com/new v0.1.0")})
		e.eventually("new dependency indexed", func() bool { return len(e.deps("acme/p")) == 2 })
		e.clock.Advance(time.Minute)
		e.publish("example.com/new", "v0.1.1")
		e.tick()
		if n := len(e.gh.PRs("acme/p")); n != 1 {
			t.Fatalf("want 1 PR for the dependency added by push, got %d", n)
		}
	})
}

func TestWebhookAuthentication(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/x", map[string]string{"go.mod": gomod("example.com/x", "example.com/lib v1.0.0")})
		payload := map[string]any{"action": "created", "installation": map[string]any{"id": 7}, "repositories": repoList("acme/x")}
		if c := e.deliver("installation", payload, "sha256=0000"); c != http.StatusUnauthorized {
			t.Errorf("bad signature: %d, want 401", c)
		}
		if c := e.deliver("installation", payload, "deadbeef"); c != http.StatusUnauthorized {
			t.Errorf("malformed signature: %d, want 401", c)
		}
		time.Sleep(200 * time.Millisecond)
		if _, ok, _ := e.st.GetRepo(t.Context(), "acme/x"); ok {
			t.Fatal("repo registered by an unsigned webhook")
		}
		if n := e.gh.TotalCalls(); n != 0 {
			t.Errorf("GitHub was called %d times for unauthenticated deliveries", n)
		}
	})
}

func TestRateLimitDefersThenRetries(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/rl", map[string]string{"go.mod": gomod("example.com/rl", "example.com/lib v1.0.0")})
		start(t, e, "acme/rl")
		e.clock.Advance(time.Minute)
		e.publish("example.com/lib", "v1.0.1")
		e.gh.RateLimitOnce = true
		e.tick()
		if n := len(e.gh.PRs("acme/rl")); n != 0 {
			t.Fatalf("PR created despite 429")
		}
		if j := e.job("acme/rl", "example.com/lib", "v1.0.1"); j.State != model.JobPending {
			t.Fatalf("after 429 job is %s, want pending", j.State)
		}
		e.clock.Advance(time.Hour)
		e.tick()
		if n := len(e.gh.PRs("acme/rl")); n != 1 {
			t.Fatalf("want 1 PR after the retry, got %d", n)
		}
	})
}
