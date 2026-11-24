package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const cargoConfig = "updates:\n  - ecosystem: cargo\n"

func cargoEnv(t *testing.T, st store.Store) (*env, *testutil.CratesIndex) {
	idx := testutil.NewCratesIndex(t)
	return newEnvOpts(t, st, defaultLimits(), opts{crates: idx}), idx
}

const cargoManifest = `[package]
name = "app"
version = "0.1.0"

[dependencies]
serde = "1.0.100"
tokio = { version = "~1.2.3", features = ["full"] }
local = { path = "../local", version = "0.1.0" }
wild = "*"

[dev-dependencies]
pretty_assertions = "1.4.0"
`

func TestCargoRequirementBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		idx.Publish("tokio", "1.2.3")
		idx.Publish("pretty_assertions", "1.4.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": cargoManifest, "Cargo.lock": "# lock\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"Cargo.toml": cargoManifest})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 3 {
			t.Fatalf("serde, tokio and pretty_assertions are tracked (not local, not wild), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.200")
		idx.Publish("serde", "1.0.210")
		idx.Yank("serde", "1.0.210")
		idx.Publish("serde", "1.1.0-rc.1") // a prerelease: ignored
		idx.Publish("tokio", "1.2.9")
		idx.Publish("local", "0.2.0") // never read: a path dependency
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per crate (serde, tokio), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Cargo.toml")
			from, to := "", ""
			switch {
			case strings.Contains(pr.Head, "serde"):
				from, to = `serde = "1.0.100"`, `serde = "1.0.200"`
			case strings.Contains(pr.Head, "tokio"):
				from, to = `version = "~1.2.3"`, `version = "~1.2.9"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(cargoManifest, from, to, 1); got != want {
				t.Errorf("%s: only that requirement moves:\n%s", pr.Head, got)
			}
			lock, _ := e.gh.FileAt("acme/app", pr.Head, "Cargo.lock")
			if lock != "# lock\n" {
				t.Errorf("%s: Cargo.lock must be left alone, got %q", pr.Head, lock)
			}
			for _, w := range []string{"Cargo.lock not regenerated", "`Cargo.lock`"} {
				if !strings.Contains(pr.Body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
				}
			}
			if !strings.HasPrefix(pr.Head, "ripple/cargo/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range idx.Requests() {
			if r.Authorization != "" || strings.Contains(r.Path, "local") {
				t.Errorf("request %+v: anonymous, and never for a path dependency", r)
			}
		}
		if n := len(e.gh.PRs("acme/optout")); n != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestCargoWorkspaceLockAtRootIsReported(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.lock": "# lock\n",
			"crates/a/Cargo.toml": "[dependencies]\nserde = \"1.0.100\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.101")
		e.tick()
		e.tick()
		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "crates/a/Cargo.toml")
		if got != "[dependencies]\nserde = \"1.0.101\"\n" {
			t.Errorf("%s", got)
		}
		if !strings.Contains(body, "`Cargo.lock`") {
			t.Errorf("the workspace-root lock is not reported:\n%s", body)
		}
	})
}

func TestCargoReplacedSourceIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": "[dependencies]\nserde = \"1.0.100\"\n",
			".cargo/config.toml": "[source.crates-io]\nreplace-with = \"mirror\"\n[source.mirror]\nregistry = \"sparse+https://crates.corp.example/\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.101")
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Fatalf("a replaced source got %d PRs", n)
		}
		j, ok, err := e.st.GetJob(context.Background(), model.JobKey("acme/app", model.DatasourceCargo, "serde", "1.0.101"))
		if err != nil || !ok || j.State != model.JobSkipped || !strings.Contains(j.Reason, "config.toml") {
			t.Errorf("job = %+v, %v, %v", j, ok, err)
		}
	})
}

func TestCargoPrivateCrateIsSkippedAndOthersStillMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		idx.Publish("secret", "1.0.0")
		idx.Deny("secret")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig,
			"web/Cargo.toml": "[dependencies]\nserde = \"1.0.100\"\nsecret = \"1.0.0\"\nghost = \"2.0.0\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.101")
		idx.Publish("secret", "1.0.1")
		e.tick()
		e.tick()
		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "web/Cargo.toml")
		if want := "[dependencies]\nserde = \"1.0.101\"\nsecret = \"1.0.0\"\nghost = \"2.0.0\"\n"; got != want {
			t.Errorf("%s", got)
		}
		if strings.Contains(body, "Cargo.lock not regenerated") {
			t.Errorf("no lock exists, none should be reported:\n%s", body)
		}
	})
}

func TestCargoUpToDateOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		idx.Publish("serde", "2.0.0-rc.1")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": "[dependencies]\nserde = \"1.0.100\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an up-to-date repo got %d PRs", n)
		}
	})
}

// Partial requirements move at the precision written, and a 0.x crate's minor bump is a
// major update (cargo's rule), so a repo that ignores majors does not get it.
func TestCargoPartialRequirementsAndZeroVersionSemantics(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		for crate, v := range map[string]string{"part": "1.2.0", "whole": "1", "zero": "0.2.3", "pin": "0.0.3", "same": "0.4.0"} {
			idx.Publish(crate, v)
		}
		cfg := cargoConfig + "    ignore:\n      - dependency: \"*\"\n        update-types: [major]\n"
		manifest := "[dependencies]\npart = \"1.2\"\nwhole = \"1\"\nzero = \"0.2.3\"\npin = \"0.0.3\"\nsame = \"0.4\"\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cfg, "Cargo.toml": manifest})
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 5 {
			t.Fatalf("partial requirements are tracked, got %+v", e.deps("acme/app"))
		}
		e.clock.Advance(time.Minute)
		idx.Publish("part", "1.7.4")  // 1.2 -> 1.7
		idx.Publish("whole", "1.9.0") // "1" already allows it: nothing to change
		idx.Publish("zero", "0.3.0")  // major under cargo: ignored
		idx.Publish("zero", "0.2.9")  // the patch the repo may take
		idx.Publish("pin", "0.0.4")   // major under cargo: ignored
		idx.Publish("same", "0.4.8")  // "0.4" already names it
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want PRs for part and zero only, got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Cargo.toml")
			from, to := `part = "1.2"`, `part = "1.7"`
			if strings.Contains(pr.Head, "zero") {
				from, to = `zero = "0.2.3"`, `zero = "0.2.9"`
			} else if !strings.Contains(pr.Head, "part") {
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(manifest, from, to, 1); got != want {
				t.Errorf("%s:\n%s", pr.Head, got)
			}
		}
	})
}

// Comparators, wildcards and every TOML spelling of a dependency are read; an upper
// bound is never moved and a wildcard moves only to a version it does not match.
func TestCargoComparatorsWildcardsAndTOMLSpellings(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		for crate, v := range map[string]string{"cmp": "1.2.0", "capped": "1.2.0", "wild": "1.4.0", "multi": "1.0.0", "dotted": "2.0.0", "stuck": "1.0.0"} {
			idx.Publish(crate, v)
		}
		manifest := `[dependencies]
cmp = ">=1.2"      # at least
capped = ">=1.2, <2"
wild = "1.*"
multi = {
    version = "1.0.0", # kept
    features = ["a"],
}
dotted.version = "2.0.0"
dotted.default-features = false
stuck = "1.*"
`
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": manifest})
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 6 {
			t.Fatalf("all six are tracked, got %+v", e.deps("acme/app"))
		}
		e.clock.Advance(time.Minute)
		idx.Publish("cmp", "1.7.4")
		idx.Publish("capped", "1.9.1")
		idx.Publish("capped", "2.0.0") // beyond the upper bound: never a target
		idx.Publish("wild", "1.9.0")   // already matched by 1.*
		idx.Publish("wild", "2.1.0")
		idx.Publish("multi", "1.0.5")
		idx.Publish("dotted", "2.3.0")
		idx.Publish("stuck", "1.8.0") // matched by 1.*: nothing to do
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 5 {
			t.Fatalf("want five PRs (not stuck), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "Cargo.toml")
			var from, to string
			switch {
			case strings.Contains(pr.Head, "/cmp-"):
				from, to = `">=1.2"      # at least`, `">=1.7"      # at least`
			case strings.Contains(pr.Head, "/capped-"):
				from, to = `">=1.2, <2"`, `">=1.9, <2"`
			case strings.Contains(pr.Head, "/wild-"):
				from, to = `wild = "1.*"`, `wild = "2.*"`
			case strings.Contains(pr.Head, "/multi-"):
				from, to = `version = "1.0.0", # kept`, `version = "1.0.5", # kept`
			case strings.Contains(pr.Head, "/dotted-"):
				from, to = `dotted.version = "2.0.0"`, `dotted.version = "2.3.0"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(manifest, from, to, 1); got != want {
				t.Errorf("%s: only that requirement moves:\n%s", pr.Head, got)
			}
		}
	})
}

func TestCargoUnparsableManifestIsSkipped(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig,
			"bad/Cargo.toml": "[dependencies]\nserde = \"1.0.100\n", "good/Cargo.toml": "[dependencies]\nserde = \"1.0.100\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.101")
		e.tick()
		e.tick()
		head, _ := onePR(t, e, "acme/app", false)
		if got, _ := e.gh.FileAt("acme/app", head, "good/Cargo.toml"); !strings.Contains(got, "1.0.101") {
			t.Errorf("the readable manifest still moves:\n%s", got)
		}
	})
}

// A release that needs a newer compiler than the repo's rust-version is not proposed:
// the job is skipped with the reason, and a crate within the repo's compiler still moves.
func TestCargoRespectsRustVersion(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		for _, c := range []string{"new", "ok", "wsnew", "none"} {
			idx.Publish(c, "1.0.0")
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig,
			"Cargo.toml":          "[workspace]\nmembers = [\"crates/a\"]\n[workspace.package]\nrust-version = \"1.70\"\n",
			"crates/a/Cargo.toml": "[package]\nname = \"a\"\nrust-version.workspace = true\n[dependencies]\nwsnew = \"1.0.0\"\n",
			"app/Cargo.toml":      "[package]\nname = \"app\"\nrust-version = \"1.70\"\n[dependencies]\nnew = \"1.0.0\"\nok = \"1.0.0\"\n",
			"free/Cargo.toml":     "[package]\nname = \"free\"\n[dependencies]\nnone = \"1.0.0\"\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.PublishMSRV("new", "1.0.1", "1.80")
		idx.PublishMSRV("ok", "1.0.1", "1.65")
		idx.PublishMSRV("wsnew", "1.0.1", "1.71")
		idx.PublishMSRV("none", "1.0.1", "1.99") // no rust-version declared: nothing to compare
		e.tick()
		e.tick()
		var heads []string
		for _, pr := range e.gh.PRs("acme/app") {
			heads = append(heads, pr.Head)
		}
		if len(heads) != 2 || !strings.Contains(strings.Join(heads, " "), "/ok-") || !strings.Contains(strings.Join(heads, " "), "/none-") {
			t.Fatalf("want PRs for ok and none only, got %v", heads)
		}
		for _, crate := range []string{"new", "wsnew"} {
			j, ok, err := e.st.GetJob(context.Background(), model.JobKey("acme/app", model.DatasourceCargo, crate, "1.0.1"))
			if err != nil || !ok || j.State != model.JobSkipped || !strings.Contains(j.Reason, "rust-version") {
				t.Errorf("%s: job = %+v, %v, %v", crate, j, ok, err)
			}
		}
	})
}
