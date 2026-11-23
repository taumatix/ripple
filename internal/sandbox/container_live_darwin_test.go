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

// Live tests of the Apple `container` backend. This is a _darwin_test.go file because CI
// fails on any skipped test and its Linux runners lack the CLI. They skip when the CLI is absent or its
// system service is not running, so they never run in CI (GitHub's hosted runners have no
// Apple container runtime); they run on a developer's Apple-silicon Mac. The image
// (RIPPLE_TEST_CONTAINER_IMAGE, default golang:1.25-alpine, about 200 MB) is pulled on
// first use and left in place; `container image prune -a` removes it.
func liveContainer(t *testing.T) (Backend, string) {
	t.Helper()
	bin, err := exec.LookPath("container")
	if err != nil {
		t.Skip("Apple container CLI not installed")
	}
	if out, err := exec.Command(bin, "system", "status").CombinedOutput(); err != nil || !strings.Contains(string(out), "running") {
		t.Skipf("container system service not running: %v %s", err, out)
	}
	image := os.Getenv("RIPPLE_TEST_CONTAINER_IMAGE")
	if image == "" {
		image = "docker.io/library/golang:1.25-alpine"
	}
	b, err := Container(FlavourApple, bin, ContainerOptions{Image: image})
	if err != nil {
		t.Fatal(err)
	}
	return b, bin
}

// lastWrapped is the argv of the most recent runLive, so a test can name its container.
var lastWrapped []string

func runLive(t *testing.T, b Backend, p Policy, timeout time.Duration, argv ...string) (string, error) {
	t.Helper()
	w, err := b.Wrap(p, argv)
	if err != nil {
		t.Fatal(err)
	}
	lastWrapped = w
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, w[0], w[1:]...)
	cmd.Env = append(os.Environ(), p.Env...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		b.(Reaper).Reap(w)
	}
	t.Logf("%v: %s", argv, out)
	return string(out), err
}

func TestAppleContainerLive(t *testing.T) {
	b, bin := liveContainer(t)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	cache, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(work, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	secret := filepath.Join(outside, "key.pem")
	os.WriteFile(secret, []byte("SECRET"), 0o600)
	p := Policy{Workdir: repo, Writable: []string{work, cache}, Env: []string{
		"HOME=" + work, "TMPDIR=" + work, "GOPATH=" + work + "/gopath", "GOCACHE=" + cache, "GOENV=off",
		"GOTOOLCHAIN=local", "GOFLAGS=-buildvcs=false", "CGO_ENABLED=0", "GOPROXY=off"}}
	const long = 5 * time.Minute // first run may pull the image

	if _, err := runLive(t, b, p, long, "/host/path/to/go", "build", "./..."); err != nil {
		t.Fatalf("go build in the container: %v", err)
	}
	if ents, _ := os.ReadDir(cache); len(ents) == 0 {
		t.Error("the writable cache mount received nothing")
	}
	if _, err := runLive(t, b, p, long, "go", "mod", "tidy"); err != nil {
		t.Fatalf("go mod tidy: %v", err)
	}
	fi, err := os.Stat(filepath.Join(repo, "go.mod"))
	if err != nil || fi.Sys() == nil {
		t.Fatalf("go.mod: %v", err)
	}

	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "touch /etc/pwned"); err == nil {
		t.Errorf("wrote to the read-only root: %s", out)
	}
	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "touch "+repo+"/ok "+cache+"/ok"); err != nil {
		t.Errorf("workspace and cache must be writable: %v %s", err, out)
	}
	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "wget -q -T 3 -O /dev/null http://1.1.1.1/"); err == nil {
		t.Errorf("a policy without a proxy reached the network: %s", out)
	}
	if out, err := runLive(t, b, p, time.Minute, "sh", "-c", "test -e "+secret); err == nil {
		t.Errorf("a host file outside the mounts is visible: %s", out)
	}
	for _, f := range []string{"ok"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("file written in the container is not on the host: %v", err)
		}
	}

	// A timeout kills the client, not the container; Reap must stop it and remove it.
	// Apple's --rm is carried out by the client, which the timeout has just SIGKILLed,
	// so without an explicit remove roughly half of the killed containers stayed behind
	// as "stopped" (measured; see Reap). Only this run's container is checked: debris
	// from another session is reported but is not this test's failure.
	if _, err := runLive(t, b, p, 4*time.Second, "sleep", "300"); err == nil {
		t.Fatal("sleep finished")
	}
	mine := ""
	for i, a := range lastWrapped {
		if a == "--name" && i+1 < len(lastWrapped) {
			mine = lastWrapped[i+1]
		}
	}
	if mine == "" {
		t.Fatalf("no container name in %v", lastWrapped)
	}
	t.Cleanup(func() { _ = exec.Command(bin, "rm", "-f", mine).Run() })
	out, _ := exec.Command(bin, "ls", "-a").CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, mine):
			t.Errorf("the container survived its timeout (Reap left it): %s\nall containers:\n%s", l, out)
		case strings.HasPrefix(l, "ripple-"):
			t.Logf("note: a container from another run is present: %s", l)
		}
	}
}

// Reap straight after the client dies, which is what a timeout in the engine does (the
// test above waits out WaitDelay first and so rarely hits it). Measured by hand with
// the CLI: when the client is SIGKILLed and the container then killed at once, about
// half of the containers stayed "stopped" because --rm is the client's job, not the
// service's.
func TestAppleContainerReapAfterClientKilled(t *testing.T) {
	b, bin := liveContainer(t)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	p := Policy{Workdir: work, Writable: []string{work}, Env: []string{"HOME=" + work}}
	for i := 0; i < 8; i++ {
		w, err := b.Wrap(p, []string{"sleep", "300"})
		if err != nil {
			t.Fatal(err)
		}
		name := w[slicesIndex(w, "--name")+1]
		t.Cleanup(func() { _ = exec.Command(bin, "rm", "-f", name).Run() })
		cmd := exec.Command(w[0], w[1:]...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(500+i*400) * time.Millisecond) // from still starting to running
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		b.(Reaper).Reap(w)
		time.Sleep(2 * time.Second) // let the service settle before looking
		out, _ := exec.Command(bin, "ls", "-a").CombinedOutput()
		if strings.Contains(string(out), name) {
			t.Errorf("run %d: Reap left %s behind:\n%s", i, name, out)
		}
	}
}

func slicesIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
