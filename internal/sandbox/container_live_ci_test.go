//go:build containerlive

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live tests of the Docker and Podman runners, run by the container-runtimes CI job with
// `-tags containerlive` and RIPPLE_TEST_CONTAINER_FLAVOUR=docker or podman. They are behind a
// build tag, not a skip, because CI fails on any skipped test; they fail (not skip) when the
// runtime is missing. They need the network: the image comes from a registry and the confined
// run fetches a module through the real proxy.golang.org.
func ciRuntime(t *testing.T) (Flavour, string, ContainerOptions) {
	t.Helper()
	fl := Flavour(os.Getenv("RIPPLE_TEST_CONTAINER_FLAVOUR"))
	if fl != FlavourDocker && fl != FlavourPodman {
		t.Fatalf("RIPPLE_TEST_CONTAINER_FLAVOUR must be docker or podman, got %q", fl)
	}
	bin, err := exec.LookPath(string(fl))
	if err != nil {
		t.Fatalf("%s is not installed: %v", fl, err)
	}
	image := os.Getenv("RIPPLE_TEST_CONTAINER_IMAGE")
	if image == "" {
		image = "docker.io/library/golang:1.25-alpine"
	}
	return fl, bin, ContainerOptions{Image: image}
}

func TestContainerRuntimeLive(t *testing.T) {
	fl, bin, o := ciRuntime(t)
	b, err := Container(fl, bin, o)
	if err != nil {
		t.Fatal(err)
	}
	checkContainerBackend(t, b, bin, fl)
}

// The commands of a confined policy reach the module proxy through the sidecar and nothing else.
func TestContainerConfinedNetworkLive(t *testing.T) {
	fl, bin, o := ciRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	conf, err := ConfineNetwork(ctx, fl, bin, ConfineOptions{Upstream: "https://proxy.golang.org"})
	if err != nil {
		t.Fatalf("ConfineNetwork: %v", err)
	}
	t.Cleanup(func() { conf.Close() })
	o.Network = conf.Network
	b, err := Container(fl, bin, o)
	if err != nil {
		t.Fatal(err)
	}
	work, _ := filepath.EvalSymlinks(t.TempDir())
	cache, _ := filepath.EvalSymlinks(t.TempDir())
	env := []string{"HOME=" + work, "TMPDIR=" + work, "GOPATH=" + work + "/gopath", "GOCACHE=" + cache, "GOENV=off",
		"GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOSUMDB=off", "GOFLAGS=-mod=mod -modcacherw", "GOPROXY=" + conf.ProxyURL}
	p := Policy{Workdir: work, Writable: []string{work, cache}, Env: env, Proxy: conf.ProxyURL}
	os.WriteFile(filepath.Join(work, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	const long = 5 * time.Minute

	if out, err := runLive(t, b, p, long, "go", "mod", "download", "-json", "golang.org/x/text@v0.3.0"); err != nil || !strings.Contains(out, "v0.3.0") {
		t.Fatalf("go could not fetch a module through the sidecar: %v\n%s", err, out)
	}
	for _, target := range []string{"http://1.1.1.1/", "https://proxy.golang.org/", "http://example.com/"} {
		if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "wget -q -T 5 -O /dev/null "+target); err == nil {
			t.Errorf("the confined network reached %s: %s", target, out)
		}
	}
	// the sidecar itself is reachable and answers its health location
	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "wget -q -T 5 -O - "+conf.ProxyURL+"/_ripple_health"); err != nil || out != "ok" {
		t.Errorf("sidecar health: %v %q", err, out)
	}
}

// The runner image carries git next to go (docker/runner/Dockerfile): the local git steps the engine
// runs in a container (checkout, clean, commit) work, with no network, as the caller's uid.
// The CI job runs it only against that image; the default go-only image has no git.
func TestContainerGitLive(t *testing.T) {
	fl, bin, o := ciRuntime(t)
	b, err := Container(fl, bin, o)
	if err != nil {
		t.Fatal(err)
	}
	work, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(work, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	p := Policy{Workdir: repo, Writable: []string{work}, Env: []string{"HOME=" + work, "TMPDIR=" + work, "GOTOOLCHAIN=local",
		"GIT_AUTHOR_NAME=ripple", "GIT_AUTHOR_EMAIL=r@example.com", "GIT_COMMITTER_NAME=ripple", "GIT_COMMITTER_EMAIL=r@example.com"}}
	const long = 5 * time.Minute // first run may pull the image
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "add", "."},
		{"git", "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "first"},
		{"git", "checkout", "--force", "main"},
		{"git", "clean", "-fdxq"},
	} {
		if out, err := runLive(t, b, p, long, argv...); err != nil {
			t.Fatalf("%v in the container: %v\n%s", argv, err, out)
		}
	}
	if out, err := runLive(t, b, p, time.Minute, "git", "log", "--format=%s"); err != nil || strings.TrimSpace(out) != "first" {
		t.Errorf("git log: %v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "HEAD")); err != nil {
		t.Errorf("the repository written in the container is not on the host: %v", err)
	}
	// the pinned Go is the image's own and satisfies GOTOOLCHAIN=local
	if out, err := runLive(t, b, p, time.Minute, "go", "version"); err != nil || !strings.Contains(out, "go version go1.") {
		t.Errorf("go version: %v %q", err, out)
	}
	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "wget -q -T 3 -O /dev/null http://1.1.1.1/"); err == nil {
		t.Errorf("local git ran with a network: %s", out)
	}
}
