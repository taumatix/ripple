package e2e

import (
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A package registry that answers a poll with 429 and Retry-After does not cost the watcher
// its update: the client waits the named time and the same tick opens the PR. Without the wait
// the read is an error, the watcher skips the package, and the PR would only come a poll later.
func TestNpmWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("@types/node", "20.1.0")
		reg.Publish("jest", "29.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": npmPackageJSON, "package-lock.json": "{}\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		reg.Publish("left-pad", "1.5.0")
		reg.Refuse("left-pad", 1, 429, "0")
		e.tick()
		if reg.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d: the 429 should be waited out and left-pad bumped in the same tick", reg.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}

func TestCargoWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := cargoEnv(t, st)
		idx.Publish("serde", "1.0.100")
		idx.Publish("tokio", "1.2.3")
		idx.Publish("pretty_assertions", "1.4.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cargoConfig, "Cargo.toml": cargoManifest, "Cargo.lock": "# lock\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		idx.Publish("serde", "1.0.200")
		idx.Refuse("serde", 1, 429, "0")
		e.tick()
		if idx.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d", idx.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}

func TestPipWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		for _, p := range [][2]string{{"requests", "2.31.0"}, {"flask-login", "0.6.2"}, {"click", "8.0"}} {
			idx.Publish(p[0], p[1])
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": pipReqs,
			"svc/pyproject.toml": pipPyproject, "poetry.lock": "# lock\n"})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		idx.Publish("click", "8.1.7")
		idx.Refuse("/pypi/click/", 1, 429, "0")
		e.tick()
		if idx.Refused() != 1 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d", idx.Refused(), len(e.gh.PRs("acme/app")))
		}
	})
}
