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
// (rest-api-projects.md). The first listing after a start already reports the head, because the
// branch it just read is the one it asks for; ListRepos names it from then on.
func TestListRepoHeadsReportsHeadsOnTheFirstListing(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/app", map[string]string{"x": "1"})
	c := newClient(f)

	repos, err := c.ListRepos(ctx)
	if err != nil || len(repos) != 1 || repos[0].DefaultBranch != "" {
		t.Fatalf("ListRepos before any head listing: %v %v", repos, err)
	}
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 1 || heads["acme/app"] != f.rev("acme/app", "refs/heads/main") {
		t.Fatalf("first ListRepoHeads %v %v, want main's commit", heads, err)
	}
	repos, _ = c.ListRepos(ctx)
	if len(repos) != 1 || repos[0].DefaultBranch != "main" {
		t.Fatalf("ListRepos after learning: %v", repos)
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

// A head that moved while ripple was down is caught by the first discovery of the new process,
// not the one after it.
func TestDiscoverCatchesAHeadThatMovedWhileRippleWasDown(t *testing.T) {
	f := newFakeGerrit(t)
	f.AddRepo("acme/moves", map[string]string{"x": "1"})
	f.AddRepo("acme/still", map[string]string{"x": "1"})
	st := store.NewMemory()
	newApp := func(c *gerrit.Client) *app.App {
		return &app.App{Hosts: []*platform.Host{{Name: "gerrit", Platform: c}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
	}
	newApp(newClient(f)).Discover(ctx)
	newApp(newClient(f)).Discover(ctx) // heads remembered
	f.Commit("acme/moves", map[string]string{"x": "while down"})

	newApp(newClient(f)).Discover(ctx) // a new process: nothing learned
	evs, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pushes []model.Event
	for _, e := range evs {
		if e.Kind == "push" {
			pushes = append(pushes, e)
		}
	}
	if len(pushes) != 1 || pushes[0].Repo != "gerrit:acme/moves" || pushes[0].Ref != "refs/heads/main" {
		t.Fatalf("first discovery after a restart queued %+v, want one push for gerrit:acme/moves", pushes)
	}
}
