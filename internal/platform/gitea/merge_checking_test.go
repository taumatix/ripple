package gitea_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/testutil"
)

func mergeEnv(t *testing.T) (*testutil.Gitea, model.Repo, platform.PR) {
	t.Helper()
	g := testutil.NewGitea(t, false)
	g.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	repo := model.Repo{FullName: "acme/app", DefaultBranch: "main"}
	pr, err := client(g).CreatePR(context.Background(), repo, platform.PRSpec{Title: "t", Head: "main", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return g, repo, pr
}

// Gitea 1.27.3 answers a merge request 405 {"message":"Please try again later"} while it is still
// checking whether the pull request can merge, which is the case right after the pull request was
// created, when ripple asks for the auto-merge. Giving up there left the pull request without it
// (the engine only logs "auto-merge not enabled"), so the request waits for the check.
func TestRequestAutoMergeWaitsWhileGiteaIsStillChecking(t *testing.T) {
	g, repo, pr := mergeEnv(t)
	g.MergeChecking = 3
	c := client(g)
	c.MergeSettle = 5 * time.Second
	if err := c.RequestAutoMerge(context.Background(), repo, pr); err != nil {
		t.Fatalf("auto-merge: %v", err)
	}
	if len(g.Merges) != 4 {
		t.Errorf("merge requests: %d, want 3 refused and 1 accepted", len(g.Merges))
	}
	if got := g.PRs("acme/app"); len(got) != 1 || !got[0].AutoMerge {
		t.Errorf("auto-merge not scheduled: %+v", got)
	}
}

// A pull request that never finishes checking costs one bounded wait and then an error that says why.
func TestRequestAutoMergeGivesUpWhenCheckingNeverEnds(t *testing.T) {
	g, repo, pr := mergeEnv(t)
	g.MergeChecking = 1 << 30
	c := client(g)
	c.MergeSettle = 150 * time.Millisecond
	err := c.RequestAutoMerge(context.Background(), repo, pr)
	if err == nil || !strings.Contains(err.Error(), "405") {
		t.Fatalf("want the 405 error after the wait, got %v", err)
	}
}

// A negative MergeSettle asks once.
func TestRequestAutoMergeNegativeSettleAsksOnce(t *testing.T) {
	g, repo, pr := mergeEnv(t)
	g.MergeChecking = 1 << 30
	c := client(g)
	c.MergeSettle = -1
	if err := c.RequestAutoMerge(context.Background(), repo, pr); err == nil {
		t.Fatal("want the 405 error")
	}
	if len(g.Merges) != 1 {
		t.Errorf("merge requests: %d, want 1", len(g.Merges))
	}
}
