package bitbucket_test

import (
	"context"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
	"github.com/taumatix/ripple/internal/platform/gitws"
)

var _ platform.PRCloser = (*bitbucket.Client)(nil)

const rippleBranch = "ripple/go/old"

// openRipplePR is a pull request ripple opened: one commit on its own branch,
// authored by ripple's identity.
func openRipplePR(t *testing.T, f *fakeBB) platform.PR {
	t.Helper()
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	work := t.TempDir()
	git(t, work, "clone", "-q", f.bare("acme/app"), "w")
	w := work + "/w"
	email := gitws.AuthorEmail(gitws.Options{})
	git(t, w, "checkout", "-q", "-b", rippleBranch)
	gitAs(t, w, "ripple", email, "commit", "-q", "--allow-empty", "-m", "bump")
	git(t, w, "push", "-q", "origin", rippleBranch)
	pr, err := newClient(f).CreatePR(context.Background(), appRepo, platform.PRSpec{Head: rippleBranch, Base: "main", Title: "bump", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func closeStale(f *fakeBB, pr platform.PR) (bool, error) {
	return newClient(f).CloseUntouchedPR(context.Background(), appRepo, pr, "superseded by v2")
}

func state(f *fakeBB) (string, []string) {
	p := f.PRs("acme/app")[0]
	return p.state(), p.Comments
}

func TestCloseUntouchedPRDeclinesAndCommentsOnce(t *testing.T) {
	f := newFakeBB(t)
	pr := openRipplePR(t, f)
	done, err := closeStale(f, pr)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if s, c := state(f); s != "DECLINED" || len(c) != 1 || c[0] != "superseded by v2" {
		t.Errorf("state %s comments %q", s, c)
	}
	if !f.hasBranch("acme/app", rippleBranch) {
		t.Error("the branch must not be deleted")
	}
}

func TestCloseUntouchedPRLeavesATouchedPRAlone(t *testing.T) {
	touches := map[string]func(f *fakeBB){
		"a comment":      func(f *fakeBB) { f.HumanComment("acme/app", 1, "please wait") },
		"an approval":    func(f *fakeBB) { f.Approve("acme/app", 1) },
		"a rename":       func(f *fakeBB) { f.Rename("acme/app", 1, "my title") },
		"an edited body": func(f *fakeBB) { f.EditDescription("acme/app", 1, "mine") },
		"another commit": func(f *fakeBB) { f.PushCommit("acme/app", 1) },
		"a force push":   func(f *fakeBB) { f.ForcePush("acme/app", 1) },
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			f := newFakeBB(t)
			pr := openRipplePR(t, f)
			touch(f)
			before := len(f.PRs("acme/app")[0].Comments)
			done, err := closeStale(f, pr)
			if err != nil || done {
				t.Fatalf("done=%v err=%v, want false, nil", done, err)
			}
			if s, c := state(f); s != "OPEN" || len(c) != before {
				t.Errorf("changed the PR: %s %q", s, c)
			}
		})
	}
}

func TestCloseUntouchedPRClosesNothingWhenAReadFails(t *testing.T) {
	for _, suffix := range []string{"/pullrequests/1", "/commits", "/comments", "/activity"} {
		t.Run(suffix, func(t *testing.T) {
			f := newFakeBB(t)
			pr := openRipplePR(t, f)
			f.Fail("GET", suffix, 500)
			done, err := closeStale(f, pr)
			if err == nil || done {
				t.Fatalf("done=%v err=%v, want an error", done, err)
			}
			if s, c := state(f); s != "OPEN" || len(c) != 0 {
				t.Errorf("changed the PR: %s %q", s, c)
			}
		})
	}
}

func TestCloseUntouchedPRIgnoresAPRThatMovedOn(t *testing.T) {
	f := newFakeBB(t)
	pr := openRipplePR(t, f)
	other := pr
	other.Head = "ripple/go/other"
	if done, err := closeStale(f, other); done || err != nil {
		t.Errorf("different head: done=%v err=%v", done, err)
	}
	if done, err := closeStale(f, pr); !done || err != nil {
		t.Fatalf("first close: done=%v err=%v", done, err)
	}
	if done, err := closeStale(f, pr); done || err != nil {
		t.Errorf("already declined: done=%v err=%v", done, err)
	}
	if _, c := state(f); len(c) != 1 {
		t.Errorf("comments %q, want exactly one", c)
	}
}

func TestCloseUntouchedPRFailedDeclineLeavesNoComment(t *testing.T) {
	f := newFakeBB(t)
	pr := openRipplePR(t, f)
	f.Fail("POST", "/decline", 500)
	if done, err := closeStale(f, pr); err == nil || done {
		t.Fatalf("done=%v err=%v, want an error", done, err)
	}
	if s, c := state(f); s != "OPEN" || len(c) != 0 {
		t.Errorf("%s %q", s, c)
	}
}

func TestCloseUntouchedPRFailedCommentStillCountsAsClosed(t *testing.T) {
	f := newFakeBB(t)
	pr := openRipplePR(t, f)
	f.Fail("POST", "/comments", 500)
	done, err := closeStale(f, pr)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if s, _ := state(f); s != "DECLINED" {
		t.Errorf("state %s", s)
	}
}

func TestCloseUntouchedPRUsesTheDocumentedEndpoints(t *testing.T) {
	f := newFakeBB(t)
	pr := openRipplePR(t, f)
	if _, err := closeStale(f, pr); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range f.Requests() {
		if r.Method == "POST" && strings.Contains(r.Path, "/pullrequests/1/") {
			got = append(got, r.Path+" "+r.Body)
		}
	}
	want := []string{"/2.0/repositories/acme/app/pullrequests/1/decline ", `/2.0/repositories/acme/app/pullrequests/1/comments {"content":{"raw":"superseded by v2"}}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q want %q", got, want)
	}
}
