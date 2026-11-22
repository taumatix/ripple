package e2e

import (
	"fmt"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// A repo over the file cap is registered with no dependencies and a visible warning;
// a normal repo next to it is scanned and updated as usual.
func TestRepoOverFileCapIsSkipped(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.app.Engine.MaxFiles = 20
		big := map[string]string{"go.mod": gomod("example.com/big", "example.com/lib v1.0.0")}
		for i := 0; i < 40; i++ {
			big[fmt.Sprintf("data/f%02d.txt", i)] = "x\n"
		}
		e.gh.AddRepo("acme/big", big)
		e.gh.AddRepo("acme/small", map[string]string{"go.mod": gomod("example.com/small", "example.com/lib v1.0.0")})
		start(t, e, "acme/big", "acme/small")

		if n := len(e.deps("acme/big")); n != 0 {
			t.Errorf("over-cap repo has %d dependencies, want 0", n)
		}
		if n := e.logLines("exceeds the file cap", "repo=acme/big"); n != 1 {
			t.Errorf("file cap skip logged %d times, want 1\n%s", n, e.logs.String())
		}
		if n := len(e.deps("acme/small")); n != 1 {
			t.Errorf("normal repo has %d dependencies, want 1", n)
		}
		e.release()
		if n := len(e.gh.PRs("acme/big")); n != 0 {
			t.Errorf("over-cap repo got %d PRs", n)
		}
		if n := len(e.gh.PRs("acme/small")); n != 1 {
			t.Errorf("normal repo got %d PRs, want 1", n)
		}
	})
}

func TestRepoOverDependencyCapIsSkipped(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.app.Engine.MaxDependencies = 2
		e.gh.AddRepo("acme/wide", map[string]string{"go.mod": gomod("example.com/wide",
			"example.com/lib v1.0.0", "example.com/a v1.0.0", "example.com/b v1.0.0")})
		e.gh.AddRepo("acme/ok", map[string]string{"go.mod": gomod("example.com/ok", "example.com/lib v1.0.0", "example.com/a v1.0.0")})
		start(t, e, "acme/wide", "acme/ok")

		if n := len(e.deps("acme/wide")); n != 0 {
			t.Errorf("over-cap repo has %d dependencies, want 0", n)
		}
		if n := e.logLines("exceeds the dependency cap", "repo=acme/wide"); n != 1 {
			t.Errorf("dependency cap skip logged %d times, want 1\n%s", n, e.logs.String())
		}
		if n := len(e.deps("acme/ok")); n != 2 {
			t.Errorf("repo at the cap has %d dependencies, want 2 (cap is inclusive)", n)
		}
		e.release()
		if n := len(e.gh.PRs("acme/wide")); n != 0 {
			t.Errorf("over-cap repo got %d PRs", n)
		}
		if n := len(e.gh.PRs("acme/ok")); n != 1 {
			t.Errorf("repo at the cap got %d PRs, want 1", n)
		}
	})
}

// With no cap configured the defaults apply and a repo with many files is fine.
func TestDefaultCapsLeaveNormalReposAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		files := map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0")}
		for i := 0; i < 300; i++ {
			files[fmt.Sprintf("d/f%03d.txt", i)] = "x\n"
		}
		e.gh.AddRepo("acme/app", files)
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 1 {
			t.Fatalf("dependencies = %d, want 1", n)
		}
		if n := e.logLines("exceeds"); n != 0 {
			t.Errorf("unexpected cap warning: %d", n)
		}
	})
}
