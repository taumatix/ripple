package scmmanager_test

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
	"github.com/taumatix/ripple/internal/store"
)

var _ app.HeadLister = (*scmmanager.Client)(nil)

func TestListRepoHeadsReportsEachDefaultBranchCommit(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/one", map[string]string{"a": "1"})
	f.AddRepo("acme/two", map[string]string{"a": "1"})
	f.AddOther("acme/legacy", true, false)
	heads, err := c.ListRepoHeads(context.Background())
	if err != nil || len(heads) != 2 || heads["acme/one"] != f.Rev("acme/one", "main") || heads["acme/two"] != f.Rev("acme/two", "main") {
		t.Fatalf("heads %v %v", heads, err)
	}
	reqs := 0
	for _, r := range f.Requests() {
		if r.Method == "GET" && len(r.Path) > 0 && r.Path[len(r.Path)-5:] == "/main" {
			reqs++
		}
	}
	if reqs != 2 {
		t.Errorf("%d branch reads, want one per repository", reqs)
	}
}

func TestListRepoHeadsSkipsAFailingRepoAndStopsOnARateLimit(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/a", map[string]string{"a": "1"})
	f.AddRepo("acme/b", map[string]string{"a": "1"})
	f.failBranch = "acme/a"
	heads, err := c.ListRepoHeads(context.Background())
	if err != nil || len(heads) != 1 || heads["acme/b"] == "" {
		t.Fatalf("one failing repo: %v %v, want only acme/b", heads, err)
	}
	f.failBranch = ""
	f.limitReads = true
	if _, err := c.ListRepoHeads(context.Background()); err == nil {
		t.Fatal("a rate limit must end the listing with an error")
	}
}

// The real client against the fake server, driven by App.Discover.
func TestDiscoverRescansAnSCMManagerRepoWhoseHeadMovedOnce(t *testing.T) {
	ctx := context.Background()
	c, f := newClient(t)
	f.AddRepo("acme/moves", map[string]string{"a": "1"})
	f.AddRepo("acme/still", map[string]string{"a": "1"})
	st := store.NewMemory()
	a := &app.App{Hosts: []*platform.Host{{Name: "scm", Platform: c}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
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
	a.Discover(ctx)
	a.Discover(ctx)
	if got := pushes(); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}
	f.Commit("acme/moves", map[string]string{"a": "2"})
	a.Discover(ctx)
	a.Discover(ctx)
	got := pushes()
	if len(got) != 1 || got[0].Repo != "scm:acme/moves" || got[0].Ref != "refs/heads/main" {
		t.Fatalf("a moved head queued %+v, want one whole-repo push for scm:acme/moves", got)
	}
}
