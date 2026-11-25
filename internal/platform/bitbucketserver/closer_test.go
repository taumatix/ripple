package bitbucketserver_test

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucketserver"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

var _ platform.PRCloser = (*bitbucketserver.Client)(nil)

const rippleBranch = "ripple/go/old"

var appRepo = model.Repo{FullName: "ACME/app", DefaultBranch: "main"}

// openRipplePR is a pull request ripple opened: one commit on its own branch,
// authored and committed by ripple's identity.
func openRipplePR(t *testing.T) (*bitbucketserver.Client, *testutil.BitbucketServer, platform.PR) {
	t.Helper()
	c, b := newClient2(t)
	b.AddRepo("ACME/app", map[string]string{"a": "1"})
	b.BranchWithCommit("ACME/app", rippleBranch, "ripple", gitws.AuthorEmail(gitws.Options{}))
	pr, err := c.CreatePR(context.Background(), appRepo, platform.PRSpec{Head: rippleBranch, Base: "main", Title: "bump", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return c, b, pr
}

func closeStale(c *bitbucketserver.Client, pr platform.PR) (bool, error) {
	return c.CloseUntouchedPR(context.Background(), appRepo, pr, "superseded by v2")
}

func state(b *testutil.BitbucketServer) (closed bool, comments []string) {
	p := b.PRs("ACME/app")[0]
	return p.Closed, p.Comments
}

func TestCloseUntouchedPRDeclinesAndCommentsOnce(t *testing.T) {
	c, b, pr := openRipplePR(t)
	done, err := closeStale(c, pr)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if closed, cm := state(b); !closed || len(cm) != 1 || cm[0] != "superseded by v2" {
		t.Errorf("closed %v comments %q", closed, cm)
	}
	if !b.HasBranch("ACME/app", rippleBranch) {
		t.Error("the branch must not be deleted")
	}
}

func TestCloseUntouchedPRLeavesATouchedPRAlone(t *testing.T) {
	touches := map[string]func(b *testutil.BitbucketServer){
		"a comment":      func(b *testutil.BitbucketServer) { b.HumanComment("ACME/app", 1, "please wait") },
		"an approval":    func(b *testutil.BitbucketServer) { b.Approve("ACME/app", 1) },
		"a rename":       func(b *testutil.BitbucketServer) { b.Rename("ACME/app", 1, "my title") },
		"an edited body": func(b *testutil.BitbucketServer) { b.EditDescription("ACME/app", 1, "mine") },
		"another commit": func(b *testutil.BitbucketServer) { b.PushCommit("ACME/app", 1) },
		"a force push":   func(b *testutil.BitbucketServer) { b.ForcePush("ACME/app", 1) },
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			c, b, pr := openRipplePR(t)
			touch(b)
			_, before := state(b)
			done, err := closeStale(c, pr)
			if err != nil || done {
				t.Fatalf("done=%v err=%v, want false, nil", done, err)
			}
			if closed, cm := state(b); closed || len(cm) != len(before) {
				t.Errorf("changed the PR: closed %v %q", closed, cm)
			}
		})
	}
}

func TestCloseUntouchedPRClosesNothingWhenAReadFails(t *testing.T) {
	for _, suffix := range []string{"/pull-requests/1", "/commits", "/activities"} {
		t.Run(suffix, func(t *testing.T) {
			c, b, pr := openRipplePR(t)
			b.FailRequest("GET", suffix, 500)
			done, err := closeStale(c, pr)
			if err == nil || done {
				t.Fatalf("done=%v err=%v, want an error", done, err)
			}
			if closed, cm := state(b); closed || len(cm) != 0 {
				t.Errorf("changed the PR: closed %v %q", closed, cm)
			}
		})
	}
}

func TestCloseUntouchedPRIgnoresAPRThatMovedOn(t *testing.T) {
	c, b, pr := openRipplePR(t)
	other := pr
	other.Head = "ripple/go/other"
	if done, err := closeStale(c, other); done || err != nil {
		t.Errorf("different head: done=%v err=%v", done, err)
	}
	if done, err := closeStale(c, pr); !done || err != nil {
		t.Fatalf("first close: done=%v err=%v", done, err)
	}
	if done, err := closeStale(c, pr); done || err != nil {
		t.Errorf("already declined: done=%v err=%v", done, err)
	}
	if _, cm := state(b); len(cm) != 1 {
		t.Errorf("comments %q, want exactly one", cm)
	}
}

func TestCloseUntouchedPRFailedDeclineLeavesNoComment(t *testing.T) {
	c, b, pr := openRipplePR(t)
	b.FailRequest("POST", "/decline", 409)
	if done, err := closeStale(c, pr); err == nil || done {
		t.Fatalf("done=%v err=%v, want an error", done, err)
	}
	if closed, cm := state(b); closed || len(cm) != 0 {
		t.Errorf("closed %v %q", closed, cm)
	}
}

func TestCloseUntouchedPRFailedCommentStillCountsAsClosed(t *testing.T) {
	c, b, pr := openRipplePR(t)
	b.FailRequest("POST", "/comments", 500)
	done, err := closeStale(c, pr)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if closed, _ := state(b); !closed {
		t.Error("not declined")
	}
}

// ListOpenPRs reports the pull request's source commit (fromRef.latestCommit), and it
// moves when a person pushes to the branch.
func TestListOpenPRsReportsHeadSHA(t *testing.T) {
	c, b, _ := openRipplePR(t)
	list := func() platform.PR {
		t.Helper()
		open, err := c.ListOpenPRs(context.Background(), appRepo, "ripple/")
		if err != nil || len(open) != 1 {
			t.Fatalf("%v %v", open, err)
		}
		return open[0]
	}
	first := b.BranchHead("ACME/app", rippleBranch)
	if got := list().HeadSHA; got == "" || got != first {
		t.Fatalf("HeadSHA %q, want %q", got, first)
	}
	b.PushCommit("ACME/app", 1)
	if got, want := list().HeadSHA, b.BranchHead("ACME/app", rippleBranch); got == first || got != want {
		t.Fatalf("after a push HeadSHA %q, want %q (not %q)", got, want, first)
	}
}

func TestListOpenPRsReportsBody(t *testing.T) {
	c, _, _ := openRipplePR(t)
	open, err := c.ListOpenPRs(context.Background(), appRepo, "ripple/")
	if err != nil || len(open) != 1 || open[0].Body != "body" {
		t.Fatalf("%+v %v", open, err)
	}
}
