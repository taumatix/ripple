package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

func newRefsFixture(t *testing.T) (platform.TagSource, model.Repo, *testutil.GitHub) {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	gh.AddRepo("up/action", map[string]string{"action.yml": "name: a\n"})
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir()}
	return c, model.Repo{FullName: "acme/app", InstallationID: installation}, gh
}

func TestTagSource(t *testing.T) {
	p, via, gh := newRefsFixture(t)
	ctx := context.Background()
	c1 := gh.Tag("up/action", "v1.0.0")
	gh.Commit("up/action", map[string]string{"x": "1"})
	c2 := gh.AnnotatedTag("up/action", "v1.1.0")
	if c1 == c2 {
		t.Fatal("fixture: tags share a commit")
	}
	tags, err := p.ListTags(ctx, via, "up/action")
	if err != nil || len(tags) != 2 {
		t.Fatalf("ListTags = %v, %v", tags, err)
	}
	for tag, want := range map[string]string{"v1.0.0": c1, "v1.1.0": c2} {
		got, err := p.TagCommit(ctx, via, "up/action", tag)
		if err != nil || got != want {
			t.Errorf("TagCommit(%s) = %q, %v; want %q (annotated tags must be dereferenced)", tag, got, err, want)
		}
	}
	if _, err := p.TagCommit(ctx, via, "up/action", "v9"); err == nil {
		t.Error("an unknown tag must fail")
	}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	gh.Release("up/action", "v1.1.0", at)
	rel, ok, err := p.ReleaseOf(ctx, via, "up/action", "v1.1.0")
	if err != nil || !ok || !rel.Published.Equal(at) || rel.URL != "https://github.com/up/action/releases/tag/v1.1.0" {
		t.Errorf("ReleaseOf = %+v, %v, %v", rel, ok, err)
	}
	if _, ok, err := p.ReleaseOf(ctx, via, "up/action", "v1.0.0"); err != nil || ok {
		t.Errorf("a tag with no release: ok=%v err=%v", ok, err)
	}
}
