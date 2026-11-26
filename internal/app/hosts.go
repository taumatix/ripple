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
	"github.com/taumatix/ripple/internal/model"
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
			if il, ok := h.Platform.(InstalledHeadLister); ok {
				if err := a.rescanInstalled(ctx, h.Name, il); err != nil && ctx.Err() == nil {
					a.log().Error("repo head rescan", "host", h.Name, "err", err)
				}
			}
			continue
		}
		repos, err := a.discover(ctx, h.Name, l)
		if err != nil {
			if ctx.Err() == nil {
				a.log().Error("repo discovery", "host", h.Name, "err", err)
			}
			continue
		}
		if hl, ok := l.(HeadLister); ok {
			if err := a.rescanMoved(ctx, h.Name, l, hl, repos); err != nil && ctx.Err() == nil {
				a.log().Error("repo head rescan", "host", h.Name, "err", err)
			}
		}
	}
}

func (a *App) discover(ctx context.Context, host string, l platform.Lister) ([]model.Repo, error) {
	a.init()
	repos, err := l.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	var archivedNow []string
	if al, ok := l.(platform.ArchivedLister); ok {
		ar, err := al.ListArchivedRepos(ctx)
		if err != nil {
			return nil, err
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
		return nil, err
	}
	archivedBeforeL, err := a.loadListing(ctx, archivedKey)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		return repos, a.saveListing(ctx, key, beforeL, now)
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
			return nil, err
		}
	}
	// The archived listing first: a crash between the two writes repeats the events (their
	// delivery ids make that a no-op) instead of losing the archive.
	if err := a.saveListing(ctx, archivedKey, archivedBeforeL, archivedNow); err != nil {
		return nil, err
	}
	return repos, a.saveListing(ctx, key, beforeL, now)
}

// HeadLister is the optional second half of a platform.Lister for a host with no push events:
// the commit each listed repo's default branch points at, by the names ListRepos returns. At
// each Discover the app compares it with the head it saw last time and queues a whole-repo
// Push for a repo whose head moved, so a host that cannot send webhooks still has its repos
// re-scanned. A repo missing from the map, or an error, leaves what was remembered in place.
type HeadLister interface {
	ListRepoHeads(ctx context.Context) (map[string]string, error)
}

// headKey is the cursor group of a host's remembered default-branch heads.
func headKey(host string) string { return "discover-head/" + host }

// rescanMoved queues one Push per default branch name for the listed repos whose head differs
// from the one remembered, then remembers the heads. A repo seen for the first time is only
// remembered (its ReposAdded scans it). The events go first and carry a delivery id derived
// from the heads, so a crash before the heads are written repeats them as a no-op. A store that
// cannot list cursors remembers nothing and so never re-scans.
func (a *App) rescanMoved(ctx context.Context, host string, l platform.Lister, hl HeadLister, repos []model.Repo) error {
	if _, ok := a.Queue.Store.(store.CursorLister); !ok {
		return nil
	}
	heads, err := hl.ListRepoHeads(ctx)
	if err != nil {
		return err
	}
	// A host that learns default branches while listing heads (Gerrit, CodeCommit) named none in
	// the listing above on its first round after a start. If a repo it just reported a head for
	// was listed without one, list again once, now that it can name them: a head that moved
	// while ripple was down is then re-scanned on the right branch in this round.
	for _, r := range repos {
		if _, listed := heads[r.FullName]; listed && r.DefaultBranch == "" {
			if again, err := l.ListRepos(ctx); err == nil {
				repos = again
			}
			break
		}
	}
	obs := make([]headObs, 0, len(repos))
	for _, r := range repos {
		obs = append(obs, headObs{name: r.FullName, branch: r.DefaultBranch, head: heads[r.FullName]})
	}
	return a.applyHeads(ctx, host, obs)
}

// InstalledHeadLister is the HeadLister of a host whose repos are not a listing the app can
// compare (GitHub: an App's installations, no ListRepos): it names, for each repo it can see,
// the installation the repo is reached through, its default branch and that branch's head. A
// moved head is queued as a Push under that installation, which is how a scan gets its token.
// The same rules apply as for HeadLister: a repo seen first is only remembered, an empty head is
// skipped, and an error leaves what was remembered in place.
type InstalledHeadLister interface {
	ListInstalledRepoHeads(ctx context.Context) ([]platform.RepoHead, error)
}

// rescanInstalled is rescanMoved for an InstalledHeadLister.
func (a *App) rescanInstalled(ctx context.Context, host string, il InstalledHeadLister) error {
	a.init() // as Run does first; a Tick right after a restart must not race it
	if _, ok := a.Queue.Store.(store.CursorLister); !ok {
		return nil
	}
	hs, err := il.ListInstalledRepoHeads(ctx)
	if err != nil {
		return err
	}
	obs := make([]headObs, 0, len(hs))
	for _, h := range hs {
		obs = append(obs, headObs{name: h.FullName, branch: h.DefaultBranch, head: h.Head, scope: h.InstallationID})
	}
	return a.applyHeads(ctx, host, obs)
}

// headObs is one repo's observed head: its default branch (empty when unknown), the commit
// (empty when it has none), and the credential scope a scan must run under (0 when the host
// has one credential).
type headObs struct {
	name, branch, head string
	scope              int64
}

// applyHeads queues the Pushes for the observed repos whose head differs from the one
// remembered, then remembers the heads. See rescanMoved.
func (a *App) applyHeads(ctx context.Context, host string, obs []headObs) error {
	st := a.Queue.Store
	cl, ok := st.(store.CursorLister)
	if !ok {
		return nil
	}
	prefix := headKey(host) + store.CursorGroupSep
	remembered := map[string]string{}
	after := ""
	for {
		var (
			page []store.Cursor
			err  error
		)
		if pl, ok := st.(store.PagedCursorLister); ok {
			page, err = pl.ListCursorsPage(ctx, prefix, after, listingPage)
		} else {
			page, err = cl.ListCursors(ctx, prefix)
		}
		if err != nil {
			return err
		}
		for _, c := range page {
			remembered[strings.TrimPrefix(c.Name, prefix)] = c.Value
		}
		if _, paged := st.(store.PagedCursorLister); !paged || len(page) == 0 {
			break
		}
		after = page[len(page)-1].Name
	}
	type group struct {
		scope  int64
		branch string
	}
	heads := make(map[string]string, len(obs))
	moved := map[group][]string{}
	var changed []string
	for _, o := range obs {
		heads[o.name] = o.head
		if o.head == "" || remembered[o.name] == o.head {
			continue
		}
		changed = append(changed, o.name)
		if _, seen := remembered[o.name]; seen && o.branch != "" {
			g := group{o.scope, o.branch}
			moved[g] = append(moved[g], o.name)
		}
	}
	groups := make([]group, 0, len(moved))
	for g := range moved {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].scope != groups[j].scope {
			return groups[i].scope < groups[j].scope
		}
		return groups[i].branch < groups[j].branch
	})
	for _, g := range groups {
		names := moved[g]
		sort.Strings(names)
		h := sha256.New()
		for _, n := range names {
			h.Write([]byte(n + "\x00" + heads[n] + "\x00"))
		}
		ev := platform.Event{Kind: platform.Push, Delivery: fmt.Sprintf("head-%x", h.Sum(nil)[:8]), Repos: names, Ref: "refs/heads/" + g.branch, Scope: g.scope}
		if g.scope != 0 {
			// The claimed default branch lets the engine heal a stored one that went stale.
			ev.DefaultBranch = g.branch
		}
		if err := a.enqueue(ctx, host, ev); err != nil {
			return err
		}
	}
	for _, n := range changed {
		if err := st.SetCursor(ctx, prefix+n, heads[n]); err != nil {
			return err
		}
	}
	for n := range remembered { // a repo that left the listing leaves nothing behind
		if _, listed := heads[n]; !listed {
			if err := cl.DeleteCursor(ctx, prefix+n); err != nil {
				return err
			}
		}
	}
	return nil
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
