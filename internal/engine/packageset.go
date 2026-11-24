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
