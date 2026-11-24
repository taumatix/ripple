// Package npmreg reads package metadata from the npm registry, anonymously.
//
// It is written from docs/vendor/npm: abbreviated metadata
// (application/vnd.npm.install-v1+json) carries dist-tags and each version's
// deprecation message, and no publication dates.
package npmreg

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

	"github.com/taumatix/ripple/internal/httpretry"
	"golang.org/x/mod/semver"
)

// DefaultBase is the public registry.
const DefaultBase = "https://registry.npmjs.org"

var (
	// ErrNotFound is a package the registry does not have (or hides: a private
	// package answers like a missing one).
	ErrNotFound = errors.New("npm: package not found")
	// ErrDenied is a package the registry will not serve anonymously.
	ErrDenied = errors.New("npm: anonymous access denied")
)

const maxBody = 64 << 20

// nameRE is npm's name shape: an optional @scope/ then a name, URL-safe characters
// only. Names come from repository files, so one is checked before it joins a URL.
var nameRE = regexp.MustCompile(`^(@[A-Za-z0-9~][A-Za-z0-9._~-]*/)?[A-Za-z0-9~][A-Za-z0-9._~-]*$`)

// ValidName reports whether name can be a registry package name.
func ValidName(name string) bool { return len(name) <= 214 && nameRE.MatchString(name) }

// Client reads the registry without ever sending a credential.
type Client struct {
	// BaseURL is the registry; empty means DefaultBase.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy
}

// Version is one published version.
type Version struct {
	Version    string
	Deprecated bool
}

// Packument is the part of a package's metadata ripple uses.
type Packument struct {
	// Latest is the version of the "latest" dist-tag, or "".
	Latest   string
	Versions []Version
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("npm: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Packument fetches the abbreviated metadata of name.
func (c *Client) Packument(ctx context.Context, name string) (Packument, error) {
	if !ValidName(name) {
		return Packument{}, fmt.Errorf("npm: %q is not a package name", name)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/" + strings.Replace(name, "/", "%2F", 1)
	resp, err := c.Retry.Do(ctx, c.client(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8, */*")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return Packument{}, fmt.Errorf("npm: %s: %w", name, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Packument{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Packument{}, fmt.Errorf("%w: %s", ErrDenied, name)
	case resp.StatusCode != http.StatusOK:
		return Packument{}, fmt.Errorf("npm: %s: HTTP %d", name, resp.StatusCode)
	}
	var doc struct {
		DistTags map[string]string                   `json:"dist-tags"`
		Versions map[string]struct{ Deprecated any } `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return Packument{}, fmt.Errorf("npm: %s: %w", name, err)
	}
	pv := Packument{Latest: doc.DistTags["latest"]}
	for v, o := range doc.Versions {
		s, _ := o.Deprecated.(string)
		pv.Versions = append(pv.Versions, Version{Version: v, Deprecated: s != ""})
	}
	return pv, nil
}

// Releases lists the versions ripple may propose: releases (no prerelease) that are
// not deprecated and not above the "latest" dist-tag when there is one.
func (p Packument) Releases() []string {
	var out []string
	for _, v := range p.Versions {
		sv := "v" + v.Version
		if v.Deprecated || !semver.IsValid(sv) || semver.Prerelease(sv) != "" {
			continue
		}
		if p.Latest != "" && semver.IsValid("v"+p.Latest) && semver.Compare(sv, "v"+p.Latest) > 0 {
			continue
		}
		out = append(out, v.Version)
	}
	return out
}

// Newest is the newest version above cur that ripple will propose: a release (no
// prerelease), not deprecated, and not above the "latest" dist-tag when there is one
// (a version published beyond latest is on a channel the maintainers did not promote).
// "" means none.
func (p Packument) Newest(cur string) string {
	best := "v" + cur
	out := ""
	for _, v := range p.Versions {
		sv := "v" + v.Version
		if v.Deprecated || !semver.IsValid(sv) || semver.Prerelease(sv) != "" || semver.Compare(sv, best) <= 0 {
			continue
		}
		if p.Latest != "" && semver.IsValid("v"+p.Latest) && semver.Compare(sv, "v"+p.Latest) > 0 {
			continue
		}
		best, out = sv, v.Version
	}
	return out
}
