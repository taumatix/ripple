package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// NpmRegistry is a fake npm registry: GET /{package} answers the abbreviated metadata
// document (name, dist-tags, versions with an optional deprecated message), a scoped
// name arriving as /@scope%2Fname. It is written from docs/vendor/npm. A package never
// published answers 404, a Denied one 401, and every request is recorded.
type NpmRegistry struct {
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	pkgs   map[string]*npmPkg
	denied map[string]bool
	reqs   []NpmRequest
}

// NpmRequest is one request the fake saw.
type NpmRequest struct{ Path, Accept, Authorization string }

type npmPkg struct {
	tags     map[string]string
	versions map[string]string // version -> deprecated message
}

func NewNpmRegistry(t testing.TB) *NpmRegistry {
	t.Helper()
	r := &NpmRegistry{pkgs: map[string]*npmPkg{}, denied: map[string]bool{}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	r.URL = r.srv.URL
	t.Cleanup(r.srv.Close)
	return r
}

// Publish adds version to name and moves the latest dist-tag to it unless it is a
// prerelease (npm's default for `npm publish` without --tag is latest; ripple's
// tests publish prereleases the way maintainers do, under another tag).
func (r *NpmRegistry) Publish(name, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pkgs[name]
	if p == nil {
		p = &npmPkg{tags: map[string]string{}, versions: map[string]string{}}
		r.pkgs[name] = p
	}
	p.versions[version] = ""
	if strings.Contains(version, "-") {
		p.tags["next"] = version
	} else {
		p.tags["latest"] = version
	}
}

// Tag points a dist-tag at a version.
func (r *NpmRegistry) Tag(name, tag, version string) {
	r.mu.Lock()
	r.pkgs[name].tags[tag] = version
	r.mu.Unlock()
}

// Deprecate marks a version deprecated with a message.
func (r *NpmRegistry) Deprecate(name, version, msg string) {
	r.mu.Lock()
	r.pkgs[name].versions[version] = msg
	r.mu.Unlock()
}

// Deny makes the registry refuse name anonymously (401).
func (r *NpmRegistry) Deny(name string) { r.mu.Lock(); r.denied[name] = true; r.mu.Unlock() }

// Requests lists every request seen.
func (r *NpmRegistry) Requests() []NpmRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]NpmRequest(nil), r.reqs...)
}

func (r *NpmRegistry) serve(w http.ResponseWriter, req *http.Request) {
	name := strings.TrimPrefix(req.URL.EscapedPath(), "/")
	name = strings.Replace(name, "%2F", "/", 1)
	name = strings.Replace(name, "%2f", "/", 1)
	r.mu.Lock()
	r.reqs = append(r.reqs, NpmRequest{req.URL.EscapedPath(), req.Header.Get("Accept"), req.Header.Get("Authorization")})
	p, ok := r.pkgs[name]
	deny := r.denied[name]
	var doc map[string]any
	if ok {
		versions := map[string]any{}
		for v, dep := range p.versions {
			o := map[string]any{"name": name, "version": v, "dist": map[string]string{"tarball": r.URL + "/" + name + "/-/" + v + ".tgz"}}
			if dep != "" {
				o["deprecated"] = dep
			}
			versions[v] = o
		}
		tags := map[string]string{}
		for k, v := range p.tags {
			tags[k] = v
		}
		doc = map[string]any{"name": name, "modified": "2026-01-01T00:00:00.000Z", "dist-tags": tags, "versions": versions}
	}
	r.mu.Unlock()
	switch {
	case deny:
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	case !ok:
		http.Error(w, `{"error":"Not found"}`, http.StatusNotFound)
	default:
		w.Header().Set("Content-Type", "application/vnd.npm.install-v1+json")
		json.NewEncoder(w).Encode(doc)
	}
}
