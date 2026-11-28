package gitlab_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/testutil"
)

// rolesEnv is a fake GitLab with one project and a branch ready for a merge request.
func rolesEnv(t *testing.T, token string) (*testutil.GitLab, *gitlab.Client) {
	t.Helper()
	gl := testutil.NewGitLab(t)
	gl.Token(token, "")
	gl.AddRepo("g/app", map[string]string{"README": "x"})
	gl.Member("g/app")
	gl.PushBranch("g/app", "ripple/x", "ripple@example.com")
	c := &gitlab.Client{BaseURL: gl.APIURL, GitBase: gl.GitBase, Tokens: gitlab.Tokens{Default: token}, Now: time.Now,
		RetrySleep: func(context.Context, time.Duration) error { return nil }}
	return gl, c
}

// ROADMAP 293, recorded on GitLab 19.6.2: a Developer token opens the merge request but auto-merge on the
// protected default branch is answered 401 {"message":"401 Unauthorized"}; a Maintainer's is accepted.
func TestDeveloperTokenOpensButCannotAutoMerge(t *testing.T) {
	gl, c := rolesEnv(t, "dev")
	gl.TokenRole("dev", testutil.GitLabDeveloper)
	pr, err := c.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
	if err != nil {
		t.Fatalf("a Developer can open a merge request: %v", err)
	}
	err = c.RequestAutoMerge(context.Background(), repo("g/app"), pr)
	if err == nil || !strings.Contains(err.Error(), "status 401") || !strings.Contains(err.Error(), `{"message":"401 Unauthorized"}`) {
		t.Fatalf("err = %v, want the recorded 401", err)
	}
	if got := gl.PRs("g/app"); len(got) != 1 || got[0].AutoMerge {
		t.Fatalf("a refused request must leave auto-merge off: %+v", got)
	}

	gl.TokenRole("dev", testutil.GitLabMaintainer)
	if err := c.RequestAutoMerge(context.Background(), repo("g/app"), pr); err != nil {
		t.Fatalf("a Maintainer's request: %v", err)
	}
	if got := gl.PRs("g/app"); !got[0].AutoMerge {
		t.Fatalf("auto-merge not set: %+v", got)
	}
}

// ROADMAP 293: the fake's branch visibility delay reaches CreatePR's retry through the real HTTP path.
func TestFakeBranchDelayIsRetriedByCreatePR(t *testing.T) {
	gl, c := rolesEnv(t, "t")
	gl.BranchDelay(2)
	if _, err := c.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"}); err != nil {
		t.Fatalf("two lagging answers must be ridden out: %v", err)
	}
	if n := len(gl.PRs("g/app")); n != 1 {
		t.Fatalf("%d merge requests, want 1", n)
	}
	posts := 0
	for _, r := range gl.Requests() {
		if strings.HasPrefix(r, "POST ") && strings.Contains(r, "merge_requests") {
			posts++
		}
	}
	if posts != 3 {
		t.Errorf("%d creation attempts, want 3 (2 lagging + 1)", posts)
	}

	gl2, c2 := rolesEnv(t, "t")
	gl2.BranchDelay(100)
	if _, err := c2.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"}); err == nil {
		t.Fatal("a branch that never becomes visible must stay an error")
	}
}

// ROADMAP 293: the fake's 429 carries Retry-After and the RateLimit-* headers a real throttle sends, and the
// client turns it into a hold-back of Retry-After seconds.
func TestFakeRateLimitHeadersAndHoldBack(t *testing.T) {
	gl, c := rolesEnv(t, "t")
	gl.RateLimitToken("t")
	now := time.Now()
	c.Now = func() time.Time { return now }
	_, err := c.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(time.Second)) {
		t.Fatalf("err = %v", err)
	}
}

// The headers themselves, read off the wire (values from the 19.6.2 recording in docs/platform-gitlab.md).
func TestFakeRateLimitWireFormat(t *testing.T) {
	gl, _ := rolesEnv(t, "t")
	gl.RateLimitToken("t")
	req, _ := http.NewRequest(http.MethodPost, gl.APIURL+"/api/v4/projects/g%2Fapp/merge_requests",
		strings.NewReader(`{"source_branch":"ripple/x","target_branch":"main","title":"t"}`))
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	h := resp.Header
	if resp.StatusCode != 429 || string(body) != "Retry later" || h.Get("Content-Type") != "text/plain" ||
		h.Get("Retry-After") != "1" || h.Get("RateLimit-Name") != "throttle_authenticated_api" ||
		h.Get("RateLimit-Remaining") != "0" || h.Get("RateLimit-Limit") == "" || h.Get("RateLimit-Reset") == "" {
		t.Fatalf("%d %q %v", resp.StatusCode, body, h)
	}
}
