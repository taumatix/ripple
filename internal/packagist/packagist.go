// Package packagist reads the versions of a Composer package from Packagist, anonymously.
//
// It is written from docs/vendor/packagist/packagist-api.txt: GET /p2/<vendor>/<package>.json
// on repo.packagist.org answers {"packages": {"<name>": [{"version": ...}, ...]}, "minified":
// "composer/2.0"}, the tagged releases only. Nothing here has run against the real service
// (see UPSTREAM.md).
package packagist

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

// DefaultBase is the public metadata host.
const DefaultBase = "https://repo.packagist.org"

var (
	// ErrNotFound is a package the registry does not have.
	ErrNotFound = errors.New("packagist: package not found")
	// ErrDenied is a package the registry will not serve anonymously.
	ErrDenied = errors.New("packagist: anonymous access denied")
)

const maxBody = 64 << 20

// nameRE is vendor/project, lowercase words separated by "-", "." or "_"
// (docs/vendor/packagist/composer-schema.md, "name").
var nameRE = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9](([_.]|-{1,2})?[a-z0-9]+)*$`)

// ValidName reports whether name can be a package name. Names come from a composer.json, so
// one is checked before it joins a URL. Platform packages (php, ext-json) have no slash and
// are refused.
func ValidName(name string) bool { return len(name) <= 200 && nameRE.MatchString(name) }

var releaseRE = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)

// Client reads the registry without ever sending a credential.
type Client struct {
	// BaseURL is the metadata host; empty means DefaultBase.
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
			return errors.New("packagist: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Versions fetches the version of every tagged release of pkg, as the registry writes it
// (a "v" prefix is kept; see Releases).
func (c *Client) Versions(ctx context.Context, pkg string) ([]string, error) {
	if !ValidName(pkg) {
		return nil, fmt.Errorf("packagist: %q is not a package name", pkg)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/p2/" + pkg + ".json"
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
		return nil, fmt.Errorf("packagist: %s: %w", pkg, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, pkg)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrDenied, pkg)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("packagist: %s: HTTP %d", pkg, resp.StatusCode)
	}
	var doc struct {
		Packages map[string][]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("packagist: %s: %w", pkg, err)
	}
	var out []string
	for _, v := range doc.Packages[pkg] {
		out = append(out, v.Version)
	}
	return out, nil
}

// Releases lists the versions ripple may propose, as X.Y.Z without a "v": three numeric
// segments only, so no prerelease (1.0.0-beta1), no branch (dev-main, 1.x-dev) and no
// four-segment version, none of which a constraint can be moved to by editing digits.
func Releases(versions []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range versions {
		m := releaseRE.FindStringSubmatch(v)
		if m == nil || seen[m[1]] || !semver.IsValid("v"+m[1]) {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}
