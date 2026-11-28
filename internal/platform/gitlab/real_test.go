//go:build gitlab

package gitlab_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitlab"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

// The GitLab host against a real GitLab CE (the gitlab/gitlab-ce image), not the fake: the
// .github/workflows/hosts-gitlab-gerrit.yml `gitlab` job starts it, runs scripts/ci-gitlab-bootstrap.sh and then
//
//	RIPPLE_TEST_GITLAB_URL=http://127.0.0.1:8929 RIPPLE_TEST_GITLAB_TOKEN=<root personal access token> \
//	  go test -tags gitlab -run Real ./internal/platform/gitlab -v
//
// Each fixture gets its own subgroup of a throwaway group and a group access token for it, so the host is run
// the way a deployment runs it: a group token, repos in a subgroup, discovery by membership. GitLab's webhook
// posts to a relay served by the test (project webhooks, which CE has), which records each real delivery
// (RIPPLE_GITLAB_RECORD_DIR) and forwards it to ripple's receiver with the secret the receiver wants. Facts the
// docs do not settle are logged as "FACT ..." lines, which the workflow prints.

const (
	relayAddr  = "127.0.0.1:18999"
	hookSecret = "ci-hook-secret" // the secret token the project webhooks carry; the CI server is throwaway
)

type realEnv struct{ base, root string }

func realConfig(t *testing.T) realEnv {
	t.Helper()
	e := realEnv{strings.TrimRight(os.Getenv("RIPPLE_TEST_GITLAB_URL"), "/"), os.Getenv("RIPPLE_TEST_GITLAB_TOKEN")}
	if e.base == "" || e.root == "" {
		t.Fatal("RIPPLE_TEST_GITLAB_URL / RIPPLE_TEST_GITLAB_TOKEN are not set; run this through the hosts-gitlab-gerrit workflow")
	}
	return e
}

// delivered is one webhook GitLab posted, as the relay saw it.
type delivered struct {
	Project, Ref, After string
	Forwarded           bool
}

type relay struct {
	mu     sync.Mutex
	target string
	got    []*delivered
	n      atomic.Int64
}

var theRelay = &relay{}

func (r *relay) setTarget(u string) { r.mu.Lock(); r.target = u; r.mu.Unlock() }

func (r *relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	if req.Header.Get("X-Gitlab-Token") != hookSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Ref     string `json:"ref"`
		After   string `json:"after"`
		Project struct {
			Path string `json:"path_with_namespace"`
		} `json:"project"`
	}
	_ = json.Unmarshal(body, &ev)
	d := &delivered{Project: ev.Project.Path, Ref: ev.Ref, After: ev.After}
	if dir := os.Getenv("RIPPLE_GITLAB_RECORD_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		var hdr strings.Builder
		var names []string
		for k := range req.Header {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v := strings.Join(req.Header[k], ",")
			if k == "X-Gitlab-Token" {
				v = "<secret token>"
			}
			fmt.Fprintf(&hdr, "// %s: %s\n", k, v)
		}
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("event-%03d.json", r.n.Add(1))), append([]byte(fmt.Sprintf("// %s %s\n%s", req.Method, req.URL.Path, hdr.String())), body...), 0o644)
	}
	r.mu.Lock()
	target := r.target
	r.mu.Unlock()
	if target != "" {
		fwd, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
		for k, v := range req.Header {
			fwd.Header[k] = v
		}
		fwd.Header.Set("X-Gitlab-Token", hookSecret)
		if resp, err := http.DefaultClient.Do(fwd); err == nil {
			resp.Body.Close()
			d.Forwarded = resp.StatusCode == http.StatusAccepted
		}
	}
	r.mu.Lock()
	r.got = append(r.got, d)
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// await waits for the forwarded push event that moved ref of project to sha.
func (r *relay) await(t *testing.T, project, ref, sha string) {
	r.awaitDelivery(t, project, ref, sha, true)
}

// awaitDelivery waits for GitLab to deliver the push (whether or not it was forwarded): a delivery
// arrives 1-2 s after the push, so one left unawaited lands in the next test.
func (r *relay) awaitDelivery(t *testing.T, project, ref, sha string, forwarded bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, d := range r.got {
			if d.Project == project && d.Ref == ref && d.After == sha && (d.Forwarded || !forwarded) {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no forwarded push event for %s %s %s within 90s; GitLab delivered %d events", project, ref, sha, len(r.got))
}

func TestMain(m *testing.M) {
	if os.Getenv("RIPPLE_TEST_GITLAB_URL") != "" {
		ln, err := net.Listen("tcp", relayAddr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "relay:", err)
			os.Exit(1)
		}
		go http.Serve(ln, theRelay)
	}
	os.Exit(m.Run())
}

// ---- REST helpers with the root token: setup the host cannot do, so it does not depend on the code under test.

type restAPI struct {
	t     *testing.T
	e     realEnv
	token string
}

func (a restAPI) do(method, path string, in any) (int, http.Header, []byte) {
	a.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.e.base+"/api/v4/"+path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func (a restAPI) must(method, path string, in any) []byte {
	a.t.Helper()
	st, _, b := a.do(method, path, in)
	if st >= 300 {
		a.t.Fatalf("%s %s: HTTP %d: %s", method, path, st, b)
	}
	return b
}

func (a restAPI) mustJSON(method, path string, in any, out any) {
	a.t.Helper()
	if err := json.Unmarshal(a.must(method, path, in), out); err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
}

func expiry() string { return time.Now().AddDate(0, 0, 30).Format("2006-01-02") }

// gitIn runs git with the credential in the environment (not argv) and fails the test on error.
func (e realEnv) gitIn(t *testing.T, user, secret, dir string, args ...string) string {
	t.Helper()
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + secret))
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
		"GIT_AUTHOR_NAME=ripple", "GIT_AUTHOR_EMAIL=ripple@example.com", "GIT_COMMITTER_NAME=ripple", "GIT_COMMITTER_EMAIL=ripple@example.com",
		"GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", args[0], err, strings.ReplaceAll(string(out), auth, "***"))
	}
	return strings.TrimSpace(string(out))
}

// ---- the fixture

var (
	topOnce  sync.Once
	topID    int
	topPath  string
	groupSeq atomic.Int64
)

type realGitLab struct {
	t       *testing.T
	e       realEnv
	api     restAPI
	subID   int
	subPath string
	token   string // the group access token ripple uses
	proxy   *countingTransport
	client  *gitlab.Client
}

type countingTransport struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	s := r.URL.Path
	if r.URL.RawPath != "" {
		s = r.URL.RawPath
	}
	if u, err := url.PathUnescape(s); err == nil {
		s = u
	}
	// Head reads belong to discovery polling (ListRepoHeads: one branch read per project on every round),
	// not to a scan or a push; the fake leaves them out of Calls and so does this.
	polling := strings.Contains(s, "/repository/branches/")
	for p := range c.n {
		if !polling && (strings.Contains(s, "/projects/"+p+"/") || strings.HasSuffix(s, "/projects/"+p)) {
			c.n[p]++
		}
	}
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

type group struct {
	ID       int    `json:"id"`
	FullPath string `json:"full_path"`
}

func newFixture(t *testing.T) *realGitLab {
	e := realConfig(t)
	return &realGitLab{t: t, e: e, api: restAPI{t: t, e: e, token: e.root}}
}

func TestRealConformance(t *testing.T) {
	platformtest.Run(t, &realGitLab{e: realConfig(t)})
}

func (f *realGitLab) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.t = t
	f.api = restAPI{t: t, e: f.e, token: f.e.root}
	topOnce.Do(func() {
		name := fmt.Sprintf("ripple-ci-%d", time.Now().Unix())
		var gr group
		f.api.mustJSON("POST", "groups", map[string]any{"name": name, "path": name, "visibility": "private"}, &gr)
		topID, topPath = gr.ID, gr.FullPath
		f.awaitWebhooks(t)
	})
	name := fmt.Sprintf("s%d", groupSeq.Add(1))
	var sub group
	f.api.mustJSON("POST", "groups", map[string]any{"name": name, "path": name, "parent_id": topID, "visibility": "private"}, &sub)
	f.subID, f.subPath = sub.ID, sub.FullPath
	var tok struct {
		Token string `json:"token"`
	}
	f.api.mustJSON("POST", fmt.Sprintf("groups/%d/access_tokens", sub.ID), map[string]any{
		"name": "ripple", "scopes": []string{"api", "write_repository"}, "access_level": 40, "expires_at": expiry()}, &tok)
	f.token = tok.Token
	f.proxy = &countingTransport{n: map[string]int{}}
	theRelay.setTarget("")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	f.client = &gitlab.Client{BaseURL: f.e.base, Tokens: gitlab.Tokens{Default: f.token}, Now: now,
		AuthorName: "ripple", AuthorEmail: "ripple@example.com", Log: log,
		HTTP:    &http.Client{Timeout: 60 * time.Second, Transport: f.proxy},
		WorkDir: g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox}
	return gitlab.NewHost(f.client, gitlab.Receiver{SecretToken: hookSecret}, log)
}

func (f *realGitLab) Path(short string) string { return f.subPath + "/" + short }

func (f *realGitLab) AddRepo(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f.proxy.mu.Lock()
	f.proxy.n[path] = 0
	f.proxy.mu.Unlock()
	short := path[strings.LastIndex(path, "/")+1:]
	f.api.must("POST", "projects", map[string]any{"name": short, "path": short, "namespace_id": f.subID,
		"initialize_with_readme": true, "default_branch": "main"})
	f.api.must("POST", "projects/"+url.PathEscape(path)+"/hooks", map[string]any{
		"url": "http://" + relayAddr + "/hook", "token": hookSecret, "push_events": true, "enable_ssl_verification": false})
	head := f.pushFiles(t, path, "main", files)
	theRelay.awaitDelivery(t, path, "refs/heads/main", head, false)
}

// pushFiles clones the project, commits files on branch (created from main when new) and pushes it, as root.
func (f *realGitLab) pushFiles(t *testing.T, path, branch string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	f.e.gitIn(t, "root", f.e.root, dir, "clone", "-q", f.e.base+"/"+path+".git", ".")
	if branch != "main" {
		f.e.gitIn(t, "root", f.e.root, dir, "checkout", "-q", "-b", branch)
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.e.gitIn(t, "root", f.e.root, dir, "add", "-A")
	f.e.gitIn(t, "root", f.e.root, dir, "commit", "-q", "-m", "test commit")
	f.e.gitIn(t, "root", f.e.root, dir, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	return f.e.gitIn(t, "root", f.e.root, dir, "rev-parse", "HEAD")
}

// Register points the relay at the app. Discovery is one listing, so a project the credential's
// membership listing does not show yet would never register; wait for it to appear (and log how
// long, as a fact) before the suite runs Discover.
func (f *realGitLab) Register(t *testing.T, webhook string, paths ...string) {
	theRelay.setTarget(webhook)
	for _, p := range paths {
		start := time.Now()
		var seen string
		for time.Since(start) < 60*time.Second {
			req, _ := http.NewRequest("GET", f.e.base+"/api/v4/projects?membership=true&archived=false&simple=true&per_page=100", nil)
			req.Header.Set("Authorization", "Bearer "+f.token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			seen = fmt.Sprintf("HTTP %d %s", resp.StatusCode, raw)
			if strings.Contains(string(raw), `"path_with_namespace":"`+p+`"`) {
				t.Logf("FACT: project %s visible in the membership listing after %v", p, time.Since(start).Round(100*time.Millisecond))
				break
			}
			time.Sleep(time.Second)
		}
		if time.Since(start) >= 60*time.Second {
			t.Fatalf("project %s never appeared in the membership listing: %s", p, seen)
		}
	}
}

func (f *realGitLab) Push(t *testing.T, webhook, path string, files map[string]string) {
	t.Helper()
	theRelay.setTarget(webhook)
	head := f.pushFiles(t, path, "main", files)
	theRelay.await(t, path, "refs/heads/main", head)
}

func (f *realGitLab) SetArchived(t *testing.T, _, path string, archived bool) {
	verb := "unarchive"
	if archived {
		verb = "archive"
	}
	f.api.must("POST", "projects/"+url.PathEscape(path)+"/"+verb, nil)
}

type realMR struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Draft        bool   `json:"draft"`
	State        string `json:"state"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WhenPipeline bool   `json:"merge_when_pipeline_succeeds"`
	AutoMerge    bool   `json:"auto_merge_enabled"`
	Detailed     string `json:"detailed_merge_status"`
}

func (f *realGitLab) mrs(path string) []realMR {
	var ms []realMR
	f.api.mustJSON("GET", "projects/"+url.PathEscape(path)+"/merge_requests?state=all&per_page=100&order_by=created_at&sort=asc", nil, &ms)
	sort.Slice(ms, func(i, j int) bool { return ms[i].IID < ms[j].IID })
	return ms
}

func (f *realGitLab) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, m := range f.mrs(path) {
		pr := platformtest.PR{Head: m.SourceBranch, Base: m.TargetBranch, Draft: m.Draft || strings.HasPrefix(m.Title, gitlab.DraftPrefix),
			// A merge request GitLab merged at once (every check already passed) is what an auto-merge
			// request does there; see docs/platform-gitlab.md.
			AutoMerge: m.WhenPipeline || m.AutoMerge || m.State == "merged", Closed: m.State == "closed"}
		var notes []struct {
			Body   string `json:"body"`
			System bool   `json:"system"`
		}
		f.api.mustJSON("GET", fmt.Sprintf("projects/%s/merge_requests/%d/notes?per_page=100&sort=asc", url.PathEscape(path), m.IID), nil, &notes)
		for _, n := range notes {
			if !n.System {
				pr.Comments = append(pr.Comments, n.Body)
			}
		}
		out = append(out, pr)
	}
	return out
}

func (f *realGitLab) FileAt(path, branch, file string) (string, bool) {
	st, _, b := f.api.do("GET", "projects/"+url.PathEscape(path)+"/repository/files/"+url.PathEscape(file)+"/raw?ref="+url.QueryEscape(branch), nil)
	if st != http.StatusOK {
		return "", false
	}
	return string(b), true
}

func (f *realGitLab) Calls(path string) int {
	f.proxy.mu.Lock()
	defer f.proxy.mu.Unlock()
	return f.proxy.n[path]
}

// RateLimitNextPR: a real GitLab limits only when its throttle is on and the quota is spent;
// TestRealRateLimit drives that, so the suite's injected-429 test skips here.
func (f *realGitLab) RateLimitNextPR() bool { return false }

// ---- facts the docs leave open

func repoFiles() map[string]string {
	return map[string]string{"go.mod": "module example.com/facts\n\ngo 1.22\n", "main.go": "package main\n\nfunc main() {}\n"}
}

func fixtureWithRepo(t *testing.T, short string) (*realGitLab, model.Repo) {
	t.Helper()
	f := newFixture(t)
	f.Host(t, gitws.Options{WorkDir: t.TempDir()}, time.Now)
	path := f.Path(short)
	f.AddRepo(t, path, repoFiles())
	return f, model.Repo{FullName: path}
}

func rawMR(t *testing.T, f *realGitLab, path string, iid int) string {
	t.Helper()
	return string(f.api.must("GET", fmt.Sprintf("projects/%s/merge_requests/%d", url.PathEscape(path), iid), nil))
}

func TestRealDraftAndAutoMerge(t *testing.T) {
	f, repo := fixtureWithRepo(t, "facts")
	ctx := t.Context()
	f.pushFiles(t, repo.FullName, "ripple/draft", map[string]string{"draft.txt": "d\n"})
	d, err := f.client.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/draft", Base: "main", Title: "Update x", Body: "body", Draft: true, Labels: []string{"dependencies", "a,b"}})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Title  string   `json:"title"`
		Draft  bool     `json:"draft"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(rawMR(t, f, repo.FullName, d.Number)), &m); err != nil {
		t.Fatal(err)
	}
	t.Logf("FACT draft MR: title %q draft=%v labels=%v", m.Title, m.Draft, m.Labels)
	if m.Title != "Draft: Update x" || !m.Draft {
		t.Errorf("the Draft: title prefix must make a draft: %+v", m)
	}

	f.pushFiles(t, repo.FullName, "ripple/auto", map[string]string{"auto.txt": "a\n"})
	pr, err := f.client.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/auto", Base: "main", Title: "Update y"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FACT fresh MR before the auto-merge request: %s", rawMR(t, f, repo.FullName, pr.Number))
	err = f.client.RequestAutoMerge(ctx, repo, pr)
	t.Logf("FACT auto_merge on a project without pipelines: error=%v", err)
	t.Logf("FACT MR after the auto-merge request: %s", rawMR(t, f, repo.FullName, pr.Number))

	// The role the token needs: a Developer token on the protected default branch.
	var dev struct {
		Token string `json:"token"`
	}
	f.api.mustJSON("POST", "projects/"+url.PathEscape(repo.FullName)+"/access_tokens", map[string]any{
		"name": "ripple-developer", "scopes": []string{"api", "write_repository"}, "access_level": 30, "expires_at": expiry()}, &dev)
	f.pushFiles(t, repo.FullName, "ripple/dev", map[string]string{"dev.txt": "v\n"})
	dc := &gitlab.Client{BaseURL: f.e.base, Tokens: gitlab.Tokens{Default: dev.Token}}
	dpr, err := dc.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/dev", Base: "main", Title: "Update w"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FACT a Developer token opened MR !%d; auto_merge by it: error=%v", dpr.Number, dc.RequestAutoMerge(ctx, repo, dpr))

	f.api.must("PUT", "projects/"+url.PathEscape(repo.FullName), map[string]any{"only_allow_merge_if_pipeline_succeeds": true})
	f.pushFiles(t, repo.FullName, "ripple/gated", map[string]string{"gated.txt": "g\n"})
	g, err := f.client.CreatePR(ctx, repo, platform.PRSpec{Head: "ripple/gated", Base: "main", Title: "Update z"})
	if err != nil {
		t.Fatal(err)
	}
	err = f.client.RequestAutoMerge(ctx, repo, g)
	t.Logf("FACT auto_merge when pipelines must succeed and none exists: error=%v", err)
	t.Logf("FACT MR after that: %s", rawMR(t, f, repo.FullName, g.Number))
}

func TestRealTokensAreBearer(t *testing.T) {
	f, repo := fixtureWithRepo(t, "tokens")
	var proj struct {
		Token string `json:"token"`
	}
	f.api.mustJSON("POST", "projects/"+url.PathEscape(repo.FullName)+"/access_tokens", map[string]any{
		"name": "ripple-project", "scopes": []string{"api", "write_repository"}, "access_level": 40, "expires_at": expiry()}, &proj)
	for name, tok := range map[string]string{"group access token": f.token, "project access token": proj.Token, "personal access token": f.e.root} {
		a := restAPI{t: t, e: f.e, token: tok}
		st, _, body := a.do("GET", "projects/"+url.PathEscape(repo.FullName), nil)
		t.Logf("FACT Authorization: Bearer with a %s: GET project %d", name, st)
		if st != http.StatusOK {
			t.Errorf("%s: %d %s", name, st, body)
		}
		user := "oauth2"
		out := f.e.gitIn(t, user, tok, t.TempDir(), "ls-remote", f.e.base+"/"+repo.FullName+".git", "refs/heads/main")
		t.Logf("FACT git over HTTPS as %s:<%s>: ls-remote answered %d bytes", user, name, len(out))
		if out == "" {
			t.Errorf("%s: ls-remote saw no main", name)
		}
	}
	// ripple's own client with the project token, scoped as RIPPLE_GITLAB_TOKENS would.
	c := &gitlab.Client{BaseURL: f.e.base, Tokens: gitlab.Tokens{Scoped: map[string]string{repo.FullName: proj.Token}}}
	var repos []model.Repo
	var err error
	start := time.Now()
	for time.Since(start) < 60*time.Second {
		if repos, err = c.ListRepos(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(repos) > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	t.Logf("FACT a new project access token's membership listing shows its project after %v", time.Since(start).Round(100*time.Millisecond))
	t.Logf("FACT membership listing with the project token: %d repos", len(repos))
	if len(repos) != 1 || repos[0].FullName != repo.FullName {
		t.Errorf("a project token lists exactly its project: %+v", repos)
	}
}

func TestRealZRateLimit(t *testing.T) {
	f, repo := fixtureWithRepo(t, "limit")
	set := func(on bool) {
		f.api.must("PUT", "application/settings", map[string]any{
			"throttle_authenticated_api_enabled": on, "throttle_authenticated_api_requests_per_period": 5,
			"throttle_authenticated_api_period_in_seconds": 10})
	}
	set(true)
	t.Cleanup(func() {
		set(false)
		// The settings are cached by the instance for up to a minute; leave it usable.
		a := restAPI{t: t, e: f.e, token: f.token}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			if st, _, _ := a.do("GET", "projects/"+url.PathEscape(repo.FullName), nil); st == http.StatusOK {
				return
			}
			time.Sleep(5 * time.Second)
		}
	})
	a := restAPI{t: t, e: f.e, token: f.token}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		st, h, body := a.do("GET", "projects/"+url.PathEscape(repo.FullName), nil)
		if st != http.StatusTooManyRequests {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		var hs []string
		for k, v := range h {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "ratelimit") || lk == "retry-after" || lk == "content-type" {
				hs = append(hs, k+": "+strings.Join(v, ","))
			}
		}
		sort.Strings(hs)
		t.Logf("FACT real 429: headers %v body %q", hs, strings.TrimSpace(string(body)))
		_, err := f.client.ListOpenPRs(t.Context(), repo, "ripple/")
		rl, ok := err.(*platform.RateLimitError)
		if !ok {
			t.Fatalf("the client read a 429 as %T %v", err, err)
		}
		t.Logf("FACT client hold-back after the real 429: until %s (in %s); Retry-After header %q", rl.Until.Format(time.RFC3339), time.Until(rl.Until).Round(time.Second), h.Get("Retry-After"))
		if !time.Now().Before(rl.Until) {
			t.Errorf("the hold-back is not in the future: %v", rl.Until)
		}
		return
	}
	t.Fatal("no 429 within 4 minutes of the throttle being on")
}

// awaitWebhooks proves project webhooks reach the relay before any test depends on one. The setting that
// allows local webhook targets is cached by the instance for up to a minute, and a hook fired in that
// window is dropped for good (GitLab does not retry a blocked URL), so push to a canary project
// until the relay sees it.
func (f *realGitLab) awaitWebhooks(t *testing.T) {
	t.Helper()
	var p struct {
		ID   int    `json:"id"`
		Path string `json:"path_with_namespace"`
	}
	f.api.mustJSON("POST", "projects", map[string]any{"name": "canary", "path": "canary", "namespace_id": topID, "initialize_with_readme": true}, &p)
	var hook struct {
		ID int `json:"id"`
	}
	f.api.mustJSON("POST", fmt.Sprintf("projects/%d/hooks", p.ID), map[string]any{
		"url": "http://" + relayAddr + "/hook", "token": hookSecret, "push_events": true, "enable_ssl_verification": false}, &hook)
	// events logs what GitLab says it did with the hook (GET /projects/:id/hooks/:hook_id/events, 17.3+),
	// so a push that never arrives shows whether it was not fired, blocked, or refused by the relay.
	events := func() {
		var evs []struct {
			ResponseStatus string  `json:"response_status"`
			ExecutionDur   float64 `json:"execution_duration"`
			Trigger        string  `json:"trigger"`
		}
		st, _, b := f.api.do("GET", fmt.Sprintf("projects/%d/hooks/%d/events", p.ID, hook.ID), nil)
		if err := json.Unmarshal(b, &evs); st >= 300 || err != nil {
			t.Logf("hook events unavailable: HTTP %d: %.200s", st, b)
			return
		}
		if len(evs) > 5 {
			evs = evs[:5]
		}
		t.Logf("hook events (newest first, max 5): %+v", evs)
	}
	start := time.Now()
	for i := 0; time.Since(start) < 6*time.Minute; i++ {
		// A real push, not the hook-test endpoint: pushes are delivered by a background worker that
		// can lag the setting behind the web process.
		head := f.pushFiles(t, p.Path, "main", map[string]string{fmt.Sprintf("canary-%d.txt", i): "x\n"})
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
			theRelay.mu.Lock()
			got := slices.ContainsFunc(theRelay.got, func(d *delivered) bool { return d.Project == p.Path && d.After == head })
			theRelay.mu.Unlock()
			if got {
				t.Logf("FACT: the first push webhook reached the relay after %v and %d push(es)", time.Since(start).Round(time.Second), i+1)
				return
			}
		}
		events()
	}
	t.Fatal("no push webhook reached the relay within 6 minutes; the hook events above say what GitLab did")
}
