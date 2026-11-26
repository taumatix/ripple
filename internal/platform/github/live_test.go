//go:build live

package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/livetest"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

// These tests read public data from api.github.com anonymously and read only (go test
// -tags live; the live workflow): the github.com half of ROADMAP 153. The client is
// ripple's own, configured as for a GHES with GitHub Connect off: its host (a fake that has
// none of these repositories) answers 404 and the tags, commits and releases are then read
// from the upstream, with the pinned X-GitHub-Api-Version and no token. Anonymous reads are
// limited to 60 an hour per address, so this makes about a dozen requests in all.
//
// Head polling (ListInstalledRepoHeads) is not covered here: it reads through GraphQL, which
// GitHub refuses without a token, and through installation endpoints that need an App. It is
// covered by heads_test.go and internal/e2e against the fake only.

func liveGitHub(t *testing.T) (*github.Client, model.Repo) {
	t.Helper()
	host := testutil.NewGitHub(t)
	host.AddRepo("acme/app", map[string]string{"go.mod": "module x\n"})
	hc, _ := livetest.Client()
	c := &github.Client{APIBase: host.APIURL, GitBase: host.GitBase, Tokens: host.Auth, WorkDir: t.TempDir(), HTTP: hc, UpstreamAPIBase: "https://api.github.com",
		// An anonymous address gets 60 requests an hour and a GitHub-hosted runner shares its address, so a
		// run can start rate limited: wait for the reset (about eight minutes at worst) rather than fail on it.
		Retry: httpretry.Policy{MaxAttempts: 3, MaxWait: 12 * time.Minute}}
	return c, model.Repo{FullName: "acme/app", InstallationID: installation}
}

func TestLiveTagsFromTheRealGitHubAnonymously(t *testing.T) {
	c, via := liveGitHub(t)
	tags, err := c.ListTags(context.Background(), via, "actions/labeler")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tg := range tags {
		if tg == "v4.0.0" {
			found = true
		}
	}
	if !found || len(tags) < 20 {
		t.Fatalf("%d tags, v4.0.0 present=%v", len(tags), found)
	}
}

func TestLiveTagCommitOfALightweightAndAnAnnotatedTag(t *testing.T) {
	c, via := liveGitHub(t)
	ctx := context.Background()
	for _, tc := range []struct{ repo, tag, want string }{
		{"actions/labeler", "v4.0.0", "9fd24f1f9d6ceb64ba34d181b329ee72f99978a0"}, // lightweight: the ref names the commit
		{"git/git", "v2.40.0", "73876f4861cd3d187a4682290ab75c9dccadbc56"},        // annotated: the ref names a tag object
	} {
		if got, err := c.TagCommit(ctx, via, tc.repo, tc.tag); err != nil || got != tc.want {
			t.Errorf("TagCommit(%s %s) = %q, %v; want %s", tc.repo, tc.tag, got, err, tc.want)
		}
	}
	if _, err := c.TagCommit(ctx, via, "actions/labeler", "ripple-no-such-tag"); err == nil {
		t.Error("a tag that does not exist resolved")
	}
}

func TestLiveReleaseOfATagWithAndWithoutOne(t *testing.T) {
	c, via := liveGitHub(t)
	ctx := context.Background()
	rel, ok, err := c.ReleaseOf(ctx, via, "actions/labeler", "v4.0.0")
	if err != nil || !ok || !rel.Published.Equal(time.Date(2022, 3, 1, 18, 57, 49, 0, time.UTC)) || rel.URL == "" {
		t.Errorf("ReleaseOf(labeler v4.0.0) = %+v, %v, %v", rel, ok, err)
	}
	if _, ok, err := c.ReleaseOf(ctx, via, "git/git", "v2.40.0"); err != nil || ok {
		t.Errorf("ReleaseOf(git v2.40.0) ok=%v err=%v, want no release and no error", ok, err)
	}
}
