package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Terraform is a fake Terraform registry, shaped by docs/vendor/terraform/module-registry-protocol.txt
// and provider-registry-protocol.txt:
//
//	GET /v1/modules/{ns}/{name}/{provider}/versions  -> {"modules":[{"source":..,"versions":[{"version":..}]}]}
//	GET /v1/providers/{ns}/{type}/versions           -> {"id":..,"versions":[{"version":..,"protocols":[..]}]}
//
// A package never published answers 404, a Denied one 401, any other path 404, and every request is
// recorded. The JSON is written out here, not produced by internal/terraform's types.
type Terraform struct {
	Throttle
	URL string
	srv *httptest.Server

	mu     sync.Mutex
	pkgs   map[string][]string // "modules/ns/name/provider" or "providers/ns/type" -> versions
	denied map[string]bool
	reqs   []TerraformRequest
}

// TerraformRequest is one request the fake saw.
type TerraformRequest struct{ Path, Authorization string }

func NewTerraform(t testing.TB) *Terraform {
	t.Helper()
	f := &Terraform{pkgs: map[string][]string{}, denied: map[string]bool{}}
	f.srv = httptest.NewServer(f.Wrap(http.HandlerFunc(f.serve)))
	f.URL = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

// Publish adds a version of "modules/ns/name/provider" or "providers/ns/type".
func (f *Terraform) Publish(pkg, version string) {
	f.mu.Lock()
	f.pkgs[pkg] = append(f.pkgs[pkg], version)
	f.mu.Unlock()
}

// Deny makes the package answer 401.
func (f *Terraform) Deny(pkg string) {
	f.mu.Lock()
	f.denied[pkg] = true
	f.mu.Unlock()
}

// Requests lists every request seen.
func (f *Terraform) Requests() []TerraformRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TerraformRequest(nil), f.reqs...)
}

func (f *Terraform) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, TerraformRequest{req.URL.Path, req.Header.Get("Authorization")})
	f.mu.Unlock()
	p, ok := strings.CutSuffix(strings.TrimPrefix(req.URL.Path, "/v1/"), "/versions")
	if !ok || !strings.HasPrefix(req.URL.Path, "/v1/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	f.mu.Lock()
	vs, known, denied := append([]string(nil), f.pkgs[p]...), f.pkgs[p] != nil, f.denied[p]
	f.mu.Unlock()
	switch {
	case denied:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	case !known:
		http.Error(w, "not found", http.StatusNotFound)
	default:
		var b strings.Builder
		list := func() {
			for i, v := range vs {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"version":%q,"protocols":["5.0"]}`, v)
			}
		}
		if src, isMod := strings.CutPrefix(p, "modules/"); isMod {
			fmt.Fprintf(&b, `{"modules":[{"source":%q,"versions":[`, src)
			list()
			b.WriteString("]}]}")
		} else {
			fmt.Fprintf(&b, `{"id":%q,"versions":[`, strings.TrimPrefix(p, "providers/"))
			list()
			b.WriteString("]}")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(b.String()))
	}
}
