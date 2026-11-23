package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Flavour is the container CLI a Container backend speaks.
type Flavour string

const (
	// FlavourApple is Apple's `container` (macOS 26, Apple silicon), CLI read from
	// `container run --help` on version 1.4.1.
	FlavourApple Flavour = "apple"
	// FlavourDocker is the Docker CLI. UNVERIFIED against a real daemon: the argv is
	// written from the documented `docker run` options and tested against a fake binary.
	FlavourDocker Flavour = "docker"
	// FlavourPodman is Podman, which documents the same options. UNVERIFIED, likewise.
	FlavourPodman Flavour = "podman"
)

// ContainerOptions configure a Container backend.
type ContainerOptions struct {
	// Image is the OCI image that carries the commands to run (go, git). Required.
	Image string
	// CPUs and Memory limit each command's container ("2", "2G"); empty uses the
	// defaults DefaultCPUs and DefaultMemory.
	CPUs, Memory string
	// Pull is "missing" (default), "always" or "never". Apple's CLI has no such option
	// (it pulls only a missing image), so only "missing" is accepted there.
	Pull string
	// Network names the runtime network a command that needs the module proxy joins; empty
	// is the runtime's default network. Commands that need no network always get none.
	Network string
	// PIDs caps processes per container (Docker and Podman; Apple has no option). Default 512.
	PIDs int
}

// Defaults for ContainerOptions.
const (
	DefaultCPUs   = "2"
	DefaultMemory = "2G"
	DefaultPIDs   = 512
)

type container struct {
	bin string
	fl  Flavour
	o   ContainerOptions
	uid string
}

// Container runs each command in a fresh container of o.Image: read-only root, all
// capabilities dropped, the caller's uid, only the policy's writable paths mounted
// (at the same paths, so the environment's paths hold), no network unless the policy
// names a proxy, CPU, memory and process limits, and a unique name so a command that
// times out can be reaped. The image must carry the command (argv[0] is run by its
// base name) and its own toolchain; the policy's ReadOnly paths are not mounted.
func Container(fl Flavour, bin string, o ContainerOptions) (Backend, error) {
	switch fl {
	case FlavourApple, FlavourDocker, FlavourPodman:
	default:
		return nil, fmt.Errorf("sandbox: unknown container flavour %q", fl)
	}
	if o.Image == "" {
		return nil, fmt.Errorf("sandbox %s: no image (set RIPPLE_CONTAINER_IMAGE)", fl)
	}
	if o.CPUs == "" {
		o.CPUs = DefaultCPUs
	}
	if o.Memory == "" {
		o.Memory = DefaultMemory
	}
	if o.PIDs == 0 {
		o.PIDs = DefaultPIDs
	}
	switch o.Pull {
	case "":
		o.Pull = "missing"
	case "missing":
	case "always", "never":
		if fl == FlavourApple {
			return nil, fmt.Errorf("sandbox container: pull policy %q is not supported by Apple's container CLI (only missing)", o.Pull)
		}
	default:
		return nil, fmt.Errorf("sandbox %s: unknown pull policy %q (missing, always, never)", fl, o.Pull)
	}
	return container{bin: bin, fl: fl, o: o, uid: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())}, nil
}

func (c container) Name() string {
	if c.fl == FlavourApple {
		return "container"
	}
	return string(c.fl)
}

func (container) OwnsNetwork() bool { return true }

func (c container) Wrap(p Policy, argv []string) ([]string, error) {
	if len(p.LoopbackPorts) > 0 {
		return nil, ErrNetUnsupported // a container cannot reach the host's loopback
	}
	if err := checkPolicy(p, argv); err != nil {
		return nil, err
	}
	mounts := mountSet(append([]string{p.Workdir}, p.Writable...))
	for _, d := range p.Deny {
		for _, m := range mounts {
			if within(m, d) {
				return nil, fmt.Errorf("sandbox: denied path %s is inside mounted path %s", d, m)
			}
		}
	}
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		return nil, err
	}
	out := []string{c.bin, "run", "--rm", "--name", "ripple-" + hex.EncodeToString(name[:]), "--label", "ripple.job=1"}
	if c.fl == FlavourApple {
		out = append(out, "--progress", "none") // keep pull progress out of the command's output
	} else {
		out = append(out, "--pull", c.o.Pull, "--pids-limit", strconv.Itoa(c.o.PIDs), "--security-opt", "no-new-privileges")
	}
	switch {
	case p.Proxy == "":
		out = append(out, "--network", "none")
	case c.o.Network != "":
		out = append(out, "--network", c.o.Network)
	}
	out = append(out, "--read-only", "--tmpfs", "/tmp", "--cap-drop", "ALL", "--user", c.uid)
	if c.fl == FlavourPodman {
		out = append(out, "--userns", "keep-id") // rootless: else --user maps outside the bind mounts' owner
	}
	if c.fl == FlavourApple {
		out = append(out, "-c", c.o.CPUs, "-m", c.o.Memory)
	} else {
		out = append(out, "--cpus", c.o.CPUs, "--memory", c.o.Memory)
	}
	for _, m := range mounts {
		out = append(out, "-v", m+":"+m)
	}
	out = append(out, "-w", p.Workdir)
	// Only the keys are on the command line; the values come from the client's own
	// environment (-e KEY), so nothing sensitive shows in a process listing.
	for _, kv := range p.Env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" || k == "PATH" { // the host's PATH means nothing in the image
			continue
		}
		out = append(out, "-e", k)
	}
	cmd := append([]string(nil), argv...)
	if filepath.IsAbs(cmd[0]) {
		cmd[0] = filepath.Base(cmd[0])
	}
	return append(append(out, c.o.Image), cmd...), nil
}

// Reap kills and removes the container a Wrap'd argv started. The client process being
// killed (a timeout) neither stops nor removes the container; verified with Apple's CLI.
func (c container) Reap(argv []string) {
	name := ""
	for i, a := range argv {
		if a == "--name" && i+1 < len(argv) {
			name = argv[i+1]
		}
	}
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, c.bin, "kill", name).Run()
	// --rm is carried out by the client, which a timeout has just killed, so every
	// flavour needs the explicit remove: with Apple's CLI about half of the containers
	// killed this way stayed behind "stopped" (TestAppleContainerReapAfterClientKilled).
	_ = exec.CommandContext(ctx, c.bin, "rm", "-f", name).Run()
}

// mountSet drops duplicates and any path inside another, keeping a stable order.
func mountSet(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for i, p := range paths {
		p = strings.TrimRight(p, "/")
		covered := seen[p]
		for j, q := range paths {
			q = strings.TrimRight(q, "/")
			if j != i && q != p && within(q, p) {
				covered = true
			}
		}
		if !covered {
			out = append(out, p)
			seen[p] = true
		}
	}
	return out
}
