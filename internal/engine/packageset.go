package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/store"
)

// A polled-package set lives in one cursor name per package, "<cursor>#<package>", when the
// store lists cursors (store.CursorLister). Replicas then add packages without overwriting
// each other's additions, which the single newline-separated cursor allowed: two replicas
// scanning different repos could each write back a list missing the other's package. The
// legacy newline cursor is still read, so a set written by an earlier version keeps
// polling; it is never written again except to remove a dropped package from it.

func packageKey(cursor, pkg string) string { return cursor + store.CursorGroupSep + pkg }

// polledPackages is the union of the legacy cursor and the per-package cursors, sorted.
func (e *Engine) polledPackages(ctx context.Context, cursor string) ([]string, error) {
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		return nil, err
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
	return out, nil
}
