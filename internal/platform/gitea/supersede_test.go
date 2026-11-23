package gitea_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

const (
	supRepo   = "acme/app"
	supBranch = "ripple/go/example.com/lib-v1.1.0"
	supNote   = "Superseded by v1.2.0."
)

var _ platform.PRCloser = (*gitea.Client)(nil)

type supEnv struct {
	g  *testutil.Gitea
	c  *gitea.Client
	pr platform.PR
}

// newSupEnv opens a pull request on the fake whose only commit is ripple's own.
func newSupEnv(t *testing.T, forgejo bool) *supEnv {
	t.Helper()
	g := testutil.NewGitea(t, forgejo)
	g.AddRepo(supRepo, map[string]string{"go.mod": "module x\n"})
	g.PushBranch(supRepo, supBranch, gitws.AuthorEmail(gitws.Options{}))
	c := client(g)
	c.Log = slog.New(slog.DiscardHandler)
	pr, err := c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{Head: supBranch, Base: "main", Title: "bump lib to v1.1.0", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return &supEnv{g: g, c: c, pr: pr}
}

func (e *supEnv) close() (bool, error) {
	return e.c.CloseUntouchedPR(context.Background(), model.Repo{FullName: supRepo}, e.pr, supNote)
}

func (e *supEnv) pull() testutil.PR { return e.g.PRs(supRepo)[0] }

func (e *supEnv) writes() int {
	n := 0
	for _, r := range e.g.Requests() {
		if strings.HasPrefix(r, "PATCH ") || strings.HasPrefix(r, "POST ") && strings.HasSuffix(r, "/comments") {
			n++
		}
	}
	return n
}

func eachFlavour(t *testing.T, f func(t *testing.T, forgejo bool)) {
	for name, forgejo := range map[string]bool{"gitea": false, "forgejo": true} {
		t.Run(name, func(t *testing.T) { f(t, forgejo) })
	}
}

// swagger "repoEditPullRequest": PATCH .../pulls/{index} with state closed; "issueCreateComment":
// POST .../issues/{index}/comments with body.
func TestCloseUntouchedPRClosesAndCommentsOnce(t *testing.T) {
	eachFlavour(t, func(t *testing.T, forgejo bool) {
		e := newSupEnv(t, forgejo)
		// Scheduling the auto-merge and labelling leave timeline entries of ripple's own.
		if err := e.c.RequestAutoMerge(context.Background(), model.Repo{FullName: supRepo}, e.pr); err != nil {
			t.Fatal(err)
		}
		e.g.TimelineEvent(supRepo, 1, "label")
		ok, err := e.close()
		if err != nil || !ok {
			t.Fatalf("want closed, got %v %v", ok, err)
		}
		if p := e.pull(); !p.Closed || len(p.Comments) != 1 || p.Comments[0] != supNote {
			t.Fatalf("want closed with exactly the one comment: %+v", p)
		}
		if !e.g.HasBranch(supRepo, supBranch) {
			t.Error("the branch must stay")
		}
		reqs := strings.Join(e.g.Requests(), "\n")
		for _, want := range []string{"PATCH /api/v1/repos/acme/app/pulls/1", "POST /api/v1/repos/acme/app/issues/1/comments"} {
			if !strings.Contains(reqs, want) {
				t.Errorf("missing request %q in\n%s", want, reqs)
			}
		}
	})
}

func TestCloseUntouchedPRLeavesATouchedPRAlone(t *testing.T) {
	touches := map[string]func(e *supEnv){
		"a comment":                       func(e *supEnv) { e.g.HumanComment(supRepo, 1, "looks fine to me") },
		"a review":                        func(e *supEnv) { e.g.HumanReview(supRepo, 1) },
		"a rename":                        func(e *supEnv) { e.g.HumanRename(supRepo, 1, "mine now") },
		"a pushed commit":                 func(e *supEnv) { e.g.HumanCommitOnBranch(supRepo, 1) },
		"a force-push":                    func(e *supEnv) { e.g.HumanForcePush(supRepo, 1) },
		"an unknown timeline":             func(e *supEnv) { e.g.TimelineEvent(supRepo, 1, "lock") },
		"a description edit (gitea only)": nil,
	}
	eachFlavour(t, func(t *testing.T, forgejo bool) {
		for name, touch := range touches {
			if touch == nil {
				if forgejo {
					continue // Forgejo's pull request has no content_version: not detectable
				}
				touch = func(e *supEnv) { e.g.HumanEditBody(supRepo, 1) }
			}
			t.Run(name, func(t *testing.T) {
				e := newSupEnv(t, forgejo)
				touch(e)
				before := e.pull()
				ok, err := e.close()
				if err != nil || ok {
					t.Fatalf("want false, nil; got %v %v", ok, err)
				}
				if p := e.pull(); p.Closed || len(p.Comments) != len(before.Comments) {
					t.Errorf("a touched pull request must not change: %+v", p)
				}
				if n := e.writes(); n != 0 {
					t.Errorf("%d writes were sent", n)
				}
			})
		}
	})
}

func TestCloseUntouchedPRLeavesAForeignAuthorAlone(t *testing.T) {
	e := newSupEnv(t, false)
	e.g.PushBranch(supRepo, "other", "person@example.invalid")
	pr, err := e.c.CreatePR(context.Background(), model.Repo{FullName: supRepo}, platform.PRSpec{Head: "other", Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := e.c.CloseUntouchedPR(context.Background(), model.Repo{FullName: supRepo}, pr, supNote)
	if err != nil || ok || e.writes() != 0 {
		t.Fatalf("want false, nil and no writes: %v %v %d", ok, err, e.writes())
	}
}

func TestCloseUntouchedPRUsesTheConfiguredIdentity(t *testing.T) {
	e := newSupEnv(t, false)
	e.c.AuthorEmail = "bot@corp.invalid"
	if ok, err := e.close(); err != nil || ok {
		t.Fatalf("commits by another identity than the configured one are a person's: %v %v", ok, err)
	}
}

func TestCloseUntouchedPRClosesNothingWhenAReadFails(t *testing.T) {
	for _, suffix := range []string{"/pulls/1", "/pulls/1/commits", "/pulls/1/reviews", "/issues/1/comments", "/issues/1/timeline"} {
		t.Run(suffix, func(t *testing.T) {
			e := newSupEnv(t, false)
			e.g.FailRead = suffix
			ok, err := e.close()
			if err == nil || ok {
				t.Fatalf("want an error, got %v %v", ok, err)
			}
			if e.pull().Closed || e.writes() != 0 {
				t.Error("nothing may be written after a failed read")
			}
		})
	}
}

func TestCloseUntouchedPRIgnoresAPRThatMovedOn(t *testing.T) {
	t.Run("another head", func(t *testing.T) {
		e := newSupEnv(t, false)
		e.pr.Head = "ripple/go/other"
		if ok, err := e.close(); err != nil || ok || e.writes() != 0 {
			t.Fatalf("want false, nil, no writes: %v %v", ok, err)
		}
	})
	t.Run("already closed", func(t *testing.T) {
		e := newSupEnv(t, false)
		if ok, err := e.close(); err != nil || !ok {
			t.Fatal(ok, err)
		}
		writes := e.writes()
		if ok, err := e.close(); err != nil || ok || e.writes() != writes {
			t.Fatalf("a second close must change nothing: %v %v", ok, err)
		}
		if n := len(e.pull().Comments); n != 1 {
			t.Errorf("want one comment, got %d", n)
		}
	})
}

func TestCloseUntouchedPRFailedCloseLeavesNoComment(t *testing.T) {
	e := newSupEnv(t, false)
	e.g.FailClose = true
	if ok, err := e.close(); err == nil || ok {
		t.Fatalf("want an error, got %v %v", ok, err)
	}
	if p := e.pull(); p.Closed || len(p.Comments) != 0 {
		t.Fatalf("a failed close must leave the pull request open and uncommented: %+v", p)
	}
	e.g.FailClose = false
	if ok, err := e.close(); err != nil || !ok {
		t.Fatalf("the retry must close it: %v %v", ok, err)
	}
	if n := len(e.pull().Comments); n != 1 {
		t.Errorf("want one comment after the retry, got %d", n)
	}
}

func TestCloseUntouchedPRFailedCommentStillCounts(t *testing.T) {
	e := newSupEnv(t, false)
	e.g.FailComment = true
	ok, err := e.close()
	if err != nil || !ok {
		t.Fatalf("the close happened, so want true, nil; got %v %v", ok, err)
	}
	if !e.pull().Closed {
		t.Fatal("not closed")
	}
}
