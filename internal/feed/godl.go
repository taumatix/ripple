package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultGoDLURL serves the Go release list as JSON (`?mode=json`).
const DefaultGoDLURL = "https://go.dev/dl/"

var goReleaseRE = regexp.MustCompile(`^go([0-9]+\.[0-9]+(\.[0-9]+)?)$`)

// GoDL reads the Go release list of go.dev/dl. Without `include=all` the list holds
// the Go releases the project supports (each major release until two newer ones are
// out): the newest stable patch of each. That list is the end-of-support policy, so
// ripple asks for it and does not compute one. The list carries no release dates.
type GoDL struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

// Supported returns the stable releases the Go project currently supports, as semver
// ("v1.27.4"), newest first as the list gives them.
func (g *GoDL) Supported(ctx context.Context) ([]string, error) {
	base := g.BaseURL
	if base == "" {
		base = DefaultGoDLURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/?mode=json", nil)
	if err != nil {
		return nil, err
	}
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	c := g.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("go releases request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("go releases: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var list []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("go releases: decode: %w", err)
	}
	var out []string
	for _, r := range list {
		m := goReleaseRE.FindStringSubmatch(r.Version)
		if !r.Stable || m == nil || !semver.IsValid("v"+m[1]) {
			continue
		}
		out = append(out, "v"+m[1])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("go releases: no stable release in the list")
	}
	return out, nil
}
