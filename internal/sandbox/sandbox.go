// Package sandbox runs untrusted build commands (go, git) under an OS-level
// sandbox: read-only filesystem except the job workspace, no access to the host's
// home or the App key, and no network except loopback ports the caller names.
//
// Backends: "bwrap" (Linux, bubblewrap), "seatbelt" (macOS sandbox-exec, which Apple
// documents as deprecated but still ships) and "none" (no confinement; the default,
// so existing installs do not change). See docs/sandbox.md for what is verified.
package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// ErrNetUnsupported is returned when a policy asks for network access the backend
// cannot confine to loopback ports. The backend refuses rather than share the host's
// network.
var ErrNetUnsupported = errors.New("sandbox: backend cannot confine network to a proxy")

// Policy is what one command may touch.
type Policy struct {
	Workdir  string   // writable; also the working directory
	Writable []string // further writable paths (e.g. a shared build cache)
	ReadOnly []string // further readable paths (e.g. the toolchain)
	Deny     []string // never readable or writable, even if inside an allowed path
	// LoopbackPorts are the only network destinations (127.0.0.1:port); empty means no network.
	LoopbackPorts []int
}

// Backend rewrites an argv so the command runs confined by a Policy.
type Backend interface {
	Name() string
	Wrap(p Policy, argv []string) ([]string, error)
}

type none struct{}

// None is the no-op backend: argv is returned unchanged.
func None() Backend { return none{} }

func (none) Name() string                                { return "none" }
func (none) Wrap(_ Policy, a []string) ([]string, error) { return a, nil }

// Detect resolves a configured name: "" and "none" give None; "auto" picks the
// platform's backend and fails if it is not installed (never silently falls back to
// no sandbox); "bwrap" and "seatbelt" require their binary. look is exec.LookPath
// when nil.
func Detect(name string, look func(string) (string, error)) (Backend, error) {
	if look == nil {
		look = exec.LookPath
	}
	need := func(bin string, mk func(string) Backend) (Backend, error) {
		p, err := look(bin)
		if err != nil {
			return nil, fmt.Errorf("sandbox %q requested but %s is not available: %w", name, bin, err)
		}
		return mk(p), nil
	}
	switch name {
	case "", "none":
		return None(), nil
	case "bwrap":
		return need("bwrap", Bwrap)
	case "seatbelt":
		return need("sandbox-exec", Seatbelt)
	case "auto":
		switch runtime.GOOS {
		case "linux":
			return need("bwrap", Bwrap)
		case "darwin":
			return need("sandbox-exec", Seatbelt)
		}
		return nil, fmt.Errorf("sandbox auto: no backend for %s", runtime.GOOS)
	}
	return nil, fmt.Errorf("unknown sandbox %q (none, auto, bwrap, seatbelt)", name)
}
