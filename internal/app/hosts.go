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
	var archivedNow []string
	if al, ok := l.(platform.ArchivedLister); ok {
		ar, err := al.ListArchivedRepos(ctx)
		if err != nil {
			return err
		}
		for _, r := range ar {
			archivedNow = append(archivedNow, r.FullName)
		}
	}
	now := make([]string, 0, len(repos))
	for _, r := range repos {
		now = append(now, r.FullName)
	}
	sort.Strings(now)
	now = slices.Compact(now)
	sort.Strings(archivedNow)
	archivedNow = slices.Compact(archivedNow)
	archivedNow = slices.DeleteFunc(archivedNow, func(n string) bool { _, active := slices.BinarySearch(now, n); return active })

	key := "discover/" + host
	archivedKey := "discover-archived/" + host
	before, err := a.listingCursor(ctx, key)
	if err != nil {
		return err
	}
	archivedBefore, err := a.listingCursor(ctx, archivedKey)
	if err != nil {
		return err
	}
	// A repo that moved between the listings was archived or unarchived; one in neither
	// listing any more was deleted (or lost to the credential); one in neither before is new.
	var archived, unarchived []string
	for _, n := range archivedNow {
		if _, was := slices.BinarySearch(before, n); was {
			archived = append(archived, n)
		}
	}
	for _, n := range now {
		if _, was := slices.BinarySearch(archivedBefore, n); was {
			unarchived = append(unarchived, n)
		}
	}
	var added []string
	for _, n := range now {
		_, known := slices.BinarySearch(before, n)
		_, wasArchived := slices.BinarySearch(archivedBefore, n)
		if !known && !wasArchived {
			added = append(added, n)
		}
	}
	var removed []string
	for _, n := range slices.Concat(before, archivedBefore) {
		_, active := slices.BinarySearch(now, n)
		_, arch := slices.BinarySearch(archivedNow, n)
		if !active && !arch {
			removed = append(removed, n)
		}
	}
	sort.Strings(removed)
	removed = slices.Compact(removed)
	if len(added)+len(removed)+len(archived)+len(unarchived) == 0 {
		if slices.Equal(archivedBefore, archivedNow) {
			return nil
		}
		return a.setListingCursor(ctx, archivedKey, archivedNow) // first seen archived: only remembered
	}
	gen := fmt.Sprintf("list-%x", hashNames(slices.Concat(now, []string{""}, archivedNow)))
	for _, c := range []struct {
		kind  platform.EventKind
		repos []string
	}{{platform.ReposAdded, added}, {platform.ReposRemoved, removed}, {platform.RepoArchived, archived}, {platform.RepoUnarchived, unarchived}} {
		if len(c.repos) == 0 {
			continue
		}
		if err := a.enqueue(ctx, host, platform.Event{Kind: c.kind, Delivery: gen + "-" + string(c.kind), Repos: c.repos}); err != nil {
			return err
		}
	}
	// The archived listing first: a crash between the two writes repeats the events (their
	// delivery ids make that a no-op) instead of losing the archive.
	if err := a.setListingCursor(ctx, archivedKey, archivedNow); err != nil {
		return err
	}
	return a.setListingCursor(ctx, key, now)
}

// listingCursor is a host's stored listing: a sorted JSON list of names, empty when none.
func (a *App) listingCursor(ctx context.Context, key string) ([]string, error) {
	raw, err := a.Queue.Store.GetCursor(ctx, key)
	if err != nil || raw == "" {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("cursor %s: %w", key, err)
	}
	return names, nil
}

func (a *App) setListingCursor(ctx context.Context, key string, names []string) error {
	b, _ := json.Marshal(names)
	return a.Queue.Store.SetCursor(ctx, key, string(b))
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
