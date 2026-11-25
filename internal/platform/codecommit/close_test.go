package codecommit_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/codecommit"
	"github.com/taumatix/ripple/internal/testutil"
)

const (
	closePath    = region + "/app"
	closeHead    = "ripple/lib-v1"
	ripplesEmail = "ripple@users.noreply.invalid"
	closeNote    = "Superseded by lib v2."
)

var _ platform.PRCloser = (*codecommit.Client)(nil)

// openRipplePR has ripple push closeHead with one commit and open a pull request for it.
func openRipplePR(t *testing.T) (*codecommit.Client, *testutil.CodeCommit, platform.PR) {
	t.Helper()
	c, cc := newClient(t)
	cc.AddRepo(closePath, map[string]string{"README.md": "x"})
	cc.PushBranch(closePath, closeHead, ripplesEmail)
	pr, err := c.CreatePR(context.Background(), model.Repo{FullName: closePath},
		platform.PRSpec{Title: "bump lib", Head: closeHead, Base: "main", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return c, cc, pr
}

func closeIt(c *codecommit.Client, pr platform.PR) (bool, error) {
	return c.CloseUntouchedPR(context.Background(), model.Repo{FullName: closePath}, pr, closeNote)
}

func state(t *testing.T, cc *testutil.CodeCommit) testutil.CodeCommitPR {
	t.Helper()
	prs := cc.PRs(closePath)
	if len(prs) != 1 {
		t.Fatalf("%d PRs", len(prs))
	}
	return prs[0]
}

func TestCloseUntouchedClosesAndCommentsOnce(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	ok, err := closeIt(c, pr)
	if err != nil || !ok {
		t.Fatalf("got %v, %v, want true, nil", ok, err)
	}
	if p := state(t, cc); p.Status != "CLOSED" || !slices.Equal(p.Comments, []string{closeNote}) {
		t.Errorf("status %s comments %q", p.Status, p.Comments)
	}
	if !cc.HasBranch(closePath, closeHead) {
		t.Error("the branch was deleted")
	}
	reqs := cc.Requests()
	closeAt, commentAt := slices.Index(reqs, "UpdatePullRequestStatus"), slices.Index(reqs, "PostCommentForPullRequest")
	if closeAt < 0 || commentAt < closeAt {
		t.Errorf("close must come before comment: %v", reqs)
	}
}

// Each of these is something a person did; none may be closed over.
func TestCloseUntouchedLeavesATouchedPullRequestAlone(t *testing.T) {
	touches := map[string]func(*testing.T, *testutil.CodeCommit, platform.PR){
		"a person's commit on the branch": func(_ *testing.T, cc *testutil.CodeCommit, _ platform.PR) {
			cc.PushBranch(closePath, closeHead, "dev@example.com")
		},
		"a comment": func(_ *testing.T, cc *testutil.CodeCommit, pr platform.PR) {
			cc.HumanComment(closePath, pr.Number, "looks good")
		},
		"an approval": func(_ *testing.T, cc *testutil.CodeCommit, pr platform.PR) {
			cc.HumanEvent(closePath, pr.Number, "PULL_REQUEST_APPROVAL_STATE_CHANGED")
		},
		"an approval rule edit": func(_ *testing.T, cc *testutil.CodeCommit, pr platform.PR) {
			cc.HumanEvent(closePath, pr.Number, "PULL_REQUEST_APPROVAL_RULE_CREATED")
		},
		"a status change event": func(_ *testing.T, cc *testutil.CodeCommit, pr platform.PR) {
			cc.HumanEvent(closePath, pr.Number, "PULL_REQUEST_STATUS_CHANGED")
		},
		"an event of a type this code does not know": func(_ *testing.T, cc *testutil.CodeCommit, pr platform.PR) {
			cc.HumanEvent(closePath, pr.Number, "PULL_REQUEST_SOMETHING_NEW")
		},
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			c, cc, pr := openRipplePR(t)
			touch(t, cc, pr)
			ok, err := closeIt(c, pr)
			if err != nil || ok {
				t.Fatalf("got %v, %v, want false, nil", ok, err)
			}
			if p := state(t, cc); p.Status != "OPEN" || len(p.Comments) > 1 {
				t.Errorf("changed: status %s comments %q", p.Status, p.Comments)
			}
			if slices.Contains(cc.Requests(), "UpdatePullRequestStatus") || slices.Contains(cc.Requests(), "PostCommentForPullRequest") {
				t.Errorf("wrote: %v", cc.Requests())
			}
		})
	}
}

func TestCloseUntouchedTreatsAPersonAsCommitterAsATouch(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	cc.PushBranchAs(closePath, closeHead, ripplesEmail, "dev@example.com")
	if ok, err := closeIt(c, pr); err != nil || ok {
		t.Fatalf("got %v, %v, want false, nil", ok, err)
	}
}

func TestCloseUntouchedFailedReadClosesNothing(t *testing.T) {
	for _, op := range []string{"GetPullRequest", "DescribePullRequestEvents", "GetCommentsForPullRequest", "GetCommit"} {
		t.Run(op, func(t *testing.T) {
			c, cc, pr := openRipplePR(t)
			cc.FailNext(op, 1)
			ok, err := closeIt(c, pr)
			if err == nil || ok {
				t.Fatalf("got %v, %v, want an error", ok, err)
			}
			if p := state(t, cc); p.Status != "OPEN" || len(p.Comments) != 0 {
				t.Errorf("changed: %+v", p)
			}
			if slices.Contains(cc.Requests(), "UpdatePullRequestStatus") {
				t.Errorf("closed after a failed read: %v", cc.Requests())
			}
		})
	}
}

func TestCloseUntouchedReadsEveryPageOfEvents(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	// The fake pages at two: created + three source updates, then the touch on page 3.
	for range 3 {
		cc.PushBranch(closePath, closeHead, ripplesEmail)
	}
	cc.HumanEvent(closePath, pr.Number, "PULL_REQUEST_APPROVAL_STATE_CHANGED")
	if ok, err := closeIt(c, pr); err != nil || ok {
		t.Fatalf("got %v, %v, want false, nil", ok, err)
	}
}

func TestCloseUntouchedAllowsRipplesOwnUpdates(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	cc.PushBranch(closePath, closeHead, ripplesEmail)
	if ok, err := closeIt(c, pr); err != nil || !ok {
		t.Fatalf("got %v, %v, want true, nil", ok, err)
	}
}

func TestCloseUntouchedNotOpenOrOtherHeadIsFalseNil(t *testing.T) {
	t.Run("already closed", func(t *testing.T) {
		c, cc, pr := openRipplePR(t)
		cc.HumanClose(closePath, pr.Number)
		if ok, err := closeIt(c, pr); err != nil || ok {
			t.Fatalf("got %v, %v, want false, nil", ok, err)
		}
		if p := state(t, cc); len(p.Comments) != 0 {
			t.Errorf("commented on a closed PR: %q", p.Comments)
		}
	})
	t.Run("head differs", func(t *testing.T) {
		c, cc, pr := openRipplePR(t)
		pr.Head = "ripple/other"
		if ok, err := closeIt(c, pr); err != nil || ok {
			t.Fatalf("got %v, %v, want false, nil", ok, err)
		}
		if p := state(t, cc); p.Status != "OPEN" {
			t.Error("closed")
		}
	})
}

func TestCloseUntouchedFailedCloseIsAnErrorWithoutAComment(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	cc.FailNext("UpdatePullRequestStatus", 1)
	if ok, err := closeIt(c, pr); err == nil || ok {
		t.Fatalf("got %v, %v, want an error", ok, err)
	}
	if p := state(t, cc); p.Status != "OPEN" || len(p.Comments) != 0 {
		t.Errorf("changed: %+v", p)
	}
}

func TestCloseUntouchedFailedCommentAfterCloseStillReportsClosed(t *testing.T) {
	c, cc, pr := openRipplePR(t)
	cc.FailNext("PostCommentForPullRequest", 1)
	ok, err := closeIt(c, pr)
	if err != nil || !ok {
		t.Fatalf("got %v, %v, want true, nil", ok, err)
	}
	if p := state(t, cc); p.Status != "CLOSED" || len(p.Comments) != 0 {
		t.Errorf("%+v", p)
	}
}

func TestCloseUntouchedBadRepoPathIsAnError(t *testing.T) {
	c, _, pr := openRipplePR(t)
	_, err := c.CloseUntouchedPR(context.Background(), model.Repo{FullName: "nope"}, pr, closeNote)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err %v", err)
	}
}

// ListOpenPRs reports the commit the source branch points at (GetPullRequest
// sourceCommit), and it follows the branch when a person pushes to it.
func TestListOpenPRsReportsHeadSHA(t *testing.T) {
	c, cc, _ := openRipplePR(t)
	list := func() platform.PR {
		t.Helper()
		open, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: closePath}, "ripple/")
		if err != nil || len(open) != 1 {
			t.Fatalf("%v %v", open, err)
		}
		return open[0]
	}
	first := cc.BranchHead(closePath, closeHead)
	if got := list().HeadSHA; got == "" || got != first {
		t.Fatalf("HeadSHA %q, want %q", got, first)
	}
	cc.PushBranch(closePath, closeHead, "person@example.invalid")
	if got, want := list().HeadSHA, cc.BranchHead(closePath, closeHead); got == first || got != want {
		t.Fatalf("after a push HeadSHA %q, want %q (not %q)", got, want, first)
	}
}

func TestListOpenPRsReportsBody(t *testing.T) {
	c, _, _ := openRipplePR(t)
	open, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: closePath}, "ripple/")
	if err != nil || len(open) != 1 || open[0].Body != "body" {
		t.Fatalf("%+v %v", open, err)
	}
}
