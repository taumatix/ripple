package testutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// BitbucketServerToken and BitbucketServerUser are the only credentials the fake accepts.
const (
	BitbucketServerToken = "bbs-test-token"
	BitbucketServerUser  = "ripple"
)

// BitbucketServerContext is the context path the fake is served under, as an instance
// at https://host/bitbucket is: every URL ripple builds must carry it.
const BitbucketServerContext = "/bitbucket"

// BitbucketServer is a fake Bitbucket Data Center: the REST API under
// <context>/rest/api/1.0 over HTTP and bare git repositories on local disk at
// <root>/scm/<PROJECT>/<slug>.git (GitBase is a file:// URL, so the clone URL is
// GitBase + "/scm/KEY/slug.git" as on a real instance). Written from the vendored
// docs (docs/vendor/bitbucket-server), never recorded from a real server. It is
// strict about the wire: Basic authentication, the create body's shape (fromRef and
// toRef are fully qualified refs with repository.slug and repository.project.key,
// `draft` only as a boolean), the merge call's `version` query and `autoMerge` body.
type BitbucketServer struct {
	// APIURL is the instance URL including the context path; ripple adds /rest/api/1.0.
	APIURL  string
	GitBase string
	// PageMax is the largest page the fake serves (the real default is 25).
	PageMax int
	// RateLimitOnce makes the next POST creating a pull request answer 429 with Retry-After: 1.
	RateLimitOnce bool
	// AutoMergeOff makes merge requests with autoMerge answer 403, as a repository
	// whose auto-merge setting is off does (docs/vendor/bitbucket-server/openapi-excerpt.json).
	AutoMergeOff bool

	t    *testing.T
	root string

	mu       sync.Mutex
	calls    map[string]int
	lookups  map[string]int
	prs      map[string][]*bbPR
	visible  []string
	archived map[string]bool
	// Merges records the raw merge request bodies, in order.
	Merges []string
	// failures make a request fail: "METHOD path-suffix" -> status (FailRequest).
	failures map[string]int
}

type bbPR struct {
	PR
	version int
	// actions is the pull request's activity log, oldest first (RestPullRequestActivity.action).
	actions []string
}

var (
	bbKey  = regexp.MustCompile(`^~?[A-Za-z0-9][A-Za-z0-9._-]*$`)
	bbSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func NewBitbucketServer(t *testing.T) *BitbucketServer {
	t.Helper()
	b := &BitbucketServer{t: t, PageMax: 25, root: t.TempDir(), calls: map[string]int{}, lookups: map[string]int{}, prs: map[string][]*bbPR{}, failures: map[string]int{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	b.APIURL = srv.URL + BitbucketServerContext
	b.GitBase = "file://" + b.root
	return b
}

func (b *BitbucketServer) bare(repo string) string {
	return filepath.Join(b.root, "scm", repo+".git")
}

// AddRepo creates a bare repository "KEY/slug" whose main branch holds files. It
// is not in the token's repository listing until Show.
func (b *BitbucketServer) AddRepo(repo string, files map[string]string) {
	b.t.Helper()
	bare := b.bare(repo)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		b.t.Fatal(err)
	}
	git(b.t, bare, "init", "--bare", "-b", "main")
	git(b.t, bare, "config", "uploadpack.allowFilter", "true")
	b.Commit(repo, files)
}

// Branch creates a branch at main's head.
func (b *BitbucketServer) Branch(repo, name string) {
	b.t.Helper()
	git(b.t, b.bare(repo), "branch", name, "main")
}

// BranchWithCommit creates branch from main with one empty commit whose author and
// committer are name <email>: a pull request's branch as its opener left it.
func (b *BitbucketServer) BranchWithCommit(repo, branch, name, email string) {
	b.t.Helper()
	work := b.t.TempDir()
	git(b.t, work, "clone", "-q", b.bare(repo), "w")
	w := filepath.Join(work, "w")
	git(b.t, w, "checkout", "-q", "-b", branch)
	cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "bump")
	cmd.Dir = w
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.t.Fatalf("git commit: %v\n%s", err, out)
	}
	git(b.t, w, "push", "-q", "origin", branch)
}

// Show makes the repositories appear in GET /repos.
func (b *BitbucketServer) Show(repos ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.visible = append(b.visible, repos...)
}

// SetArchived archives or unarchives a repository. GET /repos answers ACTIVE ones unless
// ?archived=ARCHIVED or ALL says otherwise (8.0 and later, docs/vendor/bitbucket-server).
func (b *BitbucketServer) SetArchived(repo string, archived bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.archived == nil {
		b.archived = map[string]bool{}
	}
	b.archived[repo] = archived
}

// Commit adds a commit on main with files and returns the old and new head.
func (b *BitbucketServer) Commit(repo string, files map[string]string) (from, to string) {
	b.t.Helper()
	from = b.head(repo)
	work := b.t.TempDir()
	git(b.t, work, "clone", "-q", b.bare(repo), "w")
	w := filepath.Join(work, "w")
	git(b.t, w, "checkout", "-q", "-B", "main")
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			b.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			b.t.Fatal(err)
		}
	}
	git(b.t, w, "add", "-A")
	git(b.t, w, "commit", "-q", "-m", "test commit")
	git(b.t, w, "push", "-q", "origin", "main")
	return from, b.head(repo)
}

func (b *BitbucketServer) head(repo string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/main")
	cmd.Dir = b.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return strings.Repeat("0", 40) // a ref that does not exist yet, as in a refs_changed ADD
	}
	return strings.TrimSpace(string(out))
}

// FileAt returns the content of path on branch, or ok=false when absent.
func (b *BitbucketServer) FileAt(repo, branch, path string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = b.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// HasBranch reports whether the branch exists in the repository.
func (b *BitbucketServer) HasBranch(repo, branch string) bool { return b.hasBranch(repo, branch) }

func (b *BitbucketServer) hasBranch(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = b.bare(repo)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// Calls is the number of API requests addressed to repo, lookups included.
func (b *BitbucketServer) Calls(repo string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[repo]
}

// Lookups is the part of Calls the webhook receiver makes: the default branch
// and the changed files of a push.
func (b *BitbucketServer) Lookups(repo string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lookups[repo]
}

func (b *BitbucketServer) PRs(repo string) []PR {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []PR
	for _, p := range b.prs[repo] {
		out = append(out, p.PR)
	}
	return out
}

func (b *BitbucketServer) fail(w http.ResponseWriter, code int, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	b.t.Logf("fake bitbucket server: %d %s", code, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"context": nil, "message": msg}}})
}

func (b *BitbucketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(BitbucketServerUser+":"+BitbucketServerToken))
	if r.Header.Get("Authorization") != want {
		b.t.Errorf("bad Authorization header on %s %s", r.Method, r.URL.Path)
		b.fail(w, http.StatusUnauthorized, "want Basic user:token (docs/vendor/bitbucket-server/http-access-tokens.md)")
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, BitbucketServerContext+"/rest/api/1.0")
	if !ok {
		b.t.Errorf("request outside %s/rest/api/1.0: %s", BitbucketServerContext, r.URL.Path)
		b.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	switch {
	case path == "/repos" && r.Method == http.MethodGet:
		b.listRepos(w, r)
	case strings.HasPrefix(path, "/projects/"):
		b.handleRepo(w, r, strings.Split(strings.TrimPrefix(path, "/projects/"), "/"))
	default:
		b.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

// page serves a Bitbucket page of n items: start and limit query parameters (limit
// capped at PageMax), and the isLastPage/nextPageStart envelope.
func (b *BitbucketServer) page(r *http.Request, n int) (lo, hi int, env map[string]any) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > b.PageMax {
		limit = b.PageMax
	}
	lo, _ = strconv.Atoi(r.URL.Query().Get("start"))
	lo = min(max(lo, 0), n)
	hi = min(lo+limit, n)
	env = map[string]any{"size": hi - lo, "limit": limit, "start": lo, "isLastPage": hi >= n}
	if hi < n {
		env["nextPageStart"] = hi
	}
	return lo, hi, env
}

func (b *BitbucketServer) listRepos(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("permission") != "REPO_WRITE" {
		b.t.Errorf("repositories listed without permission=REPO_WRITE: %s", r.URL.RawQuery)
	}
	b.mu.Lock()
	var vis []string
	mode := strings.ToUpper(r.URL.Query().Get("archived"))
	for _, n := range b.visible {
		if mode == "ALL" || (mode == "ARCHIVED") == b.archived[n] {
			vis = append(vis, n)
		}
	}
	arch := map[string]bool{}
	for k, v := range b.archived {
		arch[k] = v
	}
	b.mu.Unlock()
	lo, hi, env := b.page(r, len(vis))
	values := []map[string]any{}
	for _, n := range vis[lo:hi] {
		key, slug, _ := strings.Cut(n, "/")
		// No defaultBranch: the schema marks it writeOnly (docs/vendor/bitbucket-server/openapi-excerpt.json).
		values = append(values, map[string]any{"slug": slug, "id": 1, "name": slug, "scmId": "git", "state": "AVAILABLE",
			"archived": arch[n], "project": map[string]any{"key": key, "id": 1, "name": key, "type": "NORMAL"}})
	}
	env["values"] = values
	_ = json.NewEncoder(w).Encode(env)
}

func (b *BitbucketServer) handleRepo(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 4 || parts[1] != "repos" {
		b.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
		return
	}
	if !bbKey.MatchString(parts[0]) || !bbSlug.MatchString(parts[2]) {
		b.t.Errorf("malformed project key or slug in %s", r.URL.Path)
		b.fail(w, http.StatusBadRequest, "bad project key or slug")
		return
	}
	repo, rest := parts[0]+"/"+parts[2], parts[3:]
	if _, err := os.Stat(b.bare(repo)); err != nil {
		b.fail(w, http.StatusNotFound, "no such repo %s", repo)
		return
	}
	lookup := rest[0] == "default-branch" || (rest[0] == "compare" && len(rest) == 2 && rest[1] == "changes")
	b.mu.Lock()
	b.calls[repo]++
	if lookup {
		b.lookups[repo]++
	}
	limit := b.RateLimitOnce && r.Method == http.MethodPost && rest[0] == "pull-requests" && len(rest) == 1
	if limit {
		b.RateLimitOnce = false
	}
	b.mu.Unlock()
	if limit {
		w.Header().Set("Retry-After", "1")
		b.fail(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	b.mu.Lock()
	for k, status := range b.failures {
		if m, suffix, _ := strings.Cut(k, " "); m == r.Method && strings.HasSuffix(r.URL.Path, suffix) {
			b.mu.Unlock()
			b.fail(w, status, "injected failure")
			return
		}
	}
	b.mu.Unlock()
	switch {
	case rest[0] == "pull-requests" && len(rest) == 3 && rest[2] == "decline" && r.Method == http.MethodPost:
		b.decline(w, r, repo, rest[1])
	case rest[0] == "pull-requests" && len(rest) == 3 && rest[2] == "comments" && r.Method == http.MethodPost:
		b.comment(w, r, repo, rest[1])
	case rest[0] == "pull-requests" && len(rest) == 3 && rest[2] == "activities" && r.Method == http.MethodGet:
		b.activities(w, r, repo, rest[1])
	case rest[0] == "pull-requests" && len(rest) == 3 && rest[2] == "commits" && r.Method == http.MethodGet:
		b.commits(w, r, repo, rest[1])
	case rest[0] == "pull-requests" && len(rest) == 1:
		b.pulls(w, r, repo)
	case rest[0] == "pull-requests" && len(rest) == 2 && r.Method == http.MethodGet:
		b.getPull(w, repo, rest[1])
	case rest[0] == "pull-requests" && len(rest) == 3 && rest[2] == "merge" && r.Method == http.MethodPost:
		b.merge(w, r, repo, rest[1])
	case rest[0] == "default-branch" && len(rest) == 1 && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "refs/heads/main", "displayId": "main", "type": "BRANCH"})
	case rest[0] == "compare" && len(rest) == 2 && rest[1] == "changes" && r.Method == http.MethodGet:
		b.changes(w, r, repo)
	default:
		b.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

func (p *bbPR) state() string {
	if p.Closed {
		return "DECLINED"
	}
	return "OPEN"
}

func (b *BitbucketServer) prURL(repo string, p *bbPR) string {
	key, slug, _ := strings.Cut(repo, "/")
	return fmt.Sprintf("https://bitbucket.example/projects/%s/repos/%s/pull-requests/%d", key, slug, p.Number)
}

func (b *BitbucketServer) prJSON(repo string, p *bbPR) map[string]any {
	key, slug, _ := strings.Cut(repo, "/")
	ref := func(branch string) map[string]any {
		return map[string]any{"id": "refs/heads/" + branch, "displayId": branch, "repository": map[string]any{
			"slug": slug, "project": map[string]any{"key": key}}}
	}
	return map[string]any{"id": p.Number, "version": p.version, "title": p.Title, "description": p.Body, "state": p.state(), "open": !p.Closed,
		"closed": p.Closed, "draft": p.Draft, "fromRef": ref(p.Head), "toRef": ref(p.Base),
		"links": map[string]any{"self": []map[string]any{{"href": b.prURL(repo, p)}}}}
}

func (b *BitbucketServer) pulls(w http.ResponseWriter, r *http.Request, repo string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("state") != "OPEN" {
			b.t.Errorf("pull requests listed without state=OPEN: %s", r.URL.RawQuery)
		}
		b.mu.Lock()
		var all []*bbPR
		for _, p := range b.prs[repo] {
			if !p.Closed {
				all = append(all, p)
			}
		}
		lo, hi, env := b.page(r, len(all))
		values := []map[string]any{}
		for _, p := range all[lo:hi] {
			values = append(values, b.prJSON(repo, p))
		}
		b.mu.Unlock()
		env["values"] = values
		_ = json.NewEncoder(w).Encode(env)
	case http.MethodPost:
		b.create(w, r, repo)
	default:
		b.fail(w, http.StatusMethodNotAllowed, "method")
	}
}

type bbRefIn struct {
	ID         string `json:"id"`
	Repository struct {
		Slug    string `json:"slug"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
	} `json:"repository"`
}

func (b *BitbucketServer) create(w http.ResponseWriter, r *http.Request, repo string) {
	var in struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Draft       *bool   `json:"draft"`
		FromRef     bbRefIn `json:"fromRef"`
		ToRef       bbRefIn `json:"toRef"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		b.t.Errorf("create body: %v", err)
		b.fail(w, http.StatusBadRequest, "malformed pull request: %v", err)
		return
	}
	key, slug, _ := strings.Cut(repo, "/")
	heads := map[string]string{}
	for name, ref := range map[string]bbRefIn{"fromRef": in.FromRef, "toRef": in.ToRef} {
		branch, ok := strings.CutPrefix(ref.ID, "refs/heads/")
		if !ok || ref.Repository.Slug != slug || ref.Repository.Project.Key != key {
			b.t.Errorf("%s must be a fully qualified branch in %s: %+v", name, repo, ref)
			b.fail(w, http.StatusBadRequest, "bad %s", name)
			return
		}
		if !b.hasBranch(repo, branch) {
			b.fail(w, http.StatusNotFound, "branch %q does not exist in %s", branch, repo)
			return
		}
		heads[name] = branch
	}
	if in.Title == "" {
		b.fail(w, http.StatusBadRequest, "title is required")
		return
	}
	if in.Draft != nil && !*in.Draft {
		b.t.Errorf("draft sent as false; send it only for a draft")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.prs[repo] {
		if !p.Closed && p.Head == heads["fromRef"] && p.Base == heads["toRef"] {
			b.fail(w, http.StatusConflict, "A pull request between the two branches already exists")
			return
		}
	}
	p := &bbPR{PR: PR{Repo: repo, Head: heads["fromRef"], Base: heads["toRef"], Title: in.Title, Body: in.Description,
		Number: len(b.prs[repo]) + 1, Draft: in.Draft != nil && *in.Draft}, actions: []string{"OPENED"}}
	b.prs[repo] = append(b.prs[repo], p)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(b.prJSON(repo, p))
}

func (b *BitbucketServer) find(repo, id string) *bbPR {
	n, _ := strconv.Atoi(id)
	for _, p := range b.prs[repo] {
		if p.Number == n {
			return p
		}
	}
	return nil
}

func (b *BitbucketServer) getPull(w http.ResponseWriter, repo, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, id)
	if p == nil {
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	_ = json.NewEncoder(w).Encode(b.prJSON(repo, p))
}

func (b *BitbucketServer) merge(w http.ResponseWriter, r *http.Request, repo, id string) {
	var in map[string]any
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&in); err != nil || in["autoMerge"] != true {
		b.t.Errorf("merge body must set autoMerge:true, got %v (%v)", in, err)
		b.fail(w, http.StatusBadRequest, "bad merge body")
		return
	}
	raw, _ := json.Marshal(in)
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, id)
	if p == nil {
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	v, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil {
		b.t.Errorf("merge without a version query: %s", r.URL.RawQuery)
		b.fail(w, http.StatusBadRequest, "version is required")
		return
	}
	if v != p.version {
		b.fail(w, http.StatusConflict, "The specified version is out of date")
		return
	}
	if b.AutoMergeOff {
		b.fail(w, http.StatusForbidden, "auto-merge is not enabled for this repository")
		return
	}
	b.Merges = append(b.Merges, string(raw))
	p.AutoMerge = true
	_ = json.NewEncoder(w).Encode(b.prJSON(repo, p))
}

// changes answers compare/changes: the files in `from` and not in `to`.
func (b *BitbucketServer) changes(w http.ResponseWriter, r *http.Request, repo string) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	cmd := exec.Command("git", "diff", "--name-only", to, from)
	cmd.Dir = b.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		b.fail(w, http.StatusNotFound, "cannot compare %s with %s", from, to)
		return
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	lo, hi, env := b.page(r, len(files))
	values := []map[string]any{}
	for _, f := range files[lo:hi] {
		values = append(values, map[string]any{"type": "MODIFY", "path": map[string]any{
			"components": strings.Split(f, "/"), "parent": filepath.Dir(f), "name": filepath.Base(f), "toString": f}})
	}
	env["values"] = values
	_ = json.NewEncoder(w).Encode(env)
}

// FailRequest makes every request "METHOD ...suffix" answer status.
func (b *BitbucketServer) FailRequest(method, pathSuffix string, status int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures[method+" "+pathSuffix] = status
}

// decline is "Decline pull request": the version query must be the current one and
// the pull request OPEN, else 409 (docs: "not OPEN or has been updated since the
// version specified"). The branch is kept.
func (b *BitbucketServer) decline(w http.ResponseWriter, r *http.Request, repo, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, id)
	if p == nil {
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	v, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil {
		b.t.Errorf("decline without a version query: %s", r.URL.RawQuery)
		b.fail(w, http.StatusBadRequest, "version is required")
		return
	}
	if p.Closed || v != p.version {
		b.fail(w, http.StatusConflict, "The pull request is not OPEN or the version is out of date")
		return
	}
	p.Closed = true
	p.version++
	p.actions = append(p.actions, "DECLINED")
	_ = json.NewEncoder(w).Encode(b.prJSON(repo, p))
}

// comment is "Add pull request comment" for a general comment: {"text": "..."}.
func (b *BitbucketServer) comment(w http.ResponseWriter, r *http.Request, repo, id string) {
	var in struct {
		Text string `json:"text"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Text == "" {
		b.t.Errorf("comment body must be {\"text\": ...}: %v", err)
		b.fail(w, http.StatusBadRequest, "bad comment")
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, id)
	if p == nil {
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	p.Comments = append(p.Comments, in.Text)
	p.actions = append(p.actions, "COMMENTED")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": len(p.Comments), "text": in.Text})
}

func (b *BitbucketServer) activities(w http.ResponseWriter, r *http.Request, repo, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, id)
	if p == nil {
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	lo, hi, env := b.page(r, len(p.actions))
	values := []map[string]any{}
	for i, a := range p.actions[lo:hi] {
		values = append(values, map[string]any{"id": lo + i + 1, "action": a, "createdDate": 1700000000000 + i,
			"user": map[string]any{"name": "someone", "slug": "someone", "displayName": "Someone", "type": "NORMAL"}})
	}
	env["values"] = values
	_ = json.NewEncoder(w).Encode(env)
}

// commits is "Get pull request commits": RestCommit with author and committer.
func (b *BitbucketServer) commits(w http.ResponseWriter, r *http.Request, repo, id string) {
	b.mu.Lock()
	p := b.find(repo, id)
	if p == nil {
		b.mu.Unlock()
		b.fail(w, http.StatusNotFound, "no pull request %s", id)
		return
	}
	head, base := p.Head, p.Base
	b.mu.Unlock()
	cmd := exec.Command("git", "log", "--format=%H%x09%an%x09%ae%x09%cn%x09%ce", base+".."+head)
	cmd.Dir = b.bare(repo)
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	var all []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		all = append(all, map[string]any{"id": f[0], "displayId": f[0][:11],
			"author":    map[string]any{"name": f[1], "emailAddress": f[2]},
			"committer": map[string]any{"name": f[3], "emailAddress": f[4]}})
	}
	lo, hi, env := b.page(r, len(all))
	env["values"] = all[lo:hi]
	_ = json.NewEncoder(w).Encode(env)
}

// The helpers below are a person touching a pull request, each as the activity log
// would show it (RestPullRequestActivity.action).

// HumanComment adds a general comment.
func (b *BitbucketServer) HumanComment(repo string, n int, text string) {
	b.touch(repo, n, "COMMENTED", func(p *bbPR) { p.Comments = append(p.Comments, text) })
}

// Approve records an approval.
func (b *BitbucketServer) Approve(repo string, n int) {
	b.touch(repo, n, "APPROVED", func(p *bbPR) { p.Reviews++ })
}

// Rename changes the title.
func (b *BitbucketServer) Rename(repo string, n int, title string) {
	b.touch(repo, n, "UPDATED", func(p *bbPR) { p.Title, p.Renamed, p.version = title, true, p.version+1 })
}

// EditDescription changes the description.
func (b *BitbucketServer) EditDescription(repo string, n int, desc string) {
	b.touch(repo, n, "UPDATED", func(p *bbPR) { p.Body, p.BodyEdited, p.version = desc, true, p.version+1 })
}

// PushCommit adds a commit by a person to the pull request's branch.
func (b *BitbucketServer) PushCommit(repo string, n int) { b.push(repo, n, false) }

// ForcePush rewrites the branch's commit, keeping its author and committer.
func (b *BitbucketServer) ForcePush(repo string, n int) { b.push(repo, n, true) }

func (b *BitbucketServer) push(repo string, n int, rewrite bool) {
	b.t.Helper()
	b.mu.Lock()
	p := b.find(repo, strconv.Itoa(n))
	head := p.Head
	b.mu.Unlock()
	work := b.t.TempDir()
	git(b.t, work, "clone", "-q", "-b", head, b.bare(repo), "w")
	w := filepath.Join(work, "w")
	if rewrite {
		git(b.t, w, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
		git(b.t, w, "push", "-q", "--force", "origin", head)
	} else {
		cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "by a person")
		cmd.Dir = w
		cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME=Pat", "GIT_AUTHOR_EMAIL=pat@example.com", "GIT_COMMITTER_NAME=Pat", "GIT_COMMITTER_EMAIL=pat@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.t.Fatalf("git commit: %v\n%s", err, out)
		}
		git(b.t, w, "push", "-q", "origin", head)
	}
	b.touch(repo, n, "RESCOPED", func(p *bbPR) { p.ForcePushed, p.version = rewrite, p.version+1 })
}

func (b *BitbucketServer) touch(repo string, n int, action string, f func(*bbPR)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.find(repo, strconv.Itoa(n))
	if p == nil {
		b.t.Fatalf("no pull request %s#%d", repo, n)
	}
	f(p)
	p.actions = append(p.actions, action)
}
