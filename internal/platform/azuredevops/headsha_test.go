package azuredevops_test

import (
	"context"
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/azuredevops"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/testutil"
)

// ListOpenPRs reports the pull request's source commit (lastMergeSourceCommit.commitId),
// and it moves when a person pushes to the branch.
func TestListOpenPRsReportsHeadSHA(t *testing.T) {
	az := testutil.NewAzureDevOps(t, org)
	path := org + "/" + project + "/app"
	const branch = "ripple/go/old"
	az.AddRepo(path, map[string]string{"go.mod": "module x\n"})
	az.PushBranch(path, branch, gitws.AuthorEmail(gitws.Options{}))
	c := &azuredevops.Client{APIBase: az.URL, GitBase: az.URL, Org: org, Credentials: platform.StaticCredential{Secret: testutil.AzurePAT}}
	repo := model.Repo{FullName: path, DefaultBranch: "main"}
	if _, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: branch, Base: "main", Title: "bump", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	list := func() platform.PR {
		t.Helper()
		open, err := c.ListOpenPRs(context.Background(), repo, "ripple/")
		if err != nil || len(open) != 1 {
			t.Fatalf("%v %v", open, err)
		}
		return open[0]
	}
	first := az.BranchHead(path, branch)
	if got := list().HeadSHA; got == "" || got != first {
		t.Fatalf("HeadSHA %q, want %q", got, first)
	}
	az.HumanCommitOnBranch(path, branch)
	if got, want := list().HeadSHA, az.BranchHead(path, branch); got == first || got != want {
		t.Fatalf("after a push HeadSHA %q, want %q (not %q)", got, want, first)
	}
}
