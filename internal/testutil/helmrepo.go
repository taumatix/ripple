package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// HelmRepo is a fake Helm chart repository: GET /index.yaml answers the index document of
// docs/vendor/helm/chart_repository.md (apiVersion, entries of chart name to a list of chart
// versions, generated). It is served over TLS, as ripple reads only https repositories; a
// test passes Client() to the datasource. The document is written out here, not produced by
// internal/helmrepo's types, so a wrong reading there fails against this fake. Any other path
// answers 404, a Denied repository 401, and every request is recorded.
type HelmRepo struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	charts map[string][]helmVersion
	denied bool
	reqs   []HelmRequest
}

// HelmRequest is one request the fake saw.
type HelmRequest struct{ Path, Authorization string }

type helmVersion struct {
	version    string
	deprecated bool
}

func NewHelmRepo(t testing.TB) *HelmRepo {
	t.Helper()
	h := &HelmRepo{charts: map[string][]helmVersion{}}
	h.srv = httptest.NewTLSServer(h.Wrap(http.HandlerFunc(h.serve)))
	h.URL = h.srv.URL
	t.Cleanup(h.srv.Close)
	return h
}

// Client is an HTTP client that trusts the fake's certificate.
func (h *HelmRepo) Client() *http.Client { return h.srv.Client() }

// Publish adds a version of chart to the index.
func (h *HelmRepo) Publish(chart, version string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.charts[chart] = append(h.charts[chart], helmVersion{version: version})
}

// Deprecate marks a published version `deprecated: true`, as a chart's own metadata says.
func (h *HelmRepo) Deprecate(chart, version string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	vs := h.charts[chart]
	for i := range vs {
		if vs[i].version == version {
			vs[i].deprecated = true
		}
	}
}

// Deny makes the repository refuse everything anonymously (401).
func (h *HelmRepo) Deny() {
	h.mu.Lock()
	h.denied = true
	h.mu.Unlock()
}

// Requests lists every request seen.
func (h *HelmRepo) Requests() []HelmRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HelmRequest(nil), h.reqs...)
}

func (h *HelmRepo) serve(w http.ResponseWriter, req *http.Request) {
	h.mu.Lock()
	h.reqs = append(h.reqs, HelmRequest{req.URL.Path, req.Header.Get("Authorization")})
	denied := h.denied
	var names []string
	for n := range h.charts {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("apiVersion: v1\nentries:\n")
	for _, n := range names {
		vs := h.charts[n]
		fmt.Fprintf(&b, "  %s:\n", n)
		for i := len(vs) - 1; i >= 0; i-- { // newest published first, as helm repo index writes it
			fmt.Fprintf(&b, "    - created: 2016-10-06T16:23:20.499814565-06:00\n      description: chart %s\n      digest: %064d\n      name: %s\n      urls:\n      - %s/%s-%s.tgz\n      version: %s\n",
				n, i, n, h.srv.URL, n, vs[i].version, vs[i].version)
			if vs[i].deprecated {
				b.WriteString("      deprecated: true\n")
			}
		}
	}
	b.WriteString("generated: 2016-10-06T16:23:20.499029981-06:00\n")
	h.mu.Unlock()
	switch {
	case req.URL.Path != "/index.yaml":
		http.Error(w, "not found", http.StatusNotFound)
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	default:
		w.Header().Set("Content-Type", "application/x-yaml")
		fmt.Fprint(w, b.String())
	}
}
