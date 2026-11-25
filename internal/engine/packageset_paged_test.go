package engine

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// listCounts counts the two cursor listings, so a test can tell a bounded read from a whole one.
type listCounts struct {
	store.Store
	cl            store.CursorLister
	pl            store.PagedCursorLister
	whole, paged  int
	pagedLimitMax int
}

func (c *listCounts) ListCursors(ctx context.Context, p string) ([]store.Cursor, error) {
	c.whole++
	return c.cl.ListCursors(ctx, p)
}
func (c *listCounts) DeleteCursor(ctx context.Context, n string) error {
	return c.cl.DeleteCursor(ctx, n)
}

// pagedCounts is listCounts that also pages.
type pagedCounts struct{ *listCounts }

func (c pagedCounts) ListCursorsPage(ctx context.Context, p, after string, limit int) ([]store.Cursor, error) {
	c.paged++
	if limit > c.pagedLimitMax {
		c.pagedLimitMax = limit
	}
	return c.pl.ListCursorsPage(ctx, p, after, limit)
}

func newCounts(t *testing.T, pkgs int) (*listCounts, *listCounts) {
	t.Helper()
	mk := func() *listCounts {
		m := store.NewMemory()
		for i := 0; i < pkgs; i++ {
			if err := m.SetCursor(context.Background(), packageKey(npmPackagesCursor, fmt.Sprintf("p%02d", i)), "1"); err != nil {
				t.Fatal(err)
			}
		}
		return &listCounts{Store: m, cl: m, pl: m}
	}
	return mk(), mk()
}

// Reading a slice by page visits what slicing the whole sorted set visits, in the same
// rotation, for every set size around the slice size, including a set no larger than a slice.
func TestPolledPackagesPagedSliceMatchesWholeSlice(t *testing.T) {
	ctx := context.Background()
	for pkgs := 0; pkgs <= 7; pkgs++ {
		for n := 1; n <= 8; n++ {
			whole, paged := newCounts(t, pkgs)
			ew := &Engine{Store: whole, PollPackagesPerPoll: n}
			ep := &Engine{Store: pagedCounts{paged}, PollPackagesPerPoll: n}
			for poll := 0; poll < 9; poll++ {
				want, err := ew.polledPackages(ctx, npmPackagesCursor)
				if err != nil {
					t.Fatal(err)
				}
				got, err := ep.polledPackages(ctx, npmPackagesCursor)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%d packages, %d per poll, poll %d: paged %v, whole %v", pkgs, n, poll, got, want)
				}
				rw, _ := whole.GetCursor(ctx, resumeCursor(npmPackagesCursor))
				rp, _ := paged.GetCursor(ctx, resumeCursor(npmPackagesCursor))
				if rw != rp {
					t.Fatalf("%d packages, %d per poll, poll %d: resume %q, whole %q", pkgs, n, poll, rp, rw)
				}
			}
			if paged.whole != 0 {
				t.Fatalf("%d packages, %d per poll: paged read listed the whole set %d times", pkgs, n, paged.whole)
			}
			if paged.pagedLimitMax > n+1 {
				t.Fatalf("a page of %d for %d per poll", paged.pagedLimitMax, n)
			}
		}
	}
}

// Unbounded polls, a legacy newline cursor, and a store that does not page keep the whole read.
func TestPolledPackagesKeepsWholeReadWhenPagingDoesNotApply(t *testing.T) {
	ctx := context.Background()
	t.Run("unbounded", func(t *testing.T) {
		_, paged := newCounts(t, 4)
		e := &Engine{Store: pagedCounts{paged}}
		if got, _ := e.polledPackages(ctx, npmPackagesCursor); len(got) != 4 || paged.whole != 1 || paged.paged != 0 {
			t.Errorf("got %v, whole %d, paged %d", got, paged.whole, paged.paged)
		}
	})
	t.Run("legacy cursor without lister", func(t *testing.T) {
		m := store.NewMemory()
		_ = m.SetCursor(ctx, npmPackagesCursor, "a\nb\nc")
		e := &Engine{Store: onlyStore{m}, PollPackagesPerPoll: 2}
		first, _ := e.polledPackages(ctx, npmPackagesCursor)
		second, _ := e.polledPackages(ctx, npmPackagesCursor)
		if !reflect.DeepEqual(first, []string{"a", "b"}) || !reflect.DeepEqual(second, []string{"c", "a"}) {
			t.Errorf("slices %v then %v", first, second)
		}
	})
	t.Run("legacy cursor folds then pages", func(t *testing.T) {
		_, paged := newCounts(t, 0)
		_ = paged.SetCursor(ctx, npmPackagesCursor, "a\nb\nc")
		e := &Engine{Store: pagedCounts{paged}, PollPackagesPerPoll: 2}
		first, err := e.polledPackages(ctx, npmPackagesCursor)
		second, _ := e.polledPackages(ctx, npmPackagesCursor)
		if err != nil || !reflect.DeepEqual(first, []string{"a", "b"}) || !reflect.DeepEqual(second, []string{"c", "a"}) {
			t.Errorf("slices %v then %v (%v)", first, second, err)
		}
		if paged.whole != 0 {
			t.Errorf("listed the whole set %d times after the fold", paged.whole)
		}
	})
}
