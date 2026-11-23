package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ConfineOptions configure ConfineNetwork.
type ConfineOptions struct {
	// Upstream is the module proxy the sidecar forwards to, as scheme://host[:port] with
	// no path (for example https://proxy.golang.org). It must be reachable from the
	// runtime's default network. Required.
	Upstream string
	// Name names the internal network; the sidecar is Name+"-proxy". Empty picks a
	// random "ripple-net-<hex>".
	Name string
	// Image is the sidecar image; it must carry nginx and busybox wget. Default
	// DefaultProxyImage.
	Image string
}

// DefaultProxyImage is the sidecar image ConfineNetwork runs.
const DefaultProxyImage = "docker.io/library/nginx:1.27-alpine"

const proxyPort = 3128

// Confinement is an internal runtime network plus the proxy sidecar that is its only
// way out. Set ContainerOptions.Network to Network and give the commands GOPROXY
// (Policy.Proxy and the policy's environment) ProxyURL; Close removes both.
type Confinement struct {
	Network  string // pass as ContainerOptions.Network
	ProxyURL string // the sidecar as the commands reach it, http://<name>-proxy:3128
	remove   func()
}

var (
	safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	safeHost = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
)

// ConfineNetwork creates an internal network (no route out) holding only a proxy
// sidecar, which is also on the default network so it can reach o.Upstream. A command
// on the returned network can reach nothing but the sidecar. Docker and Podman only:
// Apple's container has no internal networks. It runs `<bin> network create --internal`,
// starts the sidecar and joins it to the internal network; on any failure it removes
// what it made.
func ConfineNetwork(ctx context.Context, fl Flavour, bin string, o ConfineOptions) (*Confinement, error) {
	return confine(ctx, fl, o, func(ctx context.Context, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("%s %s: %w: %s", fl, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	})
}

type runFn func(ctx context.Context, args ...string) (string, error)

func confine(ctx context.Context, fl Flavour, o ConfineOptions, run runFn) (*Confinement, error) {
	if fl != FlavourDocker && fl != FlavourPodman {
		return nil, fmt.Errorf("sandbox %s: no internal networks; confinement needs docker or podman", fl)
	}
	u, err := url.Parse(o.Upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !safeHost.MatchString(u.Host) ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("sandbox: confinement upstream %q must be http(s)://host[:port] with no path", o.Upstream)
	}
	name := o.Name
	if name == "" {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		name = "ripple-net-" + hex.EncodeToString(b[:])
	}
	if !safeName.MatchString(name) {
		return nil, fmt.Errorf("sandbox: confinement network name %q is not a plain name", name)
	}
	image := o.Image
	if image == "" {
		image = DefaultProxyImage
	}
	side := name + "-proxy"
	conf := fmt.Sprintf("events {}\nhttp {\n  access_log off;\n  server {\n    listen %d;\n"+
		"    location = /_ripple_health { return 200 'ok'; }\n"+
		"    location / {\n      proxy_pass %s://%s;\n      proxy_set_header Host %s;\n"+
		"      proxy_ssl_server_name on;\n      proxy_http_version 1.1;\n    }\n  }\n}\n",
		proxyPort, u.Scheme, u.Host, u.Host)

	remove := func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = run(c, "rm", "-f", side)
		_, _ = run(c, "network", "rm", name)
	}
	if _, err := run(ctx, "network", "create", "--internal", "--label", "ripple.job=1", name); err != nil {
		return nil, err
	}
	steps := [][]string{
		// default network first, so the sidecar can reach the upstream; then join the internal one
		{"run", "-d", "--name", side, "--label", "ripple.job=1", "-e", "RIPPLE_NGINX_CONF=" + conf, "--entrypoint", "sh", image,
			"-c", `printf '%s' "$RIPPLE_NGINX_CONF" > /etc/nginx/nginx.conf && exec nginx -g 'daemon off;'`},
		{"network", "connect", name, side},
	}
	for _, s := range steps {
		if _, err := run(ctx, s...); err != nil {
			remove()
			return nil, err
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := run(ctx, "exec", side, "wget", "-q", "-O", "/dev/null", fmt.Sprintf("http://127.0.0.1:%d/_ripple_health", proxyPort))
		if err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			remove()
			return nil, fmt.Errorf("sandbox: proxy sidecar %s did not come up: %w", side, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return &Confinement{Network: name, ProxyURL: fmt.Sprintf("http://%s:%d", side, proxyPort), remove: remove}, nil
}

// Close removes the sidecar and the network. Safe to call more than once.
func (c *Confinement) Close() error {
	if c.remove != nil {
		c.remove()
		c.remove = nil
	}
	return nil
}
