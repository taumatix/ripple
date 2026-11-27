package gitea_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/testutil"
)

const emptyRepo = "acme/fresh"

func emptyEnv(t *testing.T, readsUntilSettled int) *testutil.Gitea {
	t.Helper()
	g := testutil.NewGitea(t, false)
	g.AddRepo(emptyRepo, map[string]string{"go.mod": "module x\n"})
	g.Show(emptyRepo)
	g.SetEmpty(emptyRepo, readsUntilSettled)
	return g
}

func listed(t *testing.T, g *testutil.Gitea) []string {
	t.Helper()
	c := client(g)
	c.EmptySettle = 2 * time.Second
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	return names
}

// Real Gitea 1.27.3 and Forgejo 17.0.2 and 7.0.16 report empty=true for up to about 2 s after
// the first push returns (docs/vendor/gitea/real-answers.md). A listing that skipped such a
// repository left it unregistered until the next periodic discovery (ROADMAP 280): the listing
// now waits for a repository it has not seen empty before to settle.
func TestListReposWaitsForAFreshRepositoryToLeaveEmpty(t *testing.T) {
	g := emptyEnv(t, 3)
	if got := strings.Join(listed(t, g), ","); got != emptyRepo {
		t.Fatalf("listed %q, want the repository that settled after 3 reads", got)
	}
}

// A repository that really is empty costs one wait, not one per discovery.
func TestListReposWaitsOnlyOnceForARepositoryThatStaysEmpty(t *testing.T) {
	g := emptyEnv(t, 1<<30)
	c := client(g)
	c.EmptySettle = 100 * time.Millisecond
	ctx := context.Background()
	if repos, err := c.ListRepos(ctx); err != nil || len(repos) != 0 {
		t.Fatalf("first listing: %v %v, want none", repos, err)
	}
	before := g.EmptyReads(emptyRepo)
	if before == 0 {
		t.Fatal("the first listing did not wait for the empty repository")
	}
	if _, err := c.ListRepos(ctx); err != nil {
		t.Fatal(err)
	}
	if after := g.EmptyReads(emptyRepo); after != before {
		t.Errorf("second listing read the empty repository again (%d -> %d)", before, after)
	}
}

// Once it has been pushed to, a repository seen empty earlier is listed without waiting.
func TestListReposListsARepositoryThatWasEmptyBefore(t *testing.T) {
	g := emptyEnv(t, 1<<30)
	c := client(g)
	c.EmptySettle = 50 * time.Millisecond
	if repos, _ := c.ListRepos(context.Background()); len(repos) != 0 {
		t.Fatalf("want none while empty, got %v", repos)
	}
	g.SetEmpty(emptyRepo, 0)
	repos, err := c.ListRepos(context.Background())
	if err != nil || len(repos) != 1 {
		t.Fatalf("after the push: %v %v, want the repository", repos, err)
	}
}
