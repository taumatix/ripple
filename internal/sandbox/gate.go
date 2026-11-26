package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// Gate is a loopback reverse proxy to one upstream URL. A sandboxed command is
// allowed to reach only the gate's port, so "no network except the proxy" holds
// even though sandbox-exec filters by loopback port, not by remote host.
// The URL carries a random path prefix; a request without it is a 404, so another
// local process cannot use the gate as an open door to the upstream. Redirects are
// passed to the client, never followed, so an upstream cannot steer the gate elsewhere
// (the client then fails to follow them out of the sandbox).
type Gate struct {
	// URL is what the sandboxed command should use instead of the upstream URL,
	// without a trailing slash.
	URL string
	srv *http.Server
	ln  net.Listener
}

// NewGate listens on 127.0.0.1 and forwards to upstream.
func NewGate(upstream string) (*Gate, error) { return NewGateAuth(upstream, "", "") }

// NewGateAuth is NewGate for an upstream that wants HTTP basic authentication: when
// token is not empty the gate sends user:token on every upstream request, replacing
// whatever Authorization header the command sent, so the credential is never in the
// command's environment, argv or URL. The upstream must be https (or loopback).
func NewGateAuth(upstream, user, token string) (*Gate, error) {
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("sandbox gate: unusable upstream %q", upstream)
	}
	if token != "" && u.Scheme != "https" && !loopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("sandbox gate: refusing to send a credential to %s over plain http", u.Hostname())
	}
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	prefix := "/" + hex.EncodeToString(rnd[:])
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, prefix)
			r.Out.URL.RawPath = ""
			r.SetURL(u)
			r.Out.Host = u.Host
			if token != "" {
				r.Out.SetBasicAuth(user, token)
			}
		},
	}
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", rp)
	g := &Gate{ln: ln, srv: &http.Server{Handler: mux}}
	g.URL = "http://" + ln.Addr().String() + prefix
	go g.srv.Serve(ln)
	return g, nil
}

// ListenUnix makes the gate also answer on a unix socket at path (the file must not
// exist), for a backend that cannot reach host loopback ports. The URL prefix applies
// the same way. The socket is created 0600 and removed by Close.
func (g *Gate) ListenUnix(path string) error {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	go g.srv.Serve(ln)
	return nil
}

// Port is the loopback port to allow in a Policy.
func (g *Gate) Port() int { return g.ln.Addr().(*net.TCPAddr).Port }

// Close stops the gate.
func (g *Gate) Close() error { return g.srv.Close() }

func loopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
