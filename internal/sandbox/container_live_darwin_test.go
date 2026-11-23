package sandbox

import (
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

func TestAppleContainerLive(t *testing.T) {
	b, bin := liveContainer(t)
	checkContainerBackend(t, b, bin, FlavourApple)
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
