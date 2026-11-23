//go:build containerlive

package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/sandbox"
)

// Live test of RIPPLE_CONTAINER_CONFINE against a real Docker or Podman, run by the
// container-runtimes CI job with -tags containerlive and RIPPLE_TEST_CONTAINER_FLAVOUR. It
// fails (not skips) when the runtime is missing.
func TestServerConfinesContainerNetworkLive(t *testing.T) {
	fl := os.Getenv("RIPPLE_TEST_CONTAINER_FLAVOUR")
	if fl != "docker" && fl != "podman" {
		t.Fatalf("RIPPLE_TEST_CONTAINER_FLAVOUR must be docker or podman, got %q", fl)
	}
	bin, err := exec.LookPath(fl)
	if err != nil {
		t.Fatalf("%s is not installed: %v", fl, err)
	}
	image := os.Getenv("RIPPLE_TEST_CONTAINER_IMAGE")
	if image == "" {
		image = "docker.io/library/golang:1.25-alpine"
	}
	s := &config.Server{Sandbox: fl, ContainerImage: image, ContainerConfine: true, GoProxy: "https://proxy.golang.org"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// A server killed outright never removed its confinement: the next start replaces it.
	if _, _, err := newSandbox(ctx, s, log, nil); err != nil {
		t.Fatalf("first start: %v", err)
	}
	b, closeSB, err := newSandbox(ctx, s, log, nil)
	if err != nil {
		t.Fatalf("a start after a server that never cleaned up: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			closeSB()
		}
	})

	work, _ := filepath.EvalSymlinks(t.TempDir())
	cache, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(work, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	// The job's own GOPROXY names the real proxy, as the engine's does: the container must be pointed at the sidecar anyway.
	env := []string{"HOME=" + work, "TMPDIR=" + work, "GOPATH=" + work + "/gopath", "GOCACHE=" + cache, "GOENV=off",
		"GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOSUMDB=off", "GOFLAGS=-mod=mod -modcacherw", "GOPROXY=https://proxy.golang.org"}
	p := sandbox.Policy{Workdir: work, Writable: []string{work, cache}, Env: env, Proxy: "https://proxy.golang.org"}
	run := func(argv ...string) (string, error) {
		w, err := b.Wrap(p, argv)
		if err != nil {
			t.Fatal(err)
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(c, w[0], w[1:]...)
		cmd.Env = b.(sandbox.ClientEnver).ClientEnv(env)
		out, err := cmd.CombinedOutput()
		t.Logf("%v: %s", argv, out)
		return string(out), err
	}
	if out, err := run("go", "mod", "download", "-json", "golang.org/x/text@v0.3.0"); err != nil || !strings.Contains(out, "v0.3.0") {
		t.Fatalf("go could not fetch a module through the server's confined network: %v\n%s", err, out)
	}
	for _, target := range []string{"http://1.1.1.1/", "http://example.com/"} {
		if out, err := run("sh", "-c", "wget -q -T 5 -O /dev/null "+target); err == nil {
			t.Errorf("the confined network reached %s: %s", target, out)
		}
	}

	closeSB()
	closed = true
	out, _ := exec.Command(bin, "network", "ls", "--format", "{{.Name}}").CombinedOutput()
	if strings.Contains(string(out), confineName) {
		t.Errorf("the confined network survived close:\n%s", out)
	}
	if out, _ := exec.Command(bin, "ps", "-a", "--format", "{{.Names}}").CombinedOutput(); strings.Contains(string(out), confineName+"-proxy") {
		t.Errorf("the sidecar survived close:\n%s", out)
	}
}
