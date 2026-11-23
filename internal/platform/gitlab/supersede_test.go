package gitlab_test

import (
	"context"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

const (
	supToken  = "glpat-supersede"
	supPath   = "acme/platform/app"
	supBranch = "ripple/go/example.com/lib-v1.1.0"
	supNote   = "Superseded by v1.2.0."
)

var _ platform.PRCloser = (*gitlab.Client)(nil)

type supEnv struct {
	gl *testutil.GitLab
	c  *gitlab.Client
	pr platform.PR
}

// newSupEnv opens a merge request on the fake whose only commit is ripple's own.
func newSupEnv(t *testing.T) *supEnv {
	t.Helper()
	gl := testutil.NewGitLab(t)
	gl.Token(supToken, "")
	gl.AddRepo(supPath, map[string]string{"go.mod": "module x\n"})
	gl.PushBranch(supPath, supBranch, gitws.AuthorEmail(gitws.Options{}))
	c := &gitlab.Client{BaseURL: gl.APIURL, GitBase: gl.GitBase, Tokens: gitlab.Tokens{Default: supToken}}
	pr, err := c.CreatePR(context.Background(), repo(supPath), platform.PRSpec{Head: supBranch, Base: "main", Title: "bump lib to v1.1.0", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return &supEnv{gl: gl, c: c, pr: pr}
}

func (e *supEnv) close(t *testing.T) (bool, error) {
	t.Helper()
	return e.c.CloseUntouchedPR(context.Background(), model.Repo{FullName: supPath}, e.pr, supNote)
}

func (e *supEnv) mr(t *testing.T) testutil.GitLabPR {
	t.Helper()
	return e.gl.PRs(supPath)[0]
}

func (e *supEnv) puts() int {
	n := 0
	for _, r := range e.gl.Requests() {
		if strings.HasPrefix(r, "PUT ") || strings.HasPrefix(r, "POST ") && strings.Contains(r, "/notes") {
			n++
		}
	}
	return n
}

// api_merge_requests.md "Update a merge request": state_event close; notes.md "Create a
// merge request note": POST .../notes with body.
func TestCloseUntouchedPRClosesAndCommentsOnce(t *testing.T) {
	e := newSupEnv(t)
	if err := e.c.RequestAutoMerge(context.Background(), repo(supPath), e.pr); err != nil {
		t.Fatal(err)
	}
	ok, err := e.close(t)
	if err != nil || !ok {
		t.Fatalf("want closed, got %v %v", ok, err)
	}
	if m := e.mr(t); !m.Closed || len(m.Comments) != 1 || m.Comments[0] != supNote {
		t.Fatalf("want closed with exactly the one comment: %+v", m)
	}
	if !e.gl.HasBranch(supPath, supBranch) {
		t.Error("the branch must stay")
	}
	reqs := strings.Join(e.gl.Requests(), "\n")
	for _, want := range []string{"PUT /api/v4/projects/acme%2Fplatform%2Fapp/merge_requests/1?", "POST /api/v4/projects/acme%2Fplatform%2Fapp/merge_requests/1/notes?"} {
		if !strings.Contains(reqs, want) {
			t.Errorf("missing request %q in\n%s", want, reqs)
		}
	}
}

func TestCloseUntouchedPRLeavesATouchedPRAlone(t *testing.T) {
	touches := map[string]func(e *supEnv){
		"a comment":              func(e *supEnv) { e.gl.HumanComment(supPath, 1, "looks fine to me") },
		"an approval":            func(e *supEnv) { e.gl.HumanApprove(supPath, 1) },
		"a rename":               func(e *supEnv) { e.gl.HumanRename(supPath, 1, "mine now") },
		"a description edit":     func(e *supEnv) { e.gl.HumanEditBody(supPath, 1) },
		"a pushed commit":        func(e *supEnv) { e.gl.HumanCommitOnBranch(supPath, 1) },
		"a force-push":           func(e *supEnv) { e.gl.HumanForcePush(supPath, 1) },
		"an unknown system note": func(e *supEnv) { e.gl.SystemNote(supPath, 1, "locked the discussion in this merge request") },
	}
	for name, touch := range touches {
		t.Run(name, func(t *testing.T) {
			e := newSupEnv(t)
			touch(e)
			before := e.mr(t)
			ok, err := e.close(t)
			if err != nil || ok {
				t.Fatalf("want false, nil; got %v %v", ok, err)
			}
			if m := e.mr(t); m.Closed || len(m.Comments) != len(before.Comments) {
				t.Errorf("a touched merge request must not change: %+v", m)
			}
			if n := e.puts(); n != 0 {
				t.Errorf("%d writes were sent", n)
			}
		})
	}
}

func TestCloseUntouchedPRLeavesAForeignAuthorAlone(t *testing.T) {
	for name, email := range map[string]string{"another author": "person@example.invalid"} {
		t.Run(name, func(t *testing.T) {
			e := newSupEnv(t)
			e.gl.PushBranch(supPath, "other", email)
			pr, err := e.c.CreatePR(context.Background(), repo(supPath), platform.PRSpec{Head: "other", Base: "main", Title: "t"})
			if err != nil {
				t.Fatal(err)
			}
			ok, err := e.c.CloseUntouchedPR(context.Background(), model.Repo{FullName: supPath}, pr, supNote)
			if err != nil || ok || e.puts() != 0 {
				t.Fatalf("want false, nil and no writes: %v %v %d", ok, err, e.puts())
			}
		})
	}
}

func TestCloseUntouchedPRUsesTheConfiguredIdentity(t *testing.T) {
	e := newSupEnv(t)
	e.c.AuthorEmail = "bot@corp.invalid"
	if ok, err := e.close(t); err != nil || ok {
		t.Fatalf("commits by another identity than the configured one are a person's: %v %v", ok, err)
	}
}

func TestCloseUntouchedPRClosesNothingWhenAReadFails(t *testing.T) {
	for _, suffix := range []string{"/merge_requests/1", "/notes", "/commits", "/versions", "/approvals"} {
		t.Run(suffix, func(t *testing.T) {
			e := newSupEnv(t)
			e.gl.FailRead = suffix
			ok, err := e.close(t)
			if err == nil || ok {
				t.Fatalf("want an error, got %v %v", ok, err)
			}
			if e.mr(t).Closed || e.puts() != 0 {
				t.Error("nothing may be written after a failed read")
			}
		})
	}
}

func TestCloseUntouchedPRIgnoresAPRThatMovedOn(t *testing.T) {
	t.Run("another head", func(t *testing.T) {
		e := newSupEnv(t)
		e.pr.Head = "ripple/go/other"
		if ok, err := e.close(t); err != nil || ok || e.puts() != 0 {
			t.Fatalf("want false, nil, no writes: %v %v", ok, err)
		}
	})
	t.Run("already closed", func(t *testing.T) {
		e := newSupEnv(t)
		if ok, err := e.close(t); err != nil || !ok {
			t.Fatal(ok, err)
		}
		writes := e.puts()
		if ok, err := e.close(t); err != nil || ok || e.puts() != writes {
			t.Fatalf("a second close must change nothing: %v %v", ok, err)
		}
		if n := len(e.mr(t).Comments); n != 1 {
			t.Errorf("want one comment, got %d", n)
		}
	})
}

func TestCloseUntouchedPRFailedCloseLeavesNoComment(t *testing.T) {
	e := newSupEnv(t)
	e.gl.FailClose = true
	if ok, err := e.close(t); err == nil || ok {
		t.Fatalf("want an error, got %v %v", ok, err)
	}
	if m := e.mr(t); m.Closed || len(m.Comments) != 0 {
		t.Fatalf("a failed close must leave the merge request open and uncommented: %+v", m)
	}
	e.gl.FailClose = false
	if ok, err := e.close(t); err != nil || !ok {
		t.Fatalf("the retry must close it: %v %v", ok, err)
	}
	if n := len(e.mr(t).Comments); n != 1 {
		t.Errorf("want one comment after the retry, got %d", n)
	}
}

func TestCloseUntouchedPRFailedCommentStillCounts(t *testing.T) {
	e := newSupEnv(t)
	e.gl.FailNote = true
	ok, err := e.close(t)
	if err != nil || !ok {
		t.Fatalf("the close happened, so want true, nil; got %v %v", ok, err)
	}
	if !e.mr(t).Closed {
		t.Fatal("not closed")
	}
}
