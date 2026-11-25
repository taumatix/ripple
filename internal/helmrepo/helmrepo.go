// Package helmrepo reads chart versions from a Helm chart repository's index.yaml,
// anonymously, over https only.
//
// It is written from docs/vendor/helm: a repository is a directory served over HTTP whose
// index.yaml maps each chart name to a list of its versions (the chart's metadata plus
// urls and a digest), of which ripple uses "version" and "deprecated". It has never been
// run against a real repository.
package helmrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/httpretry"
)

var (
	// ErrNotFound is a repository or chart the server does not have.
	ErrNotFound = errors.New("helm: repository not found")
	// ErrDenied is a repository the server will not serve anonymously.
	ErrDenied = errors.New("helm: anonymous access denied")
)

// maxBody bounds an index.yaml; large public repositories are tens of MiB.
const maxBody = 128 << 20

// NormalizeURL is the canonical form of a repository URL, or "" when ripple will not read
// it: https only, a host, no credentials, query or fragment. A trailing slash is dropped,
// so the two spellings of one repository are one package.
func NormalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	return strings.TrimRight(raw, "/")
}

// Client reads indexes without ever sending a credential.
type Client struct {
	// HTTP overrides the default client, which refuses to dial a non-public address.
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy
}

var safeHTTP = &http.Client{
	Timeout: 120 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: refusePrivate}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("helm: too many redirects or a redirect to a non-https URL")
		}
		return nil
	},
}

// refusePrivate runs on the address about to be dialled, after name resolution: the
// repository URL comes from a file in someone's repository, so a name that resolves to an
// internal address, or a redirect to one, is refused.
func refusePrivate(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	a := ap.Addr().Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return fmt.Errorf("helm: refusing to connect to non-public address %s", a)
	}
	return nil
}

// Version is one published chart version.
type Version struct {
	Version    string
	Deprecated bool
}

// Index is the part of an index.yaml ripple uses: versions by chart name.
type Index struct{ Entries map[string][]Version }

// Index fetches repo's index.yaml; repo is a NormalizeURL form.
func (c *Client) Index(ctx context.Context, repo string) (Index, error) {
	if NormalizeURL(repo) != repo {
		return Index{}, fmt.Errorf("helm: %q is not a repository URL ripple reads", repo)
	}
	hc := c.HTTP
	if hc == nil {
		hc = safeHTTP
	}
	resp, err := c.Retry.Do(ctx, hc, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, repo+"/index.yaml", nil)
		if err != nil {
			return nil, err
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return Index{}, fmt.Errorf("helmrepo: %s: %w", repo, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Index{}, fmt.Errorf("%w: %s", ErrNotFound, repo)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Index{}, fmt.Errorf("%w: %s", ErrDenied, repo)
	case resp.StatusCode != http.StatusOK:
		return Index{}, fmt.Errorf("helm: %s: HTTP %d", repo, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Index{}, fmt.Errorf("helm: %s: %w", repo, err)
	}
	if len(body) > maxBody {
		return Index{}, fmt.Errorf("helm: %s: index.yaml larger than %d bytes", repo, maxBody)
	}
	var doc struct {
		Entries map[string][]struct {
			Version    string `yaml:"version"`
			Deprecated bool   `yaml:"deprecated"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return Index{}, fmt.Errorf("helm: %s: index.yaml: %w", repo, err)
	}
	out := Index{Entries: make(map[string][]Version, len(doc.Entries))}
	for name, vs := range doc.Entries {
		for _, v := range vs {
			if v.Version != "" { // one bad entry must not hide the others
				out.Entries[name] = append(out.Entries[name], Version{v.Version, v.Deprecated})
			}
		}
	}
	return out, nil
}

// Releases lists the versions of chart ripple will propose: semver releases (no
// prerelease, no build metadata) of a chart version that is not deprecated, as the index
// spells them (a leading "v" kept).
func (ix Index) Releases(chart string) []string {
	var out []string
	for _, v := range ix.Entries[chart] {
		sv := v.Version
		if !strings.HasPrefix(sv, "v") {
			sv = "v" + sv
		}
		if !v.Deprecated && semver.IsValid(sv) && semver.Prerelease(sv) == "" && semver.Build(sv) == "" && strings.Count(strings.TrimPrefix(sv, "v"), ".") == 2 {
			out = append(out, v.Version)
		}
	}
	return out
}
