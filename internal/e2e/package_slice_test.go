package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Entry 190: Engine.PollPackagesPerPoll (RIPPLE_POLL_PACKAGES_PER_POLL) bounds how many
// packages of a datasource one poll visits. Each poll takes the next slice after where the
// last one stopped and wraps, so every package is visited once per cycle and the registry
// sees at most that many package requests per poll. Unset, a poll visits them all.

const sliceJSON = `{"dependencies": {"alpha": "^1.0.0", "bravo": "^1.0.0", "charlie": "^1.0.0"}}` + "\n"

func sliceEnv(t *testing.T, st store.Store, perPoll int) (*env, func() []string) {
	reg := testutil.NewNpmRegistry(t)
	e := newEnvOpts(t, st, defaultLimits(), opts{npm: reg, engine: func(g *engine.Engine) { g.PollPackagesPerPoll = perPoll }})
	for _, p := range []string{"alpha", "bravo", "charlie"} {
		reg.Publish(p, "1.0.0")
	}
	e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": sliceJSON})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	for _, p := range []string{"alpha", "bravo", "charlie"} {
		reg.Publish(p, "1.1.0")
	}
	seen := len(reg.Requests())
	return e, func() []string {
		var out []string
		for _, r := range reg.Requests()[seen:] {
			out = append(out, strings.TrimPrefix(r.Path, "/"))
		}
		seen = len(reg.Requests())
		return out
	}
}

func TestPackagePollVisitsASliceEachPollAndWrapsAround(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, polled := sliceEnv(t, st, 1)
		var order []string
		for i := 0; i < 4; i++ {
			e.tick()
			got := polled()
			if len(got) != 1 {
				t.Fatalf("poll %d asked the registry about %v, want one package", i, got)
			}
			order = append(order, got[0])
		}
		// start() already ran one poll (alpha), so the rotation continues from bravo.
		if want := "bravo charlie alpha bravo"; strings.Join(order, " ") != want {
			t.Fatalf("visited %v, want %s", order, want)
		}
		// Slicing delays a package's check; it never drops it: all three were bumped.
		if n := len(e.gh.PRs("acme/app")); n != 3 {
			t.Fatalf("%d PRs after a full cycle, want 3", n)
		}
	})
}

func TestPackagePollVisitsEveryPackageWhenUnbounded(t *testing.T) {
	e, polled := sliceEnv(t, store.NewMemory(), 0)
	e.tick()
	if got := polled(); len(got) != 3 {
		t.Fatalf("default poll asked about %v, want all three", got)
	}
}
