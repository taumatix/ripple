package platformtest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
	"github.com/taumatix/ripple/internal/webhook"
)

// TestBareHostConforms runs the suite against the smallest host the seam allows:
// no install event (repos are discovered by listing), no auto-merge, no legacy
// alias, a prefixed repo name, and a static credential. It borrows the fake
// GitHub's API and git server for the three required calls, so the only thing
// under test is what the seam itself must carry for a host like CodeCommit or Gerrit.
func TestBareHostConforms(t *testing.T) {
	platformtest.Run(t, &bareFixture{})
}

type barePlatform struct {
	c *github.Client

	mu    sync.Mutex
	repos []string
}

func (b *barePlatform) Checkout(ctx context.Context, r model.Repo) (platform.Workspace, error) {
	return b.c.Checkout(ctx, r)
}
func (b *barePlatform) ListOpenPRs(ctx context.Context, r model.Repo, p string) ([]platform.PR, error) {
	return b.c.ListOpenPRs(ctx, r, p)
}
func (b *barePlatform) CreatePR(ctx context.Context, r model.Repo, s platform.PRSpec) (platform.PR, error) {
	return b.c.CreatePR(ctx, r, s)
}
func (b *barePlatform) RateLimitedUntil(r model.Repo) time.Time { return b.c.RateLimitedUntil(r) }
func (b *barePlatform) ListRepos(context.Context) ([]model.Repo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []model.Repo
	for _, n := range b.repos {
		out = append(out, model.Repo{FullName: n})
	}
	return out, nil
}

type bareFixture struct {
	gh     *testutil.GitHub
	p      *barePlatform
	secret []byte
}

func (f *bareFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.gh = testutil.NewGitHub(t)
	f.secret = []byte("bare secret")
	tok, err := f.gh.Auth.Token(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	f.p = &barePlatform{c: &github.Client{APIBase: f.gh.APIURL, GitBase: f.gh.GitBase, Now: now,
		Credentials: platform.StaticCredential{Username: "x-access-token", Secret: tok},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir}}
	// Push events only: this host registers nothing by webhook.
	return &platform.Host{Name: "bare", Platform: f.p, Receiver: func(s platform.Sink) http.Handler {
		return &webhook.Handler{Secret: f.secret, Sink: pushOnly{github.Sink{Sink: s}}, Log: slog.New(slog.DiscardHandler)}
	}}
}

// pushOnly drops every event but push, so registration can only come from listing.
type pushOnly struct{ github.Sink }

func (p pushOnly) Handle(ctx context.Context, e webhook.Event) error {
	if e.Kind != string(platform.Push) {
		return nil
	}
	return p.Sink.Handle(ctx, e)
}

func (f *bareFixture) Path(short string) string { return "proj/" + short }

func (f *bareFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.gh.AddRepo(path, files)
}

func (f *bareFixture) Register(_ *testing.T, _ string, paths ...string) {
	f.p.mu.Lock()
	defer f.p.mu.Unlock()
	f.p.repos = append(f.p.repos, paths...)
}

func (f *bareFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	f.gh.Commit(path, files)
	var changed []string
	for p := range files {
		changed = append(changed, p)
	}
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main",
		"repository": map[string]any{"full_name": path, "default_branch": "main"},
		"commits":    []map[string]any{{"modified": changed}}})
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", webhook.Sign(f.secret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("push webhook: %d", resp.StatusCode)
	}
}

func (f *bareFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.gh.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.AutoMerge})
	}
	return out
}

func (f *bareFixture) FileAt(path, branch, file string) (string, bool) {
	return f.gh.FileAt(path, branch, file)
}
func (f *bareFixture) Calls(path string) int { return f.gh.Calls(path) }
func (f *bareFixture) RateLimitNextPR() bool { f.gh.RateLimitOnce = true; return true }
