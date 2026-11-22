package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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
func NewGate(upstream string) (*Gate, error) {
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("sandbox gate: unusable upstream %q", upstream)
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
		},
	}
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", rp)
	g := &Gate{ln: ln, srv: &http.Server{Handler: mux}}
	g.URL = "http://" + ln.Addr().String() + prefix
	go g.srv.Serve(ln)
	return g, nil
}

// Port is the loopback port to allow in a Policy.
func (g *Gate) Port() int { return g.ln.Addr().(*net.TCPAddr).Port }

// Close stops the gate.
func (g *Gate) Close() error { return g.srv.Close() }
