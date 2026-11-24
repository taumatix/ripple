package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/sandbox"
)

// confineName names the confined network of RIPPLE_CONTAINER_CONFINE for a server with no
// RIPPLE_REPLICA, and its sidecar is confineName+"-proxy". It is fixed so that a server killed
// outright, which never removed them, finds them again at its next start. Servers that share a
// container runtime set RIPPLE_REPLICA and get a network each (confineNameFor).
const confineName = "ripple-confine"

// maxConfineReplica leaves room in a 63 character DNS label for "ripple-confine-" and "-proxy".
const maxConfineReplica = 63 - len(confineName) - len("-proxy") - 1

// confineNameFor is the confined network's name for a replica: confineName when it has no name,
// else confineName-<replica>, characters a runtime name cannot hold replaced by "-". A name that
// had to change or be shortened gets a hash of the original, so two replicas do not collide.
func confineNameFor(replica string) string {
	if replica == "" {
		return confineName
	}
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			return r
		}
		return '-'
	}, replica)
	if clean != replica || len(clean) > maxConfineReplica {
		sum := sha256.Sum256([]byte(replica))
		clean = clean[:min(len(clean), maxConfineReplica-9)] + "-" + hex.EncodeToString(sum[:4])
	}
	return confineName + "-" + clean
}

// newSandbox builds the sandbox backend the settings name. With RIPPLE_CONTAINER_CONFINE
// it first removes a confinement an earlier server left behind, then creates the internal
// network and its proxy sidecar (forwarding to RIPPLE_GOPROXY, which the server itself keeps
// using directly) and makes the container backend join that network and use the sidecar as
// GOPROXY. The returned function removes them; it is safe to call more than once and is
// never nil. look is exec.LookPath when nil.
func newSandbox(ctx context.Context, s *config.Server, log *slog.Logger, look func(string) (string, error)) (sandbox.Backend, func(), error) {
	o := sandbox.ContainerOptions{Image: s.ContainerImage, CPUs: s.ContainerCPUs, Memory: s.ContainerMemory,
		Pull: s.ContainerPull, Network: s.ContainerNetwork}
	noop := func() {}
	if !s.ContainerConfine {
		b, err := sandbox.DetectWith(s.Sandbox, look, o)
		return b, noop, err
	}
	fl := sandbox.Flavour(s.Sandbox)
	if s.Sandbox != "docker" && s.Sandbox != "podman" {
		return nil, noop, fmt.Errorf("RIPPLE_CONTAINER_CONFINE: needs RIPPLE_SANDBOX=docker or podman, not %q", s.Sandbox)
	}
	if look == nil {
		look = exec.LookPath
	}
	bin, err := look(s.Sandbox)
	if err != nil {
		return nil, noop, fmt.Errorf("sandbox %q requested but %s is not available: %w", s.Sandbox, s.Sandbox, err)
	}
	if _, err := sandbox.Container(fl, bin, o); err != nil { // bad options fail before anything is created
		return nil, noop, err
	}
	name := confineNameFor(s.Replica)
	if err := sandbox.RemoveConfinement(ctx, fl, bin, name); err != nil {
		return nil, noop, fmt.Errorf("RIPPLE_CONTAINER_CONFINE: %w", err)
	}
	conf, err := sandbox.ConfineNetwork(ctx, fl, bin, sandbox.ConfineOptions{Name: name, Upstream: s.GoProxy})
	if err != nil {
		return nil, noop, fmt.Errorf("RIPPLE_CONTAINER_CONFINE: %w", err)
	}
	o.Network, o.ProxyURL = conf.Network, conf.ProxyURL
	b, err := sandbox.Container(fl, bin, o)
	if err != nil {
		conf.Close()
		return nil, noop, err
	}
	log.Info("container network confined", "network", conf.Network, "proxy", conf.ProxyURL, "upstream", s.GoProxy)
	var once sync.Once
	return b, func() { once.Do(func() { _ = conf.Close() }) }, nil
}
