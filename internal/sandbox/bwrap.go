package sandbox

import (
	"fmt"
	"strings"
)

type bwrap struct{ bin string }

// Bwrap is the Linux backend (bubblewrap). UNVERIFIED on the development host (macOS);
// the argv is covered by unit tests and the CI "sandbox-bwrap" job runs the e2e.
// Network is always a fresh, empty namespace: bwrap cannot filter, so a policy that
// asks for a proxy port gets ErrNetUnsupported.
func Bwrap(bin string) Backend { return bwrap{bin} }

func (bwrap) Name() string { return "bwrap" }

// systemRO are mounted read-only when present. Home directories are deliberately absent.
var systemRO = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/etc/ssl", "/etc/ca-certificates", "/etc/passwd", "/etc/group", "/etc/nsswitch.conf", "/etc/localtime"}

func (b bwrap) Wrap(p Policy, argv []string) ([]string, error) {
	if len(p.LoopbackPorts) > 0 {
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
	out = append(out, "--chdir", p.Workdir, "--")
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
