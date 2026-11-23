package gerrit_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &gerritFixture{})
}

const hookSecret = "conformance secret"

// gerritFixture drives the fake Gerrit: projects are discovered by listing (the
// webhooks plugin's project-created event is optional), pushes arrive as the
// plugin's ref-updated event with the secret in the URL.
type gerritFixture struct{ fake *fakeGerrit }

func (f *gerritFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.fake = newFakeGerrit(t)
	c := &gerrit.Client{BaseURL: f.fake.URL, Now: now,
		Credentials: platform.StaticCredential{Username: f.fake.User, Secret: f.fake.Token},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return gerrit.NewHost(c, []byte(hookSecret), slog.New(slog.DiscardHandler))
}

func (f *gerritFixture) Path(short string) string { return "acme/" + short }

func (f *gerritFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.fake.AddRepo(path, files)
}

// Register delivers nothing: the suite runs discovery after it.
func (f *gerritFixture) Register(*testing.T, string, ...string) {}

func (f *gerritFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	t.Helper()
	old := f.fake.rev(path, "main")
	f.fake.Commit(path, files)
	deliverRefUpdated(t, hook, hookSecret, path, "refs/heads/main", old, f.fake.rev(path, "main"))
}

func deliverRefUpdated(t *testing.T, hook, secret, project, ref, old, head string) {
	t.Helper()
	body, _ := json.Marshal(refUpdatedBody(project, ref, old, head))
	req, _ := http.NewRequest(http.MethodPost, hook+"?token="+url.QueryEscape(secret), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("ref-updated webhook: %d %s", resp.StatusCode, b)
	}
}

// refUpdatedBody is a ref-updated event as stream-events.md ("Ref Updated") and
// json.md ("refUpdate") describe it.
func refUpdatedBody(project, ref, old, head string) map[string]any {
	return map[string]any{
		"type":           "ref-updated",
		"submitter":      map[string]any{"name": "Jane Doe", "email": "jane@example.invalid", "username": "jane"},
		"refUpdate":      map[string]any{"oldRev": old, "newRev": head, "refName": ref, "project": project},
		"eventCreatedOn": 1700000000,
	}
}

func (f *gerritFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, c := range f.fake.Changes(path) {
		pr := platformtest.PR{Head: c.Topic, Base: c.Branch, Draft: c.WIP, Closed: c.Status == "ABANDONED"}
		if c.AbandonMessage != "" {
			pr.Comments = []string{c.AbandonMessage}
		}
		out = append(out, pr)
	}
	return out
}

func (f *gerritFixture) FileAt(path, branch, file string) (string, bool) {
	return f.fake.FileAt(path, branch, file)
}
func (f *gerritFixture) Calls(path string) int { return f.fake.Calls(path) }
func (f *gerritFixture) RateLimitNextPR() bool {
	f.fake.mu.Lock()
	f.fake.rateLimit = true
	f.fake.mu.Unlock()
	return true
}

// FilelessPushes: a ref-updated event lists revisions, not files, so the receiver
// asks the commits API; see platformtest.FilelessPushes.
func (f *gerritFixture) FilelessPushes() {}

// PushCreatesProposals: the push to refs/for/<branch> is what creates the change;
// see platformtest.PushCreatesProposals.
func (f *gerritFixture) PushCreatesProposals() {}

var _ = strings.TrimSpace
