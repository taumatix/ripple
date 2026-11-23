package e2e

import (
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// A repo whose .ripple.yaml stops parsing has its jobs skipped, so the dashboard has to
// say why nothing moves; fixing the file takes the notice away again.
func TestDashboardSaysWhenTheConfigIsInvalidAndClearsItWhenFixed(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if is := e.gh.Issues(repo); len(is) != 1 || strings.Contains(is[0].Body, "Configuration invalid") {
			t.Fatalf("want one clean dashboard first: %+v", is)
		}

		scans := e.logLines("scanned", repo)
		e.push(repo, map[string]string{".ripple.yaml": optIn + "schedule: [unterminated\n"})
		e.eventually("the invalid file to be scanned", func() bool { return e.logLines("scanned", repo) > scans || e.logLines("invalid repo config", repo) > 0 })
		e.eventually("the notice on the dashboard", func() bool {
			is := e.gh.Issues(repo)
			return len(is) == 1 && strings.Contains(is[0].Body, "## Configuration invalid")
		})
		is := e.gh.Issues(repo)
		if !strings.Contains(is[0].Body, "no updates until it is fixed") || !strings.Contains(is[0].Body, "## Deferred (1)") {
			t.Errorf("the notice must explain itself and keep the open jobs listed:\n%s", is[0].Body)
		}

		e.push(repo, map[string]string{".ripple.yaml": optIn})
		e.eventually("the notice to go", func() bool {
			is := e.gh.Issues(repo)
			return len(is) == 1 && !strings.Contains(is[0].Body, "Configuration invalid")
		})
	})
}
