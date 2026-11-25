package codecommit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

var _ app.HeadLister = (*codecommit.Client)(nil)

func addMember(cc *testutil.CodeCommit, name string) string {
	path := region + "/" + name
	cc.AddRepo(path, map[string]string{"README.md": "x"})
	cc.Member(path)
	return path
}

// GetRepository names the default branch and GetBranch its commit
// (docs/vendor/codecommit/api-API_GetRepository.md, api-API_GetBranch.md). A repository is
// reported only once ListRepos can name its default branch, which this call learns.
func TestListRepoHeadsReportsDefaultBranchCommitOnceTheBranchIsKnown(t *testing.T) {
	c, cc := newClient(t)
	a := addMember(cc, "a")
	ctx := context.Background()

	repos, err := c.ListRepos(ctx)
	if err != nil || len(repos) != 1 || repos[0].DefaultBranch != "" {
		t.Fatalf("ListRepos before any head listing: %v %v", repos, err)
	}
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 0 {
		t.Fatalf("first ListRepoHeads %v %v: want nothing yet", heads, err)
	}
	repos, _ = c.ListRepos(ctx)
	if len(repos) != 1 || repos[0].DefaultBranch != "main" {
		t.Fatalf("ListRepos after learning: %v", repos)
	}
	seen := len(cc.Requests())
	heads, err = c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 1 || heads[a] != cc.Head(a) {
		t.Fatalf("ListRepoHeads %v %v, want main's commit", heads, err)
	}
	for _, op := range cc.Requests()[seen:] {
		if op == "GetRepository" {
			t.Errorf("the default branch is remembered, but GetRepository was asked again")
		}
	}
}

func TestListRepoHeadsSkipsAFailingRepoAndStopsOnThrottling(t *testing.T) {
	c, cc := newClient(t)
	a, b := addMember(cc, "a"), addMember(cc, "b")
	ctx := context.Background()
	_, _ = c.ListRepoHeads(ctx) // learn the branches

	cc.FailNext("GetBranch", 1) // a, the first listed
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 1 || heads[b] == "" || heads[a] != "" {
		t.Fatalf("one failing repo: %v %v, want only b", heads, err)
	}
	var rl *platform.RateLimitError
	cc.ThrottleNext("GetBranch", 1)
	_, err = c.ListRepoHeads(ctx)
	if !errors.As(err, &rl) {
		t.Fatalf("error %v is not a RateLimitError", err)
	}
}

// The real client against the fake server, driven by App.Discover.
func TestDiscoverRescansACodeCommitRepoWhoseHeadMovedOnce(t *testing.T) {
	ctx := context.Background()
	c, cc := newClient(t)
	moves, _ := addMember(cc, "moves"), addMember(cc, "still")
	st := store.NewMemory()
	a := &app.App{Hosts: []*platform.Host{{Name: "cc", Platform: c}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
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
	a.Discover(ctx)
	if got := pushes(); len(got) != 0 {
		t.Fatalf("unchanged heads queued %v", got)
	}
	cc.Commit(moves, map[string]string{"README.md": "y"})
	a.Discover(ctx)
	a.Discover(ctx)
	got := pushes()
	if len(got) != 1 || got[0].Repo != "cc:"+moves || got[0].Ref != "refs/heads/main" {
		t.Fatalf("a moved head queued %+v, want one whole-repo push for cc:%s", got, moves)
	}
}
