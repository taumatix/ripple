package pypi

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
)

// DefaultBase is pypi.org.
const DefaultBase = "https://pypi.org"

var (
	// ErrNotFound is a project the index does not have.
	ErrNotFound = errors.New("pypi: project not found")
	// ErrDenied is a project the index will not serve anonymously.
	ErrDenied = errors.New("pypi: anonymous access denied")
)

const maxBody = 64 << 20

var nameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)

// ValidName reports whether name can be a project name. Names come from repository
// files, so one is checked before it joins a URL.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Client reads the JSON API without ever sending a credential.
type Client struct {
	// BaseURL is the index root; empty means DefaultBase.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy
}

// Release is one published version.
type Release struct {
	Version string
	// Yanked is true when every file of the release is yanked.
	Yanked bool
	// Files is the number of files the release has; none means nothing to install.
	Files int
}

// Project is the part of a project's JSON document ripple uses.
type Project struct{ Releases []Release }

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("pypi: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Project fetches /pypi/{normalized name}/json.
func (c *Client) Project(ctx context.Context, name string) (Project, error) {
	if !ValidName(name) {
		return Project{}, fmt.Errorf("pypi: %q is not a project name", name)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/pypi/" + NormalizeName(name) + "/json"
	resp, err := c.Retry.Do(ctx, c.client(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return Project{}, fmt.Errorf("pypi: %s: %w", name, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Project{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Project{}, fmt.Errorf("%w: %s", ErrDenied, name)
	case resp.StatusCode != http.StatusOK:
		return Project{}, fmt.Errorf("pypi: %s: HTTP %d", name, resp.StatusCode)
	}
	var doc struct {
		Releases map[string][]struct {
			Yanked bool `json:"yanked"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return Project{}, fmt.Errorf("pypi: %s: %w", name, err)
	}
	var out Project
	for v, files := range doc.Releases {
		r := Release{Version: v, Files: len(files), Yanked: len(files) > 0}
		for _, f := range files {
			if !f.Yanked {
				r.Yanked = false
			}
		}
		out.Releases = append(out.Releases, r)
	}
	return out, nil
}

// Newest is the newest version above cur that ripple will propose: a final or post
// release (no pre-release, dev or local version) with at least one file, not yanked.
// "" means none.
func (p Project) Newest(cur string) string {
	best, ok := Parse(cur)
	if !ok {
		return ""
	}
	out := ""
	for _, r := range p.Releases {
		v, ok := Parse(r.Version)
		if !ok || r.Yanked || r.Files == 0 || !v.IsRelease() || Compare(v, best) <= 0 {
			continue
		}
		best, out = v, r.Version
	}
	return out
}
