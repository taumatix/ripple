package bitbucket_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/bitbucket"
)

func newClient(f *fakeBB) *bitbucket.Client {
	return &bitbucket.Client{APIBase: f.APIURL, GitBase: f.GitBase, Workspaces: []string{"acme"},
		Credentials: platform.StaticCredential{Secret: f.Token}}
}

var appRepo = model.Repo{FullName: "acme/app", DefaultBranch: "main"}

func pushBranch(t *testing.T, f *fakeBB, repo, branch string) {
	t.Helper()
	git(t, f.bare(repo), "branch", branch, "main")
}

// Wire format of CreatePR: the swagger "pullrequest" body (vendored swagger-subset.json)
// and rest-intro.md (Bearer). Labels have no Bitbucket counterpart and are not sent.
func TestCreatePRSendsTheDocumentedBody(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	pushBranch(t, f, "acme/app", "ripple/go/x")
	c := newClient(f)
	pr, err := c.CreatePR(context.Background(), appRepo, platform.PRSpec{Head: "ripple/go/x", Base: "main", Title: "T", Body: "B", Draft: true, Labels: []string{"deps"}})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 1 || pr.Head != "ripple/go/x" || !strings.HasSuffix(pr.URL, "/acme/app/pull-requests/1") {
		t.Errorf("PR = %+v", pr)
	}
	var req fakeReq
	for _, r := range f.Requests() {
		if r.Method == "POST" {
			req = r
		}
	}
	if req.Path != "/2.0/repositories/acme/app/pullrequests" || req.Auth != "Bearer "+f.Token {
		t.Errorf("request %s auth %q", req.Path, req.Auth)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(req.Body), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"title": "T", "description": "B", "draft": true, "close_source_branch": true,
		"source":      map[string]any{"branch": map[string]any{"name": "ripple/go/x"}},
		"destination": map[string]any{"branch": map[string]any{"name": "main"}}}
	wb, _ := json.Marshal(want)
	gb, _ := json.Marshal(got)
	if string(wb) != string(gb) {
		t.Errorf("body\n got %s\nwant %s", gb, wb)
	}
	if p := f.PRs("acme/app"); len(p) != 1 || !p[0].Draft {
		t.Errorf("fake recorded %+v", p)
	}
}

func TestListOpenPRsFiltersByPrefixAcrossPages(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	c := newClient(f)
	for i := 0; i < 60; i++ {
		b := "feature/b" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		if i%2 == 0 {
			b = "ripple/go/p" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		}
		pushBranch(t, f, "acme/app", b)
		if _, err := c.CreatePR(context.Background(), appRepo, platform.PRSpec{Head: b, Base: "main", Title: b}); err != nil {
			t.Fatal(err)
		}
	}
	prs, err := c.ListOpenPRs(context.Background(), appRepo, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 30 {
		t.Fatalf("got %d PRs, want the 30 under ripple/ out of 60 over two pages", len(prs))
	}
	var sawQuery bool
	for _, r := range f.Requests() {
		if r.Method == "GET" && strings.Contains(r.Query, "state=OPEN") && strings.Contains(r.Query, "page=2") {
			sawQuery = true
		}
	}
	if !sawQuery {
		t.Error("page 2 of state=OPEN was never requested")
	}
}

// Auto-merge: merge-a-pull-request.md and the swagger say the merge call queues the PR
// when the destination branch has an active merge queue, and otherwise merges at once.
func TestAutoMergeQueuesOnlyWhereAMergeQueueAcceptsEntries(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	pushBranch(t, f, "acme/app", "ripple/go/x")
	c := newClient(f)
	pr, err := c.CreatePR(context.Background(), appRepo, platform.PRSpec{Head: "ripple/go/x", Base: "main", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequestAutoMerge(context.Background(), appRepo, pr); err != nil {
		t.Fatal(err)
	}
	p := f.PRs("acme/app")[0]
	if !p.Queued || p.MergeBody["merge_strategy"] != "squash" || p.MergeBody["_async"] != "true" || p.MergeBody["close_source_branch"] != true {
		t.Errorf("merge call = %+v queued=%v", p.MergeBody, p.Queued)
	}
	var q fakeReq
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/merge-queues") {
			q = r
		}
	}
	if !strings.Contains(q.Query, "target_branch=main") {
		t.Errorf("merge queue lookup query %q", q.Query)
	}
}

func TestAutoMergeWithoutAQueueNeverMergesImmediately(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"a": "1"})
	pushBranch(t, f, "acme/app", "ripple/go/x")
	f.noQueue["acme/app"] = true
	c := newClient(f)
	pr, _ := c.CreatePR(context.Background(), appRepo, platform.PRSpec{Head: "ripple/go/x", Base: "main", Title: "T"})
	err := c.RequestAutoMerge(context.Background(), appRepo, pr)
	if !errors.Is(err, platform.ErrNoAutoMerge) {
		t.Fatalf("err = %v, want ErrNoAutoMerge", err)
	}
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, "/merge") {
			t.Fatalf("the merge endpoint was called without a queue: it would merge at once, skipping checks")
		}
	}
	if p := f.PRs("acme/app")[0]; p.Queued || p.MergeBody != nil {
		t.Errorf("PR was touched: %+v", p)
	}
}

func TestAutoMergeIgnoresQueuesThatBlockEntry(t *testing.T) {
	for state, accepts := range map[string]bool{"ACTIVE": true, "PAUSED": true, "DRAINING": false, "SUSPENDED": false, "INACTIVE": false, "UNKNOWN": false} {
		var merged bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/merge-queues") {
				json.NewEncoder(w).Encode(map[string]any{"values": []any{map[string]any{"uuid": "{1}", "name": "q", "state": state}}})
				return
			}
			merged = true
			w.WriteHeader(202)
		}))
		c := &bitbucket.Client{APIBase: srv.URL, Credentials: platform.StaticCredential{Secret: "t"}}
		err := c.RequestAutoMerge(context.Background(), appRepo, platform.PR{Number: 1})
		srv.Close()
		if accepts != merged || accepts != (err == nil) || (!accepts && !errors.Is(err, platform.ErrNoAutoMerge)) {
			t.Errorf("queue %s: merged=%v err=%v, want accepts=%v", state, merged, err, accepts)
		}
	}
}

func TestAutoMergeReportsAnImmediateMerge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/merge-queues") {
			w.Write([]byte(`{"values":[{"uuid":"{1}","name":"q","state":"ACTIVE"}]}`))
			return
		}
		w.Write([]byte(`{}`)) // 200: merged, not queued
	}))
	defer srv.Close()
	c := &bitbucket.Client{APIBase: srv.URL, Credentials: platform.StaticCredential{Secret: "t"}}
	err := c.RequestAutoMerge(context.Background(), appRepo, platform.PR{Number: 1})
	if err == nil || errors.Is(err, platform.ErrNoAutoMerge) {
		t.Fatalf("err = %v, want a loud error: the PR merged without waiting for checks", err)
	}
}

func TestRateLimitUsesRetryAfterAndRemembersIt(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for header, want := range map[string]time.Duration{"120": 2 * time.Minute, "": time.Minute} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		c := &bitbucket.Client{APIBase: srv.URL, Credentials: platform.StaticCredential{Secret: "t"}, Now: func() time.Time { return now }}
		_, err := c.ListOpenPRs(context.Background(), appRepo, "ripple/")
		srv.Close()
		var rl *platform.RateLimitError
		if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(want)) {
			t.Errorf("Retry-After %q: err = %v, want until %s", header, err, now.Add(want))
		}
		if got := c.RateLimitedUntil(appRepo); !got.Equal(now.Add(want)) {
			t.Errorf("RateLimitedUntil = %s", got)
		}
	}
}

func TestListReposListsGitReposOfEveryWorkspaceAndSkipsOthers(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/one", map[string]string{"a": "1"})
	f.AddRepo("acme/two", map[string]string{"a": "1"})
	f.AddRepo("other/three", map[string]string{"a": "1"})
	c := newClient(f)
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].FullName != "acme/one" || repos[0].DefaultBranch != "main" || repos[1].FullName != "acme/two" {
		t.Errorf("repos = %+v", repos)
	}
	c.Workspaces = []string{"../x"}
	if _, err := c.ListRepos(context.Background()); err == nil {
		t.Error("unsafe workspace accepted")
	}
}

func TestListReposDoesNotFollowANextLinkToAnotherHost(t *testing.T) {
	var evil bool
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { evil = true }))
	defer trap.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Write([]byte(`{"values":[{"full_name":"acme/a","scm":"git"}],"next":"` + trap.URL + `/steal"}`))
			return
		}
		w.Write([]byte(`{"values":[{"full_name":"acme/b","scm":"git"}]}`))
	}))
	defer srv.Close()
	c := &bitbucket.Client{APIBase: srv.URL, Workspaces: []string{"acme"}, Credentials: platform.StaticCredential{Secret: "t"}}
	repos, err := c.ListRepos(context.Background())
	if err != nil || len(repos) != 2 || evil {
		t.Fatalf("repos %+v err %v, token sent to the next link's host: %v", repos, err, evil)
	}
}

func TestUnsafeRepositoryNamesAreRefused(t *testing.T) {
	c := &bitbucket.Client{APIBase: "http://127.0.0.1:1", GitBase: "http://127.0.0.1:1", Credentials: platform.StaticCredential{Secret: "t"}}
	for _, n := range []string{"", "acme", "../x/y", "acme/../y", "acme/app/extra", "ac me/app", "acme/app?x=1"} {
		r := model.Repo{FullName: n}
		if _, err := c.ListOpenPRs(context.Background(), r, ""); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("ListOpenPRs(%q): err = %v, want a refusal before any request", n, err)
		}
		if _, err := c.Checkout(context.Background(), r); err == nil {
			t.Errorf("Checkout(%q) accepted", n)
		}
	}
}

// Git carries the token as x-token-auth basic auth (api-tokens.md, oauth.md); the fake
// refuses any other username or password.
func TestCheckoutAuthenticatesWithXTokenAuth(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"hello.txt": "hi\n"})
	c := newClient(f)
	ws, err := c.Checkout(context.Background(), appRepo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if b, err := ws.ReadFile("hello.txt"); err != nil || string(b) != "hi\n" {
		t.Fatalf("read: %q %v", b, err)
	}
	bad := newClient(f)
	bad.Credentials = platform.StaticCredential{Secret: "wrong"}
	if w, err := bad.Checkout(context.Background(), appRepo); err == nil {
		w.Close()
		t.Fatal("checkout with a wrong token succeeded")
	} else if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestPushFilesAsksDiffstatPerCommitAndSkipsOtherBranches(t *testing.T) {
	f := newFakeBB(t)
	f.AddRepo("acme/app", map[string]string{"a.txt": "1"})
	f.Commit("acme/app", map[string]string{"go.mod": "module x\n", "sub/b.go": "package b\n"})
	head := strings.TrimSpace(git(t, f.bare("acme/app"), "rev-parse", "main"))
	c := newClient(f)
	if _, err := c.ListRepos(context.Background()); err != nil { // learns main is the default branch
		t.Fatal(err)
	}
	files, err := c.PushFiles(context.Background(), "acme/app", "main", []string{head})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] == files[1] {
		t.Errorf("files = %v, want go.mod and sub/b.go", files)
	}
	before := len(f.Requests())
	if files, err := c.PushFiles(context.Background(), "acme/app", "feature", []string{head}); err != nil || files != nil || len(f.Requests()) != before {
		t.Errorf("non-default branch: files %v err %v, %d extra requests; want none", files, err, len(f.Requests())-before)
	}
	if _, err := c.PushFiles(context.Background(), "acme/app", "main", []string{"../../x"}); err == nil {
		t.Error("a commit id that is not hex was put in a URL")
	}
}
