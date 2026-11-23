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
}

type fakePR struct {
	ID                int
	Title, Desc       string
	Head, Base        string
	Draft, Queued     bool
	CloseSourceBranch bool
	MergeBody         map[string]any
}

type fakeReq struct{ Method, Path, Query, Auth, Body string }

func newFakeBB(t *testing.T) *fakeBB {
	t.Helper()
	f := &fakeBB{t: t, root: t.TempDir(), Token: "tok-123", calls: map[string]int{}, repos: map[string]bool{},
		prs: map[string][]*fakePR{}, noQueue: map[string]bool{}}
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
	switch {
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

func (f *fakeBB) prJSON(repo string, p *fakePR) map[string]any {
	return map[string]any{"type": "pullrequest", "id": p.ID, "title": p.Title, "state": "OPEN", "draft": p.Draft, "queued": p.Queued,
		"links":       map[string]any{"html": map[string]any{"href": fmt.Sprintf("https://bitbucket.example/%s/pull-requests/%d", repo, p.ID)}},
		"source":      map[string]any{"branch": map[string]any{"name": p.Head}},
		"destination": map[string]any{"branch": map[string]any{"name": p.Base}}}
}

func (f *fakeBB) listPRs(w http.ResponseWriter, r *http.Request, repo string) {
	f.mu.Lock()
	var items []any
	for _, p := range f.prs[repo] {
		items = append(items, f.prJSON(repo, p))
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
