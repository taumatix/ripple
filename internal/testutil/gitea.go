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

	mu       sync.Mutex
	calls    map[string]int
	prs      map[string][]*PR
	visible  []string
	archived map[string]bool
	ids      map[int64]string // repository id -> current full name (SetRepoID)
	idLookup int
	// Merges records the raw merge request bodies, in order.
	Merges []string
	// MergeChecking makes the next that many merge requests answer 405 "Please try again later",
	// as Gitea 1.27.3 does while it is still checking whether the pull request can merge
	// (docs/vendor/gitea/real-answers.md). Each such request is also recorded in Merges.
	MergeChecking int
	// VersionCalls counts GET /api/v1/version.
	VersionCalls int
	// FailClose makes closing a pull request (PATCH with state) answer 500; FailComment
	// does the same for creating an issue comment; FailRead makes every GET whose path
	// ends with this suffix answer 500.
	FailClose, FailComment bool
	FailRead               string
	// events are timeline entries beyond the ones derived from a PR's fields, by "repo#n".
	events   map[string][]string
	requests []string
	// known is the labels each repository has, "repo\x00name" (AddLabel).
	known map[string]int
	// LabelIDsOnly makes labelling by name answer 422 and accept only label ids, as Forgejo
	// 7.0.16 does (Gitea 1.27.3 and Forgejo 17.0.2 accept names).
	LabelIDsOnly bool
}

// AddLabel gives the repository a label of that name; labelling a pull request by a name
// the repository lacks applies nothing, as on a real instance.
func (g *Gitea) AddLabel(repo, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.known == nil {
		g.known = map[string]int{}
	}
	g.known[repo+"\x00"+name] = len(g.known) + 1
}

// repoLabels answers GET /repos/{o}/{r}/labels.
func (g *Gitea) repoLabels(w http.ResponseWriter, repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []map[string]any{}
	for k, id := range g.known {
		if strings.HasPrefix(k, repo+"\x00") {
			out = append(out, map[string]any{"id": id, "name": strings.TrimPrefix(k, repo+"\x00")})
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func NewGitea(t *testing.T, forgejo bool) *Gitea {
	t.Helper()
	g := &Gitea{t: t, Forgejo: forgejo, PageMax: 50, root: t.TempDir(), calls: map[string]int{}, prs: map[string][]*PR{}, events: map[string][]string{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	g.APIURL = srv.URL
	g.GitBase = "file://" + g.root
	return g
}

// SetRepoID makes GET /api/v1/repositories/{id} answer that the repository with this id is
// called name (an id never set is a 404; the same name under another id models a
// repository deleted and recreated).
func (g *Gitea) SetRepoID(id int64, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ids == nil {
		g.ids = map[int64]string{}
	}
	g.ids[id] = name
}

// RepoIDCalls is how many GET /api/v1/repositories/{id} requests were served.
func (g *Gitea) RepoIDCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.idLookup
}

func (g *Gitea) repoByID(w http.ResponseWriter, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	g.mu.Lock()
	g.idLookup++
	name, ok := g.ids[id]
	g.mu.Unlock()
	if err != nil || !ok {
		g.fail(w, http.StatusNotFound, "no repository with id %s", idText)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "full_name": name})
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

// SetArchived archives or unarchives a repository; the listing reports it in `archived`.
func (g *Gitea) SetArchived(repo string, archived bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.archived == nil {
		g.archived = map[string]bool{}
	}
	g.archived[repo] = archived
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
	case strings.HasPrefix(path, "/repositories/") && r.Method == http.MethodGet:
		g.repoByID(w, strings.TrimPrefix(path, "/repositories/"))
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
	arch := map[string]bool{}
	for k, v := range g.archived {
		arch[k] = v
	}
	g.mu.Unlock()
	lo, hi := g.page(w, r, len(vis))
	out := []map[string]any{}
	for _, n := range vis[lo:hi] {
		out = append(out, map[string]any{"full_name": n, "default_branch": "main", "archived": arch[n], "empty": false,
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
	g.requests = append(g.requests, r.Method+" "+r.URL.Path)
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
	if r.Method == http.MethodGet && g.FailRead != "" && strings.HasSuffix(r.URL.Path, g.FailRead) {
		g.fail(w, http.StatusInternalServerError, "injected read failure")
		return
	}
	switch {
	case parts[2] == "pulls" && len(parts) == 3:
		g.pulls(w, r, repo)
	case parts[2] == "pulls" && len(parts) == 4:
		g.pull(w, r, repo, parts[3])
	case parts[2] == "pulls" && len(parts) == 5 && r.Method == http.MethodGet && (parts[4] == "commits" || parts[4] == "reviews"):
		g.pullList(w, r, repo, parts[3], parts[4])
	case parts[2] == "issues" && len(parts) == 5 && parts[4] == "comments":
		g.issueComments(w, r, repo, parts[3])
	case parts[2] == "issues" && len(parts) == 5 && parts[4] == "timeline" && r.Method == http.MethodGet:
		g.timeline(w, r, repo, parts[3])
	case parts[2] == "pulls" && len(parts) == 5 && parts[4] == "merge" && r.Method == http.MethodPost:
		g.merge(w, r, repo, parts[3])
	case parts[2] == "issues" && len(parts) == 5 && parts[4] == "labels" && r.Method == http.MethodPost:
		g.labels(w, r, repo, parts[3])
	case parts[2] == "labels" && len(parts) == 3 && r.Method == http.MethodGet:
		g.repoLabels(w, repo)
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
			out = append(out, g.giteaPR(repo, p))
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
		resp := g.giteaPR(repo, p)
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
	if g.MergeChecking > 0 {
		g.MergeChecking--
		g.fail(w, http.StatusMethodNotAllowed, "Please try again later")
		return
	}
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
	var in struct{ Labels []any }
	n, err := strconv.Atoi(num)
	if err != nil || json.NewDecoder(r.Body).Decode(&in) != nil {
		g.fail(w, http.StatusUnprocessableEntity, "bad labels request")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[repo] {
		if p.Number == n {
			// A name the repository has no label for is dropped, and the answer lists
			// only the labels now on the issue: 200 with `[]` when none matched (Gitea
			// 1.27.3 and Forgejo 17.0.2, hosts-gitea.yml TestRealLabelByName).
			applied := []map[string]any{}
			for _, l := range in.Labels {
				name, byName := l.(string)
				if byName && g.LabelIDsOnly {
					// Forgejo 7.0.16 (Gitea 1.21): the labels are int64 ids.
					g.fail(w, http.StatusUnprocessableEntity, "[]: json: cannot unmarshal string into Go struct field IssueLabelsOption.Labels of type int64")
					return
				}
				if !byName {
					id, _ := l.(float64)
					for k, v := range g.known {
						if v == int(id) && strings.HasPrefix(k, repo+"\x00") {
							name = strings.TrimPrefix(k, repo+"\x00")
						}
					}
				}
				if name != "" && g.known[repo+"\x00"+name] != 0 {
					p.Labels = append(p.Labels, name)
					applied = append(applied, map[string]any{"name": name})
				}
			}
			_ = json.NewEncoder(w).Encode(applied)
			return
		}
	}
	g.fail(w, http.StatusNotFound, "no pull request %d", n)
}

func (g *Gitea) giteaPR(repo string, p *PR) map[string]any {
	head := map[string]any{"ref": p.Head, "label": p.Head, "repo": map[string]any{"full_name": repo}}
	if sha := branchTip(g.bare(repo), p.Head); sha != "" {
		head["sha"] = sha // head.sha: the source branch's commit
	}
	return map[string]any{"number": p.Number, "html_url": fmt.Sprintf("https://gitea.example/%s/pulls/%d", repo, p.Number),
		"state": giteaState(p), "draft": p.Draft, "title": p.Title, "body": p.Body, "merged": false,
		"head": head, "base": map[string]any{"ref": p.Base}}
}

// BranchHead is the commit branch points at in repo.
func (g *Gitea) BranchHead(repo, branch string) string { return branchTip(g.bare(repo), branch) }

func giteaState(p *PR) string {
	if p.Closed {
		return "closed"
	}
	return "open"
}

// Requests lists "METHOD path" of every repository API request, in order.
func (g *Gitea) Requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requests...)
}

func (g *Gitea) find(repo, num string) *PR {
	n, _ := strconv.Atoi(num)
	for _, p := range g.prs[repo] {
		if p.Number == n {
			return p
		}
	}
	return nil
}

// pull serves GET and PATCH /repos/{o}/{r}/pulls/{index}. PATCH accepts state (and
// only closes); Gitea reports content_version, Forgejo has no such field.
func (g *Gitea) pull(w http.ResponseWriter, r *http.Request, repo, num string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.find(repo, num)
	if p == nil {
		g.fail(w, http.StatusNotFound, "no pull request %s", num)
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := g.giteaPR(repo, p)
		if !g.Forgejo {
			v := 0
			if p.BodyEdited {
				v = 1
			}
			out["content_version"] = v
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPatch:
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["state"] != "closed" {
			g.fail(w, http.StatusUnprocessableEntity, "bad edit body, want state closed")
			return
		}
		if g.FailClose {
			g.fail(w, http.StatusInternalServerError, "injected close failure")
			return
		}
		p.Closed = true
		_ = json.NewEncoder(w).Encode(g.giteaPR(repo, p))
	default:
		g.fail(w, http.StatusMethodNotAllowed, "method")
	}
}

// pullList serves a pull request's commits (from the bare repository) or reviews.
func (g *Gitea) pullList(w http.ResponseWriter, r *http.Request, repo, num, what string) {
	g.mu.Lock()
	p := g.find(repo, num)
	if p == nil {
		g.mu.Unlock()
		g.fail(w, http.StatusNotFound, "no pull request %s", num)
		return
	}
	head, base, reviews := p.Head, p.Base, p.Reviews
	g.mu.Unlock()
	var all []map[string]any
	if what == "reviews" {
		for i := 0; i < reviews; i++ {
			all = append(all, map[string]any{"id": i + 1, "state": "APPROVED", "user": map[string]any{"login": "reviewer"}})
		}
	} else {
		for i, c := range branchLog(g.bare(repo), base, head) {
			all = append(all, map[string]any{"sha": fmt.Sprintf("%040d", i+1), "commit": map[string]any{
				"author":    map[string]any{"name": c.AuthorName, "email": c.AuthorEmail},
				"committer": map[string]any{"name": c.CommitterName, "email": c.CommitterEmail}}})
		}
	}
	lo, hi := g.page(w, r, len(all))
	out := all[lo:hi]
	if out == nil {
		out = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// issueComments serves GET and POST /repos/{o}/{r}/issues/{index}/comments.
func (g *Gitea) issueComments(w http.ResponseWriter, r *http.Request, repo, num string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.find(repo, num)
	if p == nil {
		g.fail(w, http.StatusNotFound, "no issue %s", num)
		return
	}
	switch r.Method {
	case http.MethodGet:
		lo, hi := g.page(w, r, len(p.Comments))
		out := []map[string]any{}
		for i, c := range p.Comments[lo:hi] {
			out = append(out, map[string]any{"id": lo + i + 1, "body": c})
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var in struct{ Body string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
			g.fail(w, http.StatusUnprocessableEntity, "bad comment body")
			return
		}
		if g.FailComment {
			g.fail(w, http.StatusInternalServerError, "injected comment failure")
			return
		}
		p.Comments = append(p.Comments, in.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": len(p.Comments), "body": in.Body})
	default:
		g.fail(w, http.StatusMethodNotAllowed, "method")
	}
}

// timeline serves GET /repos/{o}/{r}/issues/{index}/timeline: TimelineComment entries
// whose type is Gitea's comment type name (assumed, docs/vendor/gitea/supersede-excerpts.md).
func (g *Gitea) timeline(w http.ResponseWriter, r *http.Request, repo, num string) {
	g.mu.Lock()
	p := g.find(repo, num)
	if p == nil {
		g.mu.Unlock()
		g.fail(w, http.StatusNotFound, "no issue %s", num)
		return
	}
	// Opening a pull request records the push that created its head: the first entry of a
	// fresh one on Gitea 1.27.3 and Forgejo 17.0.2 is a pull_push (hosts-gitea.yml,
	// docs/platform-gitea.md "Real instances"). A person's later push adds another.
	types := []string{"pull_push"}
	for range p.Labels {
		types = append(types, "label")
	}
	if p.AutoMerge {
		types = append(types, "pull_scheduled_merge")
	}
	for range p.Comments {
		types = append(types, "comment")
	}
	for i := 0; i < p.Reviews; i++ {
		types = append(types, "review")
	}
	if p.Renamed {
		types = append(types, "change_title")
	}
	if p.ForcePushed {
		types = append(types, "pull_push")
	}
	types = append(types, g.events[repo+"#"+num]...)
	g.mu.Unlock()
	lo, hi := g.page(w, r, len(types))
	out := []map[string]any{}
	for i, t := range types[lo:hi] {
		out = append(out, map[string]any{"id": lo + i + 1, "type": t, "user": map[string]any{"login": "someone"}})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (g *Gitea) mutate(repo string, n int, f func(*PR)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[repo] {
		if p.Number == n {
			f(p)
			return
		}
	}
	g.t.Fatalf("no pull request %d in %s", n, repo)
}

func (g *Gitea) head(repo string, n int) string {
	var h string
	g.mutate(repo, n, func(p *PR) { h = p.Head })
	return h
}

// HumanComment adds an issue comment on a pull request, as a person would.
func (g *Gitea) HumanComment(repo string, n int, body string) {
	g.mutate(repo, n, func(p *PR) { p.Comments = append(p.Comments, body) })
}

// HumanReview records a review.
func (g *Gitea) HumanReview(repo string, n int) { g.mutate(repo, n, func(p *PR) { p.Reviews++ }) }

// HumanRename changes the title; the timeline gets a change_title entry.
func (g *Gitea) HumanRename(repo string, n int, title string) {
	g.mutate(repo, n, func(p *PR) { p.Title, p.Renamed = title, true })
}

// HumanEditBody edits the description (Gitea bumps content_version; Forgejo reports nothing).
func (g *Gitea) HumanEditBody(repo string, n int) {
	g.mutate(repo, n, func(p *PR) { p.Body, p.BodyEdited = "edited by hand", true })
}

// TimelineEvent adds a timeline entry of the given type name.
func (g *Gitea) TimelineEvent(repo string, n int, typ string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := repo + "#" + strconv.Itoa(n)
	g.events[k] = append(g.events[k], typ)
}

// HumanCommitOnBranch pushes one more commit by a person onto the pull request's branch.
func (g *Gitea) HumanCommitOnBranch(repo string, n int) {
	g.t.Helper()
	humanCommit(g.t, g.bare(repo), g.head(repo, n))
	g.TimelineEvent(repo, n, "pull_push")
}

// HumanForcePush rewrites the branch's commit keeping its author and committer, so
// only the timeline's pull_push entry tells.
func (g *Gitea) HumanForcePush(repo string, n int) {
	g.t.Helper()
	forcePushAmended(g.t, g.bare(repo), g.head(repo, n))
	g.mutate(repo, n, func(p *PR) { p.ForcePushed = true })
}

// PushBranch creates branch from main with one commit authored and committed by email.
func (g *Gitea) PushBranch(repo, branch, authorEmail string) {
	g.t.Helper()
	pushBranchCommit(g.t, g.bare(repo), branch, authorEmail)
}

// HasBranch reports whether branch exists in repo.
func (g *Gitea) HasBranch(repo, branch string) bool { return g.hasBranch(repo, branch) }
