package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
)

// TestDependencyUpdateUnderOSSandbox drives the whole webhook-to-PR path with go and
// local git confined by the platform's real backend. It needs one that can confine
// the network to a proxy port (seatbelt); bwrap cannot, so this file is macOS only
// (the CI service-backed check forbids skipped tests, so it is not skipped at run time).
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
	if !strings.Contains(got, "example.com/lib v1.1.0") {
		t.Errorf("go.mod lacks the bump:\n%s", got)
	}
}

// cgo and a proxy credential under the real seatbelt: the C compiler runs confined, and
// the gate (not go's environment) authenticates the fetch.
func TestCgoAndProxyCredentialUnderOSSandbox(t *testing.T) {
	sb, err := sandbox.Detect("seatbelt", nil)
	if err != nil {
		t.Skipf("no seatbelt: %v", err)
	}
	needCC(t)
	e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb, engine: func(en *engine.Engine) {
		en.GoProxyUser, en.GoProxyToken = "ripple", proxyToken
	}})
	e.proxy.RequireAuth("ripple", proxyToken)
	cgoRepo(t, e, map[string]string{"main.go": cgoOnly}, "    cgo: true\n")
	_, body := onePR(t, e, "acme/app", false)
	if strings.Contains(body, "could not verify") || strings.Contains(body, proxyToken) {
		t.Errorf("body:\n%s", body)
	}
}
