//go:build giteareal

// The tests in this file run against a real Gitea or Forgejo, started by
// .github/workflows/hosts-gitea.yml (docs/platform-gitea.md, "Real instances").
// They never skip: a missing variable is a failure, because a job that proves nothing
// must not look green.
package gitea_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitea"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
)

// instance is the real server under test and its bootstrap administrator.
type instance struct {
	url, adminUser, adminPass string
	flavour                   gitea.Flavour // the flavour the job started
	recordDir                 string
	hookHost                  string // how the instance reaches this machine
}

func realInstance(t *testing.T) *instance {
	t.Helper()
	get := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			t.Fatalf("%s is not set: this test needs the real instance hosts-gitea.yml starts", name)
		}
		return v
	}
	in := &instance{
		url:       strings.TrimRight(get("RIPPLE_REAL_GITEA_URL"), "/"),
		adminUser: get("RIPPLE_REAL_GITEA_ADMIN_USER"),
		adminPass: get("RIPPLE_REAL_GITEA_ADMIN_PASSWORD"),
		recordDir: os.Getenv("RIPPLE_REAL_RECORD_DIR"),
		hookHost:  os.Getenv("RIPPLE_REAL_HOOK_HOST"),
	}
	if in.hookHost == "" {
		in.hookHost = "host.docker.internal"
	}
	f, err := gitea.ParseFlavour(get("RIPPLE_REAL_GITEA_FLAVOUR"))
	if err != nil || f == gitea.Auto {
		t.Fatalf("RIPPLE_REAL_GITEA_FLAVOUR must be gitea or forgejo: %v", err)
	}
	in.flavour = f
	return in
}

type authFn func(*http.Request)

func basic(user, pass string) authFn { return func(r *http.Request) { r.SetBasicAuth(user, pass) } }
func token(tok string) authFn {
	return func(r *http.Request) { r.Header.Set("Authorization", "token "+tok) }
}

// call sends one API request; in is JSON-encoded when not nil, out receives the JSON answer
// when not nil. It returns the status and headers and never fails the test on a status: the
// caller decides, because several tests are about the status itself.
func (in *instance) call(t *testing.T, auth authFn, method, path string, body, out any) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, in.url+"/api/v1"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != nil {
		auth(req)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode, resp.Header, data
}

// must is call that fails the test on any non-2xx.
func (in *instance) must(t *testing.T, auth authFn, method, path string, body, out any) http.Header {
	t.Helper()
	st, h, data := in.call(t, auth, method, path, body, out)
	if st >= 300 {
		t.Fatalf("%s %s: status %d: %s", method, path, st, data)
	}
	return h
}

// newUser creates a user and an all-scope token for it. Every Host() call gets its own
// user, so repositories of one conformance test are invisible to the next one's discovery.
func (in *instance) newUser(t *testing.T) (name, tok string) {
	t.Helper()
	name = fmt.Sprintf("r%06x", rand.Intn(1<<24))
	pass := fmt.Sprintf("pw-%d-%d", time.Now().UnixNano(), rand.Int())
	in.must(t, basic(in.adminUser, in.adminPass), http.MethodPost, "/admin/users", map[string]any{
		"username": name, "email": name + "@example.invalid", "password": pass, "must_change_password": false}, nil)
	var tk struct {
		Sha1  string `json:"sha1"`
		Token string `json:"token"`
	}
	in.must(t, basic(name, pass), http.MethodPost, "/users/"+name+"/tokens", map[string]any{"name": "ripple", "scopes": []string{"all"}}, &tk)
	tok = tk.Sha1
	if tok == "" {
		tok = tk.Token
	}
	if tok == "" {
		t.Fatal("token creation answered no token")
	}
	return name, tok
}

// delivery is one webhook the instance sent.
type delivery struct {
	Headers http.Header
	Body    []byte
	Status  int // what the forwarded-to receiver answered
	// Absorbed marks the first push webhook of a repository the fixture hooked before pushing:
	// the fixture keeps it and does not pass it to the app (see realFixture.AddRepo).
	Absorbed bool
}

func (d delivery) event() string {
	for _, h := range []string{"X-Forgejo-Event", "X-Gitea-Event", "X-GitHub-Event"} {
		if v := d.Headers.Get(h); v != "" {
			return v
		}
	}
	return ""
}

// payload is the part of a delivery tests look at.
func (d delivery) payload() (ref, repo, action string) {
	var p struct {
		Ref        string `json:"ref"`
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(d.Body, &p)
	return p.Ref, p.Repository.FullName, p.Action
}

// forwarder receives the instance's webhooks on every interface (the instance runs in a
// container), keeps each one, and passes it on unchanged to the app under test so the
// receiver sees exactly what the instance sent.
type forwarder struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []delivery
	next string // the app's receiver URL; empty until Register
	// absorb are the repositories (lower case) whose first push webhook is kept, not forwarded.
	absorb map[string]bool
}

func newForwarder(t *testing.T, in *instance) *forwarder {
	f := &forwarder{absorb: map[string]bool{}}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		d := delivery{Headers: r.Header.Clone(), Body: body, Status: http.StatusNotFound}
		f.mu.Lock()
		next := f.next
		if ref, repo, _ := d.payload(); d.event() == "push" && ref == "refs/heads/main" && f.absorb[strings.ToLower(repo)] {
			delete(f.absorb, strings.ToLower(repo))
			d.Absorbed, d.Status, next = true, http.StatusAccepted, ""
		}
		f.mu.Unlock()
		if next != "" {
			req, _ := http.NewRequest(r.Method, next, bytes.NewReader(body))
			req.Header = r.Header.Clone()
			if resp, err := http.DefaultClient.Do(req); err == nil {
				d.Status = resp.StatusCode
				resp.Body.Close()
			} else {
				d.Status = http.StatusBadGateway
			}
		}
		f.mu.Lock()
		f.got = append(f.got, d)
		n := len(f.got)
		f.mu.Unlock()
		in.record(fmt.Sprintf("webhook-%03d-%s", n, d.event()), d)
		w.WriteHeader(d.Status)
	}))
	f.srv.Listener = ln
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	return f
}

// url is where the instance posts: this machine as the instance reaches it.
func (f *forwarder) url(in *instance) string {
	_, port, _ := net.SplitHostPort(f.srv.Listener.Addr().String())
	return "http://" + net.JoinHostPort(in.hookHost, port) + "/webhook"
}

func (f *forwarder) deliveries() []delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delivery(nil), f.got...)
}

// await waits for a delivery after the first n that match.
func (f *forwarder) await(t *testing.T, n int, what string, match func(delivery) bool) delivery {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range f.deliveries()[min(n, len(f.deliveries())):] {
			if match(d) {
				return d
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no webhook for %s within 45s; saw %d deliveries after the first %d", what, len(f.deliveries())-n, n)
	return delivery{}
}

var secretHeader = regexp.MustCompile(`(?i)^(authorization|cookie|x-gitea-signature|x-forgejo-signature|x-hub-signature(-256)?|x-gogs-signature)$`)

// record writes a delivery or an answer to RIPPLE_REAL_RECORD_DIR (uploaded by the job as an
// artifact, the source of the trimmed fixtures in the repository). Signature values are
// replaced: they are HMACs under a throwaway secret, but a fixture has no use for them.
func (in *instance) record(name string, v any) {
	if in.recordDir == "" {
		return
	}
	_ = os.MkdirAll(in.recordDir, 0o755)
	if d, ok := v.(delivery); ok {
		h := http.Header{}
		for k, vs := range d.Headers {
			if secretHeader.MatchString(k) {
				vs = []string{"<redacted>"}
			}
			h[k] = vs
		}
		v = map[string]any{"headers": h, "body": json.RawMessage(d.Body), "receiver_status": d.Status}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile(filepath.Join(in.recordDir, name+".json"), b, 0o644)
}

// countingProxy fronts the instance's API for the client under test: it counts the calls
// addressed to each repository (the suite's cost check) and can answer one POST to pulls with a
// 429, which no Gitea or Forgejo does by itself.
type countingProxy struct {
	srv *httptest.Server
	mu  sync.Mutex
	n   map[string]int
	t   *testing.T
	// seen is the "METHOD path" of every request per repository, so a cost the suite flags can be read.
	seen map[string][]string
	// limitNext makes the next POST .../pulls answer 429 with Retry-After from the proxy.
	limitNext bool
}

var repoPath = regexp.MustCompile(`^/api/v1/repos/([^/]+/[^/]+)(/|$)`)

func newCountingProxy(t *testing.T, in *instance) *countingProxy {
	target, _ := url.Parse(in.url)
	rp := httputil.NewSingleHostReverseProxy(target)
	c := &countingProxy{n: map[string]int{}, seen: map[string][]string{}, t: t}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := repoPath.FindStringSubmatch(r.URL.Path); m != nil {
			c.mu.Lock()
			c.n[strings.ToLower(m[1])]++
			c.seen[strings.ToLower(m[1])] = append(c.seen[strings.ToLower(m[1])], r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v1"))
			limit := c.limitNext && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls")
			if limit {
				c.limitNext = false
			}
			c.mu.Unlock()
			if limit {
				w.Header().Set("Retry-After", "120")
				http.Error(w, "injected by the test proxy", http.StatusTooManyRequests)
				return
			}
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *countingProxy) calls(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := strings.ToLower(path)
	c.t.Logf("calls so far to %s (%d): %v", path, c.n[k], c.seen[k])
	return c.n[k]
}

// realFixture is platformtest.Fixture over a real instance: files go in with real git, pull
// requests, files and comments are read back through the real API, and pushes reach ripple as
// the instance's own webhooks.
type realFixture struct {
	in *instance

	user, tok string
	proxy     *countingProxy
	fwd       *forwarder
	secret    []byte
	dirs      map[string]string
	hooked    map[string]bool
	preHook   bool // AddRepo hooks a repository before its first push (the suite's fixture, set by Host)
	hookType  string
	cur       *testing.T
}

func (f *realFixture) Host(t *testing.T, o gitws.Options, now func() time.Time) *platform.Host {
	f.cur = t
	f.user, f.tok = f.in.newUser(t)
	f.proxy = newCountingProxy(t, f.in)
	f.fwd = newForwarder(t, f.in)
	f.secret = []byte("conformance secret")
	f.dirs = map[string]string{}
	f.hooked = map[string]bool{}
	f.hookType = "gitea"
	if f.in.flavour == gitea.Forgejo {
		f.hookType = "forgejo"
	}
	f.preHook = true
	c := &gitea.Client{APIBase: f.proxy.srv.URL, GitBase: f.in.url, Now: now,
		// The username is arbitrary on purpose: docs/platform-gitea.md records which ones the instance accepts.
		Credentials: platform.StaticCredential{Username: "ripple", Secret: f.tok},
		WorkDir:     o.WorkDir, GoProxy: o.GoProxy, GoSumDB: o.GoSumDB, GoCacheDir: o.GoCacheDir, Sandbox: o.Sandbox}
	return gitea.NewHost(c, f.secret, slog.New(slog.DiscardHandler))
}

func (f *realFixture) Path(short string) string { return f.user + "/" + short }

func (f *realFixture) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "http.extraHeader=Authorization: token " + f.tok, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, redact(string(out), f.tok))
	}
	return string(out)
}

func redact(s, secret string) string { return strings.ReplaceAll(s, secret, "<token>") }

func writeFiles(t *testing.T, dir string, files map[string]string) {
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *realFixture) AddRepo(t *testing.T, path string, files map[string]string) {
	t.Helper()
	_, short, _ := strings.Cut(path, "/")
	f.in.must(t, token(f.tok), http.MethodPost, "/user/repos", map[string]any{"name": short, "private": true, "auto_init": false, "default_branch": "main"}, nil)
	if f.preHook {
		// The instance prepares a push's webhooks when it finishes processing the push, some
		// milliseconds to seconds after git returns, and nothing the API shows says when. A hook
		// created after the push can miss that moment or catch it, and if it catches it, ripple
		// (which scanned the repository when it registered it) scans it again inside the suite's
		// cost window. A hook that exists before the push is certain to get that webhook, so the
		// forwarder keeps it (Register waits for it) instead of passing it on.
		f.fwd.mu.Lock()
		f.fwd.absorb[strings.ToLower(path)] = true
		f.fwd.mu.Unlock()
		f.createHook(t, path)
	}
	dir := t.TempDir()
	f.dirs[path] = dir
	writeFiles(t, dir, files)
	f.git(t, dir, "init", "-q", "-b", "main")
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "initial")
	f.git(t, dir, "push", "-q", f.in.url+"/"+path+".git", "main")
	f.probeEmpty(t, path)
}

// probeEmpty reads the repository once, right after the first push, and logs whether the
// instance still calls it empty. It does not wait: the first push is processed after the git
// command returns, and the suite goes on to register the repository at once, which is the case
// ListRepos has to survive (ROADMAP 280, `Client.EmptySettle`). It used to wait here, which
// hid the finding from the suite.
func (f *realFixture) probeEmpty(t *testing.T, path string) {
	t.Helper()
	var r struct {
		Empty bool `json:"empty"`
	}
	if st, _, _ := f.in.call(t, token(f.tok), http.MethodGet, "/repos/"+path, nil, &r); st == http.StatusOK {
		t.Logf("FACT %s right after the first push returned: empty=%v", path, r.Empty)
	}
}

// settled waits until the instance reports the repository non-empty. Only for tests that call the
// API themselves; the suite's repositories are never waited for.
func (f *realFixture) settled(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var r struct {
			Empty bool `json:"empty"`
		}
		if st, _, _ := f.in.call(t, token(f.tok), http.MethodGet, "/repos/"+path, nil, &r); st == http.StatusOK && !r.Empty {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still empty 30s after the push", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (f *realFixture) Register(t *testing.T, webhook string, paths ...string) {
	t.Helper()
	f.fwd.mu.Lock()
	f.fwd.next = webhook
	f.fwd.mu.Unlock()
	for _, p := range paths {
		if f.hooked[p] {
			// Hooked before its first push (AddRepo): wait until the instance has sent that push's
			// webhook, which the forwarder keeps. After this nothing late is coming.
			f.fwd.await(t, 0, "the first push to "+p, func(d delivery) bool {
				_, repo, _ := d.payload()
				return d.Absorbed && strings.EqualFold(repo, p)
			})
			continue
		}
		f.settled(t, p)
		f.createHook(t, p)
	}
}

func (f *realFixture) createHook(t *testing.T, p string) {
	t.Helper()
	f.in.must(t, token(f.tok), http.MethodPost, "/repos/"+p+"/hooks", map[string]any{
		"type": f.hookType, "active": true, "events": []string{"push"},
		"config": map[string]string{"url": f.fwd.url(f.in), "content_type": "json", "secret": string(f.secret)}}, nil)
	f.hooked[p] = true
}

// Push commits to main with real git and waits for the instance's own push webhook for it, so
// the suite's "an irrelevant push costs nothing" check is made after the delivery, not before.
func (f *realFixture) Push(t *testing.T, _ string, path string, files map[string]string) {
	t.Helper()
	dir := f.dirs[path]
	before := len(f.fwd.deliveries())
	writeFiles(t, dir, files)
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "push")
	f.git(t, dir, "push", "-q", f.in.url+"/"+path+".git", "main")
	d := f.fwd.await(t, before, "push to "+path, func(d delivery) bool {
		ref, repo, _ := d.payload()
		return d.event() == "push" && ref == "refs/heads/main" && strings.EqualFold(repo, path)
	})
	if d.Status != http.StatusAccepted {
		t.Fatalf("ripple's receiver answered the instance's push webhook with %d, want 202", d.Status)
	}
}

type pullJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Merged bool   `json:"merged"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (f *realFixture) PRs(path string) []platformtest.PR {
	t := f.t()
	var ps []pullJSON
	f.in.must(t, token(f.tok), http.MethodGet, "/repos/"+path+"/pulls?state=all&limit=50", nil, &ps)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Number < ps[j].Number })
	var out []platformtest.PR
	for _, p := range ps {
		pr := platformtest.PR{Head: p.Head.Ref, Base: p.Base.Ref, Draft: strings.HasPrefix(p.Title, "WIP:"), Closed: p.State == "closed" && !p.Merged}
		var tl []struct {
			Type string `json:"type"`
		}
		f.in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/timeline?limit=50", path, p.Number), nil, &tl)
		for _, e := range tl {
			switch e.Type {
			case "pull_scheduled_merge":
				pr.AutoMerge = true
			case "pull_cancel_scheduled_merge":
				pr.AutoMerge = false
			}
		}
		var cs []struct {
			Body string `json:"body"`
		}
		f.in.must(t, token(f.tok), http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?limit=50", path, p.Number), nil, &cs)
		for _, c := range cs {
			pr.Comments = append(pr.Comments, c.Body)
		}
		out = append(out, pr)
	}
	return out
}

// t is the test of the running subtest: PRs, FileAt and Calls carry no *testing.T in the
// Fixture interface, so the fixture holds the one Host() was called with.
func (f *realFixture) t() *testing.T { return f.cur }

func (f *realFixture) FileAt(path, branch, file string) (string, bool) {
	req, _ := http.NewRequest(http.MethodGet, f.in.url+"/api/v1/repos/"+path+"/raw/"+file+"?ref="+url.QueryEscape(branch), nil)
	req.Header.Set("Authorization", "token "+f.tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode == http.StatusOK
}

func (f *realFixture) Calls(path string) int { return f.proxy.calls(path) }

func (f *realFixture) RateLimitNextPR() bool {
	f.proxy.mu.Lock()
	f.proxy.limitNext = true
	f.proxy.mu.Unlock()
	return true
}

func (f *realFixture) SetArchived(t *testing.T, _, path string, archived bool) {
	f.in.must(t, token(f.tok), http.MethodPatch, "/repos/"+path, map[string]any{"archived": archived}, nil)
}

// DeleteRepo deletes the repository. The instance announces nothing (TestRealRepositoryWebhook), so
// the suite's discovery is what notices.
func (f *realFixture) DeleteRepo(t *testing.T, _, path string) {
	f.in.must(t, token(f.tok), http.MethodDelete, "/repos/"+path, nil, nil)
}

// CreateEmptyRepo creates a repository with no commit, with a system webhook for `repository`
// events forwarded to ripple, and returns once ripple's receiver has answered the created event.
func (f *realFixture) CreateEmptyRepo(t *testing.T, webhook, path string) {
	t.Helper()
	f.fwd.mu.Lock()
	f.fwd.next = webhook
	f.fwd.mu.Unlock()
	var hook struct {
		ID int `json:"id"`
	}
	admin := basic(f.in.adminUser, f.in.adminPass)
	f.in.must(t, admin, http.MethodPost, "/admin/hooks", map[string]any{
		"type": f.hookType, "active": true, "events": []string{"repository", "push"},
		"config": map[string]string{"url": f.fwd.url(f.in), "content_type": "json", "secret": string(f.secret)}}, &hook)
	t.Cleanup(func() { f.in.call(t, admin, http.MethodDelete, fmt.Sprintf("/admin/hooks/%d", hook.ID), nil, nil) })
	before := len(f.fwd.deliveries())
	_, short, _ := strings.Cut(path, "/")
	f.in.must(t, token(f.tok), http.MethodPost, "/user/repos", map[string]any{"name": short, "private": true, "auto_init": false, "default_branch": "main"}, nil)
	d := f.fwd.await(t, before, "the created event of "+path, func(d delivery) bool {
		_, repo, action := d.payload()
		return d.event() == "repository" && action == "created" && strings.EqualFold(repo, path)
	})
	t.Logf("FACT created event for an empty repository answered %d by ripple's receiver", d.Status)
	if d.Status >= 300 {
		t.Fatalf("ripple's receiver answered the created event %d", d.Status)
	}
}

// FirstPush makes the first commit of a repository CreateEmptyRepo made.
func (f *realFixture) FirstPush(t *testing.T, path string, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	f.dirs[path] = dir
	writeFiles(t, dir, files)
	f.git(t, dir, "init", "-q", "-b", "main")
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "initial")
	f.git(t, dir, "push", "-q", f.in.url+"/"+path+".git", "main")
	f.probeEmpty(t, path)
}
