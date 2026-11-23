package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// CratesIndex is a fake Cargo sparse index: GET /{prefix}/{lowercase name} answers one
// JSON object per line per published version, with the fields of the index format
// (name, vers, deps, cksum, features, yanked). The path rules are written out here
// from docs/vendor/cargo, not taken from internal/cratesindex, so a wrong rule there
// fails against this fake. A crate never published answers 404, a Denied one 401, and
// every request is recorded.
type CratesIndex struct {
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	crates map[string][]crateVers // by index path
	denied map[string]bool
	reqs   []CratesRequest
}

// CratesRequest is one request the fake saw.
type CratesRequest struct{ Path, Authorization string }

type crateVers struct {
	name, vers string
	yanked     bool
}

func NewCratesIndex(t testing.TB) *CratesIndex {
	t.Helper()
	c := &CratesIndex{crates: map[string][]crateVers{}, denied: map[string]bool{}}
	c.srv = httptest.NewServer(http.HandlerFunc(c.serve))
	c.URL = c.srv.URL
	t.Cleanup(c.srv.Close)
	return c
}

func cratesIndexPath(name string) string {
	n := strings.ToLower(name)
	switch len(n) {
	case 1:
		return "/1/" + n
	case 2:
		return "/2/" + n
	case 3:
		return "/3/" + n[:1] + "/" + n
	}
	return "/" + n[:2] + "/" + n[2:4] + "/" + n
}

// Publish appends a version of name.
func (c *CratesIndex) Publish(name, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := cratesIndexPath(name)
	c.crates[p] = append(c.crates[p], crateVers{name: name, vers: version})
}

// Yank marks a published version yanked (the only field the index lets change).
func (c *CratesIndex) Yank(name, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vs := c.crates[cratesIndexPath(name)]
	for i := range vs {
		if vs[i].vers == version {
			vs[i].yanked = true
		}
	}
}

// Deny makes the index refuse name anonymously (401).
func (c *CratesIndex) Deny(name string) {
	c.mu.Lock()
	c.denied[cratesIndexPath(name)] = true
	c.mu.Unlock()
}

// Requests lists every request seen.
func (c *CratesIndex) Requests() []CratesRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CratesRequest(nil), c.reqs...)
}

func (c *CratesIndex) serve(w http.ResponseWriter, req *http.Request) {
	c.mu.Lock()
	c.reqs = append(c.reqs, CratesRequest{req.URL.Path, req.Header.Get("Authorization")})
	vs, ok := c.crates[req.URL.Path]
	deny := c.denied[req.URL.Path]
	vs = append([]crateVers(nil), vs...)
	c.mu.Unlock()
	switch {
	case deny:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !ok:
		http.Error(w, "not found", http.StatusNotFound)
	default:
		w.Header().Set("Content-Type", "text/plain")
		enc := json.NewEncoder(w) // one JSON object per line
		for _, v := range vs {
			enc.Encode(map[string]any{"name": v.name, "vers": v.vers, "deps": []any{}, "cksum": strings.Repeat("0", 64),
				"features": map[string]any{}, "yanked": v.yanked})
		}
	}
}
