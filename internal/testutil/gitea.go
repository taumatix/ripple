package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// GiteaToken is the only API token the fake accepts.
const GiteaToken = "gitea-test-token"

// Version strings as the two projects report them: gitea.com and codeberg.org
// answered these to GET /api/v1/version on 2026-11-23 (docs/vendor/README.md).
const (
	GiteaVersion   = "29.0.0+dev-160-g55186ca39b"
	ForgejoVersion = "17.0.0-121-39320c0+gitea-1.22.0"
)

// Gitea is a fake Gitea or Forgejo: the REST API under /api/v1 over HTTP and bare
// git repositories on local disk (GitBase is a file:// URL). It is written from the
// vendored docs (docs/vendor), not recorded from a real instance. It is strict about
// the wire: the Authorization scheme, the merge body's key case (`do` on Gitea,
// `Do` on Forgejo) and that a PR creation carries no draft field.
type Gitea struct {
	APIURL  string
	GitBase string
	// Forgejo makes the fake answer as Forgejo does.
	Forgejo bool
	// PageMax is the largest page the fake serves (MAX_RESPONSE_ITEMS, default 50).
	PageMax int
	// RateLimitOnce makes the next POST to pulls answer 429 with Retry-After: 1.
	RateLimitOnce bool

	t    *testing.T
	root string

	mu      sync.Mutex
	calls   map[string]int
	prs     map[string][]*PR
	visible []string
	// Merges records the raw merge request bodies, in order.
	Merges []string
	// VersionCalls counts GET /api/v1/version.
	VersionCalls int
}

func NewGitea(t *testing.T, forgejo bool) *Gitea {
	t.Helper()
	g := &Gitea{t: t, Forgejo: forgejo, PageMax: 50, root: t.TempDir(), calls: map[string]int{}, prs: map[string][]*PR{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	g.APIURL = srv.URL
	g.GitBase = "file://" + g.root
	return g
}

func (g *Gitea) bare(repo string) string { return filepath.Join(g.root, repo+".git") }

// AddRepo creates a bare repository whose main branch holds files. It is not
// visible to the token's repository listing until Show.
func (g *Gitea) AddRepo(repo string, files map[string]string) {
	g.t.Helper()
	bare := g.bare(repo)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		g.t.Fatal(err)
	}
	git(g.t, bare, "init", "--bare", "-b", "main")
	git(g.t, bare, "config", "uploadpack.allowFilter", "true")
	g.Commit(repo, files)
}

// Show makes the repositories appear in GET /user/repos.
func (g *Gitea) Show(repos ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.visible = append(g.visible, repos...)
}

// Commit adds a commit on main with files.
func (g *Gitea) Commit(repo string, files map[string]string) {
	g.t.Helper()
	work := g.t.TempDir()
	git(g.t, work, "clone", "-q", g.bare(repo), "w")
	w := filepath.Join(work, "w")
	git(g.t, w, "checkout", "-q", "-B", "main")
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			g.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			g.t.Fatal(err)
		}
	}
	git(g.t, w, "add", "-A")
	git(g.t, w, "commit", "-q", "-m", "test commit")
	git(g.t, w, "push", "-q", "origin", "main")
}

// FileAt returns the content of path on branch, or ok=false when absent.
func (g *Gitea) FileAt(repo, branch, path string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func (g *Gitea) hasBranch(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// Calls is the number of API requests addressed to repo.
func (g *Gitea) Calls(repo string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[repo]
}

func (g *Gitea) PRs(repo string) []PR {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []PR
	for _, p := range g.prs[repo] {
		out = append(out, *p)
	}
	return out
}

func (g *Gitea) fail(w http.ResponseWriter, code int, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	g.t.Logf("fake gitea: %d %s", code, msg)
	http.Error(w, msg, code)
}

func (g *Gitea) version() string {
	if g.Forgejo {
		return ForgejoVersion
	}
	return GiteaVersion
}

func (g *Gitea) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "token "+GiteaToken {
		g.fail(w, http.StatusUnauthorized, "want 'Authorization: token <token>' (docs/vendor/*/api-usage.md), got %q", r.Header.Get("Authorization"))
		g.t.Errorf("bad Authorization header on %s %s", r.Method, r.URL.Path)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case path == "/version" && r.Method == http.MethodGet:
		g.mu.Lock()
		g.VersionCalls++
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"version": g.version()})
	case path == "/user/repos" && r.Method == http.MethodGet:
		g.listRepos(w, r)
	case strings.HasPrefix(path, "/repos/"):
		g.handleRepo(w, r, strings.Split(strings.TrimPrefix(path, "/repos/"), "/"))
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

// page serves page ?page of ?limit (capped at PageMax) of n items, with a Link
// header as docs/vendor/forgejo/api-usage.md shows.
func (g *Gitea) page(w http.ResponseWriter, r *http.Request, n int) (lo, hi int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > g.PageMax {
		limit = g.PageMax
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	lo = min((page-1)*limit, n)
	hi = min(lo+limit, n)
	w.Header().Set("X-Total-Count", strconv.Itoa(n))
	if hi < n {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(page+1))
		w.Header().Set("Link", fmt.Sprintf("<%s?%s>; rel=\"next\"", r.URL.Path, q.Encode()))
	}
	return lo, hi
}

func (g *Gitea) listRepos(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	vis := append([]string(nil), g.visible...)
	g.mu.Unlock()
	lo, hi := g.page(w, r, len(vis))
	out := []map[string]any{}
	for _, n := range vis[lo:hi] {
		out = append(out, map[string]any{"full_name": n, "default_branch": "main", "archived": false, "empty": false,
			"permissions": map[string]bool{"admin": false, "push": true, "pull": true}})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (g *Gitea) handleRepo(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < 3 {
		g.fail(w, http.StatusNotFound, "unknown route %s", r.URL.Path)
		return
	}
	repo := parts[0] + "/" + parts[1]
	if _, err := os.Stat(g.bare(repo)); err != nil {
		g.fail(w, http.StatusNotFound, "no such repo %s", repo)
		return
	}
	g.mu.Lock()
	g.calls[repo]++
	limit := g.RateLimitOnce && r.Method == http.MethodPost && parts[2] == "pulls" && len(parts) == 3
	if limit {
		g.RateLimitOnce = false
	}
	g.mu.Unlock()
	if limit {
		w.Header().Set("Retry-After", "1")
		g.fail(w, http.StatusTooManyRequests, "slow down")
		return
	}
	switch {
	case parts[2] == "pulls" && len(parts) == 3:
		g.pulls(w, r, repo)
	case parts[2] == "pulls" && len(parts) == 5 && parts[4] == "merge" && r.Method == http.MethodPost:
		g.merge(w, r, repo, parts[3])
	case parts[2] == "issues" && len(parts) == 5 && parts[4] == "labels" && r.Method == http.MethodPost:
		g.labels(w, r, repo, parts[3])
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

func (g *Gitea) pulls(w http.ResponseWriter, r *http.Request, repo string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("state") != "open" {
			g.t.Errorf("pulls listed without state=open")
		}
		g.mu.Lock()
		all := append([]*PR(nil), g.prs[repo]...)
		lo, hi := g.page(w, r, len(all))
		out := []map[string]any{}
		for _, p := range all[lo:hi] {
			out = append(out, giteaPR(repo, p))
		}
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			g.fail(w, http.StatusUnprocessableEntity, "bad body")
			return
		}
		if _, ok := in["draft"]; ok {
			g.t.Errorf("CreatePullRequestOption has no draft field (docs/vendor/*/swagger-excerpt.json); got %v", in)
		}
		title, _ := in["title"].(string)
		head, _ := in["head"].(string)
		base, _ := in["base"].(string)
		body, _ := in["body"].(string)
		if title == "" || head == "" || base == "" {
			g.fail(w, http.StatusUnprocessableEntity, "title, head and base are required")
			return
		}
		for _, ref := range []string{head, base} {
			if !g.hasBranch(repo, ref) {
				g.fail(w, http.StatusNotFound, "branch %q does not exist in %s", ref, repo)
				return
			}
		}
		g.mu.Lock()
		p := &PR{Repo: repo, Head: head, Base: base, Title: title, Body: body, Number: len(g.prs[repo]) + 1,
			Draft: strings.HasPrefix(strings.ToUpper(title), "WIP:")}
		g.prs[repo] = append(g.prs[repo], p)
		resp := giteaPR(repo, p)
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	default:
		g.fail(w, http.StatusMethodNotAllowed, "method")
	}
}

func (g *Gitea) merge(w http.ResponseWriter, r *http.Request, repo, num string) {
	raw, _ := io.ReadAll(r.Body)
	// The key is `do` in Gitea's MergePullRequestForm and `Do` in Forgejo's
	// (docs/vendor/*/swagger-excerpt.json). Real servers decode case-insensitively;
	// the fake does not, so the wire format is pinned.
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		g.fail(w, http.StatusUnprocessableEntity, "bad merge body")
		return
	}
	key := "do"
	if g.Forgejo {
		key = "Do"
	}
	if _, ok := in[key]; !ok || in["merge_when_checks_succeed"] != true {
		g.t.Errorf("merge body %s lacks %q or merge_when_checks_succeed:true", raw, key)
		g.fail(w, http.StatusUnprocessableEntity, "bad merge body")
		return
	}
	n, _ := strconv.Atoi(num)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Merges = append(g.Merges, string(raw))
	for _, p := range g.prs[repo] {
		if p.Number == n {
			p.AutoMerge = true
			w.WriteHeader(http.StatusCreated)
			return
		}
	}
	g.fail(w, http.StatusNotFound, "no pull request %d", n)
}

func (g *Gitea) labels(w http.ResponseWriter, r *http.Request, repo, num string) {
	var in struct{ Labels []string }
	n, err := strconv.Atoi(num)
	if err != nil || json.NewDecoder(r.Body).Decode(&in) != nil {
		g.fail(w, http.StatusUnprocessableEntity, "bad labels request")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[repo] {
		if p.Number == n {
			p.Labels = append(p.Labels, in.Labels...)
			_ = json.NewEncoder(w).Encode([]any{})
			return
		}
	}
	g.fail(w, http.StatusNotFound, "no pull request %d", n)
}

func giteaPR(repo string, p *PR) map[string]any {
	return map[string]any{"number": p.Number, "html_url": fmt.Sprintf("https://gitea.example/%s/pulls/%d", repo, p.Number),
		"state": "open", "draft": p.Draft, "title": p.Title,
		"head": map[string]any{"ref": p.Head, "label": p.Head}, "base": map[string]any{"ref": p.Base}}
}
