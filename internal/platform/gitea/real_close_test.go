//go:build giteareal

package gitea_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
)

// TestRealCloseUntouchedPR drives the PRCloser on a pull request made the way ripple makes
// one (a single commit under ripple's author email) and logs each field its decision reads,
// so a refusal names the field (ROADMAP 12, "the real timeline and PR fields").
func TestRealCloseUntouchedPR(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	dir := f.dirs[path]
	f.git(t, dir, "-c", "user.email=ripple@users.noreply.invalid", "-c", "user.name=ripple", "checkout", "-q", "-b", "ripple/go/x")
	writeFiles(t, dir, map[string]string{"x.txt": "x\n"})
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "-c", "user.email=ripple@users.noreply.invalid", "-c", "user.name=ripple", "commit", "-q", "-m", "x")
	f.git(t, dir, "push", "-q", in.url+"/"+path+".git", "ripple/go/x")
	var pr pullJSON
	in.must(t, token(f.tok), http.MethodPost, "/repos/"+path+"/pulls", map[string]any{"title": "x", "head": "ripple/go/x", "base": "main"}, &pr)

	_, _, raw := in.call(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", path, pr.Number), nil, nil)
	t.Logf("FACT pull request: %s", snippet(raw, 1500))
	var cur struct {
		State string `json:"state"`
		Head  struct {
			Ref  string `json:"ref"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		ContentVersion *int `json:"content_version"`
	}
	_ = json.Unmarshal(raw, &cur)
	cv := "absent"
	if cur.ContentVersion != nil {
		cv = fmt.Sprint(*cur.ContentVersion)
	}
	hr := "null"
	if cur.Head.Repo != nil {
		hr = cur.Head.Repo.FullName
	}
	t.Logf("FACT pull request fields the decision reads: state=%q head.ref=%q head.repo=%q content_version=%s", cur.State, cur.Head.Ref, hr, cv)
	for _, p := range []string{"/pulls/%d/reviews", "/issues/%d/comments"} {
		_, _, b := in.call(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s"+p, path, pr.Number), nil, nil)
		t.Logf("FACT %s -> %s", p, snippet(b, 600))
	}
	_, _, raw = in.call(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/commits?limit=2&verification=false&files=false", path, pr.Number), nil, nil)
	t.Logf("FACT pull request commits: %s", snippet(raw, 1500))

	c := &gitea.Client{APIBase: in.url, GitBase: in.url, Credentials: platform.StaticCredential{Username: "ripple", Secret: f.tok}}
	repo := model.Repo{FullName: path, DefaultBranch: "main"}
	closed, err := c.CloseUntouchedPR(context.Background(), repo, platform.PR{Number: pr.Number, Head: "ripple/go/x"}, "superseded")
	t.Logf("FACT CloseUntouchedPR: closed=%v err=%v", closed, err)
	if err != nil || !closed {
		t.Fatalf("an untouched ripple pull request was not closed: closed=%v err=%v", closed, err)
	}
	var after pullJSON
	in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", path, pr.Number), nil, &after)
	if after.State != "closed" || after.Merged {
		t.Errorf("pull request after the close: state %q merged %v", after.State, after.Merged)
	}
}

func snippet(b []byte, n int) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

func (f *realFixture) timelineTypes(t *testing.T, path string, n int) []string {
	t.Helper()
	var tl []struct {
		Type string `json:"type"`
	}
	f.in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/timeline?limit=50", path, n), nil, &tl)
	var types []string
	for _, e := range tl {
		types = append(types, e.Type)
	}
	return types
}

// TestRealPullPushTimeline: which timeline entries a pull request carries when it opens, after
// one more commit on its branch and after a force-push that keeps the commit's author. The
// ownTimeline allow-list and the refusal to close a touched pull request depend on it.
func TestRealPullPushTimeline(t *testing.T) {
	in := realInstance(t)
	f := scene(t, in)
	path := f.Path("facts")
	n := f.branchPR(t, "facts", "ripple/go/tl")
	t.Logf("FACT timeline of a fresh pull request: %v", f.timelineTypes(t, path, n))
	dir := f.dirs[path]
	f.git(t, dir, "checkout", "-q", "ripple/go/tl")
	writeFiles(t, dir, map[string]string{"more.txt": "more\n"})
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "more")
	f.git(t, dir, "push", "-q", in.url+"/"+path+".git", "ripple/go/tl")
	t.Logf("FACT timeline after one more commit: %v", waitTimeline(t, f, path, n, 2))
	f.git(t, dir, "commit", "-q", "--amend", "-m", "amended")
	f.git(t, dir, "push", "-q", "--force", in.url+"/"+path+".git", "ripple/go/tl")
	t.Logf("FACT timeline after a force-push: %v", waitTimeline(t, f, path, n, 3))
}

// waitTimeline polls (the entry is written after the push returns) until at least want entries exist.
func waitTimeline(t *testing.T, f *realFixture, path string, n, want int) []string {
	t.Helper()
	var got []string
	for i := 0; i < 50; i++ {
		got = f.timelineTypes(t, path, n)
		if len(got) >= want {
			return got
		}
		time.Sleep(200 * time.Millisecond)
	}
	return got
}
