package azuredevops_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/azuredevops"
	"github.com/taumatix/ripple/internal/testutil"
)

// drive runs the whole path ripple takes on one repository against the fake:
// discovery, clone, push of a branch, pull request, list, auto-complete.
func drive(t *testing.T, c *azuredevops.Client, az *testutil.AzureDevOps, path string) {
	t.Helper()
	ctx := context.Background()
	repos, err := c.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var repo model.Repo
	for _, r := range repos {
		if r.FullName == path {
			repo = r
		}
	}
	if repo.FullName == "" {
		t.Fatalf("%q not listed: %+v", path, repos)
	}
	ws, err := c.Checkout(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteFile("new.txt", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if ok, err := ws.CommitAndPush(ctx, "ripple/test", "change"); err != nil || !ok {
		t.Fatalf("push: %v %v", ok, err)
	}
	pr, err := c.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/test", Base: repo.DefaultBranch, Title: "t", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	prs, err := c.ListOpenPRs(ctx, repo, "ripple/")
	if err != nil || len(prs) != 1 || prs[0].Number != pr.Number {
		t.Fatalf("list: %+v %v", prs, err)
	}
	if got := az.PRs(path); len(got) != 1 {
		t.Fatalf("fake has %d PRs", len(got))
	}
	if !strings.Contains(pr.URL, "%20") && strings.Contains(path, " ") {
		t.Errorf("PR URL %q does not escape the spaces", pr.URL)
	}
}

func newClient(t *testing.T, az *testutil.AzureDevOps, cred platform.Credential) *azuredevops.Client {
	t.Helper()
	return &azuredevops.Client{APIBase: az.URL, GitBase: az.URL, Org: org, Log: slog.New(slog.DiscardHandler),
		Credentials: platform.StaticCredential(cred), WorkDir: t.TempDir(),
		AuthorName: "ripple", AuthorEmail: "ripple@example.com"}
}

// Azure DevOps allows spaces in project and repository names; ripple used to skip
// them. Discovery, clone, push and the pull request calls must all escape them.
func TestSpacesInProjectAndRepositoryNames(t *testing.T) {
	az := testutil.NewAzureDevOps(t, org)
	path := org + "/Big Project/my web app"
	az.AddRepo(path, map[string]string{"README.md": "hi\n"})
	az.AddRepo(org+"/Plain/ok", map[string]string{"README.md": "hi\n"})
	c := newClient(t, az, platform.Credential{Secret: testutil.AzurePAT})
	drive(t, c, az, path)

	repos, err := c.ListRepos(context.Background())
	if err != nil || len(repos) != 2 {
		t.Fatalf("repos = %+v, %v (the plain one must still be listed)", repos, err)
	}
}

func TestUnsafeNamesStillRefused(t *testing.T) {
	c := &azuredevops.Client{APIBase: "http://127.0.0.1:1", Org: org, Credentials: platform.StaticCredential{Secret: "x"}}
	for _, n := range []string{" lead/p/r", "o/trail /r", "o/p/r ", "o/ p/r", "o/p/ ", "o/p/a  b", "o/p/a\tb", "o/p/a%2Fb", "o/p/a?b", "o/p/a#b", "o/p/.. ", "o/p/r."} {
		if _, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: n}, "x"); err == nil || !strings.Contains(err.Error(), "refusing") && !strings.Contains(err.Error(), "not organization") {
			t.Errorf("%q: err = %v, want a refusal", n, err)
		}
	}
}

// entra-oauth.md: a Microsoft Entra access token goes in "Authorization: Bearer", on
// REST and on git alike; the fake takes it as well as a PAT.
func TestEntraBearerTokenOnRESTAndGit(t *testing.T) {
	az := testutil.NewAzureDevOps(t, org)
	path := org + "/Platform/web"
	az.AddRepo(path, map[string]string{"README.md": "hi\n"})
	c := newClient(t, az, platform.Credential{Secret: testutil.AzureEntraToken, Scheme: platform.SchemeBearer})
	drive(t, c, az, path)
	if az.GitRejected != 0 {
		t.Errorf("%d git requests were refused", az.GitRejected)
	}
}

func TestWrongEntraTokenIsRefusedOnGit(t *testing.T) {
	az := testutil.NewAzureDevOps(t, org)
	path := org + "/Platform/web"
	az.AddRepo(path, map[string]string{"README.md": "hi\n"})
	c := newClient(t, az, platform.Credential{Secret: "stale", Scheme: platform.SchemeBearer})
	if _, err := c.Checkout(context.Background(), model.Repo{FullName: path, DefaultBranch: "main"}); err == nil {
		t.Fatal("clone with a wrong bearer token succeeded")
	}
	if az.GitRejected == 0 {
		t.Error("the fake saw no refused git request")
	}
}
