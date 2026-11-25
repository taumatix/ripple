package azuredevops_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/azuredevops"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/testutil"
)

const (
	org        = "acme"
	project    = "Platform"
	hookUser   = "ripple"
	hookSecret = "conformance-hook-password"
)

func TestConformance(t *testing.T) {
	platformtest.Run(t, &adoFixture{})
}

// adoFixture drives the fake Azure DevOps: repositories are found by listing (the
// service has no install event) and pushes arrive as git.push service hooks.
type adoFixture struct{ az *testutil.AzureDevOps }

func (f *adoFixture) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.az = testutil.NewAzureDevOps(t, org)
	c := &azuredevops.Client{APIBase: f.az.URL, GitBase: f.az.URL, Org: org, Now: now,
		Credentials: platform.StaticCredential{Secret: testutil.AzurePAT},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return azuredevops.NewHost(c, azuredevops.BasicAuth{Username: hookUser, Password: hookSecret}, slog.New(slog.DiscardHandler))
}

func (f *adoFixture) Path(short string) string { return org + "/" + project + "/" + short }

func (f *adoFixture) AddRepo(_ *testing.T, path string, files map[string]string) {
	f.az.AddRepo(path, files)
}

// Register delivers nothing: discovery lists the repositories.
func (f *adoFixture) Register(*testing.T, string, ...string) {}

func (f *adoFixture) Push(t *testing.T, hook, path string, files map[string]string) {
	t.Helper()
	sha := f.az.Commit(path, files)
	body, _ := json.Marshal(pushPayload(project, path[len(org)+len(project)+2:], "refs/heads/main", "refs/heads/main", sha))
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.SetBasicAuth(hookUser, hookSecret)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("git.push webhook: %d", resp.StatusCode)
	}
}

func (f *adoFixture) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, p := range f.az.PRs(path) {
		out = append(out, platformtest.PR{Head: p.Head, Base: p.Base, Draft: p.IsDraft, AutoMerge: p.AutoCompleteSetBy != ""})
	}
	return out
}

func (f *adoFixture) FileAt(path, branch, file string) (string, bool) {
	return f.az.FileAt(path, branch, file)
}
func (f *adoFixture) Calls(path string) int { return f.az.Calls(path) }
func (f *adoFixture) RateLimitNextPR() bool { f.az.RateLimitOnce = true; return true }

var pushSeq int64

// pushPayload is the git.push body of service-hooks-events.md ("git.push", sample
// payload), trimmed to the fields the receiver reads.
func pushPayload(project, repo, updated, defaultBranch, sha string) map[string]any {
	// Each notification carries its own id; a fixed one would be dropped as a repeat
	// once the store remembers handled deliveries.
	n := atomic.AddInt64(&pushSeq, 1)
	return map[string]any{
		"id": fmt.Sprintf("a0a0a0a0-bbbb-cccc-dddd-%012d", n), "eventType": "git.push", "publisherId": "tfs",
		"resource": map[string]any{
			"commits":    []map[string]any{{"commitId": sha}},
			"refUpdates": []map[string]any{{"name": updated, "oldObjectId": "d3d3", "newObjectId": "e4e4"}},
			"repository": map[string]any{"id": "f5f5", "name": repo, "defaultBranch": defaultBranch,
				"project": map[string]any{"id": "a6a6", "name": project}},
		},
	}
}

// FilelessPushes: git.push lists commits but no changed files
// (service-hooks-events.md), so the receiver asks the commit changes API; see
// platformtest.FilelessPushes.
func (f *adoFixture) FilelessPushes() {}

// RenameRepo is platformtest.ReportsRenames: the git.repo.renamed service hook
// (service-hooks-events.md, "Repository renamed").
func (f *adoFixture) RenameRepo(t *testing.T, hook, from, to string) {
	t.Helper()
	f.az.RenameRepo(from, to)
	oldName, newName := from[len(org)+len(project)+2:], to[len(org)+len(project)+2:]
	body, _ := json.Marshal(map[string]any{
		"id":        fmt.Sprintf("b0b0b0b0-bbbb-cccc-dddd-%012d", atomic.AddInt64(&pushSeq, 1)),
		"eventType": "git.repo.renamed", "publisherId": "tfs",
		"resource": map[string]any{
			"oldName": oldName, "newName": newName,
			"repository": map[string]any{"id": "f5f5", "name": newName, "defaultBranch": "refs/heads/main",
				"project": map[string]any{"id": "a6a6", "name": project}},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, hook, bytes.NewReader(body))
	req.SetBasicAuth(hookUser, hookSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("git.repo.renamed webhook: %d", resp.StatusCode)
	}
}
