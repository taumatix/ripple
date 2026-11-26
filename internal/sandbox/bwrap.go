package sandbox

import (
	"fmt"
	"strconv"
	"strings"
)

type bwrap struct{ bin, relay string }

// Bwrap is the Linux backend (bubblewrap). The network is always a fresh namespace with
// only a loopback interface. A policy that asks for a proxy port gets ErrNetUnsupported
// from this constructor's backend: reaching the gate needs the relay of BwrapRelay.
func Bwrap(bin string) Backend { return bwrap{bin: bin} }

// BwrapRelay is Bwrap that can also reach the module proxy gate. The command still runs
// in an empty network namespace (no interface but lo). relayExe, an absolute path to
// the ripple executable, is bound read-only into the sandbox and started first as
// `sandbox-relay <port> <socket> -- <command>` (RunRelay): it listens on the namespace's
// 127.0.0.1:<port> and forwards each connection to the gate's unix socket, which is the
// only host path bound in besides the workspace. Nothing else is reachable.
func BwrapRelay(bin, relayExe string) Backend { return bwrap{bin: bin, relay: relayExe} }

func (bwrap) Name() string { return "bwrap" }

// NeedsGateSocket marks a backend whose proxy access is a unix socket the caller serves
// the Gate on (Policy.GateSocket) rather than a host loopback port.
func (b bwrap) NeedsGateSocket() bool { return b.relay != "" }

// Paths of the relay and the gate socket inside the sandbox.
const (
	relayInSandbox  = "/.ripple/relay"
	socketInSandbox = "/.ripple/gate.sock"
)

// systemRO are mounted read-only when present. Home directories are deliberately absent.
var systemRO = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/etc/ssl", "/etc/ca-certificates", "/etc/passwd", "/etc/group", "/etc/nsswitch.conf", "/etc/localtime"}

func (b bwrap) Wrap(p Policy, argv []string) ([]string, error) {
	// Fail closed: a proxy is reachable only through the relay and a gate socket, exactly one port.
	viaRelay := len(p.LoopbackPorts) > 0
	if viaRelay && (len(p.LoopbackPorts) != 1 || p.GateSocket == "" || !strings.HasPrefix(b.relay, "/") || !strings.HasPrefix(p.GateSocket, "/")) {
		return nil, ErrNetUnsupported
	}
	if err := checkPolicy(p, argv); err != nil {
		return nil, err
	}
	out := []string{b.bin, "--die-with-parent", "--new-session", "--unshare-all"}
	for _, d := range systemRO {
		out = append(out, "--ro-bind-try", d, d)
	}
	out = append(out, "--tmpfs", "/tmp", "--proc", "/proc", "--dev", "/dev")
	for _, d := range p.ReadOnly {
		out = append(out, "--ro-bind", d, d)
	}
	for _, d := range append([]string{p.Workdir}, p.Writable...) {
		out = append(out, "--bind", d, d)
	}
	// Nothing else is mounted, so a denied path is unreachable by omission; one inside
	// a bound tree cannot be hidden reliably (file vs directory), so it is refused.
	for _, d := range p.Deny {
		for _, bound := range append(append([]string{p.Workdir}, p.Writable...), p.ReadOnly...) {
			if within(bound, d) {
				return nil, fmt.Errorf("sandbox: denied path %s is inside bound path %s", d, bound)
			}
		}
	}
	out = append(out, "--chdir", p.Workdir)
	if viaRelay {
		out = append(out, "--ro-bind", b.relay, relayInSandbox, "--bind", p.GateSocket, socketInSandbox, "--",
			relayInSandbox, RelayArg, strconv.Itoa(p.LoopbackPorts[0]), socketInSandbox, "--")
		return append(out, argv...), nil
	}
	out = append(out, "--")
	return append(out, argv...), nil
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimRight(dir, "/")+"/")
}

func checkPolicy(p Policy, argv []string) error {
	if p.Workdir == "" {
		return fmt.Errorf("sandbox: policy has no workdir")
	}
	if len(argv) == 0 {
		return fmt.Errorf("sandbox: empty command")
	}
	for _, s := range append(append(append([]string{p.Workdir}, p.Writable...), p.ReadOnly...), p.Deny...) {
		if s == "" || !strings.HasPrefix(s, "/") {
			return fmt.Errorf("sandbox: path %q is not absolute", s)
		}
	}
	return nil
}
