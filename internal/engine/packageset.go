package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// A polled-package set lives in one cursor name per package, "<cursor>#<package>", when the
// store lists cursors (store.CursorLister). Replicas then add packages without overwriting
// each other's additions, which the single newline-separated cursor allowed: two replicas
// scanning different repos could each write back a list missing the other's package. The
// legacy newline cursor is still read, so a set written by an earlier version keeps
// polling, and the first read folds it into per-package keys and deletes it.

func packageKey(cursor, pkg string) string { return cursor + store.CursorGroupSep + pkg }

// polledPackages is the union of the legacy cursor and the per-package cursors, sorted. Where
// the store lists cursors, a legacy list is first folded into per-package keys.
func (e *Engine) polledPackages(ctx context.Context, cursor string) ([]string, error) {
	start := time.Now()
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		return nil, err
	}
	if cl, ok := e.Store.(store.CursorLister); ok && strings.TrimSpace(raw) != "" {
		if err := e.foldLegacyPackages(ctx, cl, cursor); err != nil {
			e.log().Warn("legacy package list not folded", "cursor", cursor, "err", err)
		} else {
			raw = ""
		}
	}
	if pl, ok := e.Store.(store.PagedCursorLister); ok && e.PollPackagesPerPoll > 0 && strings.TrimSpace(raw) == "" {
		if _, ok := e.Store.(store.CursorLister); ok {
			return e.pagedSlice(ctx, pl, cursor)
		}
	}
	set := map[string]bool{}
	for _, p := range strings.Fields(raw) {
		set[p] = true
	}
	if cl, ok := e.Store.(store.CursorLister); ok {
		cs, err := cl.ListCursors(ctx, cursor+store.CursorGroupSep)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			set[strings.TrimPrefix(c.Name, cursor+store.CursorGroupSep)] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	e.log().Debug("polled packages listed", "cursor", cursor, "packages", len(out), "took", time.Since(start))
	return e.nextSlice(ctx, cursor, out)
}

// resumeCursor holds the last package of the previous slice. Its name does not start with
// "<cursor>#", so it is never listed as a package.
func resumeCursor(cursor string) string { return cursor + "!resume" }

// nextSlice is the packages of one poll: all of them unless Engine.PollPackagesPerPoll is
// set and smaller than the set, else the next that many after the package the previous poll
// stopped at, wrapping to the start. The position is stored before the slice is polled, so
// a poll that fails midway does not retry its slice at once; the slice comes round again in
// the next cycle. A package added or dropped in between only shifts which slice it falls in.
func (e *Engine) nextSlice(ctx context.Context, cursor string, sorted []string) ([]string, error) {
	n := e.PollPackagesPerPoll
	if n <= 0 || len(sorted) <= n {
		return sorted, nil
	}
	last, err := e.Store.GetCursor(ctx, resumeCursor(cursor))
	if err != nil {
		return nil, err
	}
	start := sort.SearchStrings(sorted, last)
	if start < len(sorted) && sorted[start] == last {
		start++
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, sorted[(start+i)%len(sorted)])
	}
	if err := e.Store.SetCursor(ctx, resumeCursor(cursor), out[len(out)-1]); err != nil {
		return nil, err
	}
	return out, nil
}

// pagedSlice is nextSlice for a store that lists cursors in pages and a set with no legacy
// list left: it reads only the packages one poll needs, not the set, and returns what
// nextSlice would have returned for the whole sorted set. Names compare bytewise on both
// sides, so the store's order is the sort.Strings order. It reads at most PollPackagesPerPoll+1
// names after the resume position and, when those run out before the slice is full, the same
// again from the start, so the set is told to be at most one slice (returned whole, resume
// untouched) from larger (rotated, resume stored) without listing it.
func (e *Engine) pagedSlice(ctx context.Context, pl store.PagedCursorLister, cursor string) ([]string, error) {
	n := e.PollPackagesPerPoll
	prefix := cursor + store.CursorGroupSep
	last, err := e.Store.GetCursor(ctx, resumeCursor(cursor))
	if err != nil {
		return nil, err
	}
	after := ""
	if last != "" {
		after = prefix + last
	}
	names := func(after string, limit int) ([]string, error) {
		page, err := pl.ListCursorsPage(ctx, prefix, after, limit)
		if err != nil {
			return nil, err
		}
		out := make([]string, len(page))
		for i, c := range page {
			out[i] = strings.TrimPrefix(c.Name, prefix)
		}
		return out, nil
	}
	tail, err := names(after, n+1)
	if err != nil {
		return nil, err
	}
	if len(tail) > n {
		return e.storeSlice(ctx, cursor, tail[:n])
	}
	head := []string{}
	if last != "" {
		all, err := names("", n+1)
		if err != nil {
			return nil, err
		}
		for _, p := range all {
			if p <= last {
				head = append(head, p)
			}
		}
	}
	if len(head)+len(tail) <= n {
		return append(head, tail...), nil
	}
	return e.storeSlice(ctx, cursor, append(tail, head[:n-len(tail)]...))
}

// storeSlice records where a slice ends before it is polled, as nextSlice does.
func (e *Engine) storeSlice(ctx context.Context, cursor string, out []string) ([]string, error) {
	if err := e.Store.SetCursor(ctx, resumeCursor(cursor), out[len(out)-1]); err != nil {
		return nil, err
	}
	return out, nil
}

// foldLegacyPackages writes one key per package of the legacy list and only then deletes it,
// so a crash between the two leaves both and the next read repeats the fold. It holds the
// package lock and re-reads the list, so it cannot resurrect a package a drop just removed.
func (e *Engine) foldLegacyPackages(ctx context.Context, cl store.CursorLister, cursor string) error {
	actionsPackagesMu.Lock()
	defer actionsPackagesMu.Unlock()
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		return err
	}
	for _, p := range strings.Fields(raw) {
		k := packageKey(cursor, p)
		if v, err := e.Store.GetCursor(ctx, k); err != nil {
			return err
		} else if v != "" {
			continue
		}
		if err := e.Store.SetCursor(ctx, k, "1"); err != nil {
			return err
		}
	}
	return cl.DeleteCursor(ctx, cursor)
}

// dropPackage removes pkg from the polled set of cursor when no repo uses it. It re-checks the
// dependents under the lock, so a repo that started using pkg since the poll began keeps it; a
// lost update still only costs a re-add at the next scan.
func (e *Engine) dropPackage(ctx context.Context, ds, cursor, pkg string) error {
	actionsPackagesMu.Lock()
	defer actionsPackagesMu.Unlock()
	deps, err := e.Store.ListDependents(ctx, ds, pkg)
	if err != nil || len(deps) > 0 {
		return err
	}
	if cl, ok := e.Store.(store.CursorLister); ok {
		if err := cl.DeleteCursor(ctx, packageKey(cursor, pkg)); err != nil {
			return err
		}
	}
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		return err
	}
	var keep []string
	for _, p := range strings.Fields(raw) {
		if p != pkg {
			keep = append(keep, p)
		}
	}
	if len(keep) == len(strings.Fields(raw)) {
		return nil
	}
	return e.Store.SetCursor(ctx, cursor, strings.Join(keep, "\n"))
}
