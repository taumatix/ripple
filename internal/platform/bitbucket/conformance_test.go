package bitbucket_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &bbFixture{})
}

const hookSecret = "conformance secret"

// bbFixture drives the fake Bitbucket Cloud: repositories are discovered by listing
// the workspace (Bitbucket has no install event), pushes arrive as signed repo:push.
type bbFixture struct{ fake *fakeBB }

func (f *bbFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.fake = newFakeBB(t)
	c := &bitbucket.Client{APIBase: f.fake.APIURL, GitBase: f.fake.GitBase, Now: now, Workspaces: []string{"acme"},
		Credentials: platform.StaticCredential{Secret: f.fake.Token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return bitbucket.NewHost(c, []byte(hookSecret), slog.New(slog.DiscardHandler))
}

func (f *bbFixture) Path(short string) string { return "acme/" + short }

func (f *bbFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.fake.AddRepo(path, files)
}

// Register delivers nothing: the suite runs discovery after it.
func (f *bbFixture) Register(*testing.T, string, ...string) {}

func (f *bbFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	t.Helper()
	old := strings.TrimSpace(git(t, f.fake.bare(path), "rev-parse", "main"))
	f.fake.Commit(path, files)
	head := strings.TrimSpace(git(t, f.fake.bare(path), "rev-parse", "main"))
	deliverPush(t, hook, []byte(hookSecret), path, "main", old, head)
}

func deliverPush(t *testing.T, hook string, secret []byte, repo, branch, old, head string) {
	t.Helper()
	body, _ := json.Marshal(pushBody(repo, branch, old, head))
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.Header.Set("X-Event-Key", "repo:push")
	req.Header.Set("X-Request-UUID", "req-"+head)
	req.Header.Set("X-Hub-Signature", bitbucket.Sign(secret, body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("repo:push webhook: %d", resp.StatusCode)
	}
}

// pushBody is a repo:push payload in the shape of event-payloads.md ("Push"): the
// change's new and old refs with their target hashes, and the pushed commits.
func pushBody(repo, branch, old, head string) map[string]any {
	ref := func(hash string) map[string]any {
		return map[string]any{"type": "branch", "name": branch, "target": map[string]any{"type": "commit", "hash": hash}}
	}
	return map[string]any{
		"push": map[string]any{"changes": []map[string]any{{
			"new": ref(head), "old": ref(old), "created": false, "forced": false, "closed": false, "truncated": false,
			"commits": []map[string]any{{"type": "commit", "hash": head}},
		}}},
		"repository": map[string]any{"type": "repository", "full_name": repo},
	}
}

func (f *bbFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.fake.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft, AutoMerge: p.Queued, Closed: p.state() == "DECLINED", Comments: p.Comments})
	}
	return out
}

func (f *bbFixture) FileAt(path, branch, file string) (string, bool) {
	return f.fake.FileAt(path, branch, file)
}
func (f *bbFixture) Calls(path string) int { return f.fake.Calls(path) }
func (f *bbFixture) RateLimitNextPR() bool {
	f.fake.mu.Lock()
	f.fake.rateLimit = true
	f.fake.mu.Unlock()
	return true
}

// FilelessPushes: a repo:push payload lists no files, so the receiver asks the
// diffstat API; see platformtest.FilelessPushes.
func (f *bbFixture) FilelessPushes() {}
