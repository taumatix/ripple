package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Nuget is a fake nuget.org: GET /v3/index.json answers the service index of
// docs/vendor/nuget/service-index.md with a PackageBaseAddress/3.0.0 resource at
// /v3-flatcontainer/, and GET /v3-flatcontainer/<lowercased id>/index.json answers
// {"versions": [...]} (lowercased, oldest first). An id never published answers 404, a Denied
// one 401, any other path 404, and every request is recorded. The documents are written
// out here, not produced by internal/nuget's types.
type Nuget struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	pkgs   map[string][]string // lowercased id -> versions
	denied map[string]bool
	reqs   []NugetRequest
}

// NugetRequest is one request the fake saw.
type NugetRequest struct{ Path, Authorization string }

func NewNuget(t testing.TB) *Nuget {
	t.Helper()
	n := &Nuget{pkgs: map[string][]string{}, denied: map[string]bool{}}
	n.srv = httptest.NewServer(n.Wrap(http.HandlerFunc(n.serve)))
	n.URL = n.srv.URL
	t.Cleanup(n.srv.Close)
	return n
}

// Publish adds a version of the package id (any casing).
func (n *Nuget) Publish(id, version string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := strings.ToLower(id)
	n.pkgs[k] = append(n.pkgs[k], strings.ToLower(version))
}

// Deny makes id answer 401.
func (n *Nuget) Deny(id string) {
	n.mu.Lock()
	n.denied[strings.ToLower(id)] = true
	n.mu.Unlock()
}

// Requests lists every request seen.
func (n *Nuget) Requests() []NugetRequest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NugetRequest(nil), n.reqs...)
}

func (n *Nuget) serve(w http.ResponseWriter, req *http.Request) {
	n.mu.Lock()
	n.reqs = append(n.reqs, NugetRequest{req.URL.Path, req.Header.Get("Authorization")})
	n.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if req.URL.Path == "/v3/index.json" {
		json.NewEncoder(w).Encode(map[string]any{"version": "3.0.0", "resources": []map[string]string{
			{"@id": n.URL + "/query", "@type": "SearchQueryService", "comment": "x"},
			{"@id": n.URL + "/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0", "comment": "Base URL of where NuGet packages are stored, in the format https://api.nuget.org/v3-flatcontainer/{id-lower}/{version-lower}/{id-lower}.{version-lower}.nupkg"},
		}})
		return
	}
	id, ok := strings.CutPrefix(req.URL.Path, "/v3-flatcontainer/")
	id, ok2 := strings.CutSuffix(id, "/index.json")
	if !ok || !ok2 || strings.Contains(id, "/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	n.mu.Lock()
	vs, known, denied := n.pkgs[id], n.pkgs[id] != nil, n.denied[id]
	n.mu.Unlock()
	switch {
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !known:
		http.Error(w, "not found", http.StatusNotFound)
	default:
		json.NewEncoder(w).Encode(map[string]any{"versions": vs})
	}
}
