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
	// Closed is set by a state_event=close update; Comments are the user notes on the
	// merge request, ripple's own included.
	Closed   bool
	Comments []string
	// Approvals counts people who approved; Renamed, BodyEdited and ForcePushed record
	// what a person did, which GitLab reports only as system notes and diff versions.
	Approvals                        int
	Renamed, BodyEdited, ForcePushed bool
	// Pushed counts diff versions beyond the first (a push after the merge request opened).
	Pushed int
	// SystemNotes are further system notes, worded as the test says.
	SystemNotes []string
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

	mu       sync.Mutex
	tokens   map[string]string // token -> scope ("" reaches every project)
	members  map[string]bool
	archived map[string]bool
	calls    map[string]int
	prs      map[string][]*GitLabPR
	limit    map[string]bool // token -> answer its next merge request creation with 429
	// Requests records "METHOD escaped-path" of every API request, in order.
	requests []string
	// Auths records the Authorization header of every API request.
	auths []string

	// FailClose makes closing a merge request (PUT with state_event) answer 500;
	// FailNote does the same for creating a note; FailRead makes every GET whose path
	// ends with it answer 500.
	FailClose, FailNote bool
	FailRead            string
}

func NewGitLab(t *testing.T) *GitLab {
	t.Helper()
	g := &GitLab{t: t, root: t.TempDir(), tokens: map[string]string{}, members: map[string]bool{}, archived: map[string]bool{},
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

// SetArchived archives or unarchives a project. The membership listing honours its `archived`
// filter (docs/vendor/gitlab/api_projects.md: absent lists both) and, like `simple=true`, does
// not say on each project whether it is archived.
func (g *GitLab) SetArchived(path string, archived bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.archived[path] = archived
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

// HasBranch reports whether the bare repository of path has branch.
func (g *GitLab) HasBranch(path, branch string) bool { return g.hasBranch(path, branch) }

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
	case len(segs) >= 6 && segs[4] == "merge_requests" && len(segs) <= 7:
		g.mergeRequest(w, r, project, segs[5], strings.Join(segs[6:], "/"))
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
		if !inScope(scope, p) {
			continue
		}
		if want := q.Get("archived"); want == "" || want == strconv.FormatBool(g.archived[p]) {
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
	state := "opened"
	if p.Closed {
		state = "closed"
	}
	m := map[string]any{"iid": p.IID, "id": 1000 + p.IID, "state": state, "title": p.Title,
		"source_branch": p.Head, "target_branch": p.Base, "source_project_id": 7, "target_project_id": 7,
		"web_url": "https://gitlab.example/" + project + "/-/merge_requests/" + strconv.Itoa(p.IID)}
	// sha is the head commit of the source branch (api_merge_requests.md), absent here
	// when the branch is gone.
	if sha := branchTip(g.bare(project), p.Head); sha != "" {
		m["sha"] = sha
	}
	return m
}

// BranchHead is the commit branch points at in path.
func (g *GitLab) BranchHead(path, branch string) string { return branchTip(g.bare(path), branch) }

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
		var open []*GitLabPR
		for _, p := range g.prs[project] {
			if !p.Closed {
				open = append(open, p)
			}
		}
		for i, p := range open {
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

// note is a note as GitLab's notes API shapes it.
func note(id int, body string, system bool) map[string]any {
	return map[string]any{"id": id, "body": body, "system": system, "noteable_type": "MergeRequest"}
}

// notes is every note on p, oldest first: GitLab records what a person changed as
// system notes, with the wording of app/services/system_notes in GitLab's source.
func (p *GitLabPR) notes() []map[string]any {
	var out []map[string]any
	add := func(body string, system bool) { out = append(out, note(len(out)+1, body, system)) }
	if p.AutoMerge {
		add("enabled an automatic merge when all merge checks for abc123 pass", true)
	}
	if p.Renamed {
		add("<p>changed title from <code class=\"idiff\">a</code> to <code class=\"idiff\">b</code></p>", true)
	}
	if p.BodyEdited {
		add("changed the description", true)
	}
	for i := 0; i < p.Approvals; i++ {
		add("approved this merge request", true)
	}
	for i := 0; i < p.Pushed; i++ {
		add("added 1 commit", true)
	}
	for _, n := range p.SystemNotes {
		add(n, true)
	}
	for _, c := range p.Comments {
		add(c, false)
	}
	return out
}

func (g *GitLab) mergeRequest(w http.ResponseWriter, r *http.Request, project, iid, what string) {
	n, err := strconv.Atoi(iid)
	if err != nil {
		g.fail(w, http.StatusBadRequest, "bad merge request iid")
		return
	}
	if r.Method == http.MethodGet && g.FailRead != "" && strings.HasSuffix(r.URL.Path, g.FailRead) {
		g.fail(w, http.StatusInternalServerError, "injected read failure")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var pr *GitLabPR
	for _, p := range g.prs[project] {
		if p.IID == n {
			pr = p
		}
	}
	if pr == nil {
		g.fail(w, http.StatusNotFound, "404 Merge Request Not Found")
		return
	}
	page := func(items []map[string]any) {
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if per <= 0 || per > 100 {
			per = 20
		}
		if pg <= 0 {
			pg = 1
		}
		out := []map[string]any{}
		for i := (pg - 1) * per; i < len(items) && i < pg*per; i++ {
			out = append(out, items[i])
		}
		_ = json.NewEncoder(w).Encode(out)
	}
	switch {
	case what == "" && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(g.mrJSON(project, pr))
	case what == "" && r.Method == http.MethodPut:
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["state_event"] != "close" || len(in) != 1 {
			g.fail(w, http.StatusBadRequest, "only state_event=close is supported")
			return
		}
		if g.FailClose {
			g.fail(w, http.StatusInternalServerError, "injected close failure")
			return
		}
		pr.Closed = true
		_ = json.NewEncoder(w).Encode(g.mrJSON(project, pr))
	case what == "notes" && r.Method == http.MethodGet:
		page(pr.notes())
	case what == "notes" && r.Method == http.MethodPost:
		var in struct{ Body string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
			g.fail(w, http.StatusBadRequest, "body is missing")
			return
		}
		if g.FailNote {
			g.fail(w, http.StatusInternalServerError, "injected note failure")
			return
		}
		pr.Comments = append(pr.Comments, in.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(note(len(pr.Comments), in.Body, false))
	case what == "commits" && r.Method == http.MethodGet:
		var items []map[string]any
		for _, c := range branchLog(g.bare(project), pr.Base, pr.Head) {
			items = append(items, map[string]any{"author_name": c.AuthorName, "author_email": c.AuthorEmail,
				"committer_name": c.CommitterName, "committer_email": c.CommitterEmail})
		}
		page(items)
	case what == "versions" && r.Method == http.MethodGet:
		items := make([]map[string]any, 1+pr.Pushed)
		for i := range items {
			items[i] = map[string]any{"id": 100 + i, "merge_request_id": pr.IID}
		}
		page(items)
	case what == "approvals" && r.Method == http.MethodGet:
		by := make([]map[string]any, pr.Approvals)
		for i := range by {
			by[i] = map[string]any{"user": map[string]any{"id": i + 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"approved": pr.Approvals > 0, "approved_by": by})
	default:
		g.fail(w, http.StatusNotFound, "404 Not Found")
	}
}

func (g *GitLab) mutate(path string, iid int, f func(*GitLabPR)) {
	g.t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[path] {
		if p.IID == iid {
			f(p)
			return
		}
	}
	g.t.Fatalf("no merge request !%d in %s", iid, path)
}

// HumanComment adds a note to a merge request, as a person would.
func (g *GitLab) HumanComment(path string, iid int, body string) {
	g.mutate(path, iid, func(p *GitLabPR) { p.Comments = append(p.Comments, body) })
}

// SystemNote adds a system note with body, for what GitLab would record that ripple
// has no name for.
func (g *GitLab) SystemNote(path string, iid int, body string) {
	g.mutate(path, iid, func(p *GitLabPR) { p.SystemNotes = append(p.SystemNotes, body) })
}

// HumanApprove records an approval.
func (g *GitLab) HumanApprove(path string, iid int) {
	g.mutate(path, iid, func(p *GitLabPR) { p.Approvals++ })
}

// HumanRename changes the title; GitLab records it as a system note.
func (g *GitLab) HumanRename(path string, iid int, title string) {
	g.mutate(path, iid, func(p *GitLabPR) { p.Title, p.Renamed = title, true })
}

// HumanEditBody records a description edit.
func (g *GitLab) HumanEditBody(path string, iid int) {
	g.mutate(path, iid, func(p *GitLabPR) { p.BodyEdited = true })
}

// HumanCommitOnBranch pushes one more commit by a person onto the merge request's branch.
func (g *GitLab) HumanCommitOnBranch(path string, iid int) {
	g.t.Helper()
	var branch string
	g.mutate(path, iid, func(p *GitLabPR) { branch = p.Head; p.Pushed++ })
	humanCommit(g.t, g.bare(path), branch)
}

// HumanForcePush rewrites the branch's commit keeping its author and committer, so
// only the new diff version tells it from ripple's own push.
func (g *GitLab) HumanForcePush(path string, iid int) {
	g.t.Helper()
	var branch string
	g.mutate(path, iid, func(p *GitLabPR) { branch = p.Head; p.Pushed++; p.ForcePushed = true })
	forcePushAmended(g.t, g.bare(path), branch)
}

// PushBranch creates branch off main with one commit by the given identity, as ripple's
// engine would have, and opens nothing.
func (g *GitLab) PushBranch(path, branch, authorEmail string) {
	g.t.Helper()
	pushBranchCommit(g.t, g.bare(path), branch, authorEmail)
}
