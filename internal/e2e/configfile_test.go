package e2e

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

const (
	ignoreLib = "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: example.com/lib\n        update-types: [minor]\n"
	slowCfg   = "updates:\n  - ecosystem: gomod\n    cooldown: 5d\n"
	fastCfg   = "updates:\n  - ecosystem: gomod\n    cooldown: 1m\n"

	legacyLog   = `msg="config file .github/ripple.yml is deprecated; move it to .ripple.yaml (the alias will not be removed before v0.3.0)"`
	shadowedLog = `msg="both .ripple.yaml and .github/ripple.yml exist; using .ripple.yaml and ignoring .github/ripple.yml, delete it"`
)

// cfgEnv registers one repo with the given files, publishes lib v1.1.0 a minute
// later under a one hour server cooldown and ticks once.
func cfgEnv(t *testing.T, st store.Store, files map[string]string) *env {
	t.Helper()
	lim := defaultLimits()
	lim.MinimumReleaseAge = time.Hour
	e := newEnv(t, st, lim)
	f := map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0")}
	for k, v := range files {
		f[k] = v
	}
	e.gh.AddRepo("acme/app", f)
	start(t, e, "acme/app")
	return e
}

func (e *env) release() {
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()
}

func TestRootConfigFileDrivesPRs(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		// No server cooldown, so only the repo file can keep the PR from opening.
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":       gomod("example.com/app", "example.com/lib v1.0.0"),
			".ripple.yaml": ignoreLib,
		})
		start(t, e, "acme/app")
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("ignore rule in .ripple.yaml not applied: %d PRs", n)
		}
		if n := e.logLines("deprecated"); n != 0 {
			t.Errorf("deprecation logged for the canonical file: %d lines", n)
		}
		if n := e.logLines("ignoring"); n != 0 {
			t.Errorf("shadow warning logged with one file: %d lines", n)
		}
	})
}

func TestDeprecatedConfigPathStillWorksAndWarns(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := cfgEnv(t, st, map[string]string{".github/ripple.yml": slowCfg})
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("cooldown 5d in .github/ripple.yml ignored: %d PRs", n)
		}
		if n := e.logLines(legacyLog, "repo=acme/app", "level=WARN"); n != 1 {
			t.Fatalf("want exactly one deprecation warning for the one scan, got %d\n%s", n, e.logs.String())
		}
		if n := e.logLines("ignoring"); n != 0 {
			t.Errorf("shadow warning with only the alias: %d", n)
		}
	})
}

func TestBothConfigFilesNewWins(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		// The alias says 5d (would block); the canonical file says 1m (would not).
		lim := defaultLimits()
		e := newEnv(t, st, lim)
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":             gomod("example.com/app", "example.com/lib v1.0.0"),
			".ripple.yaml":       fastCfg,
			".github/ripple.yml": slowCfg,
		})
		start(t, e, "acme/app")
		e.release()
		e.clock.Advance(2 * time.Minute) // past the new file's 1m cooldown, far short of the alias's 5d
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("the alias's 5d cooldown was applied: %d PRs, want 1", n)
		}
		if n := e.logLines(shadowedLog, "repo=acme/app", "level=WARN"); n != 1 {
			t.Fatalf("want one shadow warning, got %d\n%s", n, e.logs.String())
		}
		if n := e.logLines(legacyLog); n != 0 {
			t.Errorf("deprecation warning on top of the shadow warning: %d", n)
		}
	})
}

func TestBothConfigFilesInvalidAliasDoesNotStopUpdates(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":             gomod("example.com/app", "example.com/lib v1.0.0"),
			".ripple.yaml":       "updates:\n  - ecosystem: gomod\n",
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n    cooldwn: 3d\n",
		})
		start(t, e, "acme/app")
		if d := e.deps("acme/app"); len(d) != 1 {
			t.Fatalf("an invalid, ignored alias stopped indexing: %v", d)
		}
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 1 {
			t.Fatalf("PRs = %d, want 1", n)
		}
	})
}

// A push whose changed files are only a config file must re-read the config.
func TestPushTouchingOnlyAConfigFileRereadsIt(t *testing.T) {
	for _, path := range []string{".ripple.yaml", ".github/ripple.yml"} {
		t.Run(path, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := newEnv(t, st, defaultLimits())
				e.gh.AddRepo("acme/app", map[string]string{
					"go.mod": gomod("example.com/app", "example.com/lib v1.0.0"),
					path:     "updates:\n  - ecosystem: gomod\n",
				})
				start(t, e, "acme/app")
				if d := e.deps("acme/app"); len(d) != 1 {
					t.Fatalf("deps = %v", d)
				}
				// Break the file: only it changed, and the repo must go quiet.
				e.push("acme/app", map[string]string{path: "updates:\n  - ecosystem: gomod\n    cooldwn: 3d\n"})
				e.eventually("invalid file clears deps", func() bool { return len(e.deps("acme/app")) == 0 })
				// Fix it, again touching only the file.
				e.push("acme/app", map[string]string{path: "updates:\n  - ecosystem: gomod\n"})
				e.eventually("fixed file restores deps", func() bool { return len(e.deps("acme/app")) == 1 })
			})
		})
	}
}

// A repo whose valid config is in place re-reads it on a push touching only a new
// .ripple.yaml, which then replaces the alias.
func TestPushAddingRootFileReplacesAlias(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		lim := defaultLimits()
		e := newEnv(t, st, lim)
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":             gomod("example.com/app", "example.com/lib v1.0.0"),
			".github/ripple.yml": "updates:\n  - ecosystem: gomod\n",
		})
		start(t, e, "acme/app")
		e.push("acme/app", map[string]string{".ripple.yaml": slowCfg})
		e.eventually("shadow warning after the push", func() bool { return e.logLines(shadowedLog, "repo=acme/app") == 1 })
		e.release()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("new file's 5d cooldown not applied after the push: %d PRs", n)
		}
	})
}
