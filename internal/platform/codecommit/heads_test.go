package codecommit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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

// BatchGetRepositories names the default branches (up to 100 repositories a call) and GetBranch
// the commit (docs/vendor/codecommit/api-API_BatchGetRepositories.md, api-API_GetBranch.md). The
// first head listing after a start already reports every head, and the branch it learned is
// what ListRepos names afterwards.
func TestListRepoHeadsReportsHeadsOnTheFirstListingWithBatchedDefaultBranches(t *testing.T) {
	c, cc := newClient(t)
	// Two with a branch, 99 empty: 101 repositories are two batches, cheaply.
	first, last := addMember(cc, "r000"), addMember(cc, "r100")
	for i := 1; i < 100; i++ {
		p := fmt.Sprintf("%s/r%03d", region, i)
		cc.AddEmptyRepo(p)
		cc.Member(p)
	}
	ctx := context.Background()

	repos, err := c.ListRepos(ctx)
	if err != nil || len(repos) != 101 || repos[0].DefaultBranch != "" {
		t.Fatalf("ListRepos before any head listing: %d %v", len(repos), err)
	}
	seen := len(cc.Requests())
	heads, err := c.ListRepoHeads(ctx)
	if err != nil || len(heads) != 2 || heads[first] != cc.Head(first) || heads[last] != cc.Head(last) {
		t.Fatalf("first ListRepoHeads: %d heads, %v", len(heads), err)
	}
	ops := map[string]int{}
	for _, op := range cc.Requests()[seen:] {
		ops[op]++
	}
	if ops["BatchGetRepositories"] != 2 || ops["GetRepository"] != 0 || ops["GetBranch"] != 2 {
		t.Errorf("calls %v, want 2 BatchGetRepositories (100 + 1), no GetRepository, 2 GetBranch", ops)
	}
	repos, _ = c.ListRepos(ctx)
	if repos[0].DefaultBranch != "main" {
		t.Fatalf("ListRepos after learning: %v", repos[0])
	}
	// Only the 99 still without a branch are asked again, in one batch.
	seen = len(cc.Requests())
	if _, err := c.ListRepoHeads(ctx); err != nil {
		t.Fatal(err)
	}
	ops = map[string]int{}
	for _, op := range cc.Requests()[seen:] {
		ops[op]++
	}
	if ops["BatchGetRepositories"] != 1 || ops["GetRepository"] != 0 {
		t.Errorf("second listing calls %v, want one batch for the 99 empty repositories", ops)
	}
}

// An empty repository has no default branch yet and is left out; one that is not found does not
// fail the others.
func TestListRepoHeadsLeavesOutARepositoryWithoutADefaultBranch(t *testing.T) {
	c, cc := newClient(t)
	a := addMember(cc, "a")
	cc.AddEmptyRepo(region + "/empty")
	cc.Member(region + "/empty")
	heads, err := c.ListRepoHeads(context.Background())
	if err != nil || len(heads) != 1 || heads[a] == "" {
		t.Fatalf("%v %v, want only a", heads, err)
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

// A head that moved while ripple was down is caught by the first discovery of the new process:
// the remembered heads are in the store, and the new client learns the default branches in
// that same round (one batched call) instead of the next one.
func TestDiscoverCatchesAHeadThatMovedWhileRippleWasDown(t *testing.T) {
	ctx := context.Background()
	c, cc := newClient(t)
	moves := addMember(cc, "moves")
	addMember(cc, "still")
	st := store.NewMemory()
	newApp := func(c *codecommit.Client) *app.App {
		return &app.App{Hosts: []*platform.Host{{Name: "cc", Platform: c}}, Queue: &app.Queue{Store: st, Process: func(context.Context, model.Event) error { return nil }}}
	}
	newApp(c).Discover(ctx)
	newApp(c).Discover(ctx) // nothing moved; heads remembered
	cc.Commit(moves, map[string]string{"README.md": "while down"})

	// A new process: same endpoint and credentials, nothing learned.
	restarted := &codecommit.Client{Regions: []string{region}, Creds: creds(cc), Endpoint: cc.APIURL, GitBase: cc.GitURL,
		Now: func() time.Time { return t0 }}
	newApp(restarted).Discover(ctx)
	var pushes []model.Event
	evs, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "push" {
			pushes = append(pushes, e)
		}
	}
	if len(pushes) != 1 || pushes[0].Repo != "cc:"+moves || pushes[0].Ref != "refs/heads/main" {
		t.Fatalf("first discovery after a restart queued %+v, want one push for cc:%s", pushes, moves)
	}
}
