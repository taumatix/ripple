package github_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

// ghesFixture is the conformance fixture over a fake GHES that serves /api/v3 and
// /api/graphql only, with the delivery headers a GHES adds.
type ghesFixture struct {
	ghFixture
	ghes *testutil.GHES
}

func (f *ghesFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.ghes = testutil.NewGHES(t)
	f.gh = f.ghes.GitHub
	f.enterprise = true
	f.secret = []byte("conformance secret")
	v := &github.Versions{}
	c := &github.Client{APIBase: f.ghes.APIURL, GitBase: f.ghes.GitBase, Tokens: &github.AppAuth{AppID: testutil.AppID, Key: f.ghes.Key(), APIBase: f.ghes.APIURL, Versions: v},
		Versions: v, Now: now, WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return github.NewHost(c, f.secret, slog.New(slog.DiscardHandler))
}

func TestConformanceOnGHES(t *testing.T) {
	platformtest.Run(t, &ghesFixture{})
}

var ghesRepo = model.Repo{FullName: "acme/r", InstallationID: 7, DefaultBranch: "main"}

func ghesClient(h *testutil.GHES, v *github.Versions) *github.Client {
	return &github.Client{APIBase: h.APIURL, GitBase: h.GitBase, Versions: v,
		Tokens: &github.AppAuth{AppID: testutil.AppID, Key: h.Key(), APIBase: h.APIURL, Versions: v}, WorkDir: ""}
}

func openPR(t *testing.T, c *github.Client) platform.PR {
	t.Helper()
	pr, err := c.CreatePR(context.Background(), ghesRepo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestGHESAutoMergeUsesApiGraphql(t *testing.T) {
	h := testutil.NewGHES(t)
	addRepo(h.GitHub)
	c := ghesClient(h, nil)
	if err := c.RequestAutoMerge(context.Background(), ghesRepo, openPR(t, c)); err != nil {
		t.Fatal(err)
	}
	if prs := h.PRs("acme/r"); len(prs) != 1 || !prs[0].AutoMerge {
		t.Fatalf("PR not set to auto-merge: %+v", prs)
	}
	if h.GraphQLCalls() != 1 || len(h.Strays()) != 0 {
		t.Errorf("graphql calls %d, requests outside /api/v3 and /api/graphql: %v", h.GraphQLCalls(), h.Strays())
	}
}

func TestGHESGraphQLURLOverride(t *testing.T) {
	h := testutil.NewGHES(t)
	addRepo(h.GitHub)
	c := ghesClient(h, nil)
	c.GraphQLURL = h.URL + "/elsewhere"
	err := c.RequestAutoMerge(context.Background(), ghesRepo, openPR(t, c))
	if !errors.Is(err, platform.ErrNoAutoMerge) || !slices.Contains(h.Strays(), "/elsewhere") {
		t.Fatalf("err = %v, strays %v: the override must be the URL posted to", err, h.Strays())
	}
}

func TestGHESWithoutGraphQLMeansNoAutoMergeNotFailure(t *testing.T) {
	h := testutil.NewGHES(t)
	h.NoGraphQL = true
	addRepo(h.GitHub)
	c := ghesClient(h, nil)
	if err := c.RequestAutoMerge(context.Background(), ghesRepo, openPR(t, c)); !errors.Is(err, platform.ErrNoAutoMerge) {
		t.Fatalf("err = %v, want ErrNoAutoMerge", err)
	}
}

func TestGHESSchemaWithoutTheMutationMeansNoAutoMerge(t *testing.T) {
	h := testutil.NewGHES(t)
	h.OldSchema = true
	addRepo(h.GitHub)
	c := ghesClient(h, nil)
	if err := c.RequestAutoMerge(context.Background(), ghesRepo, openPR(t, c)); !errors.Is(err, platform.ErrNoAutoMerge) {
		t.Fatalf("err = %v, want ErrNoAutoMerge", err)
	}
}

func TestGHESOlderReleaseGetsTheFallbackVersionOnce(t *testing.T) {
	h := testutil.NewGHES(t)
	h.Versions = []string{github.FallbackAPIVersion}
	addRepo(h.GitHub)
	v := &github.Versions{}
	c := ghesClient(h, v)
	openPR(t, c)
	openPR(t, c)
	if h.Gone() != 1 {
		t.Errorf("410s = %d, want exactly 1 (then the version is remembered); seen %v", h.Gone(), h.VersionsSeen())
	}
	if got := h.VersionsSeen(); got[len(got)-1] != github.FallbackAPIVersion {
		t.Errorf("last request sent %q", got[len(got)-1])
	}
}

func TestGHESPinnedVersionIsNotLowered(t *testing.T) {
	h := testutil.NewGHES(t)
	h.Versions = []string{github.FallbackAPIVersion}
	addRepo(h.GitHub)
	v := github.PinnedVersions(github.APIVersion)
	c := ghesClient(h, v)
	if _, err := c.CreatePR(context.Background(), ghesRepo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"}); err == nil {
		t.Fatal("a pinned unsupported version must fail, not silently change")
	}
	if h.Gone() != 1 {
		t.Errorf("410s = %d, want 1 (no retry)", h.Gone())
	}
}

func TestGitHubDotComShapedBaseIsUnchanged(t *testing.T) {
	gh := testutil.NewGitHub(t)
	addRepo(gh)
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth}
	if err := c.RequestAutoMerge(context.Background(), ghesRepo, openPR(t, c)); err != nil {
		t.Fatal(err)
	}
	if prs := gh.PRs("acme/r"); len(prs) != 1 || !prs[0].AutoMerge {
		t.Fatal("auto-merge not requested through <base>/graphql")
	}
}

// addRepo creates acme/r with the branch ripple/x a PR can be opened from.
func addRepo(g *testutil.GitHub) {
	g.AddRepo("acme/r", map[string]string{"go.mod": "module x\n"})
	g.CommitOn("acme/r", "ripple/x", map[string]string{"x.txt": "x\n"})
}
