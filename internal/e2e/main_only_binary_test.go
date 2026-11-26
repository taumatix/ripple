package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// ROADMAP 227: for a repository whose only package is `package main`, the verification
// step's `go build ./...` used to write the executable into the checkout, and the next
// `git add -A` committed it. The PR must carry the files the update changed and nothing
// the build produced.
func TestMainOnlyRepoPRCarriesNoBinary(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":  gomod("example.com/app", rLib+" v1.0.0"),
			"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
		})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.publish(rLib, "v1.1.0")
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		// `git show <branch>:` lists the root tree.
		tree, ok := e.gh.FileAt("acme/app", head, "")
		if !ok {
			t.Fatalf("cannot list the tree of %s", head)
		}
		var got []string
		for _, l := range strings.Split(strings.TrimSpace(tree), "\n")[1:] {
			if l = strings.TrimSpace(l); l != "" {
				got = append(got, l)
			}
		}
		want := "go.mod go.sum main.go"
		if strings.Join(got, " ") != want {
			t.Errorf("PR tree = %v, want only %q (a build output was committed)", got, want)
		}
	})
}
