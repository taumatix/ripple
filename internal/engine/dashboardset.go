package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/store"
)

// The dashboard's two repo sets (dirtyCursor, watchCursor) live in one cursor name per repo,
// "<cursor>#<repo>", when the store lists cursors (store.CursorLister), so two replicas
// marking at once cannot overwrite each other's entry. A store that cannot list keeps the
// old newline-separated cursor. A list written by an earlier version is still read, and the
// first read folds it into per-repo keys and deletes it.

func repoSetKey(cursor, repo string) string { return cursor + store.CursorGroupSep + repo }

// repoSet reads the set named cursor, folding a legacy list first where the store lists.
func (e *Engine) repoSet(ctx context.Context, cursor string) map[string]bool {
	set := map[string]bool{}
	raw, err := e.Store.GetCursor(ctx, cursor)
	if err != nil {
		e.log().Warn("read dashboard cursor set", "cursor", cursor, "err", err)
		return set
	}
	cl, lists := e.Store.(store.CursorLister)
	if lists && strings.TrimSpace(raw) != "" {
		if err := e.foldRepoSet(ctx, cl, cursor, raw); err != nil {
			e.log().Warn("legacy dashboard set not folded", "cursor", cursor, "err", err)
		} else {
			raw = ""
		}
	}
	for _, r := range strings.Fields(raw) {
		set[r] = true
	}
	if lists {
		cs, err := cl.ListCursors(ctx, cursor+store.CursorGroupSep)
		if err != nil {
			e.log().Warn("list dashboard cursor set", "cursor", cursor, "err", err)
			return set
		}
		for _, c := range cs {
			set[strings.TrimPrefix(c.Name, cursor+store.CursorGroupSep)] = true
		}
	}
	return set
}

// foldRepoSet writes one key per repo and only then deletes the legacy cursor, so a crash
// between the two leaves both and the next read repeats the fold.
func (e *Engine) foldRepoSet(ctx context.Context, cl store.CursorLister, cursor, raw string) error {
	for _, r := range strings.Fields(raw) {
		if err := e.Store.SetCursor(ctx, repoSetKey(cursor, r), "1"); err != nil {
			return err
		}
	}
	return cl.DeleteCursor(ctx, cursor)
}

// addToRepoSet puts repo in the set: one write of its own key, or a read-modify-write of the
// legacy cursor on a store that cannot list.
func (e *Engine) addToRepoSet(ctx context.Context, cursor, repo string) error {
	if _, ok := e.Store.(store.CursorLister); ok {
		return e.Store.SetCursor(ctx, repoSetKey(cursor, repo), "1")
	}
	set := e.repoSet(ctx, cursor)
	set[repo] = true
	return e.saveLegacyRepoSet(ctx, cursor, set)
}

// removeFromRepoSet takes repo out of the set.
func (e *Engine) removeFromRepoSet(ctx context.Context, cursor, repo string) error {
	if cl, ok := e.Store.(store.CursorLister); ok {
		return cl.DeleteCursor(ctx, repoSetKey(cursor, repo))
	}
	set := e.repoSet(ctx, cursor)
	delete(set, repo)
	return e.saveLegacyRepoSet(ctx, cursor, set)
}

func (e *Engine) saveLegacyRepoSet(ctx context.Context, cursor string, set map[string]bool) error {
	repos := make([]string, 0, len(set))
	for r := range set {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	return e.Store.SetCursor(ctx, cursor, strings.Join(repos, "\n"))
}
