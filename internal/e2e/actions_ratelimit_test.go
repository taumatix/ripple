package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A GitHub that answers the tag read of an action with a 429 does not cost the run its update:
// the client waits the time Retry-After names and the same tick opens the PR. Without the wait
// the read is an error and the bump would only come a poll later.
func TestActionsWatcherSurvivesA429(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		actionsUpstream(e, "up/action", "v4", "v4.1.0")
		ci := "jobs:\n  a:\n    steps:\n      - uses: up/action@v4\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": actionsConfig, ".github/workflows/ci.yml": ci})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		addTags(e, "up/action", "v5", "v5.0.0")
		e.gh.Release("up/action", "v5", t0.Add(-time.Hour))
		e.gh.Refuse("/repos/up/action/tags", 1, 429, "0")
		e.gh.Refuse("/repos/up/action/git/ref/tags/", 1, 403, "0")
		e.tick()
		if e.gh.Refused() != 2 || len(e.gh.PRs("acme/app")) != 1 {
			t.Fatalf("refused %d, PRs %d: both limits should be waited out and the action bumped in the same tick", e.gh.Refused(), len(e.gh.PRs("acme/app")))
		}
		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, ".github/workflows/ci.yml")
		if !strings.Contains(got, "up/action@v5") {
			t.Errorf("workflow not bumped:\n%s", got)
		}
	})
}
