package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MinGHESRelease is the oldest GitHub Enterprise Server release (major.minor) ripple's
// documentation claims support for (docs/platform-github-enterprise.md). It is ripple's
// own floor, not one GitHub publishes, and not verified against any instance: a release
// below it only earns a warning at start, never a refusal.
const MinGHESRelease = "3.9"

// releaseTimeout bounds the one meta request made at start.
const releaseTimeout = 5 * time.Second

// LogRelease reads the GHES release once from GET APIBase/meta (`installed_version`;
// unauthenticated, docs/vendor/ghes) and logs it, with a warning when it is below
// MinGHESRelease. The release is used for nothing else: the API version and the
// auto-merge handling still follow what the server answers. Any failure is logged and
// returns "", it never blocks start. A base that is not a GHES (/api/v3) is not asked.
func (c *Client) LogRelease(ctx context.Context) string {
	if !isGHESBase(c.APIBase) {
		return ""
	}
	log := c.Log
	if log == nil {
		return ""
	}
	v, err := c.fetchRelease(ctx)
	if err != nil {
		log.Warn("could not read the GitHub Enterprise Server release; carrying on", "err", err)
		return ""
	}
	below, ok := releaseBelow(v, MinGHESRelease)
	switch {
	case !ok:
		log.Warn("GitHub Enterprise Server release not understood; carrying on", "installed_version", v)
	case below:
		log.Warn("GitHub Enterprise Server release is older than the minimum ripple documents; it may not work",
			"installed_version", v, "minimum", MinGHESRelease)
	default:
		log.Info("GitHub Enterprise Server release", "installed_version", v)
	}
	return v
}

func isGHESBase(apiBase string) bool {
	u, err := url.Parse(strings.TrimRight(apiBase, "/"))
	return err == nil && strings.HasSuffix(u.Path, "/api/v3")
}

func (c *Client) fetchRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.APIBase, "/")+"/meta", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ripple")
	resp, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /meta: status %d", resp.StatusCode)
	}
	var m struct {
		InstalledVersion string `json:"installed_version"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return "", fmt.Errorf("GET /meta: %w", err)
	}
	if m.InstalledVersion == "" {
		return "", fmt.Errorf("GET /meta: no installed_version")
	}
	return m.InstalledVersion, nil
}

// releaseBelow compares the major.minor of v ("3.14.2", "3.15.0-rc1") with min.
// ok is false when v does not start with two numbers.
func releaseBelow(v, min string) (below, ok bool) {
	a, ok1 := majorMinor(v)
	b, ok2 := majorMinor(min)
	if !ok1 || !ok2 {
		return false, false
	}
	return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1]), true
}

func majorMinor(v string) ([2]int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return [2]int{}, false
	}
	var out [2]int
	for i := range out {
		digits := parts[i]
		if j := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); j >= 0 {
			digits = digits[:j]
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return [2]int{}, false
		}
		out[i] = n
	}
	return out, true
}
