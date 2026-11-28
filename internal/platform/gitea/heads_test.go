package gitea_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

var _ app.HeadLister = (*gitea.Client)(nil)

func newHeadsEnv(t *testing.T, forgejo bool, repos ...string) *testutil.Gitea {
	t.Helper()
	g := testutil.NewGitea(t, forgejo)
	for _, r := range repos {
		g.AddRepo(r, map[string]string{"go.mod": "module x\n"})
	}
	g.Show(repos...)
	return g
}

func TestListRepoHeadsReadsEachDefaultBranchCommit(t *testing.T) {
	eachFlavour(t, func(t *testing.T, forgejo bool) {
		g := newHeadsEnv(t, forgejo, "acme/one", "acme/two")
		heads, err := client(g).ListRepoHeads(context.Background())
		if err != nil || len(heads) != 2 || heads["acme/one"] != g.BranchHead("acme/one", "main") || heads["acme/two"] != g.BranchHead("acme/two", "main") || heads["acme/one"] == "" {
			t.Fatalf("heads %v %v", heads, err)
		}
		reads := 0
		for _, r := range g.Requests() {
			if strings.HasPrefix(r, "GET ") && strings.HasSuffix(r, "/branches/main") {
				reads++
			}
		}
		if reads != 2 {
			t.Errorf("%d branch reads, want one per repository", reads)
		}
	})
}

func TestListRepoHeadsSkipsAFailingRepoAndStopsOnARateLimit(t *testing.T) {
	g := newHeadsEnv(t, false, "acme/a", "acme/b")
	g.FailRead = "acme/a/branches/main"
	heads, err := client(g).ListRepoHeads(context.Background())
	if err != nil || len(heads) != 1 || heads["acme/b"] == "" {
		t.Fatalf("one failing repo: %v %v, want only acme/b", heads, err)
	}
	g.FailRead = ""
	g.LimitBranches = true
	var rl *platform.RateLimitError
	if _, err := client(g).ListRepoHeads(context.Background()); !errors.As(err, &rl) {
		t.Fatalf("a rate limit must end the listing with the rate-limit error, got %v", err)
	}
}

// The real client against the fake server, driven by App.Discover: a head that moved with no
// webhook queues one whole-repo push, once.
func TestDiscoverRescansAGiteaRepoWhoseHeadMovedOnce(t *testing.T) {
	ctx := context.Background()
	g := newHeadsEnv(t, false, "acme/moves", "acme/still")
	st := store.NewMemory()
	a := &app.App{Hosts: []*platform.Host{{Name: "gitea", Platform: client(g)}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
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
	g.Commit("acme/moves", map[string]string{"a": "2"})
	a.Discover(ctx)
	a.Discover(ctx)
	got := pushes()
	if len(got) != 1 || got[0].Repo != "gitea:acme/moves" || got[0].Ref != "refs/heads/main" {
		t.Fatalf("a moved head queued %+v, want one whole-repo push for gitea:acme/moves", got)
	}
}
