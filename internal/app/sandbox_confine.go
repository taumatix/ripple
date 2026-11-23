package app

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/sandbox"
)

// confineName names the confined network of RIPPLE_CONTAINER_CONFINE, and its sidecar is
// confineName+"-proxy". It is fixed so that a server killed outright, which never removed
// them, finds them again at its next start; so one server uses one container runtime.
const confineName = "ripple-confine"

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
	if err := sandbox.RemoveConfinement(ctx, fl, bin, confineName); err != nil {
		return nil, noop, fmt.Errorf("RIPPLE_CONTAINER_CONFINE: %w", err)
	}
	conf, err := sandbox.ConfineNetwork(ctx, fl, bin, sandbox.ConfineOptions{Name: confineName, Upstream: s.GoProxy})
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
