package bitbucket_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeBB is a fake Bitbucket Cloud: the slice of REST 2.0 ripple uses, and bare git
// repositories served over smart HTTP with Basic auth (user x-token-auth). It is
// written from the vendored docs (docs/vendor/bitbucket-cloud), not recorded from
// the real service.
type fakeBB struct {
	t    *testing.T
	root string
	// APIURL is the REST base ("<server>/2.0"); GitBase the clone URL prefix.
	APIURL, GitBase string
	// Token is the only credential the fake accepts: Bearer on the API (rest-intro,
	// api-tokens "Bearer authentication"), x-token-auth:Token over git (api-tokens).
	Token string

	mu        sync.Mutex
	calls     map[string]int
	repos     map[string]bool // "workspace/slug" -> exists
	prs       map[string][]*fakePR
	noQueue   map[string]bool // repos whose main branch has no merge queue
	rateLimit bool
	gitReject int
	requests  []fakeReq
	// failures make a request fail: "METHOD path-suffix" -> status.
	failures map[string]int
}

type fakePR struct {
	ID                int
	Title, Desc       string
	Head, Base        string
	Draft, Queued     bool
	CloseSourceBranch bool
	MergeBody         map[string]any
	// State is OPEN until the PR is declined. Comments are every comment on it, by a
	// person or by ripple; Activity is its activity log (comment, update, approval).
	State    string
	Comments []string
	Activity []map[string]any
}

type fakeReq struct{ Method, Path, Query, Auth, Body string }

func newFakeBB(t *testing.T) *fakeBB {
	t.Helper()
	f := &fakeBB{t: t, root: t.TempDir(), Token: "tok-123", calls: map[string]int{}, repos: map[string]bool{},
		prs: map[string][]*fakePR{}, failures: map[string]int{}, noQueue: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.APIURL = srv.URL + "/2.0"
	f.GitBase = srv.URL + "/git"
	return f
}

func gitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitAs runs git as the given identity (the environment of git() would override -c).
func gitAs(t *testing.T, dir, name, email string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (f *fakeBB) bare(repo string) string { return filepath.Join(f.root, repo+".git") }

func (f *fakeBB) AddRepo(repo string, files map[string]string) {
	f.t.Helper()
	if err := os.MkdirAll(f.bare(repo), 0o755); err != nil {
		f.t.Fatal(err)
	}
	git(f.t, f.bare(repo), "init", "--bare", "-b", "main")
	git(f.t, f.bare(repo), "config", "uploadpack.allowFilter", "true")
	f.mu.Lock()
	f.repos[repo] = true
	f.mu.Unlock()
	f.Commit(repo, files)
}

func (f *fakeBB) Commit(repo string, files map[string]string) {
	f.t.Helper()
	work := f.t.TempDir()
	git(f.t, work, "clone", "-q", f.bare(repo), "w")
	w := filepath.Join(work, "w")
	git(f.t, w, "checkout", "-q", "-B", "main")
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	git(f.t, w, "add", "-A")
	git(f.t, w, "commit", "-q", "-m", "test commit")
	git(f.t, w, "push", "-q", "origin", "main")
}

func (f *fakeBB) FileAt(repo, branch, path string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	return string(out), err == nil
}

func (f *fakeBB) Calls(repo string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[repo] }

func (f *fakeBB) PRs(repo string) []fakePR {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakePR
	for _, p := range f.prs[repo] {
		out = append(out, *p)
	}
	return out
}

// Requests is every API request seen, in order.
func (f *fakeBB) Requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeReq(nil), f.requests...)
}

func (f *fakeBB) authOK(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+f.Token
}

func (f *fakeBB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/git/"):
		f.serveGit(w, r)
	case strings.HasPrefix(r.URL.Path, "/2.0/"):
		f.serveAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeBB) serveGit(w http.ResponseWriter, r *http.Request) {
	user, pass, _ := r.BasicAuth()
	if user != "x-token-auth" || pass != f.Token {
		f.mu.Lock()
		f.gitReject++
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="Bitbucket"`)
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	backend, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h := &cgi.Handler{Path: strings.TrimSpace(string(backend)) + "/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + f.root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"}}
	if r.ContentLength < 0 { // CGI cannot take a chunked body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		r.Body, r.ContentLength, r.TransferEncoding = io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
		r.Header.Del("Transfer-Encoding")
	}
	http.StripPrefix("/git", h).ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeBB) serveAPI(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	// /2.0/repositories/{workspace}[/{slug}/...]
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/2.0/"), "/")
	repo := ""
	if len(parts) >= 3 && parts[0] == "repositories" {
		repo = parts[1] + "/" + parts[2]
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)})
	if repo != "" {
		f.calls[repo]++
	}
	f.mu.Unlock()
	if !f.authOK(r) {
		writeJSON(w, 401, map[string]any{"type": "error", "error": map[string]any{"message": "bad credentials"}})
		return
	}
	f.mu.Lock()
	for k, status := range f.failures {
		if m, suffix, _ := strings.Cut(k, " "); m == r.Method && strings.HasSuffix(r.URL.Path, suffix) {
			f.mu.Unlock()
			writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"message": "injected failure"}})
			return
		}
	}
	f.mu.Unlock()
	switch {
	case len(parts) == 6 && parts[3] == "pullrequests" && parts[5] == "decline" && r.Method == http.MethodPost:
		f.declinePR(w, repo, parts[4])
	case len(parts) == 6 && parts[3] == "pullrequests" && parts[5] == "comments":
		f.prComments(w, r, repo, parts[4], body)
	case len(parts) == 6 && parts[3] == "pullrequests" && parts[5] == "activity" && r.Method == http.MethodGet:
		f.prActivity(w, r, repo, parts[4])
	case len(parts) == 6 && parts[3] == "pullrequests" && parts[5] == "commits" && r.Method == http.MethodGet:
		f.prCommits(w, r, repo, parts[4])
	case len(parts) == 5 && parts[3] == "pullrequests" && r.Method == http.MethodGet:
		f.getPR(w, repo, parts[4])
	case len(parts) == 2 && parts[0] == "repositories" && r.Method == http.MethodGet:
		f.listRepos(w, r, parts[1])
	case len(parts) == 4 && parts[3] == "pullrequests" && r.Method == http.MethodGet:
		f.listPRs(w, r, repo)
	case len(parts) == 4 && parts[3] == "pullrequests" && r.Method == http.MethodPost:
		f.createPR(w, repo, body)
	case len(parts) == 5 && parts[3] == "diffstat" && r.Method == http.MethodGet:
		f.diffstat(w, r, repo, parts[4])
	case len(parts) == 4 && parts[3] == "merge-queues" && r.Method == http.MethodGet:
		f.listQueues(w, r, repo)
	case len(parts) == 6 && parts[3] == "pullrequests" && parts[5] == "merge" && r.Method == http.MethodPost:
		f.mergePR(w, r, repo, parts[4], body)
	default:
		writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such resource " + r.URL.Path}})
	}
}

// paginate follows the documented envelope: pagelen, page, next (rest-intro, "Pagination").
func (f *fakeBB) paginate(w http.ResponseWriter, r *http.Request, items []any) {
	pagelen, _ := strconv.Atoi(r.URL.Query().Get("pagelen"))
	if pagelen <= 0 {
		pagelen = 10
	}
	pagelen = min(pagelen, 100)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	lo := min((page-1)*pagelen, len(items))
	hi := min(lo+pagelen, len(items))
	out := map[string]any{"pagelen": pagelen, "page": page, "values": items[lo:hi]}
	if hi < len(items) {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(page+1))
		out["next"] = "http://" + r.Host + r.URL.Path + "?" + q.Encode()
	}
	writeJSON(w, 200, out)
}

func (f *fakeBB) listRepos(w http.ResponseWriter, r *http.Request, ws string) {
	f.mu.Lock()
	var names []string
	for n := range f.repos {
		if strings.HasPrefix(n, ws+"/") {
			names = append(names, n)
		}
	}
	f.mu.Unlock()
	sort.Strings(names)
	var items []any
	for _, n := range names {
		items = append(items, map[string]any{"type": "repository", "full_name": n, "scm": "git", "mainbranch": map[string]any{"type": "branch", "name": "main"}})
	}
	f.paginate(w, r, items)
}

// BranchHead is the commit branch points at in repo, "" when it does not exist.
func (f *fakeBB) BranchHead(repo, branch string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func (f *fakeBB) prJSON(repo string, p *fakePR) map[string]any {
	src := map[string]any{"branch": map[string]any{"name": p.Head}, "repository": map[string]any{"type": "repository", "full_name": repo}}
	if sha := f.BranchHead(repo, p.Head); sha != "" {
		src["commit"] = map[string]any{"hash": sha} // source.commit.hash, pullrequests-api.md
	}
	return map[string]any{"type": "pullrequest", "id": p.ID, "title": p.Title, "description": p.Desc, "state": p.state(), "draft": p.Draft, "queued": p.Queued,
		"comment_count": len(p.Comments), "task_count": 0,
		"links":       map[string]any{"html": map[string]any{"href": fmt.Sprintf("https://bitbucket.example/%s/pull-requests/%d", repo, p.ID)}},
		"source":      src,
		"destination": map[string]any{"branch": map[string]any{"name": p.Base}}}
}

func (f *fakeBB) listPRs(w http.ResponseWriter, r *http.Request, repo string) {
	f.mu.Lock()
	var items []any
	for _, p := range f.prs[repo] {
		// The list defaults to OPEN pull requests (pullrequests-api.md, state filter).
		if want := r.URL.Query().Get("state"); p.state() == want || want == "" && p.state() == "OPEN" {
			items = append(items, f.prJSON(repo, p))
		}
	}
	f.mu.Unlock()
	f.paginate(w, r, items)
}

func (f *fakeBB) createPR(w http.ResponseWriter, repo string, body []byte) {
	f.mu.Lock()
	if f.rateLimit {
		f.rateLimit = false
		f.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		writeJSON(w, 429, map[string]any{"type": "error", "error": map[string]any{"message": "Rate limit for this resource has been exceeded"}})
		return
	}
	defer f.mu.Unlock()
	var in struct {
		Title, Description string
		Draft              bool
		CloseSourceBranch  bool `json:"close_source_branch"`
		Source             struct{ Branch struct{ Name string } }
		Destination        struct{ Branch struct{ Name string } }
	}
	if err := json.Unmarshal(body, &in); err != nil || in.Source.Branch.Name == "" {
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"message": "You must specify a valid source branch when creating a pull request."}})
		return
	}
	if !f.hasBranch(repo, in.Source.Branch.Name) {
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"message": "source branch not found"}})
		return
	}
	base := in.Destination.Branch.Name
	if base == "" {
		base = "main"
	}
	p := &fakePR{ID: len(f.prs[repo]) + 1, Title: in.Title, Desc: in.Description, Head: in.Source.Branch.Name, Base: base,
		Draft: in.Draft, CloseSourceBranch: in.CloseSourceBranch}
	f.prs[repo] = append(f.prs[repo], p)
	p.Activity = append(p.Activity, f.updateEntry(repo, p))
	writeJSON(w, 201, f.prJSON(repo, p))
}

func (f *fakeBB) hasBranch(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

func (f *fakeBB) listQueues(w http.ResponseWriter, r *http.Request, repo string) {
	f.mu.Lock()
	off := f.noQueue[repo]
	f.mu.Unlock()
	var items []any
	if !off && r.URL.Query().Get("target_branch") == "main" {
		items = append(items, map[string]any{"type": "merge_queue", "uuid": "{11111111-2222-3333-4444-555555555555}", "name": "mergequeue-main", "state": "ACTIVE"})
	}
	f.paginate(w, r, items)
}

func (f *fakeBB) mergePR(w http.ResponseWriter, r *http.Request, repo, id string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	for _, p := range f.prs[repo] {
		if p.ID != n {
			continue
		}
		if f.noQueue[repo] {
			// Without a queue the real service would merge now; the fake makes that visible.
			p.MergeBody = map[string]any{"merged_immediately": true}
			writeJSON(w, 200, f.prJSON(repo, p))
			return
		}
		_ = json.Unmarshal(body, &p.MergeBody)
		p.MergeBody["_async"] = r.URL.Query().Get("async")
		p.Queued = true
		w.Header().Set("Location", f.APIURL+"/repositories/"+repo+"/pullrequests/"+id+"/merge/task-status/1")
		w.WriteHeader(202)
		return
	}
	writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such pull request"}})
}

// diffstat is "Compare two commit diff stats" for a single commit spec: the diff
// against the commit's first parent (swagger description of /diffstat/{spec}).
func (f *fakeBB) diffstat(w http.ResponseWriter, r *http.Request, repo, spec string) {
	cmd := exec.Command("git", "diff-tree", "-r", "--root", "--no-commit-id", "--name-status", spec)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such commit"}})
		return
	}
	var items []any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		st, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		status := map[string]string{"A": "added", "D": "removed", "M": "modified"}[st]
		d := map[string]any{"type": "diffstat", "status": status}
		file := map[string]any{"path": path, "type": "commit_file"}
		if status != "added" {
			d["old"] = file
		}
		if status != "removed" {
			d["new"] = file
		}
		items = append(items, d)
	}
	f.paginate(w, r, items)
}

func (p *fakePR) state() string {
	if p.State == "" {
		return "OPEN"
	}
	return p.State
}

// updateEntry is an activity "update" as in the vendored example: the title,
// description and source commit the pull request had at that moment. The service is
// assumed to log one when the pull request is created; the docs do not say so.
func (f *fakeBB) updateEntry(repo string, p *fakePR) map[string]any {
	cmd := exec.Command("git", "rev-parse", "refs/heads/"+p.Head)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	return map[string]any{"update": map[string]any{"title": p.Title, "description": p.Desc, "state": p.state(), "reason": "",
		"source": map[string]any{"commit": map[string]any{"hash": strings.TrimSpace(string(out))}, "repository": map[string]any{"full_name": repo}}}}
}

func (f *fakeBB) pr(repo string, id int) *fakePR {
	for _, p := range f.prs[repo] {
		if p.ID == id {
			return p
		}
	}
	f.t.Fatalf("no pull request %s#%d", repo, id)
	return nil
}

// Fail makes every request "METHOD ...suffix" answer status.
func (f *fakeBB) Fail(method, pathSuffix string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method+" "+pathSuffix] = status
}

// HumanComment, Approve, Rename, EditDescription, PushCommit and ForcePush are a
// person touching a pull request, each as the service logs it in the activity.
func (f *fakeBB) HumanComment(repo string, id int, raw string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pr(repo, id)
	p.Comments = append(p.Comments, raw)
	p.Activity = append(p.Activity, map[string]any{"comment": map[string]any{"content": map[string]any{"raw": raw}}})
}

func (f *fakeBB) Approve(repo string, id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pr(repo, id)
	p.Activity = append(p.Activity, map[string]any{"approval": map[string]any{"date": "2026-11-23T00:00:00+00:00"}})
}

func (f *fakeBB) Rename(repo string, id int, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pr(repo, id)
	p.Title = title
	p.Activity = append(p.Activity, f.updateEntry(repo, p))
}

func (f *fakeBB) EditDescription(repo string, id int, desc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pr(repo, id)
	p.Desc = desc
	p.Activity = append(p.Activity, f.updateEntry(repo, p))
}

// PushCommit adds a commit by a person to the pull request's branch.
func (f *fakeBB) PushCommit(repo string, id int) {
	f.pushBranch(repo, id, false)
}

// ForcePush rewrites the branch's one commit, keeping its author.
func (f *fakeBB) ForcePush(repo string, id int) {
	f.pushBranch(repo, id, true)
}

func (f *fakeBB) pushBranch(repo string, id int, rewrite bool) {
	f.t.Helper()
	f.mu.Lock()
	p := f.pr(repo, id)
	head := p.Head
	f.mu.Unlock()
	work := f.t.TempDir()
	git(f.t, work, "clone", "-q", "-b", head, f.bare(repo), "w")
	w := filepath.Join(work, "w")
	if rewrite {
		git(f.t, w, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
		git(f.t, w, "push", "-q", "--force", "origin", head)
	} else {
		gitAs(f.t, w, "Pat", "pat@example.com", "commit", "-q", "--allow-empty", "-m", "by a person")
		git(f.t, w, "push", "-q", "origin", head)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p.Activity = append(p.Activity, f.updateEntry(repo, p))
}

func (f *fakeBB) getPR(w http.ResponseWriter, repo, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	for _, p := range f.prs[repo] {
		if p.ID == n {
			writeJSON(w, 200, f.prJSON(repo, p))
			return
		}
	}
	writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such pull request"}})
}

// declinePR is "Decline a pull request"; it does not delete the source branch.
func (f *fakeBB) declinePR(w http.ResponseWriter, repo, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	for _, p := range f.prs[repo] {
		if p.ID == n {
			if p.state() != "OPEN" {
				writeJSON(w, 555, map[string]any{"type": "error", "error": map[string]any{"message": "pull request is not open"}})
				return
			}
			p.State = "DECLINED"
			writeJSON(w, 200, f.prJSON(repo, p))
			return
		}
	}
	writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such pull request"}})
}

func (f *fakeBB) prComments(w http.ResponseWriter, r *http.Request, repo, id string, body []byte) {
	f.mu.Lock()
	n, _ := strconv.Atoi(id)
	var p *fakePR
	for _, q := range f.prs[repo] {
		if q.ID == n {
			p = q
		}
	}
	if p == nil {
		f.mu.Unlock()
		writeJSON(w, 404, map[string]any{"type": "error", "error": map[string]any{"message": "no such pull request"}})
		return
	}
	if r.Method == http.MethodPost {
		defer f.mu.Unlock()
		var in struct{ Content struct{ Raw string } }
		if err := json.Unmarshal(body, &in); err != nil || in.Content.Raw == "" {
			writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"message": "content.raw is required"}})
			return
		}
		p.Comments = append(p.Comments, in.Content.Raw)
		writeJSON(w, 201, map[string]any{"type": "pullrequest_comment", "content": map[string]any{"raw": in.Content.Raw}})
		return
	}
	var items []any
	for _, c := range p.Comments {
		items = append(items, map[string]any{"type": "pullrequest_comment", "deleted": false, "content": map[string]any{"raw": c}})
	}
	f.mu.Unlock()
	f.paginate(w, r, items)
}

func (f *fakeBB) prActivity(w http.ResponseWriter, r *http.Request, repo, id string) {
	f.mu.Lock()
	n, _ := strconv.Atoi(id)
	var items []any
	for _, p := range f.prs[repo] {
		if p.ID == n {
			for _, a := range p.Activity {
				items = append(items, a)
			}
		}
	}
	f.mu.Unlock()
	f.paginate(w, r, items)
}

// prCommits is "List commits on a pull request": chronological, empty when the
// source branch is gone. The author is "Name <email>" in author.raw (assumed: the
// vendored page does not show the commit object).
func (f *fakeBB) prCommits(w http.ResponseWriter, r *http.Request, repo, id string) {
	f.mu.Lock()
	n, _ := strconv.Atoi(id)
	var head, base string
	for _, p := range f.prs[repo] {
		if p.ID == n {
			head, base = p.Head, p.Base
		}
	}
	f.mu.Unlock()
	cmd := exec.Command("git", "log", "--reverse", "--format=%H%x09%an <%ae>", base+".."+head)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	var items []any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if hash, who, ok := strings.Cut(line, "\t"); ok {
			items = append(items, map[string]any{"type": "commit", "hash": hash, "author": map[string]any{"raw": who}})
		}
	}
	f.paginate(w, r, items)
}
