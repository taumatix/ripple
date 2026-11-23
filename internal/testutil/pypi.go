package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// PyPI is a fake pypi.org JSON API: GET /pypi/{name}/json answers {"info": ..., "releases":
// {version: [file...]}} with the file fields of docs/vendor/pip. Only the PEP 503
// normalized name is served (anything else is 404), so a client that forgets to
// normalize fails against it. A project never published answers 404, a Denied one
// 401, and every request is recorded.
type PyPI struct {
	URL string
	srv *httptest.Server

	mu       sync.Mutex
	projects map[string]map[string]*pypiRelease // by normalized name
	denied   map[string]bool
	reqs     []PyPIRequest
}

// PyPIRequest is one request the fake saw.
type PyPIRequest struct{ Path, Authorization string }

type pypiRelease struct{ files []bool } // one entry per file: yanked

func NewPyPI(t testing.TB) *PyPI {
	t.Helper()
	p := &PyPI{projects: map[string]map[string]*pypiRelease{}, denied: map[string]bool{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	p.URL = p.srv.URL
	t.Cleanup(p.srv.Close)
	return p
}

var pypiSep = regexp.MustCompile(`[-_.]+`)

func pypiNorm(name string) string { return strings.ToLower(pypiSep.ReplaceAllString(name, "-")) }

// Publish adds a version of name with an sdist and a wheel.
func (p *PyPI) Publish(name, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := pypiNorm(name)
	if p.projects[n] == nil {
		p.projects[n] = map[string]*pypiRelease{}
	}
	p.projects[n][version] = &pypiRelease{files: []bool{false, false}}
}

// PublishEmpty adds a version with no files (a release nothing can install).
func (p *PyPI) PublishEmpty(name, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := pypiNorm(name)
	if p.projects[n] == nil {
		p.projects[n] = map[string]*pypiRelease{}
	}
	p.projects[n][version] = &pypiRelease{}
}

// Yank marks every file of a published version yanked.
func (p *PyPI) Yank(name, version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.projects[pypiNorm(name)][version]; r != nil {
		for i := range r.files {
			r.files[i] = true
		}
	}
}

// Deny makes the index refuse name anonymously (401).
func (p *PyPI) Deny(name string) {
	p.mu.Lock()
	p.denied[pypiNorm(name)] = true
	p.mu.Unlock()
}

// Requests lists every request seen.
func (p *PyPI) Requests() []PyPIRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PyPIRequest(nil), p.reqs...)
}

func (p *PyPI) serve(w http.ResponseWriter, req *http.Request) {
	p.mu.Lock()
	p.reqs = append(p.reqs, PyPIRequest{req.URL.Path, req.Header.Get("Authorization")})
	rest := strings.TrimPrefix(req.URL.Path, "/pypi/")
	name := strings.TrimSuffix(rest, "/json")
	valid := strings.HasPrefix(req.URL.Path, "/pypi/") && strings.HasSuffix(rest, "/json") && !strings.Contains(name, "/")
	rels, ok := p.projects[name]
	deny := p.denied[name]
	out := map[string]any{}
	var versions []string
	for v := range rels {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	for _, v := range versions {
		files := []map[string]any{}
		for i, yanked := range rels[v].files {
			files = append(files, map[string]any{"filename": name + "-" + v + []string{".tar.gz", "-py3-none-any.whl"}[i%2],
				"packagetype": "sdist", "yanked": yanked, "yanked_reason": nil})
		}
		out[v] = files
	}
	p.mu.Unlock()
	switch {
	case !valid || (!ok && !deny):
		http.Error(w, "not found", http.StatusNotFound)
	case deny:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"name": name}, "releases": out})
	}
}
