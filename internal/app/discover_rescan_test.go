package app

import (
	"context"
	"sync"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
)

// headHost is a listed host with no push events that also reports each default branch's head.
type headHost struct {
	listingHost
	mu2   sync.Mutex
	heads map[string]string
	err   error
}

func (h *headHost) setHead(repo, sha string) {
	h.mu2.Lock()
	defer h.mu2.Unlock()
	if h.heads == nil {
		h.heads = map[string]string{}
	}
	h.heads[repo] = sha
}

func (h *headHost) ListRepos(ctx context.Context) ([]model.Repo, error) {
	rs, err := h.listingHost.ListRepos(ctx)
	for i := range rs {
		rs[i].DefaultBranch = "main"
	}
	return rs, err
}

func (h *headHost) ListRepoHeads(context.Context) (map[string]string, error) {
	h.mu2.Lock()
	defer h.mu2.Unlock()
	if h.err != nil {
		return nil, h.err
	}
	out := map[string]string{}
	for k, v := range h.heads {
		out[k] = v
	}
	return out, nil
}

func TestDiscoverRescansARepoWhoseDefaultBranchHeadMoved(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	h := &headHost{}
	a := &App{Hosts: []*platform.Host{{Name: "gerrit", Platform: h}}, Queue: &Queue{Store: st, Process: newGate().process}}
	h.set("a", "b")
	h.setHead("a", "a1")
	h.setHead("b", "b1")

	a.Discover(ctx)
	if got := queued(t, st); len(got) != 2 || got[0] != "repos_added gerrit:a" {
		t.Fatalf("first listing queued %v: new repos are scanned by their add, not by a head change", got)
	}
	drain(t, st)

	a.Discover(ctx) // heads unchanged
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}

	h.setHead("a", "a2")
	a.Discover(ctx)
	evs, _ := st.ListEvents(ctx)
	if len(evs) != 1 || evs[0].Kind != "push" || evs[0].Repo != "gerrit:a" || evs[0].Ref != "refs/heads/main" || len(evs[0].Files) != 0 {
		t.Fatalf("a moved head queued %+v, want one whole-repo push on refs/heads/main for gerrit:a", evs)
	}
	drain(t, st)

	a.Discover(ctx) // the new head is remembered: no repeat
	if got := queued(t, st); len(got) != 0 {
		t.Fatalf("an already-seen head queued %v", got)
	}

	h.mu2.Lock()
	h.err = context.DeadlineExceeded
	h.mu2.Unlock()
	h.setHead("b", "b2")
	a.Discover(ctx) // a failing head listing queues nothing and forgets nothing
	h.mu2.Lock()
	h.err = nil
	h.mu2.Unlock()
	a.Discover(ctx)
	if got := queued(t, st); len(got) != 1 || got[0] != "push gerrit:b" {
		t.Fatalf("after the head listing recovered queued %v, want a push for b", got)
	}

	h.set("b") // a leaves the listing: its remembered head goes with it
	a.Discover(ctx)
	if got, _ := st.ListCursors(ctx, "discover-head/gerrit#"); len(got) != 1 || got[0].Name != "discover-head/gerrit#b" {
		t.Fatalf("remembered heads = %v, want only b", got)
	}
}
