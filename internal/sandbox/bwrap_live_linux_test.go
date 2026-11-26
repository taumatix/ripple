package sandbox

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests run real bwrap. Where it is unavailable they skip, unless
// RIPPLE_REQUIRE_BWRAP=1 (the CI sandbox-bwrap job), where that is a failure.

func bwrapOrSkip(t *testing.T) Backend {
	t.Helper()
	b, err := Detect("bwrap", nil)
	if err != nil {
		if os.Getenv("RIPPLE_REQUIRE_BWRAP") == "1" {
			t.Fatalf("bwrap required but unavailable: %v", err)
		}
		t.Skipf("no bwrap: %v", err)
	}
	return b
}

func runBwrap(t *testing.T, p Policy, argv ...string) (string, error) {
	t.Helper()
	b := bwrapOrSkip(t)
	w, err := b.Wrap(p, argv)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(w[0], w[1:]...).CombinedOutput()
	t.Logf("%v: %s", argv, out)
	if err != nil && strings.Contains(string(out), "bwrap:") {
		if os.Getenv("RIPPLE_REQUIRE_BWRAP") != "1" {
			t.Skipf("bwrap cannot run here: %s", out)
		}
	}
	return string(out), err
}

func TestBwrapLiveFilesystem(t *testing.T) {
	work, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	secret := filepath.Join(outside, "key.pem")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Workdir: work}
	if _, err := runBwrap(t, p, "/bin/sh", "-c", "echo ok > inside.txt"); err != nil {
		t.Fatalf("write inside workdir refused: %v", err)
	}
	if _, err := runBwrap(t, p, "/bin/cat", secret); err == nil {
		t.Error("read a file outside the allowed paths")
	}
	if _, err := runBwrap(t, p, "/bin/sh", "-c", "echo x > "+filepath.Join(outside, "w.txt")); err == nil {
		t.Error("wrote outside the workdir")
	}
	if _, err := os.Stat(filepath.Join(outside, "w.txt")); err == nil {
		t.Error("file appeared outside the workdir")
	}
}

// sh -c with bash's /dev/tcp reaches a TCP port without any extra tool.
func tcpProbe(port int) string {
	return "exec 3<>/dev/tcp/127.0.0.1/" + strconv.Itoa(port) + " && printf 'GET /ping HTTP/1.0\\r\\n\\r\\n' >&3 && cat <&3"
}

func TestBwrapLiveNoNetworkWithoutProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	work, _ := filepath.EvalSymlinks(t.TempDir())
	port := ln.Addr().(*net.TCPAddr).Port
	if _, err := runBwrap(t, Policy{Workdir: work}, "/bin/bash", "-c", tcpProbe(port)); err == nil {
		t.Error("reached a host loopback port from the network namespace")
	}
}

func TestBwrapLiveReachesOnlyTheGate(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream saw " + r.URL.Path))
	}))
	defer up.Close()
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	g, err := NewGate(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	sockDir, err := os.MkdirTemp("", "gate-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "g.sock")
	if err := g.ListenUnix(sock); err != nil {
		t.Fatal(err)
	}
	work, _ := filepath.EvalSymlinks(t.TempDir())
	p := Policy{Workdir: work, LoopbackPorts: []int{g.Port()}, GateSocket: sock}
	prefix := strings.TrimPrefix(g.URL, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(g.Port())))

	probe := "exec 3<>/dev/tcp/127.0.0.1/" + strconv.Itoa(g.Port()) + " && printf 'GET " + prefix + "/x/y HTTP/1.0\\r\\n\\r\\n' >&3 && cat <&3"
	out, err := runBwrap(t, p, "/bin/bash", "-c", probe)
	if err != nil || !strings.Contains(out, "upstream saw /x/y") {
		t.Fatalf("gate unreachable through the relay: %v\n%s", err, out)
	}
	// A different host loopback port is not reachable: the namespace has only its own lo.
	if _, err := runBwrap(t, p, "/bin/bash", "-c", tcpProbe(other.Addr().(*net.TCPAddr).Port)); err == nil {
		t.Error("reached a host port other than the gate")
	}
	// Nor an external address.
	if _, err := runBwrap(t, p, "/bin/bash", "-c", "exec 3<>/dev/tcp/1.1.1.1/80"); err == nil {
		t.Error("reached an external address")
	}
	// The relay returns the command's exit status.
	if _, err := runBwrap(t, p, "/bin/sh", "-c", "exit 3"); err == nil {
		t.Error("exit status lost")
	}
}
