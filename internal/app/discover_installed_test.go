package app

import (
	"context"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
)

// installedHost is a host with no ListRepos (a GitHub App) that reports heads per installation.
type installedHost struct {
	platform.Platform
	mu    sync.Mutex
	heads []platform.RepoHead
	err   error
	calls int
}

func (h *installedHost) ListInstalledRepoHeads(context.Context) ([]platform.RepoHead, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.err != nil {
		return nil, h.err
	}
	return append([]platform.RepoHead(nil), h.heads...), nil
}

func (h *installedHost) set(hs ...platform.RepoHead) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heads = hs
}

func TestDiscoverRescansAnInstalledRepoWhoseHeadMoved(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	h := &installedHost{}
	a := &App{Hosts: []*platform.Host{{Name: "github", Platform: h}}, Queue: &Queue{Store: st, Process: newGate().process}}
	h.set(
		platform.RepoHead{FullName: "o/a", InstallationID: 7, DefaultBranch: "main", Head: "a1"},
		platform.RepoHead{FullName: "o/b", InstallationID: 7, DefaultBranch: "main", Head: "b1"},
		platform.RepoHead{FullName: "p/c", InstallationID: 9, DefaultBranch: "trunk", Head: "c1"},
		platform.RepoHead{FullName: "p/empty", InstallationID: 9, DefaultBranch: "main"},
	)

	a.Discover(ctx)
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("first round queued %v: a repo seen first is only remembered", got)
	}

	// Move a and b (installation 7) and c (installation 9, another branch name).
	h.set(
		platform.RepoHead{FullName: "o/a", InstallationID: 7, DefaultBranch: "main", Head: "a2"},
		platform.RepoHead{FullName: "o/b", InstallationID: 7, DefaultBranch: "main", Head: "b2"},
		platform.RepoHead{FullName: "p/c", InstallationID: 9, DefaultBranch: "trunk", Head: "c2"},
		platform.RepoHead{FullName: "p/empty", InstallationID: 9, DefaultBranch: "main", Head: "e1"},
	)
	a.Discover(ctx)
	evs, _ := st.ListEvents(ctx)
	type row struct {
		repo, ref, def string
		inst           int64
	}
	var got []row
	for _, e := range evs {
		if e.Kind != "push" || len(e.Files) != 0 || e.RepoID != 0 {
			t.Fatalf("event %+v: want a whole-repo push with no repo id", e)
		}
		got = append(got, row{e.Repo, e.Ref, e.DefaultBranch, e.InstallationID})
	}
	want := []row{{"o/a", "refs/heads/main", "main", 7}, {"o/b", "refs/heads/main", "main", 7}, {"p/c", "refs/heads/trunk", "trunk", 9}}
	if len(got) != len(want) {
		t.Fatalf("queued %+v, want %+v (the repo that was empty is only remembered)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	drain(t, st)

	a.Discover(ctx)
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}

	// A failing round queues nothing and forgets nothing; the move is caught when it recovers.
	h.mu.Lock()
	h.err = context.DeadlineExceeded
	h.mu.Unlock()
	h.set(platform.RepoHead{FullName: "o/a", InstallationID: 7, DefaultBranch: "main", Head: "a3"})
	a.Discover(ctx)
	if cs, _ := st.ListCursors(ctx, "discover-head/github#"); len(cs) != 4 {
		t.Fatalf("a failed round left %d remembered heads, want 4", len(cs))
	}
	h.mu.Lock()
	h.err = nil
	h.mu.Unlock()
	a.Discover(ctx)
	if got := queued(t, st); len(got) != 1 || got[0] != "push o/a" {
		t.Fatalf("after recovery queued %v, want one push for o/a", got)
	}
	// The other three left the listing: nothing is kept for them.
	if cs, _ := st.ListCursors(ctx, "discover-head/github#"); len(cs) != 1 {
		t.Fatalf("remembered heads = %v, want only o/a", cs)
	}
}
