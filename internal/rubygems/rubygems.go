// Package rubygems reads the versions of a gem from rubygems.org, anonymously.
//
// It is written from docs/vendor/rubygems/rubygems-org-api.md: GET /api/v1/versions/<gem>.json
// answers an array of versions with number, platform and prerelease. Nothing here has run
// against the real service (see UPSTREAM.md).
package rubygems

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
const DefaultBase = "https://rubygems.org"

var (
	// ErrNotFound is a gem the registry does not have.
	ErrNotFound = errors.New("rubygems: gem not found")
	// ErrDenied is a gem the registry will not serve anonymously.
	ErrDenied = errors.New("rubygems: anonymous access denied")
)

const maxBody = 64 << 20

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name can be a gem name. Names come from a Gemfile, so one is
// checked before it joins a URL.
func ValidName(name string) bool { return len(name) <= 100 && nameRE.MatchString(name) }

var releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

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
	Number     string
	Platform   string
	Prerelease bool
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("rubygems: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Versions fetches every published version of gem.
func (c *Client) Versions(ctx context.Context, gem string) ([]Version, error) {
	if !ValidName(gem) {
		return nil, fmt.Errorf("rubygems: %q is not a gem name", gem)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/api/v1/versions/" + gem + ".json"
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
		return nil, fmt.Errorf("rubygems: %s: %w", gem, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, gem)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrDenied, gem)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("rubygems: %s: HTTP %d", gem, resp.StatusCode)
	}
	var doc []struct {
		Number     string `json:"number"`
		Platform   string `json:"platform"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("rubygems: %s: %w", gem, err)
	}
	out := make([]Version, 0, len(doc))
	for _, d := range doc {
		out = append(out, Version{Number: d.Number, Platform: d.Platform, Prerelease: d.Prerelease})
	}
	return out, nil
}

// Releases lists the versions ripple may propose, as X.Y.Z: not a prerelease, built for the
// "ruby" platform (a platform build such as x86_64-linux is the same release), three numeric
// segments (a four-segment version cannot be moved, so it is not proposed either).
func Releases(vs []Version) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vs {
		if v.Prerelease || (v.Platform != "" && v.Platform != "ruby") || !releaseRE.MatchString(v.Number) || seen[v.Number] {
			continue
		}
		if !semver.IsValid("v" + v.Number) {
			continue // leading zeros: not comparable
		}
		seen[v.Number] = true
		out = append(out, v.Number)
	}
	return out
}
