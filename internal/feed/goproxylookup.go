package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
)

// lookupTTL is how long an answer, including "unknown", is reused across repos: a module
// new to twenty repos in one scan is asked once.
const lookupTTL = 10 * time.Minute

// GoProxyLookup asks a module proxy (the GOPROXY protocol) for the latest release of one
// module path. The poller only sees releases published after it started, so this is how a
// module nobody else imports, which has no stored release, is found once.
//
// It asks @latest first. A module with only prereleases has no @latest (404), so it falls
// back to the version list; a latest that its own go.mod retracts is skipped for the newest
// version that is not retracted. Stable versions beat prereleases. Answers are cached for
// ten minutes, negative ones too.
type GoProxyLookup struct {
	BaseURL     string
	User, Token string // HTTP basic auth, when the proxy needs it
	HTTP        *http.Client
	UserAgent   string
	// Now is the clock for the cache; nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]lookupEntry
}

type lookupEntry struct {
	rel     model.Release
	ok      bool
	expires time.Time
}

// Latest returns the proxy's latest usable release of pkg; ok is false when the proxy does
// not know the module (404 or 410), which is no error.
func (g *GoProxyLookup) Latest(ctx context.Context, pkg string) (model.Release, bool, error) {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	g.mu.Lock()
	if e, hit := g.cache[pkg]; hit && now().Before(e.expires) {
		g.mu.Unlock()
		return e.rel, e.ok, nil
	}
	g.mu.Unlock()
	rel, ok, err := g.lookup(ctx, pkg)
	if err != nil {
		return rel, ok, err
	}
	g.mu.Lock()
	if g.cache == nil {
		g.cache = map[string]lookupEntry{}
	}
	g.cache[pkg] = lookupEntry{rel: rel, ok: ok, expires: now().Add(lookupTTL)}
	g.mu.Unlock()
	return rel, ok, nil
}

func (g *GoProxyLookup) lookup(ctx context.Context, pkg string) (model.Release, bool, error) {
	esc, err := module.EscapePath(pkg)
	if err != nil {
		return model.Release{}, false, nil
	}
	base := strings.TrimRight(g.BaseURL, "/") + "/" + esc
	body, found, err := g.get(ctx, base+"/@latest", 1<<20)
	if err != nil {
		return model.Release{}, false, fmt.Errorf("go proxy latest %s: %w", pkg, err)
	}
	var latest model.Release
	if found {
		info, ok := decodeInfo(body)
		if !ok {
			return model.Release{}, false, fmt.Errorf("go proxy latest %s: undecodable answer", pkg)
		}
		latest = model.Release{Datasource: model.DatasourceGo, Package: pkg, Version: info.Version, Time: info.Time}
	}
	// The newest version's go.mod carries the module's retractions, and decides whether the
	// answer stands. A module with no @latest has prereleases only (or is unknown).
	list, err := g.versions(ctx, base, pkg)
	if err != nil {
		if found {
			return latest, true, nil // the list is an upgrade of the answer, not a requirement
		}
		return model.Release{}, false, err
	}
	if !found && len(list) == 0 {
		return model.Release{}, false, nil
	}
	if found {
		list = appendUnique(list, latest.Version)
	}
	sort.Slice(list, func(i, j int) bool { return semver.Compare(list[i], list[j]) > 0 })
	top := list[0]
	if found {
		top = latest.Version
	}
	retracted := g.retractions(ctx, base, top)
	for pass := 0; pass < 2; pass++ { // stable versions first, then prereleases
		for _, v := range list {
			if (semver.Prerelease(v) != "") != (pass == 1) || retracted(v) {
				continue
			}
			if found && v == latest.Version {
				return latest, true, nil
			}
			return g.release(ctx, base, pkg, v), true, nil
		}
	}
	if found {
		return latest, true, nil // everything is retracted: nothing better to offer
	}
	return model.Release{}, false, nil
}

func (g *GoProxyLookup) release(ctx context.Context, base, pkg, v string) model.Release {
	r := model.Release{Datasource: model.DatasourceGo, Package: pkg, Version: v}
	if body, ok, err := g.get(ctx, base+"/@v/"+v+".info", 1<<20); err == nil && ok {
		if info, ok := decodeInfo(body); ok {
			r.Time = info.Time
		}
	}
	return r
}

// retractions returns whether a version is retracted by the go.mod of top; a go.mod that
// cannot be read retracts nothing.
func (g *GoProxyLookup) retractions(ctx context.Context, base, top string) func(string) bool {
	none := func(string) bool { return false }
	body, ok, err := g.get(ctx, base+"/@v/"+top+".mod", 1<<20)
	if err != nil || !ok {
		return none
	}
	f, err := modfile.ParseLax("go.mod", body, nil)
	if err != nil || len(f.Retract) == 0 {
		return none
	}
	return func(v string) bool {
		for _, r := range f.Retract {
			if semver.Compare(v, r.Low) >= 0 && semver.Compare(v, r.High) <= 0 {
				return true
			}
		}
		return false
	}
}

func (g *GoProxyLookup) versions(ctx context.Context, base, pkg string) ([]string, error) {
	body, ok, err := g.get(ctx, base+"/@v/list", 4<<20)
	if err != nil {
		return nil, fmt.Errorf("go proxy list %s: %w", pkg, err)
	}
	if !ok {
		return nil, nil
	}
	var out []string
	for _, l := range strings.Fields(string(body)) {
		if semver.IsValid(l) {
			out = append(out, l)
		}
	}
	return out, nil
}

func appendUnique(l []string, v string) []string {
	for _, x := range l {
		if x == v {
			return l
		}
	}
	return append(l, v)
}

type proxyInfo struct {
	Version string
	Time    time.Time
}

func decodeInfo(b []byte) (proxyInfo, bool) {
	var info proxyInfo
	if err := json.Unmarshal(b, &info); err != nil || info.Version == "" {
		return info, false
	}
	return info, true
}

// get fetches one URL; found is false for 404 and 410.
func (g *GoProxyLookup) get(ctx context.Context, url string, limit int64) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
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
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
		return b, true, err
	case http.StatusNotFound, http.StatusGone:
		return nil, false, nil
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
}
