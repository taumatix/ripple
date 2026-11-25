package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// Entry 127: a release is no reason to move a module off a pseudo-version (a commit someone
// chose) unless the update entry says from-pseudo-versions: true. Then the first release
// above the commit that was released after the commit's timestamp is proposed, never
// automerged, and the PR says it leaves a pseudo-version. A release older than the commit
// cannot contain it, so it is not proposed.
func TestAReleaseMovesAPseudoVersionDependencyOnlyWhereOptedIn(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{})
		const future = "v1.0.1-0.20991231000000-abcdefabcdef" // a commit newer than any release below
		stubModule(e, "example.com/lib", pseudoCurrent, "v1.0.1", "v1.1.0")
		repo := func(pseudo, cfg string) map[string]string {
			return map[string]string{
				"go.mod":  gomod("example.com/app", "example.com/lib "+pseudo),
				"main.go": blankImports("example.com/lib"), ".ripple.yaml": cfg,
			}
		}
		on := "updates:\n  - ecosystem: gomod\n    automerge: major\n    from-pseudo-versions: true\n"
		e.gh.AddRepo("acme/optin", repo(pseudoCurrent, on))
		e.gh.AddRepo("acme/off", repo(pseudoCurrent, "updates:\n  - ecosystem: gomod\n"))
		e.gh.AddRepo("acme/newer", repo(future, on))
		start(t, e, "acme/optin", "acme/off", "acme/newer")

		e.clock.Advance(time.Hour)
		e.publish("example.com/lib", "v1.0.1")
		e.tick()
		e.clock.Advance(time.Hour)
		e.tick()

		_, body := onePR(t, e, "acme/optin", false)
		if !strings.Contains(body, "moves off the pseudo-version `"+pseudoCurrent+"`") {
			t.Errorf("body does not say the dependency leaves a pseudo-version:\n%s", body)
		}
		if p := e.gh.PRs("acme/optin")[0]; p.AutoMerge || p.Title != "Update example.com/lib to v1.0.1" {
			t.Errorf("PR %q automerge=%v", p.Title, p.AutoMerge)
		}
		for _, r := range []string{"acme/off", "acme/newer"} {
			if n := len(e.gh.PRs(r)); n != 0 {
				t.Errorf("%s got %d PRs", r, n)
			}
		}
	})
}

// A repo added after the releases came out gets the first release that can contain the
// commit, not the newest one.
func TestTheBackfillMovesAPseudoVersionToTheFirstReleaseAfterTheCommit(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{})
		stubModule(e, "example.com/lib", "v1.0.1", "v1.0.2", "v1.1.0")
		e.gh.AddRepo("acme/older", map[string]string{
			"go.mod":  gomod("example.com/other", "example.com/lib v1.0.0"),
			"main.go": blankImports("example.com/lib"),
		})
		start(t, e, "acme/older") // watches the module so releases are stored
		e.publish("example.com/lib", "v1.0.1")
		e.publish("example.com/lib", "v1.0.2")
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		e.gh.AddRepo("acme/late", map[string]string{
			"go.mod":       gomod("example.com/app", "example.com/lib "+pseudoCurrent),
			"main.go":      blankImports("example.com/lib"),
			".ripple.yaml": "updates:\n  - ecosystem: gomod\n    from-pseudo-versions: true\n",
		})
		e.install("acme/late")
		e.clock.Advance(time.Hour)
		e.tick()
		e.clock.Advance(time.Hour)
		e.tick()
		if _, body := onePR(t, e, "acme/late", false); !strings.Contains(body, "| `example.com/lib` | `"+pseudoCurrent+"` | `v1.0.1` |") {
			t.Errorf("want the first release, v1.0.1:\n%s", body)
		}
	})
}
