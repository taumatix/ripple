package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
)

// TestDependencyUpdateUnderBwrap drives the whole webhook-to-PR path with go and local
// git confined by real bubblewrap: go reaches the module proxy only through the gate's
// unix socket and the in-sandbox relay. RIPPLE_REQUIRE_BWRAP=1 (the CI sandbox-bwrap
// job) turns a missing bwrap into a failure instead of a skip.
func TestDependencyUpdateUnderBwrap(t *testing.T) {
	sb, err := sandbox.Detect("bwrap", nil)
	if err != nil {
		if os.Getenv("RIPPLE_REQUIRE_BWRAP") == "1" {
			t.Fatalf("bwrap required but unavailable: %v", err)
		}
		t.Skipf("no bwrap: %v", err)
	}
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb})
	baseRepos(e)
	start(t, e, "acme/uses-lib")

	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()

	prs := e.gh.PRs("acme/uses-lib")
	if len(prs) != 1 {
		t.Fatalf("want 1 PR under bwrap, got %d\nlogs:\n%s", len(prs), e.logs.String())
	}
	if strings.Contains(prs[0].Body, "could not verify") {
		t.Errorf("verification did not run under bwrap:\n%s", prs[0].Body)
	}
	got, _ := e.gh.FileAt("acme/uses-lib", prs[0].Head, "go.mod")
	if !strings.Contains(got, "example.com/lib v1.1.0") {
		t.Errorf("go.mod lacks the bump:\n%s", got)
	}
	sum, _ := e.gh.FileAt("acme/uses-lib", prs[0].Head, "go.sum")
	if !strings.Contains(sum, "example.com/lib v1.1.0 h1:") {
		t.Errorf("go.sum was not refreshed through the gate:\n%s", sum)
	}
}
