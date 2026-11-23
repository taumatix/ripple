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

	mu      sync.Mutex
	calls   map[string]int
	lookups map[string]int
	prs     map[string][]*bbPR
	visible []string
	// Merges records the raw merge request bodies, in order.
	Merges []string
}

type bbPR struct {
	PR
	version int
}

var (
	bbKey  = regexp.MustCompile(`^~?[A-Za-z0-9][A-Za-z0-9._-]*$`)
	bbSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func NewBitbucketServer(t *testing.T) *BitbucketServer {
	t.Helper()
	b := &BitbucketServer{t: t, PageMax: 25, root: t.TempDir(), calls: map[string]int{}, lookups: map[string]int{}, prs: map[string][]*bbPR{}}
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

// Show makes the repositories appear in GET /repos.
func (b *BitbucketServer) Show(repos ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.visible = append(b.visible, repos...)
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
	vis := append([]string(nil), b.visible...)
	b.mu.Unlock()
	lo, hi, env := b.page(r, len(vis))
	values := []map[string]any{}
	for _, n := range vis[lo:hi] {
		key, slug, _ := strings.Cut(n, "/")
		// No defaultBranch: the schema marks it writeOnly (docs/vendor/bitbucket-server/openapi-excerpt.json).
		values = append(values, map[string]any{"slug": slug, "id": 1, "name": slug, "scmId": "git", "state": "AVAILABLE",
			"archived": false, "project": map[string]any{"key": key, "id": 1, "name": key, "type": "NORMAL"}})
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
	switch {
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
	return map[string]any{"id": p.Number, "version": p.version, "title": p.Title, "description": p.Body, "state": "OPEN", "open": true,
		"closed": false, "draft": p.Draft, "fromRef": ref(p.Head), "toRef": ref(p.Base),
		"links": map[string]any{"self": []map[string]any{{"href": b.prURL(repo, p)}}}}
}

func (b *BitbucketServer) pulls(w http.ResponseWriter, r *http.Request, repo string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("state") != "OPEN" {
			b.t.Errorf("pull requests listed without state=OPEN: %s", r.URL.RawQuery)
		}
		b.mu.Lock()
		all := append([]*bbPR(nil), b.prs[repo]...)
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
		if p.Head == heads["fromRef"] && p.Base == heads["toRef"] {
			b.fail(w, http.StatusConflict, "A pull request between the two branches already exists")
			return
		}
	}
	p := &bbPR{PR: PR{Repo: repo, Head: heads["fromRef"], Base: heads["toRef"], Title: in.Title, Body: in.Description,
		Number: len(b.prs[repo]) + 1, Draft: in.Draft != nil && *in.Draft}}
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
