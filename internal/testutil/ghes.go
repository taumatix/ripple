package testutil

import (
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/platform/github"
)

// GHES is a fake GitHub Enterprise Server in front of a GitHub fake: it answers only
// under /api/v3 (REST) and /api/graphql (GraphQL), as a real GHES does
// (docs/vendor/ghes), and 404s everything else, including github.com's bare
// /repos/... and /graphql. Written from the docs, never compared with a real GHES.
type GHES struct {
	*GitHub
	// URL is the server root; APIURL is URL+"/api/v3"; GraphQLURL is URL+"/api/graphql".
	URL, APIURL, GraphQLURL string
	// Auth mints tokens against APIURL (it shadows the embedded fake's).
	Auth *github.AppAuth
	// Versions are the X-GitHub-Api-Version values this release supports; any other
	// value answers 410. nil means every version github.APIVersion and
	// github.FallbackAPIVersion; an empty header is always accepted (it means 2022-11-28).
	Versions []string
	// NoGraphQL makes /api/graphql answer 404.
	NoGraphQL bool
	// OldSchema makes the auto-merge mutation fail GraphQL validation (HTTP 200 with
	// an undefinedField error), as a schema without the mutation would.
	OldSchema bool

	mu     sync.Mutex
	gone   int
	seen   []string
	stray  []string
	gqlHit int
}

// NewGHES starts the fake. Repos, commits and PRs are managed through the embedded GitHub.
func NewGHES(t *testing.T) *GHES {
	t.Helper()
	h := &GHES{GitHub: NewGitHub(t)}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	h.URL = srv.URL
	h.APIURL = srv.URL + "/api/v3"
	h.GraphQLURL = srv.URL + "/api/graphql"
	h.Auth = &github.AppAuth{AppID: AppID, Key: h.GitHub.key, APIBase: h.APIURL}
	return h
}

// Gone is how many requests were refused with 410 for an unsupported version.
func (h *GHES) Gone() int { h.mu.Lock(); defer h.mu.Unlock(); return h.gone }

// VersionsSeen are the X-GitHub-Api-Version values received, in order ("" when absent).
func (h *GHES) VersionsSeen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

// Strays are the paths requested outside /api/v3 and /api/graphql.
func (h *GHES) Strays() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.stray...)
}

// GraphQLCalls is how many requests reached /api/graphql.
func (h *GHES) GraphQLCalls() int { h.mu.Lock(); defer h.mu.Unlock(); return h.gqlHit }

func (h *GHES) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var inner string
	switch {
	case r.URL.Path == "/api/graphql":
		inner = "/graphql"
	case strings.HasPrefix(r.URL.Path, "/api/v3/"):
		inner = strings.TrimPrefix(r.URL.Path, "/api/v3")
	default:
		h.mu.Lock()
		h.stray = append(h.stray, r.URL.Path)
		h.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	v := r.Header.Get("X-GitHub-Api-Version")
	h.mu.Lock()
	h.seen = append(h.seen, v)
	if inner == "/graphql" {
		h.gqlHit++
	}
	h.mu.Unlock()
	if v != "" && !h.supports(v) {
		h.mu.Lock()
		h.gone++
		h.mu.Unlock()
		http.Error(w, `{"message":"Requests that specify a closing down API version receive a 410 Gone response"}`, http.StatusGone)
		return
	}
	if inner == "/graphql" {
		if h.NoGraphQL {
			http.NotFound(w, r)
			return
		}
		if h.OldSchema {
			_, _ = w.Write([]byte(`{"errors":[{"type":"undefinedField","message":"Field 'enablePullRequestAutoMerge' doesn't exist on type 'Mutation'"}]}`))
			return
		}
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = inner
	r2.Header.Set("X-GitHub-Api-Version", github.APIVersion) // the embedded fake is strict about the version
	h.GitHub.ServeHTTP(w, r2)
}

func (h *GHES) supports(v string) bool {
	if h.Versions == nil {
		return v == github.APIVersion || v == github.FallbackAPIVersion
	}
	for _, s := range h.Versions {
		if s == v {
			return true
		}
	}
	return false
}

// Key is the App private key the fake verifies JWTs against.
func (h *GHES) Key() *rsa.PrivateKey { return h.GitHub.key }
