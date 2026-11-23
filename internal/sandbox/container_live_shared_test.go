//go:build darwin || containerlive

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// listAll is the argv that lists every container, stopped ones included, one name first
// per line (Apple prints a table whose first column is the id, which is the name).
func listAll(fl Flavour) []string {
	if fl == FlavourApple {
		return []string{"ls", "-a"}
	}
	return []string{"ps", "-a", "--format", "{{.Names}}"}
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
	cmd.Env = append(os.Environ(), clientEnv(p.Env)...)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		b.(Reaper).Reap(w)
	}
	t.Logf("%v: %s", argv, out)
	return string(out), err
}

// checkContainerBackend is the live check every container runner must pass: go build and
// tidy, a read-only root, writable mounts, no network without a proxy, no host files, and
// the reap of a timed-out container.
func checkContainerBackend(t *testing.T, b Backend, bin string, fl Flavour) {
	t.Helper()
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
	out, _ := exec.Command(bin, listAll(fl)...).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, mine):
			t.Errorf("the container survived its timeout (Reap left it): %s\nall containers:\n%s", l, out)
		case strings.HasPrefix(l, "ripple-"):
			t.Logf("note: a container from another run is present: %s", l)
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

// clientEnv is the environment the container client runs with. The runner's own HOME stays: the
// engine's policy sets HOME to the job directory, and rootless Podman keeps its image storage
// under $HOME, so it would pull the image into (and then fail to remove) the job directory.
// That the engine itself runs the client with HOME set to the job directory is a ROADMAP residual.
func clientEnv(env []string) []string {
	if runtime.GOOS != "linux" {
		return env
	}
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, "HOME=") && !strings.HasPrefix(e, "TMPDIR=") {
			out = append(out, e)
		}
	}
	return out
}
