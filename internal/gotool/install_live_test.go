//go:build live

package gotool

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestLiveInstallsARealRelease downloads a real Go release from proxy.golang.org, checked
// against sum.golang.org, and runs it: the zip extraction leaves no exec bits, so this is
// what proves the installer's chmod gives a go that starts.
func TestLiveInstallsARealRelease(t *testing.T) {
	dir := t.TempDir()
	i := &Installer{Dir: dir, Proxy: "https://proxy.golang.org"}
	bin, rel, err := i.Ensure(context.Background(), "1.21", "go1.21.0")
	if err != nil || rel != "1.21.0" {
		t.Fatalf("Ensure: %q %q %v", bin, rel, err)
	}
	cmd := exec.Command(bin, "version")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "go1.21.0") {
		t.Fatalf("go version: %q %v", out, err)
	}
	// A second call finds it without the network.
	i.Proxy = "http://127.0.0.1:1"
	if b2, _, err := i.Ensure(context.Background(), "1.21", "go1.21.0"); err != nil || b2 != bin {
		t.Fatalf("second Ensure: %q %v", b2, err)
	}
}
