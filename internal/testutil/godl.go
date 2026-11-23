package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// GoDL is a fake go.dev/dl: GET /?mode=json answers the supported Go releases in
// the documented shape ({"version":"go1.27.4","stable":true,"files":[...]}),
// newest first. Tests set the list; nothing here names a real Go version.
type GoDL struct {
	URL string

	mu       sync.Mutex
	versions []string
	requests int
}

func NewGoDL(t testing.TB) *GoDL {
	t.Helper()
	d := &GoDL{}
	srv := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(srv.Close)
	d.URL = srv.URL
	return d
}

// Set replaces the list; versions are spelled as the feed spells them ("go1.27.4",
// "go1.28rc1" for an unstable one).
func (d *GoDL) Set(versions ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.versions = versions
}

// Requests is the number of list calls served.
func (d *GoDL) Requests() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.requests
}

func (d *GoDL) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests++
	if r.URL.Path != "/" || r.URL.Query().Get("mode") != "json" {
		http.NotFound(w, r)
		return
	}
	type file struct {
		Filename string `json:"filename"`
		Kind     string `json:"kind"`
	}
	type rel struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []file `json:"files"`
	}
	var out []rel
	for _, v := range d.versions {
		stable := len(v) > 0 && v[len(v)-1] >= '0' && v[len(v)-1] <= '9'
		out = append(out, rel{Version: v, Stable: stable, Files: []file{{Filename: v + ".src.tar.gz", Kind: "source"}}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
