package github_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

const mark = "<!-- ripple:dashboard -->"

func newIssueFixture(t *testing.T) (platform.IssueTracker, model.Repo, *testutil.GitHub) {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/lib", map[string]string{"go.mod": "module x\n"})
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir()}
	return c, model.Repo{FullName: "acme/lib", InstallationID: installation}, gh
}

func TestIssuesCreateFindUpdate(t *testing.T) {
	p, repo, gh := newIssueFixture(t)
	ctx := context.Background()
	if _, ok, err := p.FindIssue(ctx, repo, "ripple", mark); err != nil || ok {
		t.Fatalf("empty repo: ok=%v err=%v", ok, err)
	}
	gh.HumanIssue(repo.FullName, "mine", mark+" but a person's, unlabelled")
	gh.HumanIssue(repo.FullName, "labelled, no marker", "hello", "ripple")
	iss, err := p.CreateIssue(ctx, repo, platform.IssueSpec{Title: "Dashboard", Body: mark + "\nv1", Labels: []string{"ripple"}})
	if err != nil || iss.Number == 0 {
		t.Fatalf("create: %+v %v", iss, err)
	}
	got, ok, err := p.FindIssue(ctx, repo, "ripple", mark)
	if err != nil || !ok || got.Number != iss.Number || got.Body != mark+"\nv1" {
		t.Fatalf("find: %+v ok=%v err=%v", got, ok, err)
	}
	if err := p.UpdateIssueBody(ctx, repo, iss.Number, mark+"\nv2"); err != nil {
		t.Fatal(err)
	}
	gh.HumanCloseIssue(repo.FullName, iss.Number)
	got, ok, _ = p.FindIssue(ctx, repo, "ripple", mark)
	if !ok || !got.Closed || got.Body != mark+"\nv2" {
		t.Fatalf("closed issue must still be found with its new body: %+v", got)
	}
}

func TestIssuesFindPaginates(t *testing.T) {
	p, repo, gh := newIssueFixture(t)
	for i := 0; i < 130; i++ {
		gh.HumanIssue(repo.FullName, fmt.Sprint(i), "x", "ripple")
	}
	want := gh.HumanIssue(repo.FullName, "dash", mark, "ripple")
	got, ok, err := p.FindIssue(context.Background(), repo, "ripple", mark)
	if err != nil || !ok || got.Number != want {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}

func TestIssuesErrorMapping(t *testing.T) {
	ctx := context.Background()
	p, repo, gh := newIssueFixture(t)
	gh.DisableIssues(repo.FullName)
	if _, _, err := p.FindIssue(ctx, repo, "ripple", mark); !errors.Is(err, platform.ErrIssuesUnavailable) {
		t.Fatalf("disabled find: %v", err)
	}
	if _, err := p.CreateIssue(ctx, repo, platform.IssueSpec{Title: "t"}); !errors.Is(err, platform.ErrIssuesUnavailable) {
		t.Fatalf("disabled create: %v", err)
	}
	p, repo, gh = newIssueFixture(t)
	gh.ForbidIssues(repo.FullName)
	if _, err := p.CreateIssue(ctx, repo, platform.IssueSpec{Title: "t"}); !errors.Is(err, platform.ErrIssuesUnavailable) {
		t.Fatalf("forbidden create: %v", err)
	}
	p, repo, gh = newIssueFixture(t)
	n := gh.HumanIssue(repo.FullName, "d", mark, "ripple")
	gh.HumanDeleteIssue(repo.FullName, n)
	if err := p.UpdateIssueBody(ctx, repo, n, "x"); !errors.Is(err, platform.ErrIssueGone) {
		t.Fatalf("deleted update: %v", err)
	}
}

func TestIssuesReopenChangesOnlyTheState(t *testing.T) {
	p, repo, gh := newIssueFixture(t)
	ctx := context.Background()
	iss, err := p.CreateIssue(ctx, repo, platform.IssueSpec{Title: "Dashboard", Body: mark + "\nv1", Labels: []string{"ripple"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.(platform.IssueCloser).CloseIssue(ctx, repo, iss.Number); err != nil {
		t.Fatal(err)
	}
	if err := p.(platform.IssueReopener).ReopenIssue(ctx, repo, iss.Number); err != nil {
		t.Fatal(err)
	}
	if err := p.(platform.IssueReopener).ReopenIssue(ctx, repo, iss.Number); err != nil {
		t.Fatalf("reopening an open issue: %v", err)
	}
	got, ok, _ := p.FindIssue(ctx, repo, "ripple", mark)
	if !ok || got.Closed || got.Body != mark+"\nv1" {
		t.Fatalf("want it open with its body: %+v", got)
	}
	gh.HumanDeleteIssue(repo.FullName, iss.Number)
	if err := p.(platform.IssueReopener).ReopenIssue(ctx, repo, iss.Number); !errors.Is(err, platform.ErrIssueGone) {
		t.Fatalf("deleted: %v", err)
	}
}
