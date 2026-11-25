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
	"strings"
	"sync"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/store"
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
	beforeL, err := a.loadListing(ctx, key)
	if err != nil {
		return err
	}
	archivedBeforeL, err := a.loadListing(ctx, archivedKey)
	if err != nil {
		return err
	}
	before, archivedBefore := beforeL.names, archivedBeforeL.names
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
		// No event: the active listing is unchanged, and a changed archived one is only remembered
		// (first seen archived). A legacy single cursor is still moved to per-repo keys.
		if err := a.saveListing(ctx, archivedKey, archivedBeforeL, archivedNow); err != nil {
			return err
		}
		return a.saveListing(ctx, key, beforeL, now)
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
	if err := a.saveListing(ctx, archivedKey, archivedBeforeL, archivedNow); err != nil {
		return err
	}
	return a.saveListing(ctx, key, beforeL, now)
}

// listing is a host's stored listing. names is sorted. Where the store lists cursors it is
// kept one cursor per repo, "<key>#<repo>", so no single item grows with the host (DynamoDB
// caps one at 400 KB); legacy says the names came, wholly or in part, from the single JSON
// cursor "<key>" an earlier version wrote, which saveListing then moves to per-repo keys.
type listing struct {
	names  []string
	keyed  []string // the names held as per-repo cursors
	legacy bool
}

const listingPage = 500

// loadListing reads the listing under key: the per-repo cursors, then any legacy cursor.
func (a *App) loadListing(ctx context.Context, key string) (listing, error) {
	var l listing
	st := a.Queue.Store
	raw, err := st.GetCursor(ctx, key)
	if err != nil {
		return l, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &l.names); err != nil {
			return l, fmt.Errorf("cursor %s: %w", key, err)
		}
		l.legacy = true
	}
	cl, ok := st.(store.CursorLister)
	if !ok {
		return l, nil
	}
	prefix := key + store.CursorGroupSep
	after := ""
	for {
		var page []store.Cursor
		if pl, ok := st.(store.PagedCursorLister); ok {
			page, err = pl.ListCursorsPage(ctx, prefix, after, listingPage)
		} else {
			page, err = cl.ListCursors(ctx, prefix)
		}
		if err != nil {
			return l, err
		}
		for _, c := range page {
			l.keyed = append(l.keyed, strings.TrimPrefix(c.Name, prefix))
		}
		if _, paged := st.(store.PagedCursorLister); !paged || len(page) == 0 {
			break
		}
		after = page[len(page)-1].Name
	}
	sort.Strings(l.keyed)
	l.names = slices.Compact(slices.Concat(l.names, l.keyed))
	sort.Strings(l.names)
	return l, nil
}

// saveListing makes the stored listing under key equal now. On a store that lists cursors it
// writes only the difference, and deletes a legacy cursor last, so a crash in between leaves
// both and the next pass repeats the move; any other store keeps one JSON cursor.
func (a *App) saveListing(ctx context.Context, key string, before listing, now []string) error {
	st := a.Queue.Store
	cl, ok := st.(store.CursorLister)
	if !ok {
		if slices.Equal(before.names, now) {
			return nil
		}
		b, _ := json.Marshal(now)
		return st.SetCursor(ctx, key, string(b))
	}
	for _, n := range now {
		if _, has := slices.BinarySearch(before.keyed, n); !has {
			if err := st.SetCursor(ctx, key+store.CursorGroupSep+n, "1"); err != nil {
				return err
			}
		}
	}
	for _, n := range before.keyed {
		if _, keep := slices.BinarySearch(now, n); !keep {
			if err := cl.DeleteCursor(ctx, key+store.CursorGroupSep+n); err != nil {
				return err
			}
		}
	}
	if before.legacy {
		return cl.DeleteCursor(ctx, key)
	}
	return nil
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
