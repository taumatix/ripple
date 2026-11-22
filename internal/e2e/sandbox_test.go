package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
)

// TestDependencyUpdateUnderOSSandbox drives the whole webhook-to-PR path with go and
// local git confined by the platform's real backend. It needs one that can confine
// the network to a proxy port (seatbelt); bwrap cannot, so Linux skips.
func TestDependencyUpdateUnderOSSandbox(t *testing.T) {
	sb, err := sandbox.Detect("seatbelt", nil)
	if err != nil {
		t.Skipf("no seatbelt: %v", err)
	}
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb})
	baseRepos(e)
	start(t, e, "acme/uses-lib")

	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	e.tick()

	prs := e.gh.PRs("acme/uses-lib")
	if len(prs) != 1 {
		t.Fatalf("want 1 PR under the sandbox, got %d\nlogs:\n%s", len(prs), e.logs.String())
	}
	got, _ := e.gh.FileAt("acme/uses-lib", prs[0].Head, "go.mod")
	if !strings.Contains(got, "\texample.com/lib v1.1.0\n") {
		t.Errorf("go.mod lacks the bump:\n%s", got)
	}
}
