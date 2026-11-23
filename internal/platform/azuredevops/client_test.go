package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

type rec struct {
	Method, URI, Auth, Body string
}

// server records every request and answers from handler.
func server(t *testing.T, handler func(r *http.Request, w http.ResponseWriter)) (*Client, func() []rec) {
	t.Helper()
	var mu sync.Mutex
	var got []rec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, rec{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), string(b)})
		mu.Unlock()
		handler(r, w)
	}))
	t.Cleanup(srv.Close)
	c := &Client{APIBase: srv.URL, GitBase: srv.URL, Org: "fabrikam", Credentials: platform.StaticCredential{Secret: "pat123"},
		Now: func() time.Time { return time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC) }}
	return c, func() []rec { mu.Lock(); defer mu.Unlock(); return append([]rec(nil), got...) }
}

var repo = model.Repo{FullName: "fabrikam/Fiber/web", DefaultBranch: "main"}

// Basic base64(":pat123") = "OnBhdDEyMw==": use-personal-access-tokens.md (empty user name, PAT as password).
const patAuth = "Basic OnBhdDEyMw=="

func reply(code int, body string) func(*http.Request, http.ResponseWriter) {
	return func(_ *http.Request, w http.ResponseWriter) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// rest-pull-requests-create.md: POST .../pullrequests?api-version=7.1 with
// sourceRefName, targetRefName (full ref names), title, description, isDraft.
func TestCreatePRWireFormat(t *testing.T) {
	c, got := server(t, reply(201, `{"pullRequestId": 22, "sourceRefName": "refs/heads/ripple/x", "createdBy": {"id": "user-1"}}`))
	pr, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: "ripple/x", Base: "main", Title: "Bump", Body: "body", Draft: true, Labels: []string{"deps"}})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 22 || pr.Head != "ripple/x" || pr.ID != "user-1" || !strings.HasSuffix(pr.URL, "/fabrikam/Fiber/_git/web/pullrequest/22") {
		t.Errorf("pr = %+v", pr)
	}
	rs := got()
	if len(rs) != 2 {
		t.Fatalf("want create + label, got %+v", rs)
	}
	if rs[0].Method != "POST" || rs[0].URI != "/fabrikam/Fiber/_apis/git/repositories/web/pullrequests?api-version=7.1" || rs[0].Auth != patAuth {
		t.Errorf("create request = %+v", rs[0])
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rs[0].Body), &body)
	want := map[string]any{"sourceRefName": "refs/heads/ripple/x", "targetRefName": "refs/heads/main", "title": "Bump", "description": "body", "isDraft": true}
	if len(body) != len(want) {
		t.Errorf("body = %v", body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
	// rest-labels-create.md: POST .../pullrequests/{id}/labels, body {"name": ...}.
	if rs[1].Method != "POST" || rs[1].URI != "/fabrikam/Fiber/_apis/git/repositories/web/pullrequests/22/labels?api-version=7.1" || rs[1].Body != `{"name":"deps"}` {
		t.Errorf("label request = %+v", rs[1])
	}
}

func TestCreatePRDoesNotSendDraftWhenNotDraftAndTruncatesDescription(t *testing.T) {
	c, got := server(t, reply(201, `{"pullRequestId": 1}`))
	if _, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: "h", Base: "main", Title: "t", Body: strings.Repeat("x", 5000)}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(got()[0].Body), &body)
	if _, ok := body["isDraft"]; ok {
		t.Error("isDraft sent for a non-draft")
	}
	if n := len(body["description"].(string)); n != maxDescription {
		t.Errorf("description length %d, want %d", n, maxDescription)
	}
}

// rest-pull-requests-get-list.md: searchCriteria.status=active, $top/$skip.
func TestListOpenPRsWireFormatAndPrefixFilter(t *testing.T) {
	c, got := server(t, reply(200, `{"count":2,"value":[
		{"pullRequestId": 5, "sourceRefName": "refs/heads/ripple/a", "createdBy": {"id": "u"}},
		{"pullRequestId": 6, "sourceRefName": "refs/heads/feature/b", "createdBy": {"id": "u"}}]}`))
	prs, err := c.ListOpenPRs(context.Background(), repo, "ripple/")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 5 || prs[0].Head != "ripple/a" {
		t.Errorf("prs = %+v", prs)
	}
	if u := got()[0].URI; u != "/fabrikam/Fiber/_apis/git/repositories/web/pullrequests?searchCriteria.status=active&$top=100&$skip=0&api-version=7.1" {
		t.Errorf("uri = %s", u)
	}
}

// rest-pull-requests-update.md: PATCH with autoCompleteSetBy.id and completionOptions.mergeStrategy.
func TestRequestAutoMergeWireFormat(t *testing.T) {
	c, got := server(t, reply(200, `{}`))
	if err := c.RequestAutoMerge(context.Background(), repo, platform.PR{Number: 22, ID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	r := got()[0]
	if r.Method != "PATCH" || r.URI != "/fabrikam/Fiber/_apis/git/repositories/web/pullrequests/22?api-version=7.1" {
		t.Errorf("request = %+v", r)
	}
	if r.Body != `{"autoCompleteSetBy":{"id":"user-1"},"completionOptions":{"deleteSourceBranch":true,"mergeStrategy":"squash"}}` {
		t.Errorf("body = %s", r.Body)
	}
	if err := c.RequestAutoMerge(context.Background(), repo, platform.PR{Number: 22}); err == nil {
		t.Error("auto-complete without a creator identity should fail")
	}
}

// rate-limits.md: 429 with Retry-After is a rate limit; TF400733 is its message.
func TestRateLimited429(t *testing.T) {
	c, _ := server(t, func(_ *http.Request, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
		io.WriteString(w, "TF400733: The request has been canceled")
	})
	_, err := c.CreatePR(context.Background(), repo, platform.PRSpec{Head: "h", Base: "main", Title: "t"})
	var rl *platform.RateLimitError
	want := c.now().Add(30 * time.Second)
	if !errors.As(err, &rl) || !rl.Until.Equal(want) {
		t.Fatalf("err = %v, want rate limit until %v", err, want)
	}
	if got := c.RateLimitedUntil(repo); !got.Equal(want) {
		t.Errorf("RateLimitedUntil = %v", got)
	}
}

// rate-limits.md: "If you receive [Retry-After] ... The response still returns HTTP 200."
func TestRetryAfterOnA200IsHonoured(t *testing.T) {
	c, _ := server(t, func(_ *http.Request, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "60")
		io.WriteString(w, `{"value":[]}`)
	})
	if _, err := c.ListOpenPRs(context.Background(), repo, "ripple/"); err != nil {
		t.Fatal(err)
	}
	if got, want := c.RateLimitedUntil(repo), c.now().Add(time.Minute); !got.Equal(want) {
		t.Errorf("RateLimitedUntil = %v, want %v", got, want)
	}
}

func TestOtherErrorsAreNotRateLimits(t *testing.T) {
	c, _ := server(t, reply(401, "TF400813"))
	_, err := c.ListOpenPRs(context.Background(), repo, "ripple/")
	var rl *platform.RateLimitError
	if err == nil || errors.As(err, &rl) {
		t.Fatalf("err = %v", err)
	}
	if !c.RateLimitedUntil(repo).IsZero() {
		t.Error("a 401 must not hold the host back")
	}
}

// rest-projects-list.md and rest-repositories-list.md.
func TestListRepos(t *testing.T) {
	c, got := server(t, func(r *http.Request, w http.ResponseWriter) {
		switch r.URL.Path {
		case "/fabrikam/_apis/projects":
			io.WriteString(w, `{"count":2,"value":[{"name":"Fiber"},{"name":"Has Space"}]}`)
		case "/fabrikam/Fiber/_apis/git/repositories":
			io.WriteString(w, `{"count":3,"value":[
				{"name":"web","defaultBranch":"refs/heads/main","isDisabled":false},
				{"name":"old","defaultBranch":"refs/heads/main","isDisabled":true},
				{"name":"empty"}]}`)
		default:
			w.WriteHeader(404)
		}
	})
	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].FullName != "fabrikam/Fiber/web" || repos[0].DefaultBranch != "main" {
		t.Errorf("repos = %+v", repos)
	}
	if u := got()[0].URI; u != "/fabrikam/_apis/projects?$top=100&$skip=0&api-version=7.1" {
		t.Errorf("projects uri = %s", u)
	}
}

func TestUnsafeRepoNamesAreRefusedBeforeAnyRequest(t *testing.T) {
	c, got := server(t, reply(200, `{}`))
	for _, name := range []string{"", "a/b", "a/b/c/d", "a/../c", "a/b/..", "a/b/c d", "a/b/c?x=1", "a/b/c#", "a/b/%2e%2e", "a//c", "-x/b/c/"} {
		if _, err := c.ListOpenPRs(context.Background(), model.Repo{FullName: name}, "ripple/"); err == nil {
			t.Errorf("%q accepted", name)
		}
		if _, err := c.Checkout(context.Background(), model.Repo{FullName: name}); err == nil {
			t.Errorf("checkout %q accepted", name)
		}
	}
	if n := len(got()); n != 0 {
		t.Errorf("%d requests sent for unsafe names", n)
	}
}

const sha1 = "1111111111111111111111111111111111111111"
const sha2 = "2222222222222222222222222222222222222222"

// rest-commits-get-changes.md: GET .../repositories/{repo}/commits/{commitId}/changes?api-version=7.1;
// items carry a leading-slash path, folders are items with isFolder, a rename has originalPath.
func TestChangedFilesWireFormat(t *testing.T) {
	c, reqs := server(t, func(r *http.Request, w http.ResponseWriter) {
		if strings.Contains(r.URL.Path, sha1) {
			io.WriteString(w, `{"changeCounts":{"Add":1,"Edit":1},"changes":[{"item":{"path":"/go.mod"},"changeType":"edit"},{"item":{"path":"/src","isFolder":true},"changeType":"edit"},{"item":{"path":"/src/a.go"},"changeType":"add"}]}`)
			return
		}
		io.WriteString(w, `{"changeCounts":{"Rename":1},"changes":[{"item":{"path":"/new.txt"},"originalPath":"/old.txt","changeType":"rename"},{"item":{"path":"/go.mod"},"changeType":"edit"}]}`)
	})
	got, err := c.ChangedFiles(context.Background(), "fabrikam/Fiber/web", []string{sha1, sha2})
	if err != nil {
		t.Fatal(err)
	}
	if want := "go.mod,src/a.go,new.txt,old.txt"; strings.Join(got, ",") != want {
		t.Errorf("files = %v, want %s", got, want)
	}
	r := reqs()
	if len(r) != 2 || r[0].Method != "GET" || r[0].Auth != patAuth ||
		r[0].URI != "/fabrikam/Fiber/_apis/git/repositories/web/commits/"+sha1+"/changes?api-version=7.1" {
		t.Errorf("requests = %+v", r)
	}
}

// A truncated answer, an unknown commit or a huge push must not yield a partial file list.
func TestChangedFilesRefusesIncompleteAnswers(t *testing.T) {
	c, _ := server(t, reply(200, `{"changeCounts":{"Edit":2},"changes":[{"item":{"path":"/a"},"changeType":"edit"}]}`))
	if _, err := c.ChangedFiles(context.Background(), "fabrikam/Fiber/web", []string{sha1}); err == nil {
		t.Error("truncated changes accepted")
	}
	if _, err := c.ChangedFiles(context.Background(), "fabrikam/Fiber/web", nil); err == nil {
		t.Error("no commits accepted")
	}
	if _, err := c.ChangedFiles(context.Background(), "fabrikam/Fiber/web", make([]string, maxPushCommits+1)); err == nil {
		t.Error("oversized push accepted")
	}
	if _, err := c.ChangedFiles(context.Background(), "fabrikam/Fiber/web", []string{"../x"}); err == nil {
		t.Error("bad commit id accepted")
	}
}
