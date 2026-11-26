package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestNoneLeavesArgvUnchanged(t *testing.T) {
	got, err := None().Wrap(Policy{Workdir: "/w"}, []string{"go", "build"})
	if err != nil || strings.Join(got, " ") != "go build" {
		t.Fatalf("Wrap = %v, %v", got, err)
	}
}

func TestBwrapArgv(t *testing.T) {
	got, err := Bwrap("/usr/bin/bwrap").Wrap(Policy{
		Workdir: "/w/job", Writable: []string{"/w/cache"}, ReadOnly: []string{"/opt/go"},
	}, []string{"/usr/bin/go", "build", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	s := " " + strings.Join(got, " ") + " "
	for _, want := range []string{
		" /usr/bin/bwrap ", " --die-with-parent ", " --new-session ", " --unshare-all ",
		" --ro-bind-try /usr /usr ", " --ro-bind /opt/go /opt/go ",
		" --bind /w/job /w/job ", " --bind /w/cache /w/cache ", " --chdir /w/job ",
		" --tmpfs /tmp ", " --proc /proc ", " --dev /dev ", " -- /usr/bin/go build ./... ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("argv lacks %q: %v", want, got)
		}
	}
	for _, bad := range []string{"--share-net", "/home", "/root"} {
		if strings.Contains(s, bad) {
			t.Errorf("argv contains %q: %v", bad, got)
		}
	}
	if got[len(got)-4] != "--" {
		t.Errorf("command not after --: %v", got)
	}
}

func TestBwrapCannotRestrictNetworkToAProxy(t *testing.T) {
	_, err := Bwrap("/usr/bin/bwrap").Wrap(Policy{Workdir: "/w", LoopbackPorts: []int{1234}}, []string{"go"})
	if !errors.Is(err, ErrNetUnsupported) {
		t.Fatalf("err = %v, want ErrNetUnsupported", err)
	}
}

func TestSeatbeltProfile(t *testing.T) {
	got, err := Seatbelt("/usr/bin/sandbox-exec").Wrap(Policy{
		Workdir: "/w/job", Writable: []string{"/w/cache"}, ReadOnly: []string{"/opt/go"},
		Deny: []string{"/etc/ripple/key.pem"}, LoopbackPorts: []int{4242},
	}, []string{"go", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "/usr/bin/sandbox-exec" || got[1] != "-p" || got[3] != "go" || got[4] != "build" {
		t.Fatalf("argv = %v", got)
	}
	prof := got[2]
	for _, want := range []string{
		"(deny file-write*)", `(subpath "/w/job")`, `(subpath "/w/cache")`, "(deny file-read*)",
		`(subpath "/opt/go")`, "(deny network*)", `(remote ip "localhost:4242")`,
		`(deny file-read* file-write* (subpath "/etc/ripple/key.pem"))`,
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %q:\n%s", want, prof)
		}
	}
	// Deny rules for the key must come after every allow, so they win.
	if strings.LastIndex(prof, "key.pem") < strings.LastIndex(prof, `(subpath "/opt/go")`) {
		t.Error("key deny is not last")
	}
}

func TestSeatbeltNoNetworkWithoutPorts(t *testing.T) {
	got, _ := Seatbelt("/usr/bin/sandbox-exec").Wrap(Policy{Workdir: "/w"}, []string{"git"})
	if strings.Contains(got[2], "remote ip") {
		t.Errorf("profile allows network:\n%s", got[2])
	}
}

func TestSeatbeltRejectsPathsThatCouldInjectProfile(t *testing.T) {
	for _, p := range []string{`/w") (allow default) ("`, "/w\nx", `/w\x`} {
		if _, err := Seatbelt("/usr/bin/sandbox-exec").Wrap(Policy{Workdir: p}, []string{"x"}); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
}

func TestWrapRequiresWorkdir(t *testing.T) {
	for _, b := range []Backend{Bwrap("b"), Seatbelt("s")} {
		if _, err := b.Wrap(Policy{}, []string{"x"}); err == nil {
			t.Errorf("%s accepted empty workdir", b.Name())
		}
	}
}

func TestDetect(t *testing.T) {
	if b, err := Detect("", nil); err != nil || b.Name() != "none" {
		t.Errorf("empty = %v, %v", b, err)
	}
	if _, err := Detect("bogus", nil); err == nil {
		t.Error("accepted bogus")
	}
	look := func(name string) (string, error) { return "", errors.New("absent") }
	if _, err := Detect("bwrap", look); err == nil {
		t.Error("bwrap detected when absent")
	}
	if _, err := Detect("auto", look); err == nil {
		t.Error("auto must fail closed when nothing is available")
	}
}

func TestBwrapRefusesDenyInsideBoundTree(t *testing.T) {
	_, err := Bwrap("b").Wrap(Policy{Workdir: "/w", Deny: []string{"/w/secret"}}, []string{"x"})
	if err == nil {
		t.Fatal("accepted a deny inside the workdir")
	}
	if _, err := Bwrap("b").Wrap(Policy{Workdir: "/w", Deny: []string{"/etc/ripple/key"}}, []string{"x"}); err != nil {
		t.Fatalf("unrelated deny refused: %v", err)
	}
}

func TestBwrapProxyNeedsRelayAndGateSocket(t *testing.T) {
	p := Policy{Workdir: "/w", LoopbackPorts: []int{1234}, GateSocket: "/tmp/g/gate.sock"}
	for name, b := range map[string]Backend{
		"no relay binary": Bwrap("/usr/bin/bwrap"),
		"relative relay":  BwrapRelay("/usr/bin/bwrap", "ripple"),
	} {
		if _, err := b.Wrap(p, []string{"go"}); !errors.Is(err, ErrNetUnsupported) {
			t.Errorf("%s: err = %v, want ErrNetUnsupported", name, err)
		}
	}
	noSock := p
	noSock.GateSocket = ""
	if _, err := BwrapRelay("/usr/bin/bwrap", "/opt/ripple").Wrap(noSock, []string{"go"}); !errors.Is(err, ErrNetUnsupported) {
		t.Errorf("no gate socket: err = %v, want ErrNetUnsupported", err)
	}
	two := p
	two.LoopbackPorts = []int{1, 2}
	if _, err := BwrapRelay("/usr/bin/bwrap", "/opt/ripple").Wrap(two, []string{"go"}); !errors.Is(err, ErrNetUnsupported) {
		t.Errorf("two ports: err = %v, want ErrNetUnsupported", err)
	}
}

func TestBwrapRelayArgv(t *testing.T) {
	got, err := BwrapRelay("/usr/bin/bwrap", "/opt/ripple").Wrap(Policy{
		Workdir: "/w/job", LoopbackPorts: []int{1234}, GateSocket: "/tmp/g/gate.sock",
	}, []string{"/usr/bin/go", "build"})
	if err != nil {
		t.Fatal(err)
	}
	s := " " + strings.Join(got, " ") + " "
	for _, want := range []string{
		" --unshare-all ", " --ro-bind /opt/ripple /.ripple/relay ", " --bind /tmp/g/gate.sock /.ripple/gate.sock ",
		" -- /.ripple/relay sandbox-relay 1234 /.ripple/gate.sock -- /usr/bin/go build ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("argv lacks %q: %v", want, got)
		}
	}
	if strings.Contains(s, "--share-net") {
		t.Errorf("network shared: %v", got)
	}
}
