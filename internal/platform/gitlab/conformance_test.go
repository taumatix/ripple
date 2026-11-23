package gitlab_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &glFixture{})
}

const (
	confToken  = "glpat-conformance"
	confSecret = "conformance secret"
)

// glFixture drives the fake GitLab. Repos live in a subgroup, so every conformance
// test also covers URL-encoded namespaced project ids. GitLab has no install event:
// Register makes the repos members of the token and the suite's discovery lists them.
type glFixture struct{ gl *testutil.GitLab }

func (f *glFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.gl = testutil.NewGitLab(t)
	f.gl.Token(confToken, "")
	c := &gitlab.Client{BaseURL: f.gl.APIURL, GitBase: f.gl.GitBase, Tokens: gitlab.Tokens{Default: confToken}, Now: now,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return gitlab.NewHost(c, gitlab.Receiver{SecretToken: confSecret}, slog.New(slog.DiscardHandler))
}

func (f *glFixture) Path(short string) string { return "acme/platform/" + short }

func (f *glFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.gl.AddRepo(path, files)
}

func (f *glFixture) Register(_ *testing.T, _ string, paths ...string) {
	for _, p := range paths {
		f.gl.Member(p)
	}
}

func (f *glFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	f.gl.Commit(path, files)
	var changed []string
	for p := range files {
		changed = append(changed, p)
	}
	body, _ := json.Marshal(map[string]any{
		"object_kind": "push", "ref": "refs/heads/main", "total_commits_count": 1,
		"project": map[string]any{"path_with_namespace": path, "default_branch": "main"},
		"commits": []map[string]any{{"added": []string{}, "modified": changed, "removed": []string{}}},
	})
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", confSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("push webhook: %d", resp.StatusCode)
	}
}

func (f *glFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.gl.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.AutoMerge, Closed: p.Closed, Comments: p.Comments})
	}
	return out
}

func (f *glFixture) FileAt(path, branch, file string) (string, bool) {
	return f.gl.FileAt(path, branch, file)
}
func (f *glFixture) Calls(path string) int { return f.gl.Calls(path) }
func (f *glFixture) RateLimitNextPR() bool { f.gl.RateLimitToken(confToken); return true }
