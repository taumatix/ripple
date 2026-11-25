package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// Entry 220: with RIPPLE_POLL_PACKAGES_PER_POLL set and a store that pages cursors, a poll
// reads only the packages of its slice (store.PagedCursorLister) instead of listing the
// whole set, and visits the same packages in the same rotation as the whole read does.

// cursorCalls counts the listings of the npm package set. The embedded Store hides the
// optional interfaces, so wholeOnly is a store that cannot page, as a third-party one.
type cursorCalls struct {
	store.Store
	cl           store.CursorLister
	whole, paged int
}

func (c *cursorCalls) ListCursors(ctx context.Context, p string) ([]store.Cursor, error) {
	if strings.HasPrefix(p, "npm:packages#") {
		c.whole++
	}
	return c.cl.ListCursors(ctx, p)
}
func (c *cursorCalls) DeleteCursor(ctx context.Context, n string) error {
	return c.cl.DeleteCursor(ctx, n)
}

type pagingCalls struct {
	*cursorCalls
	pl store.PagedCursorLister
}

func (c pagingCalls) ListCursorsPage(ctx context.Context, p, after string, limit int) ([]store.Cursor, error) {
	if strings.HasPrefix(p, "npm:packages#") {
		c.paged++
		if limit > 3 {
			panic("page larger than a slice plus one")
		}
	}
	return c.pl.ListCursorsPage(ctx, p, after, limit)
}

func TestPackagePollReadsOnlyItsSliceThroughThePagedListing(t *testing.T) {
	forEachFake(t, func(t *testing.T, st store.Store) {
		cl, ok1 := st.(store.CursorLister)
		pl, ok2 := st.(store.PagedCursorLister)
		if !ok1 || !ok2 {
			t.Fatalf("%T does not page cursors", st)
		}
		names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
		reg := testutil.NewNpmRegistry(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{npm: reg, engine: func(g *engine.Engine) { g.PollPackagesPerPoll = 2 }})
		json := `{"dependencies": {"alpha": "^1.0.0", "bravo": "^1.0.0", "charlie": "^1.0.0", "delta": "^1.0.0", "echo": "^1.0.0"}}` + "\n"
		for _, p := range names {
			reg.Publish(p, "1.0.0")
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": json})
		start(t, e, "acme/app") // poll 1: alpha, bravo
		e.clock.Advance(time.Minute)
		for _, p := range names {
			reg.Publish(p, "1.1.0")
		}
		calls := &cursorCalls{Store: st, cl: cl}
		paging := pagingCalls{calls, pl}
		seen := len(reg.Requests())
		// Polls alternate between the paged and the whole read on one store: the rotation
		// state is shared, so each form must resume exactly where the other stopped.
		var order []string
		for i, s := range []store.Store{paging, calls, paging, paging, calls, paging} {
			e.app.Engine.Store = s
			calls.whole, calls.paged = 0, 0
			e.tick()
			_, isPaging := s.(pagingCalls)
			if isPaging && (calls.whole != 0 || calls.paged == 0) {
				t.Fatalf("poll %d: whole listings %d, pages %d; want a paged read only", i, calls.whole, calls.paged)
			}
			if !isPaging && (calls.whole == 0 || calls.paged != 0) {
				t.Fatalf("poll %d: whole listings %d, pages %d; want the whole read", i, calls.whole, calls.paged)
			}
			for _, r := range reg.Requests()[seen:] {
				order = append(order, strings.TrimPrefix(r.Path, "/"))
			}
			seen = len(reg.Requests())
		}
		want := "charlie delta echo alpha bravo charlie delta echo alpha bravo charlie delta"
		if strings.Join(order, " ") != want {
			t.Fatalf("visited %v, want %s", order, want)
		}
	})
}
