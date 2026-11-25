// Package maven reads the versions of a Maven artifact from a Maven repository (Maven Central by
// default), anonymously.
//
// It is written from docs/vendor/maven/repository-layout.txt and repository-metadata.txt: the
// artifact's directory holds a maven-metadata.xml whose <versioning><versions> lists every version.
// Nothing here has run against the real service beyond one look at a sample file (see UPSTREAM.md).
package maven

import (
	"context"
	"encoding/xml"
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

// DefaultBase is Maven Central.
const DefaultBase = "https://repo.maven.apache.org/maven2"

var (
	// ErrNotFound is an artifact the repository does not have.
	ErrNotFound = errors.New("maven: artifact not found")
	// ErrDenied is an artifact the repository will not serve anonymously.
	ErrDenied = errors.New("maven: anonymous access denied")
)

const maxBody = 64 << 20

var partRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

var releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidCoordinate reports whether groupId and artifactId can name an artifact. They come from a
// pom.xml, so they are checked before they join a URL ("..", "/" and the like are refused).
func ValidCoordinate(group, artifact string) bool {
	ok := func(s string) bool { return len(s) <= 200 && partRE.MatchString(s) && !strings.Contains(s, "..") }
	return ok(group) && ok(artifact)
}

// Split splits the package key "groupId:artifactId".
func Split(pkg string) (group, artifact string, ok bool) {
	group, artifact, ok = strings.Cut(pkg, ":")
	return group, artifact, ok && ValidCoordinate(group, artifact)
}

// Client reads the repository without ever sending a credential.
type Client struct {
	// BaseURL is the repository root; empty means DefaultBase.
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
			return errors.New("maven: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Versions fetches every version of the artifact "groupId:artifactId", as maven-metadata.xml lists them.
func (c *Client) Versions(ctx context.Context, pkg string) ([]string, error) {
	group, artifact, ok := Split(pkg)
	if !ok {
		return nil, fmt.Errorf("maven: %q is not groupId:artifactId", pkg)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/" + strings.ReplaceAll(group, ".", "/") + "/" + artifact + "/maven-metadata.xml"
	resp, err := c.Retry.Do(ctx, c.client(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/xml, text/xml")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("maven: %s: %w", pkg, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, pkg)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrDenied, pkg)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("maven: %s: HTTP %d", pkg, resp.StatusCode)
	}
	var doc struct {
		Versions []string `xml:"versioning>versions>version"`
	}
	dec := xml.NewDecoder(io.LimitReader(resp.Body, maxBody))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("maven: %s: %w", pkg, err)
	}
	for i := range doc.Versions {
		doc.Versions[i] = strings.TrimSpace(doc.Versions[i])
	}
	return doc.Versions, nil
}

// Releases lists the versions ripple may propose: three numeric segments, which leaves out
// every qualified version (1.0.0-SNAPSHOT, 1.0.0.RELEASE, 1.0-beta) and the one- and
// two-segment ones, none of which a three-segment edit can move to.
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
