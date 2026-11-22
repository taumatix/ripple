package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

type seatbelt struct{ bin string }

// Seatbelt is the macOS backend, sandbox-exec(1). Apple marks sandbox-exec DEPRECATED
// ("Developers who wish to sandbox an app should instead adopt the App Sandbox")
// but it ships on current macOS and has no replacement for command-line confinement.
// If it disappears, Detect fails and the operator must choose "none" explicitly.
func Seatbelt(bin string) Backend { return seatbelt{bin} }

func (seatbelt) Name() string { return "seatbelt" }

// Readable system trees. "/" itself is a literal: dyld aborts without it.
var macRO = []string{"/usr", "/bin", "/sbin", "/System", "/Library", "/private/etc", "/private/var/db", "/private/var/select", "/dev", "/opt/homebrew", "/Applications"}

func (s seatbelt) Wrap(p Policy, argv []string) ([]string, error) {
	if err := checkPolicy(p, argv); err != nil {
		return nil, err
	}
	p = resolved(p)
	all := append(append(append([]string{p.Workdir}, p.Writable...), p.ReadOnly...), p.Deny...)
	for _, path := range all {
		if strings.ContainsAny(path, "\"\\\n\r\x00") || strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 }) {
			return nil, fmt.Errorf("sandbox: path %q has characters unsafe in a profile", path)
		}
	}
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	// Last matching rule wins, so each deny is followed by its narrower allows.
	b.WriteString("(deny file-write*)\n(allow file-write* (literal \"/dev/null\") (literal \"/dev/dtracehelper\")")
	for _, d := range append([]string{p.Workdir}, p.Writable...) {
		fmt.Fprintf(&b, " (subpath %q)", d)
	}
	b.WriteString(")\n(deny file-read*)\n(allow file-read-metadata)\n(allow file-read* (literal \"/\")")
	for _, d := range append(append(append([]string(nil), macRO...), p.ReadOnly...), append([]string{p.Workdir}, p.Writable...)...) {
		fmt.Fprintf(&b, " (subpath %q)", d)
	}
	b.WriteString(")\n(deny network*)\n")
	for _, port := range p.LoopbackPorts {
		fmt.Fprintf(&b, "(allow network* (remote ip \"localhost:%d\"))\n", port)
	}
	for _, d := range p.Deny {
		fmt.Fprintf(&b, "(deny file-read* file-write* (subpath %q))\n", d)
	}
	return append([]string{s.bin, "-p", b.String()}, argv...), nil
}

// resolved returns p with symlinks in every path followed. Seatbelt matches the real
// path, so a policy naming /var/... (a symlink to /private/var/... on macOS) would
// otherwise silently allow nothing. A path that does not exist yet is kept as given.
func resolved(p Policy) Policy {
	one := func(s string) string {
		if r, err := filepath.EvalSymlinks(s); err == nil {
			return r
		}
		return s
	}
	many := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = one(s)
		}
		return out
	}
	p.Workdir = one(p.Workdir)
	p.Writable, p.ReadOnly, p.Deny = many(p.Writable), many(p.ReadOnly), many(p.Deny)
	return p
}
