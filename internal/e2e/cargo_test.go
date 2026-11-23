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
