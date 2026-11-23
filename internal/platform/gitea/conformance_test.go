package gitea_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestConformanceGitea(t *testing.T)   { platformtest.Run(t, &fixture{forgejo: false}) }
func TestConformanceForgejo(t *testing.T) { platformtest.Run(t, &fixture{forgejo: true}) }

// fixture drives the fake Gitea/Forgejo. Neither project has an install event, so
// the host is a Lister: Register only makes the repos visible to the token, and
// pushes are delivered as signed webhooks under each flavour's header names.
type fixture struct {
	forgejo bool
	g       *testutil.Gitea
	secret  []byte
}

func (f *fixture) Host(t *testing.T, o gitws.Options, now func() time.Time) *platform.Host {
	f.g = testutil.NewGitea(t, f.forgejo)
	f.secret = []byte("conformance secret")
	c := &gitea.Client{APIBase: f.g.APIURL, GitBase: f.g.GitBase, Now: now,
		Credentials: platform.StaticCredential{Username: "ripple", Secret: testutil.GiteaToken},
		WorkDir:     o.WorkDir, GoProxy: o.GoProxy, GoSumDB: o.GoSumDB, GoCacheDir: o.GoCacheDir, Sandbox: o.Sandbox}
	return gitea.NewHost(c, f.secret, slog.New(slog.DiscardHandler))
}

func (f *fixture) Path(short string) string { return "acme/" + short }
func (f *fixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.g.AddRepo(path, files)
}
func (f *fixture) Register(_ *testing.T, _ string, paths ...string) { f.g.Show(paths...) }

func (f *fixture) Push(t *testing.T, hook, path string, files map[string]string) {
	f.g.Commit(path, files)
	var changed []string
	for p := range files {
		changed = append(changed, p)
	}
	body, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/main", "total_commits": 1,
		"repository": map[string]any{"full_name": path, "default_branch": "main"},
		"commits":    []map[string]any{{"modified": changed}},
	})
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	sig := gitea.Sign(f.secret, body)
	if f.forgejo {
		req.Header.Set("X-Forgejo-Event", "push")
		req.Header.Set("X-Forgejo-Signature", sig)
	} else {
		req.Header.Set("X-Gitea-Event", "push")
		req.Header.Set("X-Gitea-Signature", sig)
	}
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

func (f *fixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.g.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.AutoMerge, Closed: p.Closed, Comments: p.Comments})
	}
	return out
}
func (f *fixture) FileAt(path, branch, file string) (string, bool) {
	return f.g.FileAt(path, branch, file)
}
func (f *fixture) Calls(path string) int { return f.g.Calls(path) }
func (f *fixture) RateLimitNextPR() bool { f.g.RateLimitOnce = true; return true }
