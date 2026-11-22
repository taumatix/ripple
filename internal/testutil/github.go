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
}

// GitHub is a fake GitHub: the REST API for apps, pulls and GraphQL over HTTP, and
// bare git repositories served from the local disk (GitBase is a file:// URL).
type GitHub struct {
	APIURL  string
	GitBase string
	Auth    *github.AppAuth

	t    *testing.T
	root string
	key  *rsa.PrivateKey

	mu      sync.Mutex
	calls   map[string]int
	prs     map[string][]*PR
	graphql int
	tokens  int
	// RateLimitOnce makes the next pulls POST answer 429 with Retry-After: 1.
	RateLimitOnce bool
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
	g.Commit(repo, files)
}

// Commit adds a commit on main with files (a human pushing to the default branch).
func (g *GitHub) Commit(repo string, files map[string]string) {
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

// Branches lists branches of the repo other than main.
func (g *GitHub) Branches(repo string) []string {
	out := git(g.t, g.bare(repo), "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	var bs []string
	for _, b := range strings.Fields(out) {
		if b != "main" {
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
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_fake" + strconv.Itoa(n), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		return
	}
	if !strings.HasPrefix(auth, "ghs_fake") {
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
	if len(parts) < 3 || (parts[2] != "pulls" && !(len(parts) == 5 && parts[2] == "issues" && parts[4] == "labels")) {
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
	limit := g.RateLimitOnce && r.Method == http.MethodPost
	if limit {
		g.RateLimitOnce = false
	}
	g.mu.Unlock()
	if limit {
		w.Header().Set("Retry-After", "1")
		g.fail(w, http.StatusTooManyRequests, "slow down")
		return
	}
	if parts[2] == "issues" {
		g.handleLabels(w, r, repo, parts[3])
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.mu.Lock()
		out := []map[string]any{}
		for _, p := range g.prs[repo] {
			out = append(out, prJSON(repo, p))
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

func (g *GitHub) handleLabels(w http.ResponseWriter, r *http.Request, repo, num string) {
	var in struct{ Labels []string }
	n, err := strconv.Atoi(num)
	if r.Method != http.MethodPost || err != nil || json.NewDecoder(r.Body).Decode(&in) != nil {
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

func prJSON(repo string, p *PR) map[string]any {
	return map[string]any{
		"number": p.Number, "html_url": fmt.Sprintf("https://github.example/%s/pull/%d", repo, p.Number),
		"node_id": fmt.Sprintf("PR_%s#%d", repo, p.Number), "state": "open",
		"head": map[string]any{"ref": p.Head}, "base": map[string]any{"ref": p.Base},
	}
}
