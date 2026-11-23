package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// GitLabPR is a merge request the fake recorded.
type GitLabPR struct {
	Project, Head, Base, Title, Description, Labels string
	IID                                             int
	// Draft is derived from the title the way GitLab does: a leading "Draft:".
	Draft     bool
	AutoMerge bool
	// MergeBody is the body of the merge request, for tests that assert its fields.
	MergeBody map[string]any
}

// GitLab is a fake GitLab: the REST API v4 pieces ripple uses over HTTP (merge
// requests, merge when pipeline succeeds, membership listing) with Bearer tokens that
// are each scoped to a namespace or project, and bare git repositories served from
// local disk (GitBase is a file:// URL). Project ids in the URL must be one
// URL-encoded segment, as on the real API; a raw slash is a 404.
type GitLab struct {
	APIURL  string // the instance base URL; the API is APIURL/api/v4
	GitBase string

	t    *testing.T
	root string

	mu      sync.Mutex
	tokens  map[string]string // token -> scope ("" reaches every project)
	members map[string]bool
	calls   map[string]int
	prs     map[string][]*GitLabPR
	limit   map[string]bool // token -> answer its next merge request creation with 429
	// Requests records "METHOD escaped-path" of every API request, in order.
	requests []string
	// Auths records the Authorization header of every API request.
	auths []string
}

func NewGitLab(t *testing.T) *GitLab {
	t.Helper()
	g := &GitLab{t: t, root: t.TempDir(), tokens: map[string]string{}, members: map[string]bool{},
		calls: map[string]int{}, prs: map[string][]*GitLabPR{}, limit: map[string]bool{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	g.APIURL = srv.URL
	g.GitBase = "file://" + g.root
	return g
}

// Token accepts token for the projects under scope: a namespace ("group/sub"), a
// project path, or "" for all.
func (g *GitLab) Token(token, scope string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tokens[token] = scope
}

// Member makes path appear in the membership listing of every token that reaches it.
func (g *GitLab) Member(path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.members[path] = true
}

// RateLimitToken answers the next merge request creation made with token with 429.
func (g *GitLab) RateLimitToken(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limit[token] = true
}

func (g *GitLab) bare(path string) string { return filepath.Join(g.root, path+".git") }

// AddRepo creates a bare repository whose main branch holds files.
func (g *GitLab) AddRepo(path string, files map[string]string) {
	g.t.Helper()
	bare := g.bare(path)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		g.t.Fatal(err)
	}
	git(g.t, bare, "init", "--bare", "-b", "main")
	git(g.t, bare, "config", "uploadpack.allowFilter", "true")
	g.Commit(path, files)
}

// Commit adds a commit on main with files (a human pushing to the default branch).
func (g *GitLab) Commit(path string, files map[string]string) {
	g.t.Helper()
	work := g.t.TempDir()
	git(g.t, work, "clone", "-q", g.bare(path), "w")
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

// FileAt returns the content of file on branch, or ok=false when absent.
func (g *GitLab) FileAt(path, branch, file string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+file)
	cmd.Dir = g.bare(path)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func (g *GitLab) hasBranch(path, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = g.bare(path)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// Calls is the number of project-addressed API requests for path.
func (g *GitLab) Calls(path string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[path]
}

// PRs lists the merge requests opened in path.
func (g *GitLab) PRs(path string) []GitLabPR {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []GitLabPR
	for _, p := range g.prs[path] {
		out = append(out, *p)
	}
	return out
}

// Requests is "METHOD escaped-path" of every API request so far.
func (g *GitLab) Requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requests...)
}

// Auths is the Authorization header of every API request so far.
func (g *GitLab) Auths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auths...)
}

func (g *GitLab) fail(w http.ResponseWriter, code int, msg string) {
	g.t.Logf("fake gitlab: %d %s", code, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func inScope(scope, path string) bool {
	return scope == "" || path == scope || strings.HasPrefix(path, scope+"/")
}

func (g *GitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	scope, known := g.tokens[tok]
	g.requests = append(g.requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	g.auths = append(g.auths, r.Header.Get("Authorization"))
	g.mu.Unlock()
	if !ok || !known {
		g.fail(w, http.StatusUnauthorized, "401 Unauthorized")
		return
	}
	segs := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	if len(segs) < 3 || segs[0] != "api" || segs[1] != "v4" || segs[2] != "projects" {
		g.fail(w, http.StatusNotFound, "404 Not Found")
		return
	}
	if len(segs) == 3 && r.Method == http.MethodGet {
		g.listProjects(w, r, scope)
		return
	}
	if len(segs) < 5 {
		g.fail(w, http.StatusNotFound, "404 Not Found")
		return
	}
	project, err := url.PathUnescape(segs[3])
	if err != nil || !inScope(scope, project) {
		g.fail(w, http.StatusNotFound, "404 Project Not Found")
		return
	}
	if _, err := os.Stat(g.bare(project)); err != nil {
		g.fail(w, http.StatusNotFound, "404 Project Not Found")
		return
	}
	g.mu.Lock()
	g.calls[project]++
	limited := g.limit[tok] && r.Method == http.MethodPost
	if limited {
		g.limit[tok] = false
	}
	g.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "1")
		g.fail(w, http.StatusTooManyRequests, "Retry later")
		return
	}
	switch {
	case len(segs) == 5 && segs[4] == "merge_requests":
		g.mergeRequests(w, r, project)
	case len(segs) == 7 && segs[4] == "merge_requests" && segs[6] == "merge" && r.Method == http.MethodPut:
		g.merge(w, r, project, segs[5])
	default:
		g.fail(w, http.StatusNotFound, "404 Not Found")
	}
}

func (g *GitLab) listProjects(w http.ResponseWriter, r *http.Request, scope string) {
	q := r.URL.Query()
	if q.Get("membership") != "true" {
		g.fail(w, http.StatusBadRequest, "membership=true required")
		return
	}
	per, _ := strconv.Atoi(q.Get("per_page"))
	page, _ := strconv.Atoi(q.Get("page"))
	if per <= 0 || per > 100 {
		per = 20
	}
	if page <= 0 {
		page = 1
	}
	g.mu.Lock()
	var all []string
	for p := range g.members {
		if inScope(scope, p) {
			all = append(all, p)
		}
	}
	g.mu.Unlock()
	sort.Strings(all)
	out := []map[string]any{}
	for i := (page - 1) * per; i < len(all) && i < page*per; i++ {
		out = append(out, map[string]any{"path_with_namespace": all[i]})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (g *GitLab) mrJSON(project string, p *GitLabPR) map[string]any {
	return map[string]any{"iid": p.IID, "id": 1000 + p.IID, "state": "opened", "title": p.Title,
		"source_branch": p.Head, "target_branch": p.Base,
		"web_url": "https://gitlab.example/" + project + "/-/merge_requests/" + strconv.Itoa(p.IID)}
}

func (g *GitLab) mergeRequests(w http.ResponseWriter, r *http.Request, project string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("state") != "opened" {
			g.fail(w, http.StatusBadRequest, "state=opened required")
			return
		}
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if per <= 0 || per > 100 {
			per = 20
		}
		if page <= 0 {
			page = 1
		}
		g.mu.Lock()
		out := []map[string]any{}
		for i, p := range g.prs[project] {
			if i >= (page-1)*per && i < page*per {
				out = append(out, g.mrJSON(project, p))
			}
		}
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var in struct {
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
			Title        string `json:"title"`
			Description  string `json:"description"`
			Labels       string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.SourceBranch == "" || in.TargetBranch == "" || in.Title == "" {
			g.fail(w, http.StatusBadRequest, "400 Bad request - source_branch, target_branch and title are required")
			return
		}
		for _, ref := range []string{in.SourceBranch, in.TargetBranch} {
			if !g.hasBranch(project, ref) {
				g.fail(w, http.StatusBadRequest, "branch "+ref+" does not exist")
				return
			}
		}
		g.mu.Lock()
		p := &GitLabPR{Project: project, Head: in.SourceBranch, Base: in.TargetBranch, Title: in.Title, Description: in.Description,
			Labels: in.Labels, IID: len(g.prs[project]) + 1, Draft: strings.HasPrefix(in.Title, "Draft:")}
		g.prs[project] = append(g.prs[project], p)
		resp := g.mrJSON(project, p)
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	default:
		g.fail(w, http.StatusMethodNotAllowed, "405 Method Not Allowed")
	}
}

func (g *GitLab) merge(w http.ResponseWriter, r *http.Request, project, iid string) {
	n, err := strconv.Atoi(iid)
	var in map[string]any
	if err != nil || json.NewDecoder(r.Body).Decode(&in) != nil {
		g.fail(w, http.StatusBadRequest, "bad merge request")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[project] {
		if p.IID == n {
			// No pipeline here, so the answer is the real one for "merge when checks pass"
			// being requested: the request stays open with the flag set.
			p.AutoMerge = in["auto_merge"] == true || in["merge_when_pipeline_succeeds"] == true
			p.MergeBody = in
			_ = json.NewEncoder(w).Encode(g.mrJSON(project, p))
			return
		}
	}
	g.fail(w, http.StatusNotFound, "404 Merge Request Not Found")
}
