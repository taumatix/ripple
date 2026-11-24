package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/store"
)

// racingStore runs hook once, just before the first write to a cursor whose name starts
// with prefix: the moment another replica gets its own write in between this one's read and
// write.
type racingStore struct {
	store.Store
	prefix string
	hook   func()
}

func (r *racingStore) SetCursor(ctx context.Context, name, value string) error {
	if r.hook != nil && strings.HasPrefix(name, r.prefix) {
		h := r.hook
		r.hook = nil
		h()
	}
	return r.Store.SetCursor(ctx, name, value)
}

func (r *racingStore) ListCursors(ctx context.Context, p string) ([]store.Cursor, error) {
	return r.Store.(store.CursorLister).ListCursors(ctx, p)
}

func (r *racingStore) DeleteCursor(ctx context.Context, n string) error {
	return r.Store.(store.CursorLister).DeleteCursor(ctx, n)
}

// Two replicas marking different repos at the same moment must both be remembered: the set
// used to be one cursor each read and wrote whole, so the later write dropped the other's repo.
func TestDashboardDirtySetKeepsAMarkRacingReplicaWrote(t *testing.T) {
	ctx := context.Background()
	shared := store.NewMemory()
	rs := &racingStore{Store: shared, prefix: dirtyCursor}
	a := &Engine{Store: rs}
	b := &Engine{Store: shared}
	rs.hook = func() { b.markDashboard("acme/other") }
	a.markDashboard("acme/mine")
	got := a.storedDirty(ctx)
	if !got["acme/mine"] || !got["acme/other"] {
		t.Fatalf("a mark was lost to the race: %v", got)
	}
}

// The watch list has the same race, between two dashboards being created.
func TestDashboardWatchSetKeepsARacingReplicasRepo(t *testing.T) {
	ctx := context.Background()
	shared := store.NewMemory()
	rs := &racingStore{Store: shared, prefix: watchCursor}
	a := &Engine{Store: rs}
	b := &Engine{Store: shared}
	rs.hook = func() {
		if err := b.setDashboardCursor(ctx, "acme/other", "9|x"); err != nil {
			t.Error(err)
		}
	}
	if err := a.setDashboardCursor(ctx, "acme/mine", "8|x"); err != nil {
		t.Fatal(err)
	}
	got := a.repoSet(ctx, watchCursor)
	if !got["acme/mine"] || !got["acme/other"] {
		t.Fatalf("a watched repo was lost to the race: %v", got)
	}
	if err := a.setDashboardCursor(ctx, "acme/mine", ""); err != nil {
		t.Fatal(err)
	}
	if got := a.repoSet(ctx, watchCursor); got["acme/mine"] || !got["acme/other"] {
		t.Fatalf("turning one dashboard off changed the other: %v", got)
	}
}

// A set an earlier version wrote as one cursor is still read, and folded into per-repo keys.
func TestDashboardSetsFoldTheLegacyCursor(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	if err := s.SetCursor(ctx, dirtyCursor, "acme/a\nacme/b"); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: s}
	got := e.storedDirty(ctx)
	if len(got) != 2 || !got["acme/a"] || !got["acme/b"] {
		t.Fatalf("legacy list not read: %v", got)
	}
	if v, _ := s.GetCursor(ctx, dirtyCursor); v != "" {
		t.Fatalf("legacy cursor not deleted: %q", v)
	}
	if v, _ := s.GetCursor(ctx, repoSetKey(dirtyCursor, "acme/a")); v == "" {
		t.Fatal("per-repo key not written")
	}
}

// A store that cannot list cursors keeps the single-cursor behaviour.
type plainStore struct{ store.Store }

func TestDashboardSetsOnAStoreThatCannotList(t *testing.T) {
	ctx := context.Background()
	e := &Engine{Store: plainStore{store.NewMemory()}}
	e.markDashboard("acme/a")
	e.markDashboard("acme/b")
	if got := e.storedDirty(ctx); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if err := e.removeFromRepoSet(ctx, dirtyCursor, "acme/a"); err != nil {
		t.Fatal(err)
	}
	if got := e.storedDirty(ctx); len(got) != 1 || !got["acme/b"] {
		t.Fatalf("got %v", got)
	}
}
