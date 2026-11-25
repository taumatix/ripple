package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Rubygems is a fake rubygems.org: GET /api/v1/versions/<gem>.json answers the array of
// version objects of docs/vendor/rubygems/rubygems-org-api.md (number, platform,
// prerelease, ...). A gem never published answers 404, a Denied one 401, any other path
// 404, and every request is recorded. The document is written out here, not produced by
// internal/rubygems' types, so a wrong reading there fails against this fake.
type Rubygems struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	gems   map[string][]gemVersion
	denied map[string]bool
	reqs   []RubygemsRequest
}

// RubygemsRequest is one request the fake saw.
type RubygemsRequest struct{ Path, Authorization string }

type gemVersion struct {
	number, platform string
	prerelease       bool
}

func NewRubygems(t testing.TB) *Rubygems {
	t.Helper()
	g := &Rubygems{gems: map[string][]gemVersion{}, denied: map[string]bool{}}
	g.srv = httptest.NewServer(g.Wrap(http.HandlerFunc(g.serve)))
	g.URL = g.srv.URL
	t.Cleanup(g.srv.Close)
	return g
}

// Publish adds a release of gem for the "ruby" platform.
func (g *Rubygems) Publish(gem, number string) {
	g.add(gem, gemVersion{number: number, platform: "ruby"})
}

// PublishPrerelease adds a prerelease (prerelease: true).
func (g *Rubygems) PublishPrerelease(gem, number string) {
	g.add(gem, gemVersion{number: number, platform: "ruby", prerelease: true})
}

// PublishPlatform adds a build of a release for another platform, such as "java".
func (g *Rubygems) PublishPlatform(gem, number, platform string) {
	g.add(gem, gemVersion{number: number, platform: platform})
}

func (g *Rubygems) add(gem string, v gemVersion) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gems[gem] = append(g.gems[gem], v)
}

// Deny makes gem answer 401.
func (g *Rubygems) Deny(gem string) {
	g.mu.Lock()
	g.denied[gem] = true
	g.mu.Unlock()
}

// Requests lists every request seen.
func (g *Rubygems) Requests() []RubygemsRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]RubygemsRequest(nil), g.reqs...)
}

func (g *Rubygems) serve(w http.ResponseWriter, req *http.Request) {
	g.mu.Lock()
	g.reqs = append(g.reqs, RubygemsRequest{req.URL.Path, req.Header.Get("Authorization")})
	g.mu.Unlock()
	name, ok := strings.CutPrefix(req.URL.Path, "/api/v1/versions/")
	name, ok2 := strings.CutSuffix(name, ".json")
	if !ok || !ok2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	g.mu.Lock()
	vs, known, denied := g.gems[name], g.gems[name] != nil, g.denied[name]
	g.mu.Unlock()
	switch {
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !known:
		http.Error(w, "This rubygem could not be found.", http.StatusNotFound)
	default:
		var out []map[string]any
		for i := len(vs) - 1; i >= 0; i-- { // newest first, as the API lists them
			out = append(out, map[string]any{
				"authors": "someone", "built_at": "2011-08-08T04:00:00.000Z", "created_at": "2011-08-08T21:23:40.254Z",
				"description": "gem " + name, "downloads_count": 1, "metadata": map[string]string{},
				"number": vs[i].number, "summary": "s", "platform": vs[i].platform, "rubygems_version": ">= 0",
				"ruby_version": nil, "prerelease": vs[i].prerelease, "licenses": nil, "requirements": nil,
				"sha": strings.Repeat("0", 64),
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(out)
	}
}
