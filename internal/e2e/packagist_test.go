package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const composerConfig = "updates:\n  - ecosystem: composer\n"

func packagistEnv(t *testing.T, st store.Store) (*env, *testutil.Packagist) {
	p := testutil.NewPackagist(t)
	return newEnvOpts(t, st, defaultLimits(), opts{packagist: p}), p
}

const composerE2E = `{
    "name": "acme/app",
    "require": {
        "php": "^8.1",
        "monolog/monolog": "^2.9.0",
        "symfony/console": "v5.4.1",
        "two/seg": "^1.2",
        "fromrepo/x": "1.0.0"
    },
    "require-dev": {
        "phpunit/phpunit": "9.5.0"
    }
}
`

func TestComposerPackageBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := packagistEnv(t, st)
		reg.Publish("monolog/monolog", "2.9.0")
		reg.Publish("symfony/console", "v5.4.1")
		reg.Publish("phpunit/phpunit", "9.5.0")
		reg.Publish("two/seg", "1.2.0")
		reg.Publish("fromrepo/x", "1.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": composerConfig, "composer.json": composerE2E, "composer.lock": "{}\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"composer.json": composerE2E})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 5 {
			t.Fatalf("monolog, console, two/seg, fromrepo and phpunit are tracked (not php), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("monolog/monolog", "2.9.3")
		reg.Publish("monolog/monolog", "3.0.0-RC1") // a prerelease: not proposed
		reg.Publish("monolog/monolog", "dev-main")  // a branch: not proposed
		reg.Publish("symfony/console", "v5.4.9")
		reg.Publish("phpunit/phpunit", "9.6.0")
		reg.Publish("two/seg", "1.3.0") // "^1.2" is ">=1.2.0 <2.0.0": already allowed, no PR
		reg.Publish("two/seg", "2.4.1") // a later major leaves the constraint
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 4 {
			t.Fatalf("one PR per package (monolog, console, phpunit, two/seg), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "composer.json")
			from, to := "", ""
			switch {
			case strings.Contains(pr.Head, "monolog"):
				from, to = `"^2.9.0"`, `"^2.9.3"`
			case strings.Contains(pr.Head, "symfony-console"):
				from, to = `"v5.4.1"`, `"v5.4.9"`
			case strings.Contains(pr.Head, "phpunit"):
				from, to = `"9.5.0"`, `"9.6.0"`
			case strings.Contains(pr.Head, "two-seg"):
				from, to = `"^1.2"`, `"^2.4"` // the new release's first two segments
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(composerE2E, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout kept:\n%s", pr.Head, got)
			}
			if lock, _ := e.gh.FileAt("acme/app", pr.Head, "composer.lock"); lock != "{}\n" {
				t.Errorf("%s: composer.lock must be left alone, got %q", pr.Head, lock)
			}
			for _, w := range []string{"composer.lock not regenerated", "`composer.lock`", "composer update"} {
				if !strings.Contains(pr.Body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
				}
			}
			if !strings.HasPrefix(pr.Head, "ripple/packagist/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range reg.Requests() {
			if r.Authorization != "" || !strings.HasPrefix(r.Path, "/p2/") {
				t.Errorf("request %+v: anonymous, and only for a package's metadata", r)
			}
			if r.Path == "/p2/php.json" {
				t.Errorf("a requirement ripple does not track was asked for: %s", r.Path)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestComposerDeniedPackageIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := packagistEnv(t, st)
		reg.Publish("secret/pkg", "1.0.0")
		reg.Deny("secret/pkg")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": composerConfig, "composer.json": `{"require": {"secret/pkg": "1.0.0"}}` + "\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("a package that refuses anonymous reads opened %d PRs", n)
		}
	})
}

func TestComposerPrivateRepositoryIsNotRead(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := packagistEnv(t, st)
		reg.Publish("a/b", "1.0.0")
		doc := `{"repositories": [{"type": "composer", "url": "https://repo.example.com"}], "require": {"a/b": "1.0.0"}}` + "\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": composerConfig, "sub/composer.json": doc})
		start(t, e, "acme/app")
		reg.Publish("a/b", "1.1.0")
		e.clock.Advance(time.Minute)
		e.tick()
		if len(e.deps("acme/app")) != 0 || len(e.gh.PRs("acme/app")) != 0 || len(reg.Requests()) != 0 {
			t.Errorf("a composer.json with a private repository was read: deps %v, requests %v", e.deps("acme/app"), reg.Requests())
		}
	})
}

const composerPartialE2E = `{
    "require": {
        "one/caret": "^1",
        "one/tilde": "~1",
        "zero/tilde": "~0.3",
        "zero/caret": "^0"
    }
}
`

// Composer 2 locks "^1" and "~1" to the 1.x line, "~0.3" to 0.3 up to 1.0 and "^0" to 0.x
// (TestRealtoolComposerConstraintMeaning), so only a later major is an update, and the text
// becomes the new release's major (or first two segments for "~0.3").
func TestComposerOneSegmentAndTildeZero(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := packagistEnv(t, st)
		reg.Publish("one/caret", "1.0.0")
		reg.Publish("one/tilde", "1.0.0")
		reg.Publish("zero/tilde", "0.3.0")
		reg.Publish("zero/caret", "0.1.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": composerConfig, "composer.json": composerPartialE2E})
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 4 {
			t.Fatalf("all four are tracked, got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("one/caret", "1.9.0")  // inside ^1
		reg.Publish("one/caret", "2.3.4")  // a later major
		reg.Publish("one/tilde", "1.5.0")  // inside ~1
		reg.Publish("zero/tilde", "0.9.0") // inside ~0.3 (>=0.3 <1.0)
		reg.Publish("zero/tilde", "1.2.0") // a later major
		reg.Publish("zero/caret", "0.5.0") // inside ^0 (>=0 <1)
		e.tick()
		e.tick()

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one/caret and zero/tilde only (the rest is allowed already), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "composer.json")
			from, to := "", ""
			switch {
			case strings.Contains(pr.Head, "one-caret"):
				from, to = `"^1"`, `"^2"`
			case strings.Contains(pr.Head, "zero-tilde"):
				from, to = `"~0.3"`, `"~1.2"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(composerPartialE2E, from, to, 1); got != want {
				t.Errorf("%s: only that constraint moves:\n%s", pr.Head, got)
			}
		}
	})
}
