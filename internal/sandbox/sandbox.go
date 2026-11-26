// Package sandbox runs untrusted build commands (go, git) under an OS-level
// sandbox: read-only filesystem except the job workspace, no access to the host's
// home or the App key, and no network except loopback ports the caller names.
//
// Backends: "bwrap" (Linux, bubblewrap), "seatbelt" (macOS sandbox-exec, which Apple
// documents as deprecated but still ships), "container", "docker" and "podman" (the
// command runs in an OCI image) and "none" (no confinement; the default,
// so existing installs do not change). See docs/sandbox.md for what is verified.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// GateSocket is a unix socket the gate also listens on. A backend that cannot share
	// host loopback ports (bwrap, in its own network namespace) forwards LoopbackPorts[0]
	// to it; see GateSocketer. Other backends ignore it.
	GateSocket string
	// Env names the variables the command runs with ("KEY=value"). Process-level backends
	// ignore it (the command inherits them); a container backend passes the keys through.
	Env []string
	// Proxy is the upstream the command may reach when the backend owns its network
	// (NetworkOwner): empty means no network at all. Backends that filter by loopback
	// port use LoopbackPorts and a Gate instead and ignore it.
	Proxy string
	// Image, when set, is the image a container backend runs this command in instead of
	// its configured one (the Go minor a module names, RIPPLE_CONTAINER_GO_IMAGES).
	// Backends that run no image ignore it.
	Image string
}

// NetworkOwner marks a backend that cannot reach a loopback gate on the host (a
// container has its own network namespace). The caller then skips the Gate and sets
// Policy.Proxy, and the backend decides what network the command gets.
type NetworkOwner interface{ OwnsNetwork() bool }

// GateSocketer marks a backend that reaches the Gate through a unix socket
// (Policy.GateSocket) instead of a host loopback port; the caller then calls
// Gate.ListenUnix before wrapping.
type GateSocketer interface{ NeedsGateSocket() bool }

// Reaper is a backend whose confined command can outlive the process that started it
// (a container keeps running when its client is killed). Reap stops the one that was
// started with argv, as returned by Wrap; it is best effort and never blocks long.
type Reaper interface{ Reap(argv []string) }

// ClientEnver is a backend whose client process (the container CLI) needs an
// environment of its own, apart from the one the confined command runs with: the
// client's HOME holds the runtime's state (rootless Podman keeps its image storage under
// it), while the command's HOME is the job directory. ClientEnv returns the environment
// to start the argv returned by Wrap with, given the policy's Env.
type ClientEnver interface{ ClientEnv(job []string) []string }

// Backend rewrites an argv so the command runs confined by a Policy.
type Backend interface {
	Name() string
	Wrap(p Policy, argv []string) ([]string, error)
}

// bwrapWithSelf is the bwrap backend whose in-sandbox relay is this very executable
// (which must dispatch RelayArg to RunRelay: ripple's main does). Without a resolvable
// path it is plain Bwrap, which refuses to run a command that needs the proxy.
func bwrapWithSelf(bin string) Backend {
	if exe, err := os.Executable(); err == nil && filepath.IsAbs(exe) {
		return BwrapRelay(bin, exe)
	}
	return Bwrap(bin)
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
	return DetectWith(name, look, ContainerOptions{})
}

// DetectWith is Detect for backends that need settings: "container" (Apple), "docker"
// and "podman" take o, and fail when o.Image is empty. Other names ignore o.
func DetectWith(name string, look func(string) (string, error), o ContainerOptions) (Backend, error) {
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
		return need("bwrap", bwrapWithSelf)
	case "seatbelt":
		return need("sandbox-exec", Seatbelt)
	case "container", "docker", "podman":
		fl := Flavour(name)
		if name == "container" {
			fl = FlavourApple
		}
		p, err := look(name)
		if err != nil {
			return nil, fmt.Errorf("sandbox %q requested but %s is not available: %w", name, name, err)
		}
		return Container(fl, p, o)
	case "auto":
		switch runtime.GOOS {
		case "linux":
			return need("bwrap", bwrapWithSelf)
		case "darwin":
			return need("sandbox-exec", Seatbelt)
		}
		return nil, fmt.Errorf("sandbox auto: no backend for %s", runtime.GOOS)
	}
	return nil, fmt.Errorf("unknown sandbox %q (none, auto, bwrap, seatbelt, container, docker, podman)", name)
}
