package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A store written by an earlier version holds the polled npm packages in one newline cursor,
// naming a package nobody uses any more. Running the app over it folds the list into
// per-package keys, deletes it, prunes the unused package without asking the registry about
// it, and still polls and bumps the package a repo does use.
func TestLegacyPackageListIsFoldedAndPrunedByTheApp(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		ctx := context.Background()
		if _, ok := st.(store.CursorLister); !ok {
			t.Fatal("every shipped store lists cursors")
		}
		if err := st.SetCursor(ctx, "npm:packages", "ghost\nleft-pad"); err != nil {
			t.Fatal(err)
		}
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("ghost", "1.0.0")
		pj := `{"dependencies": {"left-pad": "^1.2.3"}}` + "\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": pj})
		start(t, e, "acme/app")

		e.clock.Advance(time.Minute)
		reg.Publish("left-pad", "1.3.0")
		e.tick()

		if raw, _ := st.GetCursor(ctx, "npm:packages"); raw != "" {
			t.Errorf("legacy cursor survived: %q", raw)
		}
		cs, err := st.(store.CursorLister).ListCursors(ctx, "npm:packages#")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range cs {
			names = append(names, c.Name)
		}
		if len(names) != 1 || names[0] != "npm:packages#left-pad" {
			t.Errorf("per-package keys = %v, want only left-pad", names)
		}
		for _, r := range reg.Requests() {
			if strings.Contains(r.Path, "ghost") {
				t.Errorf("registry asked about an unused package: %+v", r)
			}
		}
		head, _ := onePR(t, e, "acme/app", false)
		if got, _ := e.gh.FileAt("acme/app", head, "package.json"); !strings.Contains(got, `"^1.3.0"`) {
			t.Errorf("left-pad not bumped:\n%s", got)
		}
	})
}
