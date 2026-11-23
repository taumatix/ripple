package scmmanager_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

func newClient(t *testing.T) (*scmmanager.Client, *fakeSCM) {
	t.Helper()
	f := newFakeSCM(t)
	return &scmmanager.Client{BaseURL: f.BaseURL, Credentials: platform.StaticCredential{Secret: f.Token}}, f
}

var acmeApp = model.Repo{FullName: "acme/app", DefaultBranch: "main"}

// source-excerpts.md#pr-create: POST pull-requests/{ns}/{name}, media type
// application/vnd.scmm-pullRequest+json;v=2, body {source, target, title, description,
// status}, answered 201 with a Location and no body.
func TestCreatePRWireFormat(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	f.CommitOn("acme/app", "ripple/go/x", map[string]string{"b": "2"})

	pr, err := c.CreatePR(context.Background(), acmeApp, platform.PRSpec{Title: "bump x", Body: "body text", Head: "ripple/go/x", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	r := reqs[len(reqs)-1]
	if r.Method != "POST" || r.Path != "/scm/api/v2/pull-requests/acme/app" {
		t.Errorf("request %s %s", r.Method, r.Path)
	}
	if r.ContentType != mtPR || r.Accept != mtPR {
		t.Errorf("media types: content %q accept %q, want %q", r.ContentType, r.Accept, mtPR)
	}
	if r.Auth != "Bearer "+f.Token {
		t.Errorf("authorization %q", r.Auth)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"source": "ripple/go/x", "target": "main", "title": "bump x", "description": "body text", "status": "OPEN", "shouldDeleteSourceBranch": true}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body %v, want %v", body, want)
	}
	if pr.Number != 1 || pr.Head != "ripple/go/x" || pr.URL != f.BaseURL+"/repo/acme/app/pull-request/1/comments/" {
		t.Errorf("pr %+v", pr)
	}
}

func TestCreatePRDraftIsStatusDraft(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	f.CommitOn("acme/app", "h", map[string]string{"b": "2"})
	if _, err := c.CreatePR(context.Background(), acmeApp, platform.PRSpec{Title: "t", Head: "h", Base: "main", Draft: true}); err != nil {
		t.Fatal(err)
	}
	if prs := f.PRs("acme/app"); len(prs) != 1 || !prs[0].Draft {
		t.Fatalf("prs %+v, want one draft", prs)
	}
}

// A duplicate answers 409 (source-excerpts.md#pr-create); it is an error, not a rate limit.
func TestCreatePRDuplicateIsAnError(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	f.CommitOn("acme/app", "h", map[string]string{"b": "2"})
	spec := platform.PRSpec{Title: "t", Head: "h", Base: "main"}
	if _, err := c.CreatePR(context.Background(), acmeApp, spec); err != nil {
		t.Fatal(err)
	}
	_, err := c.CreatePR(context.Background(), acmeApp, spec)
	var rl *platform.RateLimitError
	if err == nil || errors.As(err, &rl) || !strings.Contains(err.Error(), "409") {
		t.Fatalf("err %v, want a 409 error", err)
	}
}

// source-excerpts.md#pr-list: status defaults to IN_PROGRESS (draft and open), pages
// are zero-based and pageTotal says how many there are.
func TestListOpenPRsPagesAndFilters(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	for i := range 120 {
		f.SeedPR("acme/app", fmt.Sprintf("ripple/go/%03d", i), "main", i == 0)
	}
	f.SeedPR("acme/app", "feature/human", "main", false)
	prs, err := c.ListOpenPRs(context.Background(), acmeApp, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 120 {
		t.Fatalf("got %d PRs, want the 120 ripple ones across two pages", len(prs))
	}
	if prs[0].Number != 1 || prs[0].Head != "ripple/go/000" || prs[0].URL != f.BaseURL+"/repo/acme/app/pull-request/1/comments/" {
		t.Errorf("first PR %+v (the draft must be listed too)", prs[0])
	}
	var queries []string
	for _, r := range f.Requests() {
		queries = append(queries, r.Query)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "status=IN_PROGRESS") || !strings.Contains(queries[1], "page=1") {
		t.Errorf("queries %v", queries)
	}
}

func TestListReposKeepsGitSkipsArchivedAndEmptyAndCachesDefaults(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/one", map[string]string{"a": "1"})
	f.AddRepo("acme/two", map[string]string{"a": "1"})
	f.AddOther("acme/legacy", true, false)
	f.AddOther("acme/old", false, true)
	f.AddOther("acme/empty", false, false) // listed, but no branch to be the default
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Repo{{FullName: "acme/one", DefaultBranch: "main"}, {FullName: "acme/two", DefaultBranch: "main"}}
	if !reflect.DeepEqual(repos, want) {
		t.Fatalf("repos %v, want %v", repos, want)
	}
	before := f.DefaultBranchCalls()
	if _, err := c.ListRepos(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Only the repository that had no default is asked again; the others are remembered.
	if n := f.DefaultBranchCalls() - before; n != 1 {
		t.Errorf("second listing asked %d default branches, want 1 (the empty repository)", n)
	}
}

func TestRateLimitIsRemembered(t *testing.T) {
	c, f := newClient(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c.Now = func() time.Time { return now }
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	f.CommitOn("acme/app", "h", map[string]string{"b": "2"})
	f.mu.Lock()
	f.rateLimit = true
	f.mu.Unlock()
	_, err := c.CreatePR(context.Background(), acmeApp, platform.PRSpec{Title: "t", Head: "h", Base: "main"})
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(time.Second)) {
		t.Fatalf("err %v, want a rate limit until %v", err, now.Add(time.Second))
	}
	if got := c.RateLimitedUntil(acmeApp); !got.Equal(now.Add(time.Second)) {
		t.Errorf("RateLimitedUntil %v", got)
	}
}

func TestBadCredentialIsAnError(t *testing.T) {
	c, f := newClient(t)
	c.Credentials = platform.StaticCredential{Secret: "wrong"}
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	if _, err := c.ListOpenPRs(context.Background(), acmeApp, ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v, want 401", err)
	}
}

func TestUnsafeRepositoryNamesAreRefused(t *testing.T) {
	c, _ := newClient(t)
	for _, n := range []string{"../x/y", "a/..", "a", "a/b/c", "a b/c", "a/b?x=1", "", "a/b#c", "./a/b"} {
		r := model.Repo{FullName: n, DefaultBranch: "main"}
		if _, err := c.ListOpenPRs(context.Background(), r, ""); err == nil {
			t.Errorf("ListOpenPRs accepted %q", n)
		}
		if _, err := c.CreatePR(context.Background(), r, platform.PRSpec{}); err == nil {
			t.Errorf("CreatePR accepted %q", n)
		}
		if _, err := c.Checkout(context.Background(), r); err == nil {
			t.Errorf("Checkout accepted %q", n)
		}
	}
}

// source-excerpts.md#modifications: added, modified, removed and renamed paths.
func TestPushFilesUnionsTheChangesetsModifications(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"go.mod": "1"})
	f.Commit("acme/app", map[string]string{"go.mod": "2", "x/new.go": "n"})
	first := f.Rev("acme/app", "main")
	f.Commit("acme/app", map[string]string{"README.md": "r", "go.mod": "3"})
	second := f.Rev("acme/app", "main")

	got, err := c.PushFiles(context.Background(), "acme/app", "main", []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"x/new.go", "go.mod", "README.md"}) {
		t.Fatalf("files %v", got)
	}
	var mods []string
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, "/modifications/") {
			mods = append(mods, r.Path)
			if r.Accept != mtMods {
				t.Errorf("accept %q, want %q", r.Accept, mtMods)
			}
		}
	}
	if !reflect.DeepEqual(mods, []string{"/scm/api/v2/repositories/acme/app/modifications/" + first, "/scm/api/v2/repositories/acme/app/modifications/" + second}) {
		t.Errorf("modification requests %v", mods)
	}
}

func TestPushFilesOffTheDefaultBranchAsksNothingMore(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	got, err := c.PushFiles(context.Background(), "acme/app", "feature", []string{f.Rev("acme/app", "main")})
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil", got, err)
	}
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, "/modifications/") {
			t.Errorf("asked %s for a push to another branch", r.Path)
		}
	}
}

func TestPushFilesRejectsBadIDsAndGivesUpOnHugePushes(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	if _, err := c.PushFiles(context.Background(), "acme/app", "main", []string{"../../etc"}); err == nil {
		t.Error("accepted a non-hex changeset id")
	}
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("%040x", i+1)
	}
	got, err := c.PushFiles(context.Background(), "acme/app", "main", ids)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil (a re-scan) for 101 changesets", got, err)
	}
	for _, r := range f.Requests() {
		if strings.Contains(r.Path, "/modifications/") {
			t.Fatal("asked for modifications of a huge push instead of re-scanning")
		}
	}
}

func TestPushFilesUnknownChangesetIsAnError(t *testing.T) {
	c, f := newClient(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	if _, err := c.PushFiles(context.Background(), "acme/app", "main", []string{strings.Repeat("a", 40)}); err == nil {
		t.Fatal("want an error for a changeset the server does not know")
	}
}

func TestClientIsNotAnAutoMerger(t *testing.T) {
	var p platform.Platform = &scmmanager.Client{}
	if _, ok := p.(platform.AutoMerger); ok {
		t.Fatal("SCM-Manager has no auto-merge (docs/vendor/scm-manager/README.md); the client must not claim one")
	}
}
