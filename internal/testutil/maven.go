package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Maven is a fake Maven repository: GET /<group with / for .>/<artifact>/maven-metadata.xml
// answers the metadata document of docs/vendor/maven/repository-metadata.txt, with every published
// version under <versioning><versions> (oldest first, as Maven writes them). An artifact never
// published answers 404, a Denied one 401, any other path 404, and every request is recorded.
// The XML is written out here, not produced by internal/maven's types.
type Maven struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	pkgs   map[string][]string // "group:artifact" -> versions
	denied map[string]bool
	reqs   []MavenRequest
}

// MavenRequest is one request the fake saw.
type MavenRequest struct{ Path, Authorization string }

func NewMaven(t testing.TB) *Maven {
	t.Helper()
	m := &Maven{pkgs: map[string][]string{}, denied: map[string]bool{}}
	m.srv = httptest.NewServer(m.Wrap(http.HandlerFunc(m.serve)))
	m.URL = m.srv.URL
	t.Cleanup(m.srv.Close)
	return m
}

// Publish adds a version of "groupId:artifactId".
func (m *Maven) Publish(pkg, version string) {
	m.mu.Lock()
	m.pkgs[pkg] = append(m.pkgs[pkg], version)
	m.mu.Unlock()
}

// Deny makes the artifact answer 401.
func (m *Maven) Deny(pkg string) {
	m.mu.Lock()
	m.denied[pkg] = true
	m.mu.Unlock()
}

// Requests lists every request seen.
func (m *Maven) Requests() []MavenRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MavenRequest(nil), m.reqs...)
}

func (m *Maven) serve(w http.ResponseWriter, req *http.Request) {
	m.mu.Lock()
	m.reqs = append(m.reqs, MavenRequest{req.URL.Path, req.Header.Get("Authorization")})
	m.mu.Unlock()
	p, ok := strings.CutSuffix(strings.TrimPrefix(req.URL.Path, "/"), "/maven-metadata.xml")
	i := strings.LastIndex(p, "/")
	if !ok || i < 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	group, artifact := strings.ReplaceAll(p[:i], "/", "."), p[i+1:]
	key := group + ":" + artifact
	m.mu.Lock()
	vs, known, denied := append([]string(nil), m.pkgs[key]...), m.pkgs[key] != nil, m.denied[key]
	m.mu.Unlock()
	switch {
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !known:
		http.Error(w, "not found", http.StatusNotFound)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<metadata>\n  <groupId>%s</groupId>\n  <artifactId>%s</artifactId>\n  <versioning>\n    <latest>%s</latest>\n    <release>%s</release>\n    <versions>\n", group, artifact, vs[len(vs)-1], vs[len(vs)-1])
		for _, v := range vs {
			fmt.Fprintf(&b, "      <version>%s</version>\n", v)
		}
		b.WriteString("    </versions>\n    <lastUpdated>20240101000000</lastUpdated>\n  </versioning>\n</metadata>\n")
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(b.String()))
	}
}
