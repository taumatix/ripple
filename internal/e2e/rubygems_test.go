package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const bundlerConfig = "updates:\n  - ecosystem: bundler\n"

func gemsEnv(t *testing.T, st store.Store) (*env, *testutil.Rubygems) {
	g := testutil.NewRubygems(t)
	return newEnvOpts(t, st, defaultLimits(), opts{gems: g}), g
}

const gemfileE2E = `source "https://rubygems.org"

gem "rails", "7.1.2"
gem 'nokogiri', '~> 1.15.4', require: false   # keeps its operator
gem "two", ">= 1.2"
gem "bare"
gem "fromgit", "1.0.0", git: "https://example.com/x.git"
`

func TestBundlerGemBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, gems := gemsEnv(t, st)
		for _, g := range []string{"rails", "nokogiri", "two", "bare", "fromgit"} {
			gems.Publish(g, "1.0.0")
		}
		gems.Publish("rails", "7.1.2")
		gems.Publish("nokogiri", "1.15.4")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": bundlerConfig, "Gemfile": gemfileE2E, "Gemfile.lock": "GEM\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"Gemfile": gemfileE2E})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("rails and nokogiri are tracked (not a \">=\" floor, a bare gem or a git gem), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		gems.Publish("rails", "7.1.3")
		gems.PublishPlatform("rails", "7.2.0", "java") // a platform build only: not proposed
		gems.PublishPrerelease("rails", "8.0.0.rc1")
		gems.Publish("nokogiri", "1.16.0")
		gems.Publish("fromgit", "9.0.0") // never read: a git gem
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per gem (rails, nokogiri), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Gemfile")
			from, to := "", ""
			switch {
			case strings.Contains(pr.Head, "rails"):
				from, to = `"7.1.2"`, `"7.1.3"`
			case strings.Contains(pr.Head, "nokogiri"):
				from, to = "'~> 1.15.4'", "'~> 1.16.0'"
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(gemfileE2E, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout and comments kept:\n%s", pr.Head, got)
			}
			if lock, _ := e.gh.FileAt("acme/app", pr.Head, "Gemfile.lock"); lock != "GEM\n" {
				t.Errorf("%s: Gemfile.lock must be left alone, got %q", pr.Head, lock)
			}
			for _, w := range []string{"Gemfile.lock not regenerated", "`Gemfile.lock`", "bundle install"} {
				if !strings.Contains(pr.Body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
				}
			}
			if !strings.HasPrefix(pr.Head, "ripple/rubygems/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range gems.Requests() {
			if r.Authorization != "" || !strings.HasPrefix(r.Path, "/api/v1/versions/") {
				t.Errorf("request %+v: anonymous, and only for a gem's versions", r)
			}
			if strings.Contains(r.Path, "fromgit") || strings.Contains(r.Path, "bare") || strings.Contains(r.Path, "two") {
				t.Errorf("a gem ripple does not track was asked for: %s", r.Path)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestBundlerDeniedGemIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, gems := gemsEnv(t, st)
		gems.Publish("secret", "1.0.0")
		gems.Deny("secret")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": bundlerConfig, "Gemfile": "gem \"secret\", \"1.0.0\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("a gem that refuses anonymous reads opened %d PRs", n)
		}
	})
}

func TestBundlerOtherSourceIsNotRead(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, gems := gemsEnv(t, st)
		gems.Publish("a", "1.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": bundlerConfig, "sub/Gemfile": "source \"https://gems.example.com\"\ngem \"a\", \"1.0.0\"\n"})
		start(t, e, "acme/app")
		gems.Publish("a", "1.1.0")
		e.clock.Advance(time.Minute)
		e.tick()
		if len(e.deps("acme/app")) != 0 || len(e.gh.PRs("acme/app")) != 0 || len(gems.Requests()) != 0 {
			t.Errorf("a Gemfile on a private source was read: deps %v, requests %v", e.deps("acme/app"), gems.Requests())
		}
	})
}

const gemfileTwoSegmentE2E = `source "https://rubygems.org"

gem "pess", "~> 1.2", require: false   # two segments
gem "other", "~> 3.4"
`

// "~> 1.2" is ">= 1.2, < 2.0" (docs/vendor/rubygems/gemfile.md, "Gems > Version"): a later 1.x is
// already allowed and is no PR, a 2.x release is, and the file then says "~> 2.1".
func TestBundlerTwoSegmentMajor(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, gems := gemsEnv(t, st)
		gems.Publish("pess", "1.2.0")
		gems.Publish("other", "3.4.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": bundlerConfig, "Gemfile": gemfileTwoSegmentE2E, "Gemfile.lock": "GEM\n"})
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("both two-segment gems are tracked, got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		gems.Publish("pess", "1.9.4") // inside "~> 1.2": a lock refresh, not an edit
		gems.Publish("other", "3.9.0")
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("a release the constraint already allows opened %d PRs", n)
		}

		e.clock.Advance(time.Minute)
		gems.Publish("pess", "2.1.3")
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 1 {
			t.Fatalf("one PR for the major, got %d", len(prs))
		}
		got, _ := e.gh.FileAt("acme/app", prs[0].Head, "Gemfile")
		if want := strings.Replace(gemfileTwoSegmentE2E, `"~> 1.2"`, `"~> 2.1"`, 1); got != want {
			t.Errorf("two digits move, layout kept:\n%s", got)
		}
		if !strings.HasPrefix(prs[0].Head, "ripple/rubygems/pess-2.1.3") {
			t.Errorf("branch %q", prs[0].Head)
		}
		if lock, _ := e.gh.FileAt("acme/app", prs[0].Head, "Gemfile.lock"); lock != "GEM\n" {
			t.Errorf("Gemfile.lock must be left alone, got %q", lock)
		}
		if !strings.Contains(prs[0].Body, "Gemfile.lock not regenerated") {
			t.Errorf("body lacks the lock note:\n%s", prs[0].Body)
		}
	})
}
