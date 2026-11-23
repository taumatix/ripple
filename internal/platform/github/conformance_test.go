package github_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
	"github.com/taumatix/ripple/internal/webhook"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &ghFixture{})
}

// ghFixture drives the fake GitHub: App installation events register repos, push
// events re-scan them, deliveries signed with the webhook secret.
type ghFixture struct {
	gh     *testutil.GitHub
	secret []byte
}

const installation = 7

func (f *ghFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.gh = testutil.NewGitHub(t)
	f.secret = []byte("conformance secret")
	c := &github.Client{APIBase: f.gh.APIURL, GitBase: f.gh.GitBase, Tokens: f.gh.Auth, Now: now,
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return github.NewHost(c, f.secret, slog.New(slog.DiscardHandler))
}

func (f *ghFixture) Path(short string) string { return "acme/" + short }

func (f *ghFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.gh.AddRepo(path, files)
}

func (f *ghFixture) deliver(t *testing.T, hook, event string, payload any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", webhook.Sign(f.secret, body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s webhook: %d", event, resp.StatusCode)
	}
}

func (f *ghFixture) Register(t *testing.T, hook string, paths ...string) {
	var repos []map[string]string
	for _, p := range paths {
		repos = append(repos, map[string]string{"full_name": p})
	}
	f.deliver(t, hook, "installation", map[string]any{"action": "created", "installation": map[string]any{"id": installation}, "repositories": repos})
}

func (f *ghFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	f.gh.Commit(path, files)
	var changed []string
	for p := range files {
		changed = append(changed, p)
	}
	f.deliver(t, hook, "push", map[string]any{
		"ref": "refs/heads/main", "installation": map[string]any{"id": installation},
		"repository": map[string]any{"full_name": path, "default_branch": "main"},
		"commits":    []map[string]any{{"modified": changed}},
	})
}

func (f *ghFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.gh.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.AutoMerge, Closed: p.Closed, Comments: p.Comments})
	}
	return out
}

func (f *ghFixture) FileAt(path, branch, file string) (string, bool) {
	return f.gh.FileAt(path, branch, file)
}
func (f *ghFixture) Calls(path string) int { return f.gh.Calls(path) }
func (f *ghFixture) RateLimitNextPR() bool { f.gh.RateLimitOnce = true; return true }
