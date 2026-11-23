package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/module"

	"github.com/taumatix/ripple/internal/model"
)

// GoProxyLookup asks a module proxy (the GOPROXY protocol) for the latest release of one
// module path. The poller only sees releases published after it started, so this is how a
// module nobody else imports, which has no stored release, is found once.
type GoProxyLookup struct {
	BaseURL     string
	User, Token string // HTTP basic auth, when the proxy needs it
	HTTP        *http.Client
	UserAgent   string
}

// Latest returns the proxy's latest release of pkg; ok is false when the proxy does not
// know the module (404 or 410), which is no error.
func (g *GoProxyLookup) Latest(ctx context.Context, pkg string) (model.Release, bool, error) {
	esc, err := module.EscapePath(pkg)
	if err != nil {
		return model.Release{}, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.BaseURL, "/")+"/"+esc+"/@latest", nil)
	if err != nil {
		return model.Release{}, false, err
	}
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if g.Token != "" {
		req.SetBasicAuth(g.User, g.Token)
	}
	c := g.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return model.Release{}, false, fmt.Errorf("go proxy latest %s: %w", pkg, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return model.Release{}, false, nil
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return model.Release{}, false, fmt.Errorf("go proxy latest %s: status %d: %s", pkg, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var info struct {
		Version string
		Time    time.Time
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil || info.Version == "" {
		return model.Release{}, false, fmt.Errorf("go proxy latest %s: undecodable answer", pkg)
	}
	return model.Release{Datasource: model.DatasourceGo, Package: pkg, Version: info.Version, Time: info.Time}, true, nil
}
