package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Packagist is a fake repo.packagist.org: GET /p2/<vendor>/<package>.json answers the
// metadata document of docs/vendor/packagist/packagist-api.txt ("packages" keyed by name,
// newest first, "minified": "composer/2.0"; after the first entry only the keys that
// changed are written, which is what the real file does). A package never published
// answers 404, a Denied one 401, any other path 404, and every request is recorded. The
// document is written out here, not produced by internal/packagist's types.
type Packagist struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	pkgs   map[string][]string
	denied map[string]bool
	reqs   []PackagistRequest
}

// PackagistRequest is one request the fake saw.
type PackagistRequest struct{ Path, Authorization string }

func NewPackagist(t testing.TB) *Packagist {
	t.Helper()
	p := &Packagist{pkgs: map[string][]string{}, denied: map[string]bool{}}
	p.srv = httptest.NewServer(p.Wrap(http.HandlerFunc(p.serve)))
	p.URL = p.srv.URL
	t.Cleanup(p.srv.Close)
	return p
}

// Publish adds a version of pkg exactly as the registry would write it: "1.2.3",
// "v1.2.3", "1.0.0-beta1", "dev-main".
func (p *Packagist) Publish(pkg, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pkgs[pkg] = append(p.pkgs[pkg], version)
}

// Deny makes pkg answer 401.
func (p *Packagist) Deny(pkg string) {
	p.mu.Lock()
	p.denied[pkg] = true
	p.mu.Unlock()
}

// Requests lists every request seen.
func (p *Packagist) Requests() []PackagistRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PackagistRequest(nil), p.reqs...)
}

func (p *Packagist) serve(w http.ResponseWriter, req *http.Request) {
	p.mu.Lock()
	p.reqs = append(p.reqs, PackagistRequest{req.URL.Path, req.Header.Get("Authorization")})
	p.mu.Unlock()
	name, ok := strings.CutPrefix(req.URL.Path, "/p2/")
	name, ok2 := strings.CutSuffix(name, ".json")
	if !ok || !ok2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	p.mu.Lock()
	vs, known, denied := p.pkgs[name], p.pkgs[name] != nil, p.denied[name]
	p.mu.Unlock()
	switch {
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !known:
		http.Error(w, "not found", http.StatusNotFound)
	default:
		var list []map[string]any
		for i := len(vs) - 1; i >= 0; i-- { // newest first
			if len(list) == 0 {
				list = append(list, map[string]any{"name": name, "description": "pkg", "version": vs[i], "version_normalized": vs[i] + ".0", "type": "library"})
			} else {
				list = append(list, map[string]any{"version": vs[i], "version_normalized": vs[i] + ".0"}) // minified: only what changed
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"packages": map[string]any{name: list}, "minified": "composer/2.0"})
	}
}
