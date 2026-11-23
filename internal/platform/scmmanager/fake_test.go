package scmmanager_test

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

// fakeSCM is a fake SCM-Manager 3.x: the slice of REST v2 ripple uses, the review
// plugin's pull requests, and bare git repositories served over smart HTTP at
// /scm/repo/<namespace>/<name> with Basic auth. It is written from
// docs/vendor/scm-manager (source-excerpts.md names the section each handler follows),
// not recorded from a real instance.
type fakeSCM struct {
	t    *testing.T
	root string
	// BaseURL is the instance's context path ("<server>/scm").
	BaseURL string
	// Token is the only credential accepted: Bearer on the API, the password over git.
	Token string

	mu        sync.Mutex
	calls     map[string]int
	repos     map[string]bool // "namespace/name" -> exists
	archived  map[string]bool
	svn       map[string]bool
	prs       map[string][]*fakePR
	rateLimit bool
	gitReject int
	requests  []fakeReq
	defCalls  int
}

type fakePR struct {
	ID            int
	Title, Desc   string
	Head, Base    string
	Draft, Delete bool
}

func (p fakePR) Status() string {
	if p.Draft {
		return "DRAFT"
	}
	return "OPEN"
}

type fakeReq struct{ Method, Path, Query, Auth, Accept, ContentType, Body string }

func newFakeSCM(t *testing.T) *fakeSCM {
	t.Helper()
	f := &fakeSCM{t: t, root: t.TempDir(), Token: "api-key-123", calls: map[string]int{}, repos: map[string]bool{},
		archived: map[string]bool{}, svn: map[string]bool{}, prs: map[string][]*fakePR{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.BaseURL = srv.URL + "/scm"
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

// bare is the repository directory. SCM-Manager's clone URL has no ".git" suffix
// (source-excerpts.md#git-url), so neither has the directory.
func (f *fakeSCM) bare(repo string) string { return filepath.Join(f.root, repo) }

func (f *fakeSCM) AddRepo(repo string, files map[string]string) {
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

// AddOther lists a repository that is not a usable git one: type "svn", or archived.
func (f *fakeSCM) AddOther(repo string, svn, archived bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repo] = true
	f.svn[repo] = svn
	f.archived[repo] = archived
}

func (f *fakeSCM) Commit(repo string, files map[string]string) {
	f.t.Helper()
	f.CommitOn(repo, "main", files)
}

func (f *fakeSCM) CommitOn(repo, branch string, files map[string]string) {
	f.t.Helper()
	work := f.t.TempDir()
	git(f.t, work, "clone", "-q", f.bare(repo), "w")
	w := filepath.Join(work, "w")
	if strings.Contains(git(f.t, w, "branch", "-a"), "origin/"+branch) {
		git(f.t, w, "checkout", "-q", "-B", branch, "origin/"+branch)
	} else {
		git(f.t, w, "checkout", "-q", "-B", branch)
	}
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
	git(f.t, w, "push", "-q", "origin", branch)
}

func (f *fakeSCM) Rev(repo, ref string) string {
	return strings.TrimSpace(git(f.t, f.bare(repo), "rev-parse", ref))
}

func (f *fakeSCM) FileAt(repo, branch, path string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	return string(out), err == nil
}

func (f *fakeSCM) Calls(repo string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[repo] }

func (f *fakeSCM) DefaultBranchCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.defCalls }

func (f *fakeSCM) PRs(repo string) []fakePR {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakePR
	for _, p := range f.prs[repo] {
		out = append(out, *p)
	}
	return out
}

// SeedPR is a pull request somebody else (or an earlier run) opened.
func (f *fakeSCM) SeedPR(repo, head, base string, draft bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs[repo] = append(f.prs[repo], &fakePR{ID: len(f.prs[repo]) + 1, Title: "seed", Head: head, Base: base, Draft: draft})
}

func (f *fakeSCM) Requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeReq(nil), f.requests...)
}

func (f *fakeSCM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/scm/repo/"):
		f.serveGit(w, r)
	case strings.HasPrefix(r.URL.Path, "/scm/api/v2/"):
		f.serveAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSCM) serveGit(w http.ResponseWriter, r *http.Request) {
	_, pass, _ := r.BasicAuth() // the username is ignored for API keys (source-excerpts.md#auth)
	if pass != f.Token {
		f.mu.Lock()
		f.gitReject++
		f.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="SCM-Manager"`)
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
	http.StripPrefix("/scm/repo", h).ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"errorCode": "FAKE", "message": msg})
}

const (
	mtRepos   = "application/vnd.scmm-repositoryCollection+json;v=2"
	mtPRs     = "application/vnd.scmm-pullRequestCollection+json;v=2"
	mtPR      = "application/vnd.scmm-pullRequest+json;v=2"
	mtDefault = "application/vnd.scmm-gitDefaultBranch+json;v=2"
	mtMods    = "application/vnd.scmm-modifications+json;v=2"
)

func (f *fakeSCM) serveAPI(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/scm/api/v2/"), "/")
	// Which repository the request is addressed to, for the per-repo call count.
	repo := ""
	switch {
	case len(parts) >= 3 && (parts[0] == "pull-requests" || parts[0] == "repositories"):
		repo = parts[1] + "/" + parts[2]
	case len(parts) >= 4 && parts[0] == "config" && parts[1] == "git":
		repo = parts[2] + "/" + parts[3]
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"),
		r.Header.Get("Accept"), r.Header.Get("Content-Type"), string(body)})
	if repo != "" {
		f.calls[repo]++
	}
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		apiError(w, 401, "not authenticated")
		return
	}
	switch {
	case len(parts) == 1 && parts[0] == "repositories" && r.Method == http.MethodGet:
		f.listRepos(w, r)
	case len(parts) == 5 && parts[0] == "config" && parts[1] == "git" && parts[4] == "default-branch" && r.Method == http.MethodGet:
		f.defaultBranch(w, repo)
	case len(parts) == 3 && parts[0] == "pull-requests" && r.Method == http.MethodGet:
		f.listPRs(w, r, repo)
	case len(parts) == 3 && parts[0] == "pull-requests" && r.Method == http.MethodPost:
		f.createPR(w, r, repo, body)
	case len(parts) == 5 && parts[0] == "repositories" && parts[3] == "modifications" && r.Method == http.MethodGet:
		f.modifications(w, repo, parts[4])
	default:
		apiError(w, 404, "no such resource "+r.URL.Path)
	}
}

// hal is the paging envelope of the repository and pull request collections:
// zero-based page, pageTotal, items under _embedded (source-excerpts.md#pr-list).
func hal(w http.ResponseWriter, r *http.Request, media, key string, items []any) {
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if size <= 0 {
		size = 10
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	lo := min(max(page, 0)*size, len(items))
	hi := min(lo+size, len(items))
	total := (len(items) + size - 1) / size
	w.Header().Set("Content-Type", media)
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "pageTotal": total, "_embedded": map[string]any{key: items[lo:hi]}})
}

func (f *fakeSCM) listRepos(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	var names []string
	for n := range f.repos {
		names = append(names, n)
	}
	f.mu.Unlock()
	sort.Strings(names)
	var items []any
	for _, n := range names {
		ns, name, _ := strings.Cut(n, "/")
		typ := "git"
		if f.svn[n] {
			typ = "svn"
		}
		items = append(items, map[string]any{"namespace": ns, "name": name, "type": typ, "archived": f.archived[n]})
	}
	hal(w, r, mtRepos, "repositories", items)
}

func (f *fakeSCM) defaultBranch(w http.ResponseWriter, repo string) {
	f.mu.Lock()
	f.defCalls++
	ok := f.repos[repo] && !f.svn[repo]
	f.mu.Unlock()
	if !ok || !f.hasBranch(repo, "main") {
		apiError(w, 404, "no default branch")
		return
	}
	w.Header().Set("Content-Type", mtDefault)
	_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "main"})
}

func (f *fakeSCM) prJSON(p *fakePR) map[string]any {
	return map[string]any{"id": strconv.Itoa(p.ID), "title": p.Title, "status": p.Status(), "source": p.Head, "target": p.Base}
}

func (f *fakeSCM) listPRs(w http.ResponseWriter, r *http.Request, repo string) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "IN_PROGRESS"
	}
	f.mu.Lock()
	var items []any
	for _, p := range f.prs[repo] {
		if status == "IN_PROGRESS" || status == "ALL" || status == p.Status() {
			items = append(items, f.prJSON(p))
		}
	}
	f.mu.Unlock()
	hal(w, r, mtPRs, "pullRequests", items)
}

func (f *fakeSCM) createPR(w http.ResponseWriter, r *http.Request, repo string, body []byte) {
	f.mu.Lock()
	if f.rateLimit {
		f.rateLimit = false
		f.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		apiError(w, 429, "too many requests")
		return
	}
	defer f.mu.Unlock()
	if r.Header.Get("Content-Type") != mtPR {
		apiError(w, 415, "unsupported media type")
		return
	}
	var in struct {
		Source, Target, Title, Description, Status string
		ShouldDeleteSourceBranch                   bool
	}
	if err := json.Unmarshal(body, &in); err != nil || in.Source == "" || in.Target == "" || in.Title == "" {
		apiError(w, 400, "source, target and title are required")
		return
	}
	if !f.hasBranch(repo, in.Source) || !f.hasBranch(repo, in.Target) {
		apiError(w, 400, "branch not found")
		return
	}
	for _, p := range f.prs[repo] {
		if p.Head == in.Source && p.Base == in.Target {
			apiError(w, 409, "a pull request for these branches already exists")
			return
		}
	}
	p := &fakePR{ID: len(f.prs[repo]) + 1, Title: in.Title, Desc: in.Description, Head: in.Source, Base: in.Target,
		Draft: in.Status == "DRAFT", Delete: in.ShouldDeleteSourceBranch}
	f.prs[repo] = append(f.prs[repo], p)
	w.Header().Set("Location", fmt.Sprintf("http://%s/scm/api/v2/pull-requests/%s/%d", r.Host, repo, p.ID))
	w.WriteHeader(201)
}

func (f *fakeSCM) hasBranch(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// modifications is source-excerpts.md#modifications: added, modified, removed, renamed.
func (f *fakeSCM) modifications(w http.ResponseWriter, repo, rev string) {
	cmd := exec.Command("git", "diff-tree", "-r", "--root", "--no-commit-id", "--name-status", rev)
	cmd.Dir = f.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		apiError(w, 404, "no such changeset")
		return
	}
	m := map[string]any{"revision": rev, "added": []string{}, "modified": []string{}, "removed": []string{}, "renamed": []any{}}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		st, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		key := map[string]string{"A": "added", "D": "removed", "M": "modified"}[st]
		m[key] = append(m[key].([]string), path)
	}
	w.Header().Set("Content-Type", mtMods)
	_ = json.NewEncoder(w).Encode(m)
}
