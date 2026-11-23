package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

// upstreamFixture is a host that has its own copy of "own/action" and an upstream (the
// stand-in for api.github.com) that has "up/action" and its own, different "own/action".
func upstreamFixture(t *testing.T) (*github.Client, model.Repo, *testutil.GitHub, *testutil.GitHub) {
	t.Helper()
	host, up := testutil.NewGitHub(t), testutil.NewGitHub(t)
	up.PublicReads = true
	host.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	host.AddRepo("own/action", map[string]string{"action.yml": "name: a\n"})
	up.AddRepo("up/action", map[string]string{"action.yml": "name: a\n"})
	up.AddRepo("own/action", map[string]string{"action.yml": "name: other\n"})
	c := &github.Client{APIBase: host.APIURL, GitBase: host.GitBase, Tokens: host.Auth, WorkDir: t.TempDir(), UpstreamAPIBase: up.APIURL}
	return c, model.Repo{FullName: "acme/app", InstallationID: installation}, host, up
}

func TestTagsOfAnActionTheHostLacksAreReadFromUpstream(t *testing.T) {
	c, via, _, up := upstreamFixture(t)
	ctx := context.Background()
	up.Tag("up/action", "v1.0.0")
	up.Commit("up/action", map[string]string{"x": "1"})
	want := up.AnnotatedTag("up/action", "v1.1.0")
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	up.Release("up/action", "v1.1.0", at)

	tags, err := c.ListTags(ctx, via, "up/action")
	if err != nil || len(tags) != 2 {
		t.Fatalf("ListTags = %v, %v", tags, err)
	}
	if got, err := c.TagCommit(ctx, via, "up/action", "v1.1.0"); err != nil || got != want {
		t.Errorf("TagCommit = %q, %v; want %q", got, err, want)
	}
	if rel, ok, err := c.ReleaseOf(ctx, via, "up/action", "v1.1.0"); err != nil || !ok || !rel.Published.Equal(at) {
		t.Errorf("ReleaseOf = %+v, %v, %v", rel, ok, err)
	}
}

func TestAnActionTheHostHasIsNeverReadFromUpstream(t *testing.T) {
	c, via, host, up := upstreamFixture(t)
	ctx := context.Background()
	host.Tag("own/action", "v1.0.0")
	up.Tag("own/action", "v9.9.9")
	up.Release("own/action", "v1.0.0", time.Now()) // upstream's release of a tag the host's copy has no release for

	tags, err := c.ListTags(ctx, via, "own/action")
	if err != nil || len(tags) != 1 || tags[0] != "v1.0.0" {
		t.Errorf("ListTags = %v, %v; want the host's tags only", tags, err)
	}
	if _, ok, err := c.ReleaseOf(ctx, via, "own/action", "v1.0.0"); err != nil || ok {
		t.Errorf("a release of upstream's same-named repo was taken for the host's: ok=%v err=%v", ok, err)
	}
	if _, err := c.TagCommit(ctx, via, "own/action", "v9.9.9"); err == nil {
		t.Error("a tag only upstream's copy has was resolved")
	}
}

func TestWithoutUpstreamAMissingActionIsAnError(t *testing.T) {
	c, via, _, up := upstreamFixture(t)
	c.UpstreamAPIBase = ""
	up.Tag("up/action", "v1.0.0")
	if _, err := c.ListTags(context.Background(), via, "up/action"); err == nil {
		t.Error("an action the host lacks must fail when no upstream is configured")
	}
}

func TestUpstreamNeverSeesTheHostsTokenButGetsItsOwn(t *testing.T) {
	c, via, _, up := upstreamFixture(t)
	up.Tag("up/action", "v1.0.0")
	if _, err := c.ListTags(context.Background(), via, "up/action"); err != nil {
		t.Fatal(err)
	}
	c.UpstreamToken = "pat-for-upstream"
	if _, err := c.ListTags(context.Background(), via, "up/action"); err != nil {
		t.Fatal(err)
	}
	got := up.Bearers()
	if len(got) != 2 || got[0] != "" || got[1] != "pat-for-upstream" {
		t.Errorf("upstream saw bearers %q; want anonymous, then its own token, never the host's", got)
	}
}
