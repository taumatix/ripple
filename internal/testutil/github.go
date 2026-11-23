package testutil

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform/github"
)

const AppID = "424242"

// PR is a pull request the fake recorded.
type PR struct {
	Repo, Head, Base, Title, Body string
	Number                        int
	AutoMerge, Draft              bool
	Labels                        []string
	// Closed is set when the PR was closed through the API; Comments are the issue
	// comments left on it (a human's or ripple's), in order; Reviews counts review
	// comments and reviews a test added; Renamed records a title edit by a human.
	Closed   bool
	Comments []string
	Reviews  int
	Renamed  bool
	// BodyEdited records a description edit by a human; ForcePushed a force-push of
	// the head branch (the issue events and GraphQL report them, nothing else does).
	BodyEdited, ForcePushed bool
}

// GitHub is a fake GitHub: the REST API for apps, pulls and GraphQL over HTTP, and
// bare git repositories served from the local disk (GitBase is a file:// URL).
type GitHub struct {
	APIURL  string
	GitBase string
	Auth    *github.AppAuth
	// PublicReads lets a GET carrying no installation token (none, or any non-"ghs_"
	// bearer) through, as api.github.com answers an anonymous read of a public repo.
	PublicReads bool

	t    *testing.T
	root string
	key  *rsa.PrivateKey

	mu      sync.Mutex
	bearers []string
	calls   map[string]int
	prs     map[string][]*PR
	// issueState holds the issues API's records; see github_issues.go.
	issueState *issueState
	// refState holds the tags and releases API's records; see github_refs.go.
	refState *refState
	graphql  int
	tokens   int
	issued   map[string]*issuedToken
	// TokenTTL, when set, is how long a minted installation token lives. It is
	// reported to the client as expires_at and enforced by the git HTTP server.
	TokenTTL time.Duration
	// RateLimitOnce makes the next pulls POST answer 429 with Retry-After: 1.
	RateLimitOnce bool
	// FailPath makes every request whose path ends with it answer 500.
	FailPath string
	// FailClose makes closing a pull request (PATCH /pulls/{n}) answer 500.
	FailClose bool
}

func NewGitHub(t *testing.T) *GitHub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &GitHub{t: t, root: t.TempDir(), key: key, calls: map[string]int{}, prs: map[string][]*PR{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	g.APIURL = srv.URL
	g.GitBase = "file://" + g.root
	g.Auth = &github.AppAuth{AppID: AppID, Key: key, APIBase: srv.URL}
	return g
}

// KeyPEM is the App private key in PKCS#1 PEM, for tests that load it from a file.
func (g *GitHub) KeyPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(g.key)})
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

func (g *GitHub) bare(repo string) string { return filepath.Join(g.root, repo+".git") }

// AddRepo creates a bare repository whose main branch holds files.
func (g *GitHub) AddRepo(repo string, files map[string]string) {
	g.t.Helper()
	bare := g.bare(repo)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		g.t.Fatal(err)
	}
	git(g.t, bare, "init", "--bare", "-b", "main")
	// GitHub serves partial clones; so does the fake.
	git(g.t, bare, "config", "uploadpack.allowFilter", "true")
	git(g.t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	g.Commit(repo, files)
}

// DisablePartialClone makes repo's server refuse to filter, as a host that does not
// implement partial clone does: git then sends every object and warns.
func (g *GitHub) DisablePartialClone(repo string) {
	g.SetPartialClone(repo, false, false)
}

// SetPartialClone sets the two capabilities a scan without a checkout needs, apart:
// whether the server filters a clone (`filter=blob:none`) and whether it serves a
// blob asked for by object id. A host with only the first clones lazily but refuses
// the batched fetch, so the scan must fall back to a full checkout.
func (g *GitHub) SetPartialClone(repo string, filter, blobByID bool) {
	g.t.Helper()
	git(g.t, g.bare(repo), "config", "uploadpack.allowFilter", strconv.FormatBool(filter))
	git(g.t, g.bare(repo), "config", "uploadpack.allowAnySHA1InWant", strconv.FormatBool(blobByID))
}

// Commit adds a commit on main with files (a human pushing to the default branch).
// A content of the form "symlink:<target>" commits a symbolic link instead.
func (g *GitHub) Commit(repo string, files map[string]string) { g.CommitOn(repo, "main", files) }

// CommitOn adds a commit on branch, which is created from the current head when absent.
func (g *GitHub) CommitOn(repo, branch string, files map[string]string) {
	g.t.Helper()
	work := g.t.TempDir()
	git(g.t, work, "clone", "-q", g.bare(repo), "w")
	w := filepath.Join(work, "w")
	if g.hasBranch(repo, branch) {
		git(g.t, w, "checkout", "-q", "-B", branch, "origin/"+branch)
	} else {
		git(g.t, w, "checkout", "-q", "-B", branch)
	}
	for p, c := range files {
		full := filepath.Join(w, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			g.t.Fatal(err)
		}
		if target, ok := strings.CutPrefix(c, "symlink:"); ok {
			_ = os.Remove(full)
			if err := os.Symlink(target, full); err != nil {
				g.t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			g.t.Fatal(err)
		}
	}
	git(g.t, w, "add", "-A")
	git(g.t, w, "commit", "-q", "-m", "test commit")
	git(g.t, w, "push", "-q", "origin", branch)
}

// FileAt returns the content of path on branch, or ok=false when absent.
func (g *GitHub) FileAt(repo, branch, path string) (string, bool) {
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func (g *GitHub) hasBranch(repo, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	return cmd.Run() == nil
}

// RenameRepo renames a repository the way GitHub does: the new name serves the git
// data and the API, the old one answers 404, and the pull requests move with it.
func (g *GitHub) RenameRepo(from, to string) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Dir(g.bare(to)), 0o755); err != nil {
		g.t.Fatal(err)
	}
	if err := os.Rename(g.bare(from), g.bare(to)); err != nil {
		g.t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[from] {
		p.Repo = to
	}
	g.prs[to], g.calls[to] = g.prs[from], g.calls[from]
	delete(g.prs, from)
	delete(g.calls, from)
}

// SetDefaultBranch renames the default branch of repo from its current name to
// branch, as the repository settings page does (open PRs keep the base they have).
func (g *GitHub) SetDefaultBranch(repo, branch string) {
	g.t.Helper()
	old := strings.TrimSpace(git(g.t, g.bare(repo), "symbolic-ref", "--short", "HEAD"))
	git(g.t, g.bare(repo), "branch", "-m", old, branch)
}

// Branches lists branches of the repo other than its default branch.
func (g *GitHub) Branches(repo string) []string {
	def := strings.TrimSpace(git(g.t, g.bare(repo), "symbolic-ref", "--short", "HEAD"))
	out := git(g.t, g.bare(repo), "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	var bs []string
	for _, b := range strings.Fields(out) {
		if b != def {
			bs = append(bs, b)
		}
	}
	sort.Strings(bs)
	return bs
}

// Calls is the number of API requests (token minting excluded) addressed to repo.
func (g *GitHub) Calls(repo string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[repo]
}

// TotalCalls counts every repo-addressed API request.
func (g *GitHub) TotalCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		n += c
	}
	return n
}

func (g *GitHub) PRs(repo string) []PR {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []PR
	for _, p := range g.prs[repo] {
		out = append(out, *p)
	}
	return out
}

func (g *GitHub) Tokens() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokens
}

func (g *GitHub) fail(w http.ResponseWriter, code int, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	g.t.Logf("fake github: %d %s", code, msg)
	http.Error(w, msg, code)
}

func b64d(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}

func (g *GitHub) verifyJWT(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	var hdr struct{ Alg string }
	var claims struct {
		Iss string
		Exp int64
		Iat int64
	}
	if json.Unmarshal(b64d(parts[0]), &hdr) != nil || hdr.Alg != "RS256" || json.Unmarshal(b64d(parts[1]), &claims) != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&g.key.PublicKey, crypto.SHA256, sum[:], b64d(parts[2])) != nil {
		return false
	}
	return claims.Iss == AppID && claims.Exp > time.Now().Unix() && claims.Exp-claims.Iat <= 600+60
}

func (g *GitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Header.Get("X-GitHub-Api-Version") != github.APIVersion {
		g.fail(w, http.StatusBadRequest, "missing or wrong X-GitHub-Api-Version %q", r.Header.Get("X-GitHub-Api-Version"))
		g.t.Errorf("request without X-GitHub-Api-Version %s: %s %s", github.APIVersion, r.Method, r.URL.Path)
		return
	}
	if r.Header.Get("Accept") != "application/vnd.github+json" {
		g.t.Errorf("wrong Accept header %q", r.Header.Get("Accept"))
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/app/installations/") && strings.HasSuffix(path, "/access_tokens") && r.Method == http.MethodPost {
		if !g.verifyJWT(auth) {
			g.fail(w, http.StatusUnauthorized, "bad app JWT")
			return
		}
		g.mu.Lock()
		g.tokens++
		n := g.tokens
		ttl := g.TokenTTL
		if ttl == 0 {
			ttl = time.Hour
		}
		tok := "ghs_fake" + strconv.Itoa(n)
		if g.issued == nil {
			g.issued = map[string]*issuedToken{}
		}
		g.issued[tok] = &issuedToken{expires: time.Now().Add(ttl)}
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(ttl).UTC().Format(time.RFC3339)})
		return
	}
	g.mu.Lock()
	g.bearers = append(g.bearers, auth)
	g.mu.Unlock()
	if !strings.HasPrefix(auth, "ghs_fake") && !(g.PublicReads && r.Method == http.MethodGet && !strings.HasPrefix(auth, "ghs_")) {
		g.fail(w, http.StatusUnauthorized, "not an installation token")
		return
	}
	switch {
	case path == "/graphql" && r.Method == http.MethodPost:
		g.handleGraphQL(w, r)
	case strings.HasPrefix(path, "/repos/"):
		g.handleRepo(w, r)
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, path)
	}
}

func (g *GitHub) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string
		Variables struct{ ID string }
	}
	if json.Unmarshal(body, &req) == nil && strings.Contains(req.Query, "userContentEdits") {
		id := strings.TrimPrefix(req.Variables.ID, "PR_")
		repo, num, _ := strings.Cut(id, "#")
		n, _ := strconv.Atoi(num)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.calls[repo]++
		for _, p := range g.prs[repo] {
			if p.Number == n {
				edits := 0
				if p.BodyEdited {
					edits = 1
				}
				_, _ = fmt.Fprintf(w, `{"data":{"node":{"userContentEdits":{"totalCount":%d}}}}`, edits)
				return
			}
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"Could not resolve to a node"}]}`))
		return
	}
	if json.Unmarshal(body, &req) != nil || !strings.Contains(req.Query, "enablePullRequestAutoMerge") {
		g.fail(w, http.StatusBadRequest, "unexpected graphql")
		return
	}
	// node ids are "PR_<owner/repo>#<n>"
	id := strings.TrimPrefix(req.Variables.ID, "PR_")
	repo, num, _ := strings.Cut(id, "#")
	n, _ := strconv.Atoi(num)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls[repo]++
	for _, p := range g.prs[repo] {
		if p.Number == n {
			p.AutoMerge = true
			_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":{"pullRequest":{"number":` + num + `}}}}`))
			return
		}
	}
	_, _ = w.Write([]byte(`{"errors":[{"message":"Could not resolve to a node"}]}`))
}

func (g *GitHub) handleRepo(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	if len(parts) < 3 || !(parts[2] == "pulls" || parts[2] == "issues" || parts[2] == "labels" || parts[2] == "tags" || parts[2] == "git" || parts[2] == "releases") {
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
	limit := g.RateLimitOnce && r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "pulls"
	if limit {
		g.RateLimitOnce = false
	}
	failing := g.FailPath != "" && strings.HasSuffix(r.URL.Path, g.FailPath)
	g.mu.Unlock()
	if limit {
		w.Header().Set("Retry-After", "1")
		g.fail(w, http.StatusTooManyRequests, "slow down")
		return
	}
	if failing {
		g.fail(w, http.StatusInternalServerError, "injected failure")
		return
	}
	if parts[2] == "labels" && len(parts) == 3 && r.Method == http.MethodPost {
		g.handleLabelCreate(w, r, repo)
		return
	}
	if parts[2] == "issues" && len(parts) < 5 {
		g.handleIssues(w, r, repo, parts)
		return
	}
	if parts[2] == "tags" || parts[2] == "git" || parts[2] == "releases" {
		g.handleRefs(w, r, repo, parts)
		return
	}
	if len(parts) > 3 {
		g.handleSub(w, r, repo, parts[2:])
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.mu.Lock()
		out := []map[string]any{}
		for _, p := range g.prs[repo] {
			if !p.Closed {
				out = append(out, g.withHeadSHA(repo, p, prJSON(repo, p)))
			}
		}
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var in struct {
			Title, Head, Base, Body string
			Draft                   bool
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" || in.Head == "" || in.Base == "" {
			g.fail(w, http.StatusUnprocessableEntity, "bad pull request body")
			return
		}
		for _, ref := range []string{in.Head, in.Base} {
			if !g.hasBranch(repo, ref) {
				g.fail(w, http.StatusUnprocessableEntity, "branch %q does not exist in %s", ref, repo)
				return
			}
		}
		g.mu.Lock()
		p := &PR{Repo: repo, Head: in.Head, Base: in.Base, Title: in.Title, Body: in.Body, Draft: in.Draft, Number: len(g.prs[repo]) + 1}
		g.prs[repo] = append(g.prs[repo], p)
		resp := prJSON(repo, p)
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	default:
		g.fail(w, http.StatusMethodNotAllowed, "method")
	}
}

// handleSub serves /pulls/{n}[/commits|/reviews|/comments] and
// /issues/{n}/{labels|comments|events}; sub is the path after /repos/o/r/.
func (g *GitHub) handleSub(w http.ResponseWriter, r *http.Request, repo string, sub []string) {
	n, err := strconv.Atoi(sub[1])
	g.mu.Lock()
	defer g.mu.Unlock()
	var pr *PR
	for _, p := range g.prs[repo] {
		if err == nil && p.Number == n {
			pr = p
		}
	}
	if pr == nil {
		g.fail(w, http.StatusNotFound, "no pull request %s", sub[1])
		return
	}
	page := func(items []any) {
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if per < 1 {
			per = 30
		}
		if pg < 1 {
			pg = 1
		}
		lo, hi := min((pg-1)*per, len(items)), min(pg*per, len(items))
		_ = json.NewEncoder(w).Encode(append([]any{}, items[lo:hi]...))
	}
	what := ""
	if len(sub) == 3 {
		what = sub[2]
	}
	switch {
	case sub[0] == "pulls" && what == "" && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(prJSON(repo, pr))
	case sub[0] == "pulls" && what == "" && r.Method == http.MethodPatch && g.FailClose:
		g.fail(w, http.StatusInternalServerError, "injected close failure")
	case sub[0] == "pulls" && what == "" && r.Method == http.MethodPatch:
		var in struct{ State string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.State != "closed" {
			g.fail(w, http.StatusUnprocessableEntity, "bad pull request update")
			return
		}
		pr.Closed = true
		_ = json.NewEncoder(w).Encode(prJSON(repo, pr))
	case sub[0] == "pulls" && what == "commits" && r.Method == http.MethodGet:
		var items []any
		for _, c := range g.branchCommits(repo, pr) {
			items = append(items, c)
		}
		page(items)
	case sub[0] == "pulls" && (what == "reviews" || what == "comments") && r.Method == http.MethodGet:
		items := make([]any, pr.Reviews)
		for i := range items {
			items[i] = map[string]any{"id": i + 1}
		}
		page(items)
	case sub[0] == "issues" && what == "labels" && r.Method == http.MethodPost:
		var in struct{ Labels []string }
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			g.fail(w, http.StatusUnprocessableEntity, "bad labels request")
			return
		}
		pr.Labels = append(pr.Labels, in.Labels...)
		_ = json.NewEncoder(w).Encode([]any{})
	case sub[0] == "issues" && what == "comments" && r.Method == http.MethodGet:
		var items []any
		for i, c := range pr.Comments {
			items = append(items, map[string]any{"id": i + 1, "body": c})
		}
		page(items)
	case sub[0] == "issues" && what == "comments" && r.Method == http.MethodPost:
		var in struct{ Body string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
			g.fail(w, http.StatusUnprocessableEntity, "bad comment")
			return
		}
		pr.Comments = append(pr.Comments, in.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": len(pr.Comments), "body": in.Body})
	case sub[0] == "issues" && what == "events" && r.Method == http.MethodGet:
		items := []any{map[string]any{"event": "labeled"}}
		if pr.Renamed {
			items = append(items, map[string]any{"event": "renamed"})
		}
		if pr.ForcePushed {
			items = append(items, map[string]any{"event": "head_ref_force_pushed"})
		}
		page(items)
	default:
		g.fail(w, http.StatusNotFound, "unknown route %s %s", r.Method, r.URL.Path)
	}
}

// branchCommits lists the commits the PR's head has beyond its base, oldest first,
// as the pulls commits API shapes them.
func (g *GitHub) branchCommits(repo string, pr *PR) []map[string]any {
	cmd := exec.Command("git", "log", "--reverse", "--format=%an%x09%ae%x09%cn%x09%ce", pr.Base+".."+pr.Head)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	out, _ := cmd.Output()
	var cs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 4 {
			continue
		}
		cs = append(cs, map[string]any{"commit": map[string]any{
			"author":    map[string]any{"name": f[0], "email": f[1]},
			"committer": map[string]any{"name": f[2], "email": f[3]}}})
	}
	return cs
}

// HumanComment adds an issue comment to a PR, as a person would.
func (g *GitHub) HumanComment(repo string, number int, body string) {
	g.mutatePR(repo, number, func(p *PR) { p.Comments = append(p.Comments, body) })
}

// HumanClosePR closes a PR as a merge or a person's decision would, without an API call
// from ripple, so ripple sees it only as no longer open.
func (g *GitHub) HumanClosePR(repo string, number int) {
	g.mutatePR(repo, number, func(p *PR) { p.Closed = true })
}

// HumanReview records a review on a PR.
func (g *GitHub) HumanReview(repo string, number int) {
	g.mutatePR(repo, number, func(p *PR) { p.Reviews++ })
}

// HumanRename changes a PR's title and records the rename event.
func (g *GitHub) HumanRename(repo string, number int, title string) {
	g.mutatePR(repo, number, func(p *PR) { p.Title, p.Renamed = title, true })
}

// HumanEditBody records an edit of the PR description.
func (g *GitHub) HumanEditBody(repo string, number int) {
	g.mutatePR(repo, number, func(p *PR) { p.BodyEdited = true })
}

// HumanForcePush rewrites the PR's branch commit keeping its author and committer,
// so only the force-push event tells it from ripple's own push.
func (g *GitHub) HumanForcePush(repo string, number int) {
	g.t.Helper()
	var branch string
	g.mutatePR(repo, number, func(p *PR) { branch, p.ForcePushed = p.Head, true })
	work := g.t.TempDir()
	git(g.t, work, "clone", "-q", "--branch", branch, g.bare(repo), "w")
	w := filepath.Join(work, "w")
	cmd := exec.Command("git", "commit", "-q", "--amend", "--no-edit", "--allow-empty", "-m", "amended")
	cmd.Dir = w
	f := strings.Split(strings.TrimSpace(git(g.t, w, "log", "-1", "--format=%an\t%ae\t%cn\t%ce")), "\t")
	cmd.Env = append(gitEnv(), "GIT_AUTHOR_NAME="+f[0], "GIT_AUTHOR_EMAIL="+f[1], "GIT_COMMITTER_NAME="+f[2], "GIT_COMMITTER_EMAIL="+f[3])
	if out, err := cmd.CombinedOutput(); err != nil {
		g.t.Fatalf("amend: %v\n%s", err, out)
	}
	git(g.t, w, "push", "-q", "--force", "origin", branch)
}

// AddForeignPR opens a PR on a new branch shaped like ripple's that no ripple job
// opened: the branch is a copy of the first PR's branch, commit identity included.
func (g *GitHub) AddForeignPR(repo, branch, title string) {
	g.t.Helper()
	g.mu.Lock()
	src := g.prs[repo][0]
	g.mu.Unlock()
	git(g.t, g.bare(repo), "branch", branch, src.Head)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prs[repo] = append(g.prs[repo], &PR{Repo: repo, Head: branch, Base: src.Base, Title: title, Number: len(g.prs[repo]) + 1})
}

func (g *GitHub) mutatePR(repo string, number int, f func(*PR)) {
	g.t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.prs[repo] {
		if p.Number == number {
			f(p)
			return
		}
	}
	g.t.Fatalf("no pull request %d in %s", number, repo)
}

// HumanCommitOnBranch pushes one more commit, authored by a person, onto branch.
func (g *GitHub) HumanCommitOnBranch(repo, branch string) {
	g.t.Helper()
	work := g.t.TempDir()
	git(g.t, work, "clone", "-q", "--branch", branch, g.bare(repo), "w")
	w := filepath.Join(work, "w")
	if err := os.WriteFile(filepath.Join(w, "HUMAN.txt"), []byte("hand edit\n"), 0o644); err != nil {
		g.t.Fatal(err)
	}
	git(g.t, w, "add", "-A")
	git(g.t, w, "commit", "-q", "-m", "hand edit")
	git(g.t, w, "push", "-q", "origin", branch)
}

// withHeadSHA adds the head commit the listing reports (head.sha); absent when the
// branch does not exist in the bare repository.
func (g *GitHub) withHeadSHA(repo string, p *PR, m map[string]any) map[string]any {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+p.Head)
	cmd.Dir = g.bare(repo)
	cmd.Env = gitEnv()
	if out, err := cmd.Output(); err == nil {
		m["head"].(map[string]any)["sha"] = strings.TrimSpace(string(out))
	}
	return m
}

// HumanResetBranch force-pushes branch back by n commits, as a person undoing their
// own edit would. Unlike a force-push the host's issue events report, it leaves no
// trace but the moved head.
func (g *GitHub) HumanResetBranch(repo, branch string, n int) {
	g.t.Helper()
	git(g.t, g.bare(repo), "update-ref", "refs/heads/"+branch, fmt.Sprintf("refs/heads/%s~%d", branch, n))
}

func prJSON(repo string, p *PR) map[string]any {
	return map[string]any{
		"number": p.Number, "html_url": fmt.Sprintf("https://github.example/%s/pull/%d", repo, p.Number),
		"node_id": fmt.Sprintf("PR_%s#%d", repo, p.Number), "state": map[bool]string{false: "open", true: "closed"}[p.Closed],
		"title": p.Title, "draft": p.Draft,
		"head": map[string]any{"ref": p.Head, "repo": map[string]any{"full_name": repo}}, "base": map[string]any{"ref": p.Base},
	}
}

// Bearers are the Authorization values (without "Bearer ") of the requests received, in order.
func (g *GitHub) Bearers() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.bearers...)
}
