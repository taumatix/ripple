//go:build gerrit

package gerrit_test

import (
	"bytes"
	"context"
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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gerrit"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

// The Gerrit host against a real Gerrit (the gerritcodereview/gerrit image), not the in-process fake:
// the .github/workflows/hosts-gitlab-gerrit.yml `gerrit` job starts it, runs scripts/ci-gerrit-bootstrap.sh
// and then
//
//	RIPPLE_TEST_GERRIT_URL=http://127.0.0.1:8080 RIPPLE_TEST_GERRIT_USER=... RIPPLE_TEST_GERRIT_PASSWORD=... \
//	  go test -tags gerrit -run Real ./internal/platform/gerrit -v
//
// Gerrit's webhooks plugin posts to a URL, so the test serves a relay on the runner that the plugin is
// configured to post to; the relay records each real event (RIPPLE_GERRIT_RECORD_DIR) and forwards it to
// ripple's receiver with the secret the receiver wants. Nothing is simulated: every push is a real git push
// and every event a real delivery of the plugin.

const (
	relayAddr  = "127.0.0.1:18999"
	relayToken = "ci-hook-secret" // the ?token= scripts/ci-gerrit-bootstrap.sh gives the plugin; the CI server is throwaway
	authorMail = "ripple@example.com"
)

type realEnv struct{ base, user, pass string }

// ripple is the account the host under test runs as: an ordinary one (ROADMAP 290, scripts/ci-gerrit-bootstrap.sh)
// with read, push to refs/for/* and Forge Author. e.user stays the administrator, which only builds the fixtures.
func (e realEnv) ripple() (user, pass string) {
	if u := os.Getenv("RIPPLE_TEST_GERRIT_RIPPLE_USER"); u != "" {
		return u, os.Getenv("RIPPLE_TEST_GERRIT_RIPPLE_PASSWORD")
	}
	return e.user, e.pass
}

func realConfig(t *testing.T) realEnv {
	t.Helper()
	e := realEnv{os.Getenv("RIPPLE_TEST_GERRIT_URL"), os.Getenv("RIPPLE_TEST_GERRIT_USER"), os.Getenv("RIPPLE_TEST_GERRIT_PASSWORD")}
	if e.base == "" {
		t.Fatal("RIPPLE_TEST_GERRIT_URL is not set; run this through the hosts-gitlab-gerrit workflow")
	}
	return e
}

// delivered is one event the plugin posted, as the relay saw it.
type delivered struct {
	Raw       []byte
	Project   string
	Ref       string
	NewRev    string
	Forwarded bool
}

// relay is the HTTP endpoint the webhooks plugin posts to.
type relay struct {
	mu     sync.Mutex
	target string // ripple's receiver URL, "" while no app is listening
	got    []*delivered
	n      atomic.Int64
}

var theRelay = &relay{}

func (r *relay) setTarget(u string) { r.mu.Lock(); r.target = u; r.mu.Unlock() }

func (r *relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	if req.URL.Query().Get("token") != relayToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Type      string `json:"type"`
		RefUpdate struct {
			Project string `json:"project"`
			RefName string `json:"refName"`
			NewRev  string `json:"newRev"`
		} `json:"refUpdate"`
	}
	_ = json.Unmarshal(body, &ev)
	d := &delivered{Raw: body, Project: ev.RefUpdate.Project, Ref: ev.RefUpdate.RefName, NewRev: ev.RefUpdate.NewRev}
	if dir := os.Getenv("RIPPLE_GERRIT_RECORD_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		hdr := fmt.Sprintf("%s %s", req.Method, req.URL.Path)
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("event-%03d-%s.json", r.n.Add(1), ev.Type)), append([]byte("// "+hdr+" content-type="+req.Header.Get("Content-Type")+" user-agent="+req.Header.Get("User-Agent")+"\n"), body...), 0o644)
	}
	r.mu.Lock()
	target := r.target
	r.mu.Unlock()
	if target != "" {
		fwd, _ := http.NewRequest(http.MethodPost, target+"?token="+url.QueryEscape(hookSecret), bytes.NewReader(body))
		fwd.Header.Set("Content-Type", "application/json")
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

// await waits for the forwarded ref-updated event that moved ref of project to rev.
func (r *relay) await(t *testing.T, project, ref, rev string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, d := range r.got {
			if d.Project == project && d.Ref == ref && d.NewRev == rev && d.Forwarded {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no forwarded ref-updated event for %s %s %s within 60s; the plugin delivered %d events", project, ref, rev, len(r.got))
}

func TestMain(m *testing.M) {
	// The plugin is told this address by the bootstrap script, so the relay starts before any test.
	if os.Getenv("RIPPLE_TEST_GERRIT_URL") != "" {
		ln, err := net.Listen("tcp", relayAddr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "relay:", err)
			os.Exit(1)
		}
		go http.Serve(ln, theRelay)
	}
	os.Exit(m.Run())
}

// ---- REST helpers: the setup the host cannot do, so it does not depend on the code under test.

type restAPI struct {
	t  *testing.T
	e  realEnv
	hc *http.Client
}

func (a restAPI) do(method, path string, in any) (int, []byte) {
	a.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimRight(a.e.base, "/")+"/a/"+path, body)
	req.SetBasicAuth(a.e.user, a.e.pass)
	if in != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(raw), []byte(")]}'")))
}

func (a restAPI) must(method, path string, in any) []byte {
	a.t.Helper()
	st, b := a.do(method, path, in)
	if st >= 300 {
		a.t.Fatalf("%s %s: HTTP %d: %s", method, path, st, b)
	}
	return b
}

// gitIn runs git with the credential in the environment (not argv) and fails the test on error.
func (e realEnv) gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	auth := base64.StdEncoding.EncodeToString([]byte(e.user + ":" + e.pass))
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
		"GIT_AUTHOR_NAME=ripple", "GIT_AUTHOR_EMAIL="+authorMail, "GIT_COMMITTER_NAME=ripple", "GIT_COMMITTER_EMAIL="+authorMail,
		"GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", args[0], err, strings.ReplaceAll(string(out), auth, "***"))
	}
	return strings.TrimSpace(string(out))
}

// ---- the fixture

var projectSeq atomic.Int64

type realGerrit struct {
	t      *testing.T
	e      realEnv
	api    restAPI
	prefix string
	proxy  *countingTransport
}

// countingTransport counts the host's own API requests per project.
type countingTransport struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	s := r.URL.Path + "?" + r.URL.RawQuery
	if u, err := url.PathUnescape(s); err == nil {
		s = u
	}
	// Head reads belong to discovery polling (ListRepoHeads: "Get HEAD" + "Get Branch" per project on
	// every round), not to a scan or a push; the fake leaves them out of Calls and so does this.
	polling := strings.HasSuffix(r.URL.Path, "/HEAD") || strings.Contains(r.URL.Path, "/branches/")
	for p := range c.n {
		if !polling && strings.Contains(s, p) {
			c.n[p]++
		}
	}
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

// TestRealRippleIsNotAnAdministrator guards ROADMAP 290: the conformance suite below proves the host's
// permissions only if the account it runs as holds nothing beyond the documented minimum.
func TestRealRippleIsNotAnAdministrator(t *testing.T) {
	e := realConfig(t)
	user, pass := e.ripple()
	if user == e.user {
		t.Fatal("RIPPLE_TEST_GERRIT_RIPPLE_USER is not set: the host would run as the administrator, which proves nothing about permissions")
	}
	a := restAPI{t: t, e: realEnv{base: e.base, user: user, pass: pass}, hc: &http.Client{Timeout: 60 * time.Second}}
	var caps map[string]any
	if err := json.Unmarshal(a.must("GET", "accounts/self/capabilities", nil), &caps); err != nil {
		t.Fatal(err)
	}
	if _, admin := caps["administrateServer"]; admin {
		t.Fatalf("the ripple account is an administrator: %v", caps)
	}
	t.Logf("FACT ordinary account capabilities: %v", caps)
}

// proposeAs checks out a fresh project as user and proposes one change with the given author email ("" is
// ripple's default), returning the push error.
func proposeAs(t *testing.T, e realEnv, user, pass, email string) error {
	t.Helper()
	f := &realGerrit{t: t, e: e, api: restAPI{t: t, e: e, hc: &http.Client{Timeout: 60 * time.Second}}}
	project := fmt.Sprintf("ripple-ci-forge-%d-%d", time.Now().Unix(), projectSeq.Add(1))
	f.proxy = &countingTransport{n: map[string]int{}}
	f.AddRepo(t, project, map[string]string{"go.mod": "module x\n"})
	c := &gerrit.Client{BaseURL: e.base, AuthorName: "ripple", AuthorEmail: email, WorkDir: t.TempDir(),
		Credentials: platform.StaticCredential{Username: user, Secret: pass}}
	ws, err := c.Checkout(context.Background(), model.Repo{FullName: project, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteFile("go.mod", []byte("module x\n\nrequire a v1\n")); err != nil {
		t.Fatal(err)
	}
	_, err = ws.CommitAndPush(context.Background(), "ripple/lib-v1", "chore(deps): bump example.com/lib")
	return err
}

// TestRealDefaultAuthorEmail (ROADMAP 291): the default .invalid email is accepted for an account holding Forge
// Author (what ripple's own CI account has) and refused with a hint naming the fix for one that does not.
func TestRealDefaultAuthorEmail(t *testing.T) {
	e := realConfig(t)
	user, pass := e.ripple()
	if err := proposeAs(t, e, user, pass, ""); err != nil {
		t.Fatalf("an account with Forge Author must be able to propose with the default email: %v", err)
	}
	bu, bp := os.Getenv("RIPPLE_TEST_GERRIT_BARE_USER"), os.Getenv("RIPPLE_TEST_GERRIT_BARE_PASSWORD")
	if bu == "" {
		t.Fatal("RIPPLE_TEST_GERRIT_BARE_USER is not set")
	}
	err := proposeAs(t, e, bu, bp, "")
	if err == nil {
		t.Fatal("an account without Forge Author and Forge Committer and without the email registered was allowed to push")
	}
	t.Logf("FACT refusal: %v", err)
	for _, want := range []string{"forge committer", "RIPPLE_GERRIT_AUTHOR_EMAIL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestRealConformance(t *testing.T) {
	e := realConfig(t)
	platformtest.Run(t, &realGerrit{e: e})
}

func (f *realGerrit) Host(t *testing.T, g gitws.Options, now func() time.Time) *platform.Host {
	f.t = t
	f.api = restAPI{t: t, e: f.e, hc: &http.Client{Timeout: 60 * time.Second}}
	f.prefix = fmt.Sprintf("ripple-ci-%d-%d", time.Now().Unix(), projectSeq.Add(1))
	f.proxy = &countingTransport{n: map[string]int{}}
	theRelay.setTarget("")
	user, pass := f.e.ripple()
	c := &gerrit.Client{BaseURL: f.e.base, Now: now, ProjectPrefix: f.prefix + "/", AuthorName: "ripple", AuthorEmail: authorMail,
		HTTP:        &http.Client{Timeout: 60 * time.Second, Transport: f.proxy},
		Credentials: platform.StaticCredential{Username: user, Secret: pass},
		WorkDir:     g.WorkDir, GoProxy: g.GoProxy, GoSumDB: g.GoSumDB, GoCacheDir: g.GoCacheDir, Sandbox: g.Sandbox,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	return gerrit.NewHost(c, []byte(hookSecret), slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func (f *realGerrit) Path(short string) string { return f.prefix + "/" + short }

func (f *realGerrit) AddRepo(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f.proxy.mu.Lock()
	f.proxy.n[path] = 0
	f.proxy.mu.Unlock()
	f.api.must("PUT", "projects/"+url.PathEscape(path), map[string]any{"description": "ripple CI", "branches": []string{"main"}, "create_empty_commit": true})
	f.api.must("PUT", "projects/"+url.PathEscape(path)+"/HEAD", map[string]any{"ref": "refs/heads/main"})
	f.pushFiles(t, path, files)
}

// pushFiles clones the project (empty or not), commits files on main and pushes it, returning the new head.
func (f *realGerrit) pushFiles(t *testing.T, path string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	clone := strings.TrimRight(f.e.base, "/") + "/a/" + path
	f.e.gitIn(t, dir, "init", "-q", "-b", "main", ".")
	f.e.gitIn(t, dir, "remote", "add", "origin", clone)
	if out := f.e.gitIn(t, dir, "ls-remote", "origin", "refs/heads/main"); out != "" {
		f.e.gitIn(t, dir, "fetch", "-q", "origin", "main")
		f.e.gitIn(t, dir, "reset", "-q", "--hard", "FETCH_HEAD")
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
	f.e.gitIn(t, dir, "add", "-A")
	f.e.gitIn(t, dir, "commit", "-q", "-m", "test commit")
	f.e.gitIn(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	return f.e.gitIn(t, dir, "rev-parse", "HEAD")
}

// Register delivers nothing: the suite runs discovery after it. It does point the relay at the receiver.
func (f *realGerrit) Register(_ *testing.T, webhook string, _ ...string) { theRelay.setTarget(webhook) }

// Push is a real push; the event ripple's receiver gets is the plugin's own, through the relay.
func (f *realGerrit) Push(t *testing.T, webhook, path string, files map[string]string) {
	t.Helper()
	theRelay.setTarget(webhook)
	head := f.pushFiles(t, path, files)
	theRelay.await(t, path, "refs/heads/main", head)
}

// SetArchived: READ_ONLY is Gerrit's archive; it sends no event.
func (f *realGerrit) SetArchived(t *testing.T, _, path string, archived bool) {
	state := "ACTIVE"
	if archived {
		state = "READ_ONLY"
	}
	f.api.must("PUT", "projects/"+url.PathEscape(path)+"/config", map[string]any{"state": state})
}

type realChange struct {
	Number   int    `json:"_number"`
	Topic    string `json:"topic"`
	Branch   string `json:"branch"`
	Status   string `json:"status"`
	WIP      bool   `json:"work_in_progress"`
	Revision string `json:"current_revision"`
	Messages []struct {
		Message string `json:"message"`
	} `json:"messages"`
}

func (f *realGerrit) changes(path string) []realChange {
	q := url.Values{"q": {"project:" + path}, "o": {"CURRENT_REVISION", "MESSAGES"}, "n": {"100"}}
	var cs []realChange
	if err := json.Unmarshal(f.api.must("GET", "changes/?"+q.Encode(), nil), &cs); err != nil {
		f.t.Fatal(err)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Number < cs[j].Number })
	return cs
}

func (f *realGerrit) PRs(path string) []platformtest.PR {
	var out []platformtest.PR
	for _, c := range f.changes(path) {
		pr := platformtest.PR{Head: c.Topic, Base: c.Branch, Draft: c.WIP, Closed: c.Status == "ABANDONED"}
		if pr.Closed {
			for _, m := range c.Messages {
				if rest, ok := strings.CutPrefix(m.Message, "Abandoned\n\n"); ok {
					pr.Comments = append(pr.Comments, rest)
				}
			}
		}
		out = append(out, pr)
	}
	return out
}

// FileAt reads from the branch, or from the current patch set of the change whose topic is branch.
func (f *realGerrit) FileAt(path, branch, file string) (string, bool) {
	for _, c := range f.changes(path) {
		if c.Topic == branch {
			return f.content(fmt.Sprintf("changes/%d/revisions/current/files/%s/content", c.Number, url.PathEscape(file)))
		}
	}
	return f.content("projects/" + url.PathEscape(path) + "/branches/" + url.PathEscape(branch) + "/files/" + url.PathEscape(file) + "/content")
}

func (f *realGerrit) content(p string) (string, bool) {
	st, b := f.api.do("GET", p, nil)
	if st != http.StatusOK {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		f.t.Fatalf("content is not base64 (rest-api-changes.md, \"Get Content\"): %q", b)
	}
	return string(raw), true
}

func (f *realGerrit) Calls(path string) int {
	f.proxy.mu.Lock()
	defer f.proxy.mu.Unlock()
	return f.proxy.n[path]
}

// RateLimitNextPR: a real Gerrit has no quota to exhaust on demand, so the suite skips that test.
func (f *realGerrit) RateLimitNextPR() bool { return false }
func (f *realGerrit) FilelessPushes()       {}
func (f *realGerrit) PushCreatesProposals() {}
