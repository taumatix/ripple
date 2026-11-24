package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// An upper bound spelled as a post release (`<1.0.post1`) admits the final 1.0 and
// nothing later: packaging's SpecifierSet says so (internal/pypi/testdata/pep440.json),
// and the target of a range follows it.
func TestPipRangeBoundedByPostReleaseTakesTheFinalRelease(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, idx := pipEnv(t, st)
		idx.Publish("pkg", "0.5")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": pipConfig, "requirements.txt": "pkg>=0.5,<1.0.post1\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		for _, v := range []string{"0.9", "1.0", "1.0.post1", "1.1"} {
			idx.Publish("pkg", v)
		}
		e.tick()
		e.tick()
		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "requirements.txt")
		if want := "pkg>=1.0,<1.0.post1\n"; got != want || !strings.HasPrefix(head, "ripple/pip/pkg-1.0") {
			t.Errorf("%s: %q, want %q", head, got, want)
		}
	})
}
