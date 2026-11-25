package pypi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
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
	// RequiresPython is the distinct non-empty Requires-Python values of the release's
	// files (the JSON API reports it per file); empty when no file states one.
	RequiresPython []string
	// Unconstrained is true when some file states no Requires-Python, so it runs on
	// any interpreter.
	Unconstrained bool
}

// RunsOn reports whether some file of the release admits the Python version py
// ("3.9"). It is true when py is unknown or any Requires-Python is not understood:
// only a stated, understood exclusion counts against the release.
func (r Release) RunsOn(py string) bool {
	v, ok := Parse(py)
	if !ok || r.Unconstrained || len(r.RequiresPython) == 0 {
		return true
	}
	for _, spec := range r.RequiresPython {
		if sat, known := Satisfies(spec, v); sat || !known {
			return true
		}
	}
	return false
}

// Release returns the release with exactly this version string.
func (p Project) Release(version string) (Release, bool) {
	for _, r := range p.Releases {
		if r.Version == version {
			return r, true
		}
	}
	return Release{}, false
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
			Yanked         bool    `json:"yanked"`
			RequiresPython *string `json:"requires_python"`
		} `json:"releases"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return Project{}, fmt.Errorf("pypi: %s: %w", name, err)
	}
	var out Project
	for v, files := range doc.Releases {
		r := Release{Version: v, Files: len(files), Yanked: len(files) > 0}
		seen := map[string]bool{}
		for _, f := range files {
			if !f.Yanked {
				r.Yanked = false
			}
			if f.RequiresPython == nil || strings.TrimSpace(*f.RequiresPython) == "" {
				r.Unconstrained = true
			} else if rp := strings.TrimSpace(*f.RequiresPython); !seen[rp] {
				seen[rp] = true
				r.RequiresPython = append(r.RequiresPython, rp)
			}
		}
		sort.Strings(r.RequiresPython)
		out.Releases = append(out.Releases, r)
	}
	return out, nil
}

// Newest is the newest version above cur that ripple will propose: a final or post
// release (no pre-release, dev or local version) with at least one file, not yanked.
// "" means none.
func (p Project) Newest(cur string) string { return p.NewestWhere(cur, nil) }

// NewestWhere is Newest restricted to versions allow accepts (nil accepts all).
func (p Project) NewestWhere(cur string, allow func(Version) bool) string {
	best, ok := Parse(cur)
	if !ok {
		return ""
	}
	out := ""
	for _, r := range p.Releases {
		v, ok := Parse(r.Version)
		if !ok || r.Yanked || r.Files == 0 || !v.IsRelease() || Compare(v, best) <= 0 || (allow != nil && !allow(v)) {
			continue
		}
		best, out = v, r.Version
	}
	return out
}
