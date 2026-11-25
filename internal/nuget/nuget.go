// Package nuget reads the versions of a NuGet package from nuget.org, anonymously.
//
// It is written from docs/vendor/nuget/package-base-address-resource.md and service-index.md:
// the service index lists the PackageBaseAddress resource, and GET {@id}/{lower id}/index.json
// answers {"versions": [...]}, every version listed or unlisted, lowercased and normalized.
// Nothing here has run against the real service (see UPSTREAM.md).
package nuget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/httpretry"
)

// DefaultBase is the public host; its service index is at /v3/index.json.
const DefaultBase = "https://api.nuget.org"

var (
	// ErrNotFound is a package the registry does not have.
	ErrNotFound = errors.New("nuget: package not found")
	// ErrDenied is a package the registry will not serve anonymously.
	ErrDenied = errors.New("nuget: anonymous access denied")
)

const maxBody = 64 << 20

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name can be a package id. Ids come from a project file, so one is
// checked before it joins a URL.
func ValidName(name string) bool { return len(name) <= 100 && nameRE.MatchString(name) }

var releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Client reads the registry without ever sending a credential.
type Client struct {
	// BaseURL is the host whose /v3/index.json is the service index; empty means DefaultBase.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After.
	Retry httpretry.Policy

	mu   sync.Mutex
	base string // the PackageBaseAddress, found once in the service index
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("nuget: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

func (c *Client) get(ctx context.Context, target string) (*http.Response, error) {
	return c.Retry.Do(ctx, c.client(), func() (*http.Request, error) {
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
}

// packageBase returns the PackageBaseAddress/3.0.0 resource of the service index. The
// spec says the address may change, so it is read from the index, not built.
func (c *Client) packageBase(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base != "" {
		return c.base, nil
	}
	host := strings.TrimRight(c.BaseURL, "/")
	if host == "" {
		host = DefaultBase
	}
	resp, err := c.get(ctx, host+"/v3/index.json")
	if err != nil {
		return "", fmt.Errorf("nuget: service index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nuget: service index: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return "", fmt.Errorf("nuget: service index: %w", err)
	}
	for _, r := range doc.Resources {
		if strings.HasPrefix(r.Type, "PackageBaseAddress/3.0.0") && r.ID != "" {
			if !strings.HasPrefix(r.ID, "https://") && !strings.HasPrefix(r.ID, host) {
				return "", fmt.Errorf("nuget: service index names a non-https package base %q", r.ID)
			}
			c.base = strings.TrimRight(r.ID, "/")
			return c.base, nil
		}
	}
	return "", errors.New("nuget: service index has no PackageBaseAddress/3.0.0 resource")
}

// Versions fetches every version of id, listed or not, as the registry writes them.
func (c *Client) Versions(ctx context.Context, id string) ([]string, error) {
	if !ValidName(id) {
		return nil, fmt.Errorf("nuget: %q is not a package id", id)
	}
	base, err := c.packageBase(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.get(ctx, base+"/"+strings.ToLower(id)+"/index.json")
	if err != nil {
		return nil, fmt.Errorf("nuget: %s: %w", id, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrDenied, id)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("nuget: %s: HTTP %d", id, resp.StatusCode)
	}
	var doc struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("nuget: %s: %w", id, err)
	}
	return doc.Versions, nil
}

// Releases lists the versions ripple may propose: three numeric segments, which leaves out
// every prerelease (1.0.0-beta) and every four-segment version (1.2.3.4), neither of which a
// three-segment edit can move to.
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
