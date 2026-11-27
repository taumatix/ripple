package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
)

// bwrapBackend is the real bwrap backend, or a skip where it is missing (a failure under
// RIPPLE_REQUIRE_BWRAP=1).
func bwrapBackend(t *testing.T) sandbox.Backend {
	t.Helper()
	sb, err := sandbox.Detect("bwrap", nil)
	if err != nil {
		if os.Getenv("RIPPLE_REQUIRE_BWRAP") == "1" {
			t.Fatalf("bwrap required but unavailable: %v", err)
		}
		t.Skipf("no bwrap: %v", err)
	}
	return sb
}

// TestDependencyUpdateUnderBwrap drives the whole webhook-to-PR path with go and local
// git confined by real bubblewrap: go reaches the module proxy only through the gate's
// unix socket and the in-sandbox relay. RIPPLE_REQUIRE_BWRAP=1 (the CI sandbox-bwrap
// job) turns a missing bwrap into a failure instead of a skip.
func TestDependencyUpdateUnderBwrap(t *testing.T) {
	sb := bwrapBackend(t)
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

// cgo: true under real bwrap (ROADMAP 1): the host's C compiler runs inside the sandbox, with
// only the system trees, the Go toolchain and the workspace visible. A cgo-only package builds
// only if gcc and cgo really ran there, so the green PR is the proof; a C error in the same
// package is still a failed verification, not "could not verify" (the sandbox did not hide
// the compiler). The CI job installs gcc and libc6-dev itself; where cc is missing this skips
// outside RIPPLE_REQUIRE_BWRAP=1, and fails inside it.
func TestCgoBuildUnderBwrap(t *testing.T) {
	sb := bwrapBackend(t)
	if _, err := exec.LookPath("cc"); err != nil {
		if os.Getenv("RIPPLE_REQUIRE_BWRAP") == "1" {
			t.Fatalf("cc required but unavailable: %v", err)
		}
		t.Skip("no C compiler on this machine")
	}
	t.Run("builds", func(t *testing.T) {
		e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb})
		cgoRepo(t, e, map[string]string{"main.go": cgoOnly}, "    cgo: true\n")
		_, body := onePR(t, e, "acme/app", false)
		if strings.Contains(body, "cgo off") || strings.Contains(body, "could not verify") {
			t.Errorf("a cgo build under bwrap should verify cleanly:\n%s\nlogs:\n%s", body, e.logs.String())
		}
	})
	t.Run("a C error is a failed verification", func(t *testing.T) {
		e := newEnvOpts(t, store.NewMemory(), defaultLimits(), opts{sandbox: sb})
		bad := strings.Replace(cgoOnly, "return 1;", "return undeclared_symbol;", 1)
		cgoRepo(t, e, map[string]string{"main.go": bad}, "    cgo: true\n")
		_, body := onePR(t, e, "acme/app", true)
		if !strings.Contains(body, "Verification failed") || !strings.Contains(body, "undeclared_symbol") {
			t.Errorf("the compiler's own error should reach the PR:\n%s", body)
		}
	})
}
