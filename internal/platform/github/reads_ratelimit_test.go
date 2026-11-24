package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/testutil"
)

func throttledClient(t *testing.T) (*github.Client, model.Repo, *testutil.GitHub, *[]time.Duration) {
	t.Helper()
	gh := testutil.NewGitHub(t)
	gh.AddRepo("acme/lib", map[string]string{"go.mod": "module x\n"})
	var waits []time.Duration
	c := &github.Client{APIBase: gh.APIURL, GitBase: gh.GitBase, Tokens: gh.Auth, WorkDir: t.TempDir(),
		Retry: httpretry.Policy{Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}}}
	return c, model.Repo{FullName: "acme/lib", InstallationID: installation}, gh, &waits
}

// The PR list and the issue search are reads inside a job: a 429 is waited out, as for a tag.
func TestPRAndIssueReadsWaitOutA429(t *testing.T) {
	c, repo, gh, waits := throttledClient(t)
	gh.HumanIssue(repo.FullName, "dash", mark, "ripple")
	gh.Refuse("/pulls", 1, 429, "4")
	gh.Refuse("/issues", 1, 429, "6")
	ctx := context.Background()
	if _, err := c.ListOpenPRs(ctx, repo, "ripple/"); err != nil {
		t.Fatalf("ListOpenPRs: %v", err)
	}
	if _, ok, err := c.FindIssue(ctx, repo, "ripple", mark); err != nil || !ok {
		t.Fatalf("FindIssue ok=%v err=%v", ok, err)
	}
	if len(*waits) != 2 || (*waits)[0] != 4*time.Second || (*waits)[1] != 6*time.Second {
		t.Fatalf("waits %v", *waits)
	}
}

// A write is never repeated blindly: a limited issue create still returns at once.
func TestIssueWriteIsNotWaitedOut(t *testing.T) {
	c, repo, gh, waits := throttledClient(t)
	gh.Refuse("/issues", 1, 429, "4")
	if _, err := c.CreateIssue(context.Background(), repo, platform.IssueSpec{Title: "t", Body: "b"}); err == nil {
		t.Fatal("want the rate-limit error")
	}
	if len(*waits) != 0 {
		t.Fatalf("waited %v", *waits)
	}
}
