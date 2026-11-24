// Package cratesindex reads crate versions from a Cargo sparse registry index
// (crates.io's index.crates.io), anonymously.
//
// It is written from docs/vendor/cargo: one file per crate at a path derived from the
// lowercased name, each line a JSON object with "vers" and "yanked" (and more ripple
// does not use).
package cratesindex

import (
	"bufio"
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

// DefaultBase is crates.io's sparse index.
const DefaultBase = "https://index.crates.io"

var (
	// ErrNotFound is a crate the index does not have.
	ErrNotFound = errors.New("cargo: crate not found")
	// ErrDenied is a crate the index will not serve anonymously.
	ErrDenied = errors.New("cargo: anonymous access denied")
)

const maxBody = 64 << 20

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidName reports whether name can be a crate name (alphanumeric, '-' and '_').
// Names come from repository files, so one is checked before it joins a URL.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Path is the index file of a crate, relative to the index root: 1 and 2 character
// names live in directories 1 and 2, 3 character names in 3/{first}, longer ones in
// {first two}/{next two}; the file name is lowercase.
func Path(name string) string {
	n := strings.ToLower(name)
	switch len(n) {
	case 1:
		return "1/" + n
	case 2:
		return "2/" + n
	case 3:
		return "3/" + n[:1] + "/" + n
	}
	return n[:2] + "/" + n[2:4] + "/" + n
}

// Client reads the index without ever sending a credential.
type Client struct {
	// BaseURL is the index root; empty means DefaultBase.
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy
}

// Version is one published version.
type Version struct {
	Version string
	Yanked  bool
}

// Crate is the part of a crate's index file ripple uses.
type Crate struct{ Versions []Version }

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("cargo: too many redirects or a redirect to a non-https URL")
		}
		return nil
	}}
}

// Crate fetches the index file of name.
func (c *Client) Crate(ctx context.Context, name string) (Crate, error) {
	if !ValidName(name) {
		return Crate{}, fmt.Errorf("cargo: %q is not a crate name", name)
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBase
	}
	target := base + "/" + Path(name)
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
		return Crate{}, fmt.Errorf("cratesindex: %s: %w", name, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Crate{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Crate{}, fmt.Errorf("%w: %s", ErrDenied, name)
	case resp.StatusCode != http.StatusOK:
		return Crate{}, fmt.Errorf("cargo: %s: HTTP %d", name, resp.StatusCode)
	}
	var out Crate
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxBody))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v struct {
			Name   string `json:"name"`
			Vers   string `json:"vers"`
			Yanked bool   `json:"yanked"`
		}
		if json.Unmarshal([]byte(line), &v) != nil || v.Vers == "" {
			continue // one bad line must not hide the others
		}
		out.Versions = append(out.Versions, Version{Version: v.Vers, Yanked: v.Yanked})
	}
	if err := sc.Err(); err != nil {
		return Crate{}, fmt.Errorf("cargo: %s: %w", name, err)
	}
	return out, nil
}

// Newest is the newest version above cur that ripple will propose: a release (no
// prerelease) that is not yanked. "" means none.
func (c Crate) Newest(cur string) string {
	best := "v" + cur
	out := ""
	for _, v := range c.Versions {
		sv := "v" + v.Version
		if v.Yanked || !semver.IsValid(sv) || semver.Prerelease(sv) != "" || semver.Compare(sv, best) <= 0 {
			continue
		}
		best, out = sv, v.Version
	}
	return out
}
