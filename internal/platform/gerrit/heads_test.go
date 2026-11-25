package gerrit_test

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/store"
)

var _ app.HeadLister = (*gerrit.Client)(nil)

// ListRepoHeads: HEAD names the default branch, "Get Branch" gives its commit
// (rest-api-projects.md). A project's head is withheld until ListRepos can name its default
// branch, which the client learns here, so a moved head is never remembered without a branch
// to re-scan.
func TestListRepoHeadsReportsDefaultBranchCommitOnceTheBranchIsKnown(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"x": "1"})
	c := newClient(f)

	repos, err := c.ListRepos(ctx)
	if err != nil || len(repos) != 1 || repos[0].DefaultBranch != "" {
		t.Fatalf("ListRepos before any head listing: %v %v", repos, err)
	}
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 0 {
		t.Fatalf("first ListRepoHeads %v %v: want nothing yet (default branch unknown to ListRepos)", heads, err)
	}
	repos, _ = c.ListRepos(ctx)
	if len(repos) != 1 || repos[0].DefaultBranch != "main" {
		t.Fatalf("ListRepos after learning: %v", repos)
	}
	heads, err = c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 1 || heads["acme/app"] != f.rev("acme/app", "refs/heads/main") {
		t.Fatalf("ListRepoHeads %v %v, want main's commit", heads, err)
	}
}

func TestListRepoHeadsSkipsAProjectItCannotReadAndStopsOnARateLimit(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/a", map[string]string{"x": "1"})
	f.AddRepo("acme/b", map[string]string{"x": "1"})
	c := newClient(f)
	_, _ = c.ListRepoHeads(ctx) // learn the branches

	f.mu.Lock()
	f.failRead = "acme/a/branches"
	f.mu.Unlock()
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 1 || heads["acme/b"] == "" {
		t.Fatalf("one unreadable project: %v %v, want only acme/b", heads, err)
	}

	f.mu.Lock()
	f.failRead = ""
	f.mu.Unlock()
	f.limitReads = true
	if _, err := c.ListRepoHeads(ctx); err == nil {
		t.Fatal("a rate limit must end the listing with an error")
	}
}

// The real client against the fake server, driven by App.Discover: a project whose default
// branch moves is re-scanned once, an unchanged one is not.
func TestDiscoverRescansAGerritProjectWhoseHeadMovedOnce(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/moves", map[string]string{"x": "1"})
	f.AddRepo("acme/still", map[string]string{"x": "1"})
	st := store.NewMemory()
	a := &app.App{Hosts: []*platform.Host{{Name: "gerrit", Platform: newClient(f)}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
	pushes := func() []model.Event {
		evs, err := st.ListEvents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []model.Event
		for _, e := range evs {
			if e.Kind == "push" {
				out = append(out, e)
			}
		}
		return out
	}

	a.Discover(ctx) // adds both; learns the default branches
	a.Discover(ctx) // remembers the heads
	a.Discover(ctx)
	if got := pushes(); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}
	f.Commit("acme/moves", map[string]string{"x": "2"})
	a.Discover(ctx)
	a.Discover(ctx)
	got := pushes()
	if len(got) != 1 || got[0].Repo != "gerrit:acme/moves" || got[0].Ref != "refs/heads/main" {
		t.Fatalf("a moved head queued %+v, want one whole-repo push for gerrit:acme/moves", got)
	}
}
