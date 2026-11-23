package scmmanager_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &scmFixture{})
}

const hookSecret = "conformance secret"

// scmFixture drives the fake SCM-Manager: repositories are discovered by listing
// (the webhook plugin announces no new repository), pushes arrive as webhook
// deliveries carrying the shared secret.
type scmFixture struct{ fake *fakeSCM }

func (f *scmFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.fake = newFakeSCM(t)
	c := &scmmanager.Client{BaseURL: f.fake.BaseURL, Now: now,
		Credentials: platform.StaticCredential{Secret: f.fake.Token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return scmmanager.NewHost(c, []byte(hookSecret), slog.New(slog.DiscardHandler))
}

func (f *scmFixture) Path(short string) string { return "acme/" + short }

func (f *scmFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.fake.AddRepo(path, files)
}

// Register delivers nothing: the suite runs discovery after it.
func (f *scmFixture) Register(*testing.T, string, ...string) {}

func (f *scmFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	t.Helper()
	f.fake.Commit(path, files)
	deliverPush(t, hook, []byte(hookSecret), path, "main", f.fake.Rev(path, "main"))
}

// deliverPush posts a webhook plugin delivery the way an administrator configures it:
// the URL carries the repository, a header carries the secret, the body is the
// documented {"changeset": [...]} (docs/vendor/scm-manager/webhook-plugin-configuration.md).
func deliverPush(t *testing.T, hook string, secret []byte, repo, branch string, ids ...string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(pushRequest(t, hook, secret, repo, branch, ids...))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook: %d", resp.StatusCode)
	}
}

func pushRequest(t *testing.T, hook string, secret []byte, repo, branch string, ids ...string) *http.Request {
	t.Helper()
	ns, name, _ := strings.Cut(repo, "/")
	var cs []map[string]any
	for _, id := range ids {
		cs = append(cs, map[string]any{"id": id, "branches": []string{branch}, "parents": []string{}, "description": "x"})
	}
	body, _ := json.Marshal(map[string]any{"changeset": cs})
	q := url.Values{"namespace": {ns}, "name": {name}}
	req, _ := http.NewRequest(http.MethodPost, hook+"?"+q.Encode(), bytes.NewReader(body))
	req.Header.Set(scmmanager.SecretHeader, string(secret))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (f *scmFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.fake.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.Draft})
	}
	return out
}

func (f *scmFixture) FileAt(path, branch, file string) (string, bool) {
	return f.fake.FileAt(path, branch, file)
}
func (f *scmFixture) Calls(path string) int { return f.fake.Calls(path) }
func (f *scmFixture) RateLimitNextPR() bool {
	f.fake.mu.Lock()
	f.fake.rateLimit = true
	f.fake.mu.Unlock()
	return true
}

// FilelessPushes: the webhook payload lists no files, so the receiver asks the
// modifications API; see platformtest.FilelessPushes.
func (f *scmFixture) FilelessPushes() {}
