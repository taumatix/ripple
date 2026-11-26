package github_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

// The facts these tests hold the client to come from docs/vendor/github/graphql-repository-heads.md:
// GET /app/installations takes a JWT, per_page is at most 100 and suspended_at is null for an
// active installation; GraphQL answers repository(...).defaultBranchRef.target.oid; a primary
// GraphQL limit is HTTP 200, a REST or secondary one 403 or 429 with retry-after.

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func headClient(gh *testutil.GitHub, clk *clock) *github.Client {
	return &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, Now: clk.now}
}

func byName(hs []platform.RepoHead) map[string]platform.RepoHead {
	m := map[string]platform.RepoHead{}
	for _, h := range hs {
		m[h.FullName] = h
	}
	return m
}

func TestListInstalledRepoHeadsReadsEveryActiveInstallationInOneQuery(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/a", map[string]string{"go.mod": "module a\n"})
	gh.AddRepo("other/b", map[string]string{"go.mod": "module b\n"})
	gh.AddRepo("acme/old", map[string]string{"go.mod": "module o\n"})
	gh.AddRepo("gone/c", map[string]string{"go.mod": "module c\n"})
	gh.SetDefaultBranch("other/b", "trunk")
	gh.Install(7, "acme/a", "acme/old")
	gh.Install(9, "other/b")
	gh.Install(11, "gone/c")
	gh.SuspendInstallation(11)
	gh.ArchiveRepo("acme/old")
	c := headClient(gh, &clock{time.Unix(1_800_000_000, 0)})

	hs, err := c.ListInstalledRepoHeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byName(hs)
	if len(got) != 2 {
		t.Fatalf("heads = %+v, want only acme/a and other/b (suspended installation and archived repo left out)", hs)
	}
	if want := (platform.RepoHead{FullName: "acme/a", InstallationID: 7, DefaultBranch: "main", Head: gh.HeadSHA("acme/a", "main")}); got["acme/a"] != want {
		t.Errorf("acme/a = %+v, want %+v", got["acme/a"], want)
	}
	if want := (platform.RepoHead{FullName: "other/b", InstallationID: 9, DefaultBranch: "trunk", Head: gh.HeadSHA("other/b", "trunk")}); got["other/b"] != want {
		t.Errorf("other/b = %+v, want %+v", got["other/b"], want)
	}
	if got["acme/a"].Head == "" || len(got["acme/a"].Head) != 40 {
		t.Errorf("head %q is not a 40 character oid", got["acme/a"].Head)
	}
	// One GraphQL read per installation (a token is per installation), none per repository.
	if q := gh.HeadQueries(); !slices.Equal(q, []int{1, 1}) {
		t.Errorf("head queries asked for %v repositories, want one per active installation, 1 and 1 (archived left out)", q)
	}
	if n, _ := gh.HeadListCalls(); n != 1 {
		t.Errorf("GET /app/installations = %d, want 1", n)
	}
	if gh.Calls("acme/a")+gh.Calls("other/b") != 0 {
		t.Errorf("a per-repository REST call was made")
	}
}

func TestListInstalledRepoHeadsBatchesAHundredRepositoriesPerQuery(t *testing.T) {
	gh := testutil.NewGitHub(t)
	var names []string
	for i := 0; i < 205; i++ {
		names = append(names, fmt.Sprintf("acme/r%03d", i)) // no bare repository: read as unresolved
	}
	gh.AddRepo("acme/real", map[string]string{"go.mod": "module r\n"})
	gh.Install(7, append(names, "acme/real")...)
	c := headClient(gh, &clock{time.Unix(1_800_000_000, 0)})

	hs, err := c.ListInstalledRepoHeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 206 {
		t.Fatalf("got %d repositories, want 206", len(hs))
	}
	if q := gh.HeadQueries(); !slices.Equal(q, []int{100, 100, 6}) {
		t.Errorf("query sizes = %v, want [100 100 6]", q)
	}
	if _, pages := gh.HeadListCalls(); pages != 3 {
		t.Errorf("GET /installation/repositories pages = %d, want 3 (per_page 100)", pages)
	}
	m := byName(hs)
	if m["acme/real"].Head != gh.HeadSHA("acme/real", "main") || m["acme/r000"].Head != "" || m["acme/r000"].DefaultBranch != "main" {
		t.Errorf("real = %+v, unresolved = %+v: an unresolved repository has no head but keeps the listed default branch", m["acme/real"], m["acme/r000"])
	}
}

func TestListInstalledRepoHeadsReusesItsReadForHeadEvery(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/a", map[string]string{"go.mod": "module a\n"})
	gh.Install(7, "acme/a")
	clk := &clock{time.Unix(1_800_000_000, 0)}
	c := headClient(gh, clk)
	before := gh.HeadSHA("acme/a", "main")

	if _, err := c.ListInstalledRepoHeads(context.Background()); err != nil {
		t.Fatal(err)
	}
	gh.Commit("acme/a", map[string]string{"x": "1"})
	clk.t = clk.t.Add(9 * time.Minute)
	hs, err := c.ListInstalledRepoHeads(context.Background())
	if err != nil || hs[0].Head != before || len(gh.HeadQueries()) != 1 {
		t.Fatalf("within the default 10 minutes: %+v, %v, %d queries; want the first read reused", hs, err, len(gh.HeadQueries()))
	}
	clk.t = clk.t.Add(2 * time.Minute)
	hs, err = c.ListInstalledRepoHeads(context.Background())
	if err != nil || hs[0].Head != gh.HeadSHA("acme/a", "main") || hs[0].Head == before || len(gh.HeadQueries()) != 2 {
		t.Fatalf("after 11 minutes: %+v, %v; want a fresh read", hs, err)
	}
}

func TestListInstalledRepoHeadsEndsTheRoundOnA429AndReadsAgainWhenItLifts(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/a", map[string]string{"go.mod": "module a\n"})
	gh.Install(7, "acme/a")
	gh.Refuse("/graphql", 1, 429, "30")
	clk := &clock{time.Unix(1_800_000_000, 0)}
	c := headClient(gh, clk)

	_, err := c.ListInstalledRepoHeads(context.Background())
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(clk.t.Add(30*time.Second)) {
		t.Fatalf("err = %v, want a rate limit until Retry-After (30s) from now", err)
	}
	if gh.Refused() != 1 {
		t.Fatalf("refusals = %d: a 429 must not be retried inside the round", gh.Refused())
	}
	// While held back nothing is sent, not even the listing.
	repoPagesBefore := func() int { _, p := gh.HeadListCalls(); return p }
	p0 := repoPagesBefore()
	if _, err := c.ListInstalledRepoHeads(context.Background()); !errors.As(err, &rl) || repoPagesBefore() != p0 {
		t.Fatalf("while held: err = %v, listing pages %d -> %d; want the same error and no request", err, p0, repoPagesBefore())
	}
	clk.t = clk.t.Add(31 * time.Second)
	hs, err := c.ListInstalledRepoHeads(context.Background())
	if err != nil || len(hs) != 1 || hs[0].Head == "" {
		t.Fatalf("after the limit lifted: %+v, %v", hs, err)
	}
}

func TestListInstalledRepoHeadsStopsBelowTheReserveOfPoints(t *testing.T) {
	gh := testutil.NewGitHub(t)
	var names []string
	for i := 0; i < 150; i++ {
		names = append(names, fmt.Sprintf("acme/r%03d", i))
	}
	gh.Install(7, names...)
	gh.SetRateLimitRemaining(50) // rateLimit.remaining after the first query
	clk := &clock{time.Now()}    // the fake's resetAt is an hour from the real now
	c := headClient(gh, clk)

	_, err := c.ListInstalledRepoHeads(context.Background())
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.After(clk.t) {
		t.Fatalf("err = %v, want a rate limit error until resetAt", err)
	}
	if q := gh.HeadQueries(); !slices.Equal(q, []int{100}) {
		t.Errorf("queries = %v, want only the first batch read before stopping", q)
	}
	if !c.RateLimitedUntil(ghesRepo).After(clk.t) {
		t.Errorf("the installation is not held back until the window resets")
	}
}

func TestListInstalledRepoHeadsFailsTheWholeRoundOnAnyError(t *testing.T) {
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/a", map[string]string{"go.mod": "module a\n"})
	gh.Install(7, "acme/a")
	gh.Install(9, "acme/a")
	gh.Refuse("/installation/repositories?per_page=100&page=1", 1, 500, "") // the first installation's listing fails once
	c := headClient(gh, &clock{time.Unix(1_800_000_000, 0)})
	if hs, err := c.ListInstalledRepoHeads(context.Background()); err == nil || hs != nil {
		t.Fatalf("got %+v, %v; want an error and no partial listing (the app would forget the rest)", hs, err)
	}
}

func TestListInstalledRepoHeadsNeedsAnApp(t *testing.T) {
	gh := testutil.NewGitHub(t)
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: github.StaticToken("x")}
	hs, err := c.ListInstalledRepoHeads(context.Background())
	if hs != nil || err != nil || gh.TotalCalls() != 0 {
		t.Fatalf("a token that cannot list installations: %+v, %v", hs, err)
	}
}

func TestListInstalledRepoHeadsOnGHESUsesApiV3AndApiGraphQL(t *testing.T) {
	h := testutil.NewGHES(t)
	h.AddRepo("acme/a", map[string]string{"go.mod": "module a\n"})
	h.Install(7, "acme/a")
	c := &github.Client{APIBase: h.APIURL, GitBase: h.GitBase, GraphQLURL: h.GraphQLURL, Tokens: h.Auth}
	hs, err := c.ListInstalledRepoHeads(context.Background())
	if err != nil || len(hs) != 1 || hs[0].Head != h.HeadSHA("acme/a", "main") {
		t.Fatalf("got %+v, %v", hs, err)
	}
	if h.GraphQLCalls() != 1 || len(h.Strays()) != 0 {
		t.Errorf("graphql calls = %d, strays = %v; want 1 and none", h.GraphQLCalls(), h.Strays())
	}
}
