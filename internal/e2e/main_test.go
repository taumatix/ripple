package e2e

import (
	"os"
	"testing"

	"github.com/taumatix/ripple/internal/sandbox"
)

// The test binary is the in-sandbox relay of the bwrap backend (sandbox.Detect binds
// os.Executable() into the sandbox); ripple's own main does the same dispatch.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sandbox.RelayArg {
		os.Exit(sandbox.RunRelay(os.Args[2:], os.Stderr))
	}
	os.Exit(m.Run())
}
