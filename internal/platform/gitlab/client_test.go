package gitlab_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/testutil"
)

type seen struct {
	Method, Path, Query, Auth, Body string
}

// recorder answers every request with status and body and remembers what it was sent,
// so a test can assert the exact bytes of the wire format against literals from the docs.
func recorder(t *testing.T, status int, reply string, hdr map[string]string) (*httptest.Server, func() []seen) {
	t.Helper()
	var mu sync.Mutex
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), string(b)})
		mu.Unlock()
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []seen { mu.Lock(); defer mu.Unlock(); return slices.Clone(got) }
}

func repo(p string) model.Repo { return model.Repo{FullName: p, DefaultBranch: "main"} }

// api_rest__index.md "Namespaced paths": a project path in a URL is URL-encoded, so
// `gitlab-org/gitlab` is `gitlab-org%2Fgitlab`; api_rest_authentication.md: OAuth and
// access tokens go in `Authorization: Bearer <token>`.
func TestCreatePRWireFormat(t *testing.T) {
	srv, got := recorder(t, 201, `{"iid":7,"web_url":"https://gitlab.example/g/s/app/-/merge_requests/7","source_branch":"ripple/go/x"}`, nil)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "glpat-abc"}}
	pr, err := c.CreatePR(context.Background(), repo("group/sub/app"), platform.PRSpec{Head: "ripple/go/x", Base: "main", Title: "bump x", Body: "details", Labels: []string{"deps", "go", "a,b"}, Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.Head != "ripple/go/x" || !strings.HasSuffix(pr.URL, "/merge_requests/7") {
		t.Errorf("pr = %+v", pr)
	}
	r := got()[0]
	if r.Method != "POST" || r.Path != "/api/v4/projects/group%2Fsub%2Fapp/merge_requests" {
		t.Errorf("request = %s %s", r.Method, r.Path)
	}
	if r.Auth != "Bearer glpat-abc" {
		t.Errorf("Authorization = %q", r.Auth)
	}
	// user_project_merge_requests_drafts.md: add `Draft:` to the beginning of the title;
	// api_merge_requests.md "Create MR": labels is a comma-separated list.
	var body map[string]string
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"source_branch": "ripple/go/x", "target_branch": "main", "title": "Draft: bump x", "description": "details", "labels": "deps,go"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %q, want %q", k, body[k], v)
		}
	}
}

// A real GitLab CE 18.x answers 400 {"message":{"source_branch":["does not exist"]}} for a branch
// pushed 1-2 s earlier (recorded by the hosts-gitlab-gerrit workflow); CreatePR asks again, and a
// branch that never shows up is still an error after the bounded retries.
func TestCreatePRRetriesABranchNotYetVisible(t *testing.T) {
	const lag = `{"message":{"source_branch":["does not exist"]}}`
	for _, tc := range []struct {
		name     string
		failures int
		wantErr  bool
		calls    int
	}{{"recovers", 2, false, 3}, {"never", 100, true, 6}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			n := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				n++
				fail := n <= tc.failures
				mu.Unlock()
				if fail {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(lag))
					return
				}
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"iid":3,"web_url":"https://gitlab.example/g/app/-/merge_requests/3"}`))
			}))
			defer srv.Close()
			var slept []time.Duration
			c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"},
				RetrySleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
			pr, err := c.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "ripple/x", Base: "main", Title: "t"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && pr.Number != 3 {
				t.Errorf("pr = %+v", pr)
			}
			if n != tc.calls {
				t.Errorf("%d calls, want %d", n, tc.calls)
			}
			if len(slept) != tc.calls-1 {
				t.Errorf("slept %v", slept)
			}
		})
	}
	// Another 400 is not retried.
	srv, got := recorder(t, 400, `{"message":{"title":["can't be blank"]}}`, nil)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"},
		RetrySleep: func(context.Context, time.Duration) error { t.Error("slept"); return nil }}
	if _, err := c.CreatePR(context.Background(), repo("g/app"), platform.PRSpec{Head: "h", Base: "main"}); err == nil || len(got()) != 1 {
		t.Errorf("err = %v after %d calls", err, len(got()))
	}
}

// api_merge_requests.md "Merge a merge request": PUT /projects/:id/merge_requests/:merge_request_iid/merge
// with `auto_merge` (current) and `merge_when_pipeline_succeeds` (deprecated name).
func TestRequestAutoMergeWireFormat(t *testing.T) {
	srv, got := recorder(t, 200, `{}`, nil)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"}}
	if err := c.RequestAutoMerge(context.Background(), repo("g/app"), platform.PR{Number: 12}); err != nil {
		t.Fatal(err)
	}
	r := got()[0]
	if r.Method != "PUT" || r.Path != "/api/v4/projects/g%2Fapp/merge_requests/12/merge" {
		t.Errorf("request = %s %s", r.Method, r.Path)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(r.Body), &body)
	if body["auto_merge"] != true || body["merge_when_pipeline_succeeds"] != true {
		t.Errorf("body = %s", r.Body)
	}
	if err := c.RequestAutoMerge(context.Background(), repo("g/app"), platform.PR{}); err == nil {
		t.Error("a merge request with no iid was accepted")
	}
}

// api_merge_requests.md "List project merge requests": state=opened, paging with
// per_page and page; the list has no source-branch prefix filter, so ripple filters.
func TestListOpenPRsFiltersAndPages(t *testing.T) {
	var mu sync.Mutex
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pages = append(pages, r.URL.RawQuery)
		mu.Unlock()
		var out []map[string]any
		if r.URL.Query().Get("page") == "1" {
			for i := 1; i <= 100; i++ {
				b := "feature/x"
				if i%2 == 0 {
					b = "ripple/go/m"
				}
				out = append(out, map[string]any{"iid": i, "source_branch": b, "web_url": "u"})
			}
		} else {
			out = append(out, map[string]any{"iid": 101, "source_branch": "ripple/go/last", "web_url": "u"})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"}}
	prs, err := c.ListOpenPRs(context.Background(), repo("g/app"), "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 51 {
		t.Errorf("got %d PRs, want 50 on page 1 and 1 on page 2", len(prs))
	}
	want := []string{"state=opened&per_page=100&page=1", "state=opened&per_page=100&page=2"}
	if !slices.Equal(pages, want) {
		t.Errorf("queries = %q, want %q", pages, want)
	}
}

// administration_settings_user_and_ip_rate_limits.md: a limited request gets 429 with
// `Retry-After: 30` (seconds).
func TestRateLimitRetryAfter(t *testing.T) {
	srv, _ := recorder(t, 429, `{"message":"Retry later"}`, map[string]string{"Retry-After": "30"})
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"}, Now: func() time.Time { return now }}
	_, err := c.ListOpenPRs(context.Background(), repo("g/app"), "ripple/")
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(30*time.Second)) {
		t.Fatalf("err = %v", err)
	}
	if got := c.RateLimitedUntil(repo("g/app")); !got.Equal(now.Add(30 * time.Second)) {
		t.Errorf("RateLimitedUntil = %v", got)
	}
}

// Paths come from webhook payloads and listings; none may reach a URL unchecked.
func TestUnsafePathsAreRefused(t *testing.T) {
	srv, got := recorder(t, 200, `[]`, nil)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"}}
	for _, p := range []string{"", "app", "g/../x", "g//app", "g/app?x=1", "g/app#f", "g/%2e%2e/app", "g/app.git", ".hidden/app", "g/ap p", "g/app\n"} {
		if _, err := c.ListOpenPRs(context.Background(), repo(p), "ripple/"); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	if n := len(got()); n != 0 {
		t.Errorf("%d requests sent for unsafe paths", n)
	}
	for _, p := range []string{"g/app", "a/b/c/d/app", "g/my.app-2_x"} {
		if err := gitlab.CheckPath(p); err != nil {
			t.Errorf("path %q refused: %v", p, err)
		}
	}
	if err := gitlab.CheckPath(strings.Repeat("g/", 21) + "app"); err == nil {
		t.Error("a path nested deeper than GitLab's 20 subgroup levels was accepted")
	}
}

// Tokens are per project or group: each reaches only what it was issued for
// (user_project_settings_project_access_tokens.md), the longest scope wins, and one
// token's rate limit holds back only its own repos.
func TestScopedTokensAgainstTheFake(t *testing.T) {
	gl := testutil.NewGitLab(t)
	gl.Token("glpat-group", "team")
	gl.Token("glpat-project", "team/deep/app")
	gl.Token("glpat-default", "other")
	for _, p := range []string{"team/api", "team/deep/app", "team/deep/lib", "other/x"} {
		gl.AddRepo(p, map[string]string{"README": "x"})
		gl.Member(p)
	}
	c := &gitlab.Client{BaseURL: gl.APIURL, GitBase: gl.GitBase, Tokens: gitlab.Tokens{
		Default: "glpat-default", Scoped: map[string]string{"team": "glpat-group", "team/deep/app": "glpat-project"}},
		Now: time.Now}
	ctx := context.Background()

	repos, err := c.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	slices.Sort(names)
	if want := []string{"other/x", "team/api", "team/deep/app", "team/deep/lib"}; !slices.Equal(names, want) {
		t.Errorf("repos = %v, want %v (union over tokens, no duplicates)", names, want)
	}

	// Each repo is reached with the token that applies; the fake refuses any other.
	for _, p := range names {
		if _, err := c.ListOpenPRs(ctx, repo(p), "ripple/"); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	wantAuth := map[string]string{"team/api": "Bearer glpat-group", "team/deep/app": "Bearer glpat-project", "team/deep/lib": "Bearer glpat-group", "other/x": "Bearer glpat-default"}
	reqs, auths := gl.Requests(), gl.Auths()
	for i, rq := range reqs {
		for p, a := range wantAuth {
			if strings.Contains(rq, "/merge_requests") && strings.Contains(rq, strings.ReplaceAll(p, "/", "%2F")+"/") && auths[i] != a {
				t.Errorf("%s used %q, want %q", p, auths[i], a)
			}
		}
	}

	// Rate limit one token: its repos are held back, the others are not.
	gl.RateLimitToken("glpat-project")
	if _, err := c.CreatePR(ctx, repo("team/deep/app"), platform.PRSpec{Head: "x", Base: "main", Title: "t"}); err == nil {
		t.Fatal("expected a rate limit")
	}
	if c.RateLimitedUntil(repo("team/deep/app")).IsZero() {
		t.Error("the limited token's repo is not held back")
	}
	for _, p := range []string{"team/api", "team/deep/lib", "other/x"} {
		if !c.RateLimitedUntil(repo(p)).IsZero() {
			t.Errorf("%s held back by another token's limit", p)
		}
	}

	// A project no token covers is an error before any request.
	noTok := &gitlab.Client{BaseURL: gl.APIURL, Tokens: gitlab.Tokens{Scoped: map[string]string{"team": "glpat-group"}}}
	before := len(gl.Requests())
	if _, err := noTok.ListOpenPRs(ctx, repo("other/x"), "r"); err == nil || len(gl.Requests()) != before {
		t.Errorf("uncovered project: err = %v, requests sent = %d", err, len(gl.Requests())-before)
	}
	// A prefix must match at a path-segment boundary: scope "team" does not cover "teammate/x".
	if _, _, err := tokenFor(noTok, "teammate/x"); err == nil {
		t.Error("scope team covered teammate/x")
	}
}

func tokenFor(c *gitlab.Client, p string) (platform.Credential, bool, error) {
	cr, err := c.Tokens.Credential(context.Background(), repo(p))
	return cr, err == nil, err
}

// Listing pages through the whole membership (api_rest__index.md "Pagination").
func TestListReposPages(t *testing.T) {
	gl := testutil.NewGitLab(t)
	gl.Token("t", "")
	for i := 0; i < 205; i++ {
		gl.Member(fmt.Sprintf("big/p%03d", i))
	}
	c := &gitlab.Client{BaseURL: gl.APIURL, Tokens: gitlab.Tokens{Default: "t"}}
	repos, err := c.ListRepos(context.Background())
	if err != nil || len(repos) != 205 {
		t.Fatalf("repos = %d, err = %v", len(repos), err)
	}
	// api_projects.md "List all projects": membership, archived and simple filters.
	if q := gl.Requests()[0]; !strings.Contains(q, "membership=true") || !strings.Contains(q, "archived=false") {
		t.Errorf("first request = %s", q)
	}
}

// A project token is the git password, with any non-empty user
// (user_project_settings_project_access_tokens.md).
func TestCredentialIsOAuth2UserAndToken(t *testing.T) {
	tk := gitlab.Tokens{Default: "glpat-d", Scoped: map[string]string{"g/sub": "glpat-s"}}
	got, err := tk.Credential(context.Background(), repo("g/sub/app"))
	if err != nil || got.Username != "oauth2" || got.Secret != "glpat-s" {
		t.Errorf("credential = %+v, err %v", got, err)
	}
}

// The real 429 of GitLab CE 19.6.2 (throttle_authenticated_api, recorded 2026-11-27): text/plain
// "Retry later", Retry-After in seconds, plus RateLimit-* headers. The hold-back follows Retry-After.
func TestRealThrottle429(t *testing.T) {
	srv, _ := recorder(t, 429, "Retry later", map[string]string{
		"Content-Type": "text/plain", "Ratelimit-Limit": "30", "Ratelimit-Name": "throttle_authenticated_api",
		"Ratelimit-Observed": "6", "Ratelimit-Remaining": "0", "Ratelimit-Reset": "1795790380",
		"Ratelimit-Resettime": "Fri, 27 Nov 2026 14:39:40 GMT", "Retry-After": "2"})
	now := time.Unix(1795790378, 0)
	c := &gitlab.Client{BaseURL: srv.URL, Tokens: gitlab.Tokens{Default: "t"}, Now: func() time.Time { return now }}
	_, err := c.ListRepos(context.Background())
	var rl *platform.RateLimitError
	if !errors.As(err, &rl) || !rl.Until.Equal(now.Add(2*time.Second)) {
		t.Fatalf("err = %v", err)
	}
}
