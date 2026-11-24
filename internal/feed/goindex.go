package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/model"
)

const DefaultGoIndexURL = "https://index.golang.org"

// GoIndex reads https://index.golang.org/index (new line delimited JSON with
// Path, Version, Timestamp; sorted chronologically; limit max 2000).
type GoIndex struct {
	BaseURL   string
	HTTP      *http.Client
	PageSize  int
	UserAgent string
	// Retry bounds the waiting on a 429 or a 503 with Retry-After; the zero value is
	// three attempts, none of them waited on longer than 30s.
	Retry httpretry.Policy
}

func (g *GoIndex) Name() string       { return "go-index" }
func (g *GoIndex) Datasource() string { return model.DatasourceGo }

func (g *GoIndex) Fetch(ctx context.Context, since string) ([]model.Release, bool, error) {
	size := g.PageSize
	if size <= 0 || size > 2000 {
		size = 2000
	}
	q := url.Values{"limit": {fmt.Sprint(size)}}
	if since != "" {
		q.Set("since", since)
	}
	target := strings.TrimRight(g.BaseURL, "/") + "/index?" + q.Encode()
	c := g.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := g.Retry.Do(ctx, c, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		if g.UserAgent != "" {
			req.Header.Set("User-Agent", g.UserAgent)
		}
		return req, nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("go index request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("go index: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out []model.Release
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 64<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e struct {
			Path, Version string
			Timestamp     time.Time
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, false, fmt.Errorf("go index: bad line %q: %w", truncate(line, 120), err)
		}
		if e.Path == "" || e.Version == "" {
			return nil, false, fmt.Errorf("go index: line without Path or Version: %q", truncate(line, 120))
		}
		out = append(out, model.Release{Datasource: model.DatasourceGo, Package: e.Path, Version: e.Version, Time: e.Timestamp.UTC()})
	}
	if err := sc.Err(); err != nil {
		return nil, false, fmt.Errorf("go index: read body: %w", err)
	}
	return out, len(out) >= size, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
