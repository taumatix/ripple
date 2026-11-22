package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"sync"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

// HostDeps is what the server hands every host builder.
type HostDeps struct {
	Server *config.Server
	// Getenv reads the host's own settings (RIPPLE_<HOST>_*); os.Getenv in production.
	Getenv func(string) string
	HTTP   *http.Client
	Log    *slog.Logger
	// Git is the checkout setup every host shares; the builder sets GitBase.
	Git gitws.Options
}

// HostBuilder builds one host. It returns nil, nil when the host is not configured
// (its settings are absent), so an unused host costs nothing.
type HostBuilder func(ctx context.Context, d HostDeps) (*platform.Host, error)

var (
	buildersMu sync.Mutex
	builders   = map[string]HostBuilder{}
)

// RegisterHost is the single registration point for a code host. Call it from an
// init function in internal/app/host_<name>.go; see docs/platform-authoring.md.
func RegisterHost(name string, b HostBuilder) {
	buildersMu.Lock()
	defer buildersMu.Unlock()
	if _, dup := builders[name]; dup {
		panic("app: host " + name + " registered twice")
	}
	builders[name] = b
}

// RegisteredHosts lists the names of every compiled-in host, sorted.
func RegisteredHosts() []string {
	buildersMu.Lock()
	defer buildersMu.Unlock()
	var out []string
	for n := range builders {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// BuildHosts runs every registered builder and keeps the hosts that are configured.
func BuildHosts(ctx context.Context, d HostDeps) ([]*platform.Host, error) {
	var hosts []*platform.Host
	for _, name := range RegisteredHosts() {
		buildersMu.Lock()
		b := builders[name]
		buildersMu.Unlock()
		h, err := b(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", name, err)
		}
		if h == nil {
			continue
		}
		if h.Name != name {
			return nil, fmt.Errorf("host registered as %s built itself as %s", name, h.Name)
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// Discover lists the repos of every host that is a platform.Lister and queues
// ReposAdded and ReposRemoved for what changed since its previous listing. The
// previous listing is kept as a store cursor per host, so a restart does not
// re-register everything. A listing error leaves the previous listing in place.
func (a *App) Discover(ctx context.Context) {
	for _, h := range a.Hosts {
		l, ok := h.Platform.(platform.Lister)
		if !ok {
			continue
		}
		if err := a.discover(ctx, h.Name, l); err != nil && ctx.Err() == nil {
			a.log().Error("repo discovery", "host", h.Name, "err", err)
		}
	}
}

func (a *App) discover(ctx context.Context, host string, l platform.Lister) error {
	a.init()
	repos, err := l.ListRepos(ctx)
	if err != nil {
		return err
	}
	now := make([]string, 0, len(repos))
	for _, r := range repos {
		now = append(now, r.FullName)
	}
	sort.Strings(now)
	now = slices.Compact(now)
	key := "discover/" + host
	raw, err := a.Queue.Store.GetCursor(ctx, key)
	if err != nil {
		return err
	}
	var before []string
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &before); err != nil {
			return fmt.Errorf("cursor %s: %w", key, err)
		}
	}
	added, removed := diff(before, now)
	if len(added)+len(removed) == 0 {
		return nil
	}
	gen := fmt.Sprintf("list-%x", hashNames(now))
	for _, c := range []struct {
		kind  platform.EventKind
		repos []string
	}{{platform.ReposAdded, added}, {platform.ReposRemoved, removed}} {
		if len(c.repos) == 0 {
			continue
		}
		if err := a.enqueue(ctx, host, platform.Event{Kind: c.kind, Delivery: gen + "-" + string(c.kind), Repos: c.repos}); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(now)
	return a.Queue.Store.SetCursor(ctx, key, string(b))
}

// diff returns what is in now and not before, and what is in before and not now;
// both inputs are sorted.
func diff(before, now []string) (added, removed []string) {
	for _, n := range now {
		if _, found := slices.BinarySearch(before, n); !found {
			added = append(added, n)
		}
	}
	for _, b := range before {
		if _, found := slices.BinarySearch(now, b); !found {
			removed = append(removed, b)
		}
	}
	return added, removed
}

func (a *App) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.New(slog.DiscardHandler)
}

func hashNames(names []string) []byte {
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
	}
	return h.Sum(nil)[:8]
}
