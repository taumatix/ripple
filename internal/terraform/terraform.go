// Package terraform reads the versions of a Terraform module or provider from the public
// registry, registry.terraform.io, anonymously.
//
// It is written from docs/vendor/terraform/module-registry-protocol.txt and
// provider-registry-protocol.txt:
//
//	GET /v1/modules/{namespace}/{name}/{system}/versions  -> {"modules":[{"versions":[{"version":"1.2.3"}]}]}
//	GET /v1/providers/{namespace}/{type}/versions         -> {"versions":[{"version":"1.2.3"}]}
//
// A package is named "modules/ns/name/system" or "providers/ns/type". Nothing here has run against
// the real service beyond one look at a sample of each answer (see UPSTREAM.md).
package terraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/httpretry"
)

// DefaultBase is the public registry.
const DefaultBase = "https://registry.terraform.io"

var (
	// ErrNotFound is a module or provider the registry does not have.
	ErrNotFound = errors.New("terraform: not found")
	// ErrDenied is one the registry will not serve anonymously.
	ErrDenied = errors.New("terraform: anonymous access denied")
)

const maxBody = 64 << 20

var (
	partRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// ModulePackage and ProviderPackage build the package key of a registry source address
// ("terraform-aws-modules/vpc/aws", "hashicorp/aws"); ok is false when the address is not a
// plain registry one (a host, a git or local path, a wrong number of parts, a bad character).
func ModulePackage(source string) (string, bool) { return pkg("modules/", source, 3) }

func ProviderPackage(source string) (string, bool) { return pkg("providers/", source, 2) }

func pkg(kind, source string, n int) (string, bool) {
	parts := strings.Split(source, "/")
	if len(parts) != n {
		return "", false
	}
	for _, p := range parts {
		if len(p) > 64 || !partRE.MatchString(p) {
			return "", false
		}
	}
	return kind + source, true
}

// ValidPackage reports whether p is a package key this client can read.
func ValidPackage(p string) bool {
	if s, ok := strings.CutPrefix(p, "modules/"); ok {
		_, ok = ModulePackage(s)
		return ok
	}
	if s, ok := strings.CutPrefix(p, "providers/"); ok {
		_, ok = ProviderPackage(s)
		return ok
	}
	return false
}

// Client reads the registry without ever sending a credential.
type Client struct {
	// BaseURL is the registry host; empty means DefaultBase.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After.
	Retry httpretry.Policy
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("terraform: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Versions fetches every version of the package ("modules/..." or "providers/..."), as the
// registry writes them.
func (c *Client) Versions(ctx context.Context, p string) ([]string, error) {
	if !ValidPackage(p) {
		return nil, fmt.Errorf("terraform: %q is not a registry package", p)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/v1/" + p + "/versions"
	resp, err := c.Retry.Do(ctx, c.client(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("terraform: %s: %w", p, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, p)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrDenied, p)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("terraform: %s: HTTP %d", p, resp.StatusCode)
	}
	type versions struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}
	var doc struct {
		versions
		Modules []versions `json:"modules"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("terraform: %s: %w", p, err)
	}
	var out []string
	for _, v := range doc.Versions {
		out = append(out, v.Version)
	}
	for _, m := range doc.Modules {
		for _, v := range m.Versions {
			out = append(out, v.Version)
		}
	}
	return out, nil
}

// Releases lists the versions ripple may propose: three numeric segments, which leaves out
// every prerelease (1.0.0-beta1), none of which a three-segment edit can move to.
func Releases(versions []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range versions {
		if !releaseRE.MatchString(v) || seen[v] || !semver.IsValid("v"+v) {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
