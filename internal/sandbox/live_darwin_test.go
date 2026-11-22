package sandbox

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// runSeatbelt executes argv under the real seatbelt backend and returns its error.
func runSeatbelt(t *testing.T, p Policy, argv ...string) error {
	t.Helper()
	bin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec not available")
	}
	w, err := Seatbelt(bin).Wrap(p, argv)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(w[0], w[1:]...)
	cmd.Dir = p.Workdir
	out, err := cmd.CombinedOutput()
	t.Logf("%v: %s", argv, out)
	return err
}

func TestSeatbeltLiveConfinement(t *testing.T) {
	work, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	secret := filepath.Join(outside, "key.pem")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Workdir: work}

	if err := runSeatbelt(t, p, "/bin/sh", "-c", "echo ok > inside.txt"); err != nil {
		t.Fatalf("write inside workdir refused: %v", err)
	}
	if err := runSeatbelt(t, p, "/bin/cat", secret); err == nil {
		t.Error("read a file outside the allowed paths")
	}
	if err := runSeatbelt(t, p, "/bin/sh", "-c", "echo x > "+filepath.Join(outside, "w.txt")); err == nil {
		t.Error("wrote outside the workdir")
	}
}

func TestSeatbeltLiveNetworkOnlyToNamedPort(t *testing.T) {
	allowed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	denied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	defer denied.Close()
	work, _ := filepath.EvalSymlinks(t.TempDir())
	port := func(l net.Listener) int { return l.Addr().(*net.TCPAddr).Port }
	p := Policy{Workdir: work, LoopbackPorts: []int{port(allowed)}}
	probe := func(l net.Listener) error {
		return runSeatbelt(t, p, "/usr/bin/nc", "-z", "127.0.0.1", strconv.Itoa(port(l)))
	}
	if err := probe(allowed); err != nil {
		t.Errorf("named port refused: %v", err)
	}
	if err := probe(denied); err == nil {
		t.Error("reached a port the policy did not name")
	}
}
