package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// headState is what the fake keeps for head polling: which repositories each App
// installation can see, and how often the listing and GraphQL head reads were served. It is
// written from docs/vendor/github/graphql-repository-heads.md and never compared with a
// real GitHub beyond that.
type headState struct {
	installs  map[int64][]string
	suspended map[int64]bool
	archived  map[string]bool
	// listCalls counts GET /app/installations, repoPages GET /installation/repositories,
	// queries each GraphQL head read, sizes the number of repositories each asked for.
	listCalls, repoPages, queries int
	sizes                         []int
	// remaining is rateLimit.remaining the next heads reads report; 0 means 5000.
	remaining int
}

func (g *GitHub) heads() *headState {
	if g.headSt == nil {
		g.headSt = &headState{installs: map[int64][]string{}, suspended: map[int64]bool{}, archived: map[string]bool{}}
	}
	return g.headSt
}

// Install makes repos visible to the App installation id (as GET /installation/repositories
// of its tokens lists them). Call it again to add more.
func (g *GitHub) Install(id int64, repos ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.heads()
	h.installs[id] = append(h.installs[id], repos...)
}

// SuspendInstallation marks the installation suspended (suspended_at set).
func (g *GitHub) SuspendInstallation(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.heads().suspended[id] = true
}

// ArchiveRepo makes the repository list as archived.
func (g *GitHub) ArchiveRepo(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.heads().archived[repo] = true
}

// SetRateLimitRemaining is the rateLimit.remaining the GraphQL head reads report from now on.
func (g *GitHub) SetRateLimitRemaining(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.heads().remaining = n
}

// HeadQueries is how many GraphQL head reads were served and how many repositories each
// asked for, in order.
func (g *GitHub) HeadQueries() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int(nil), g.heads().sizes...)
}

// HeadListCalls is how many GET /app/installations and GET /installation/repositories
// requests were served.
func (g *GitHub) HeadListCalls() (installations, repositoryPages int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.heads().listCalls, g.heads().repoPages
}

func pageOf(r *http.Request, n int) (from, to int) {
	pp, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if pp <= 0 {
		pp = 30
	}
	pp = min(pp, 100) // per_page maximum 100 (docs/vendor/github/graphql-repository-heads.md)
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pg <= 0 {
		pg = 1
	}
	from = min((pg-1)*pp, n)
	return from, min(from+pp, n)
}

// handleInstallations serves GET /app/installations: a JWT is required.
func (g *GitHub) handleInstallations(w http.ResponseWriter, r *http.Request, jwt string) {
	if !g.verifyJWT(jwt) {
		g.fail(w, http.StatusUnauthorized, "bad app JWT")
		return
	}
	g.mu.Lock()
	h := g.heads()
	h.listCalls++
	ids := make([]int64, 0, len(h.installs))
	for id := range h.installs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	from, to := pageOf(r, len(ids))
	var out []map[string]any
	for _, id := range ids[from:to] {
		var sus any
		if h.suspended[id] {
			sus = "2026-01-01T00:00:00Z"
		}
		out = append(out, map[string]any{"id": id, "account": map[string]any{"login": fmt.Sprintf("acct%d", id)}, "suspended_at": sus})
	}
	g.mu.Unlock()
	_ = json.NewEncoder(w).Encode(out)
}

// handleInstallationRepos serves GET /installation/repositories for the installation the
// token was minted for.
func (g *GitHub) handleInstallationRepos(w http.ResponseWriter, r *http.Request, token string) {
	g.mu.Lock()
	h := g.heads()
	h.repoPages++
	var inst int64
	if it := g.issued[token]; it != nil {
		inst = it.inst
	}
	repos := append([]string(nil), h.installs[inst]...)
	sort.Strings(repos)
	from, to := pageOf(r, len(repos))
	type repo = map[string]any
	var out []repo
	for i, name := range repos[from:to] {
		out = append(out, repo{"id": 1000 + from + i, "node_id": "R_" + name, "full_name": name,
			"default_branch": g.defaultBranchOf(name), "archived": h.archived[name], "disabled": false})
	}
	g.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(repos), "repositories": out})
}

// defaultBranchOf is the branch the bare repository's HEAD names; "main" when unreadable.
func (g *GitHub) defaultBranchOf(repo string) string {
	cmd := gitCmd(g.bare(repo), "symbolic-ref", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

// handleHeadsQuery answers the aliased repository(owner:, name:) { defaultBranchRef { name
// target { oid } } } read ripple batches; it reports false for any other GraphQL request.
// A repository the token's installation cannot see answers null for its alias with a NOT_FOUND
// error, as GitHub does for a repository the token cannot read (assumed, unverified).
func (g *GitHub) handleHeadsQuery(w http.ResponseWriter, r *http.Request, body []byte) bool {
	var req struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	if json.Unmarshal(body, &req) != nil || !strings.Contains(req.Query, "defaultBranchRef") {
		return false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.mu.Lock()
	h := g.heads()
	var inst int64
	if it := g.issued[token]; it != nil {
		inst = it.inst
	}
	visible := map[string]bool{}
	for _, n := range h.installs[inst] {
		visible[n] = true
	}
	remaining := h.remaining
	g.mu.Unlock()
	if remaining == 0 {
		remaining = 5000
	}
	data := map[string]any{}
	var errs []map[string]any
	n := 0
	for i := 0; ; i++ {
		owner, ok := req.Variables["o"+strconv.Itoa(i)]
		if !ok {
			break
		}
		name := owner + "/" + req.Variables["n"+strconv.Itoa(i)]
		n++
		alias := "r" + strconv.Itoa(i)
		if !visible[name] {
			data[alias] = nil
			errs = append(errs, map[string]any{"type": "NOT_FOUND", "path": []string{alias}, "message": "Could not resolve to a Repository with the name '" + name + "'."})
			continue
		}
		tip := branchTip(g.bare(name), g.defaultBranchOf(name))
		if tip == "" {
			data[alias] = map[string]any{"defaultBranchRef": nil}
			continue
		}
		data[alias] = map[string]any{"defaultBranchRef": map[string]any{"name": g.defaultBranchOf(name), "target": map[string]any{"oid": tip}}}
	}
	g.mu.Lock()
	h.queries++
	h.sizes = append(h.sizes, n)
	g.mu.Unlock()
	data["rateLimit"] = map[string]any{"cost": 1, "remaining": remaining, "resetAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	resp := map[string]any{"data": data}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	_ = json.NewEncoder(w).Encode(resp)
	return true
}

func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	return cmd
}
