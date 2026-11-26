package sandbox

import (
	"os"
	"testing"
)

// The test binary doubles as the in-sandbox relay of the bwrap backend: bwrapWithSelf
// binds os.Executable() into the sandbox and starts it as `sandbox-relay ...`.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == RelayArg {
		os.Exit(RunRelay(os.Args[2:], os.Stderr))
	}
	os.Exit(m.Run())
}
