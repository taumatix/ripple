//go:build scmreal

package scmmanager_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/platform/platformtest"
	"github.com/taumatix/ripple/internal/platform/scmmanager"
)

const (
	mtUser    = "application/vnd.scmm-user+json;v=2"
	mtPerms   = "application/vnd.scmm-permissionCollection+json;v=2"
	mtPRList  = "application/vnd.scmm-pullRequestCollection+json;v=2"
	mtPRBody  = "application/vnd.scmm-pullRequest+json;v=2"
	conformPW = "conformance secret"
)

func hookHost() string {
	if h := os.Getenv("RIPPLE_REAL_HOOK_HOST"); h != "" {
		return h
	}
	return "host.docker.internal"
}

// delivery is one webhook the instance sent.
type delivery struct {
	Query   string
	Headers http.Header
	Body    []byte
	Status  int // what the receiver of the app under test answered
	// Absorbed marks the first push webhook of a repository: the fixture keeps it, it does not pass
	// it to the app (the global webhook exists before the first push, so that push is announced).
	Absorbed bool
}

func (d delivery) repo() string {
	q, _ := url.ParseQuery(d.Query)
	return q.Get("namespace") + "/" + q.Get("name")
}

func (d delivery) branches() []string {
	var p struct {
		Changeset []struct {
			Branches []string `json:"branches"`
		} `json:"changeset"`
	}
	_ = json.Unmarshal(d.Body, &p)
	var out []string
	for _, c := range p.Changeset {
		out = append(out, c.Branches...)
	}
	return out
}

// forwarder receives the instance's webhooks on every interface (the instance runs in a container),
// keeps each one and passes it on unchanged to the app under test.
type forwarder struct {
	srv    *httptest.Server
	mu     sync.Mutex
	got    []delivery
	next   string // the app's receiver URL; empty until Register
	absorb map[string]bool
}

func newForwarder(t *testing.T) *forwarder {
	f := &forwarder{absorb: map[string]bool{}}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		d := delivery{Query: r.URL.RawQuery, Headers: r.Header.Clone(), Body: body, Status: http.StatusNotFound}
		f.mu.Lock()
		next := f.next
		if f.absorb[strings.ToLower(d.repo())] {
			delete(f.absorb, strings.ToLower(d.repo()))
			d.Absorbed, d.Status, next = true, http.StatusAccepted, ""
		}
		f.mu.Unlock()
		if next != "" {
			req, _ := http.NewRequest(r.Method, next+"?"+r.URL.RawQuery, bytes.NewReader(body))
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
		f.mu.Unlock()
		w.WriteHeader(d.Status)
	}))
	f.srv.Listener = ln
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	return f
}

// pattern is the global webhook's URL pattern: the instance fills in the repository.
func (f *forwarder) pattern() string {
	_, port, _ := net.SplitHostPort(f.srv.Listener.Addr().String())
	return "http://" + net.JoinHostPort(hookHost(), port) + "/hook?namespace=${repository.namespace}&name=${repository.name}"
}

func (f *forwarder) deliveries() []delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]delivery(nil), f.got...)
}

// await waits for a delivery after the first n that matches.
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

// countingProxy fronts the instance's API for the client under test: it counts the calls addressed
// to each repository (the suite's cost check) and can answer one POST that creates a pull request
// with a 429, which SCM-Manager does not do by itself.
type countingProxy struct {
	srv       *httptest.Server
	mu        sync.Mutex
	n         map[string]int
	seen      map[string][]string
	limitNext bool
	t         *testing.T
}

func newCountingProxy(t *testing.T, base string) *countingProxy {
	target, _ := url.Parse(strings.TrimSuffix(base, "/scm"))
	rp := httputil.NewSingleHostReverseProxy(target)
	c := &countingProxy{n: map[string]int{}, seen: map[string][]string{}, t: t}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/scm/api/v2/")
		if p != r.URL.Path {
			// Every REST resource of one repository has its namespace and name as consecutive path
			// segments after the resource name (repositories/ns/name/..., pull-requests/ns/name/...,
			// config/git/ns/name/...).
			segs := strings.Split(strings.Trim(p, "/"), "/")
			skip := 1
			if len(segs) > 0 && segs[0] == "config" {
				skip = 2
			}
			if len(segs) >= skip+2 {
				key := strings.ToLower(segs[skip] + "/" + segs[skip+1])
				c.mu.Lock()
				c.n[key]++
				c.seen[key] = append(c.seen[key], r.Method+" "+p)
				limit := c.limitNext && r.Method == http.MethodPost && segs[0] == "pull-requests" && len(segs) == skip+2
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

// realFixture is platformtest.Fixture over a real SCM-Manager: a fresh user per Host (so repositories
// of earlier subtests are not visible to the one under test), files in through real git, pull
// requests and files read back through the REST API, pushes arriving as the instance's own webhooks.
type realFixture struct {
	admin  *scm
	user   *scm // the fresh user: repositories are created and read as it
	key    string
	proxy  *countingProxy
	fwd    *forwarder
	suffix string
	dirs   map[string]string
	cur    *testing.T
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (f *realFixture) Host(t *testing.T, o gitws.Options, now func() time.Time) *platform.Host {
	f.cur = t
	f.admin.apiKey(t) // plugins installed once
	name := "u" + randHex(4)
	pw := "pw-" + randHex(12)
	fmt.Printf("::add-mask::%s\n", pw)
	code, _, body := f.admin.call(t, http.MethodPost, f.admin.base+"/api/v2/users", "", mtUser, map[string]any{
		"name": name, "displayName": name, "mail": name + "@example.invalid", "password": pw, "active": true, "external": false})
	t.Logf("FACT create user: %d %s", code, clip(body))
	if code != http.StatusCreated {
		t.Fatalf("creating user: %d", code)
	}
	code, _, body = f.admin.call(t, http.MethodPut, f.admin.base+"/api/v2/users/"+name+"/permissions", "", mtPerms, map[string]any{"permissions": []string{"repository:create"}})
	t.Logf("FACT grant repository:create: %d %s", code, clip(body))
	f.user = &scm{base: f.admin.base, user: name, password: pw}
	f.key = f.user.newKey(t)
	f.suffix = randHex(2)
	f.dirs = map[string]string{}
	f.fwd = newForwarder(t)
	f.proxy = newCountingProxy(t, f.admin.base)

	// The global webhook, replaced for this Host's forwarder.
	hooks := f.admin.call
	_ = hooks
	var idx struct {
		Links links `json:"_links"`
	}
	_, _, body = f.admin.call(t, http.MethodGet, f.admin.base+"/api/v2/", mtIndex, "", nil)
	_ = json.Unmarshal(body, &idx)
	cfg := map[string]any{"webhooks": []map[string]any{{"name": "SimpleWebHook", "configuration": map[string]any{
		"urlPattern": f.fwd.pattern(), "method": "AUTO", "executeOnEveryCommit": false, "sendCommitData": true,
		"headers": []map[string]any{{"key": scmmanager.SecretHeader, "value": conformPW, "concealed": true}}}}}}
	if code, _, body := f.admin.call(t, http.MethodPut, idx.Links.href("webHookConfig"), "", "application/json", cfg); code != http.StatusNoContent {
		t.Fatalf("setting the global webhook: %d %s", code, clip(body))
	}

	c := &scmmanager.Client{BaseURL: f.proxy.srv.URL + "/scm", Now: now,
		Credentials: platform.StaticCredential{Secret: f.key},
		WorkDir:     o.WorkDir, GoProxy: o.GoProxy, GoSumDB: o.GoSumDB, GoCacheDir: o.GoCacheDir, Sandbox: o.Sandbox}
	return scmmanager.NewHost(c, []byte(conformPW), slog.New(slog.DiscardHandler))
}

// Path is under the fresh user's namespace: the default namespace strategy puts every repository
// there whatever namespace the creation asks for.
func (f *realFixture) Path(short string) string { return f.user.user + "/" + short + "-" + f.suffix }

func (f *realFixture) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("ripple:"+f.key))
	full := append([]string{"-c", "http.extraHeader=" + auth, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.ReplaceAll(string(out), f.key, "<token>"))
	}
	return string(out)
}

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

func (f *realFixture) cloneURL(path string) string { return f.admin.base + "/repo/" + path }

func (f *realFixture) AddRepo(t *testing.T, path string, files map[string]string) {
	t.Helper()
	_, short, _ := strings.Cut(path, "/")
	code, _, body := f.user.call(t, http.MethodPost, f.user.base+"/api/v2/repositories", "", mtRepo,
		map[string]any{"name": short, "type": "git", "contextEntries": map[string]any{}})
	if code != http.StatusCreated {
		t.Fatalf("creating repository %s: %d %s", path, code, clip(body))
	}
	// The global webhook exists before the first push, so that push is announced: keep it.
	f.fwd.mu.Lock()
	f.fwd.absorb[strings.ToLower(path)] = true
	f.fwd.mu.Unlock()
	dir := t.TempDir()
	f.dirs[path] = dir
	writeFiles(t, dir, files)
	f.git(t, dir, "init", "-q", "-b", "main")
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "initial")
	f.git(t, dir, "push", "-q", f.cloneURL(path), "main")
}

// Register waits for the first push's webhook (kept by the forwarder), then points the forwarder at
// the app's receiver; discovery is by listing.
func (f *realFixture) Register(t *testing.T, webhook string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		f.fwd.await(t, 0, "the first push to "+p, func(d delivery) bool { return d.Absorbed && strings.EqualFold(d.repo(), p) })
	}
	f.fwd.mu.Lock()
	f.fwd.next = webhook
	f.fwd.mu.Unlock()
}

func (f *realFixture) Push(t *testing.T, _ string, path string, files map[string]string) {
	t.Helper()
	dir := f.dirs[path]
	before := len(f.fwd.deliveries())
	writeFiles(t, dir, files)
	f.git(t, dir, "add", "-A")
	f.git(t, dir, "commit", "-q", "-m", "push")
	f.git(t, dir, "push", "-q", f.cloneURL(path), "main")
	d := f.fwd.await(t, before, "push to "+path, func(d delivery) bool {
		return strings.EqualFold(d.repo(), path) && len(d.branches()) > 0 && d.branches()[0] == "main"
	})
	if d.Status != http.StatusAccepted {
		t.Fatalf("ripple's receiver answered the instance's push webhook with %d, want 202", d.Status)
	}
}

func (f *realFixture) t() *testing.T { return f.cur }

func (f *realFixture) PRs(path string) []platformtest.PR {
	t := f.t()
	var out []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
		Status string `json:"status"`
	}
	var p struct {
		Embedded struct {
			PRs []struct {
				ID     string `json:"id"`
				Source string `json:"source"`
				Target string `json:"target"`
				Status string `json:"status"`
			} `json:"pullRequests"`
		} `json:"_embedded"`
	}
	code, _, body := f.user.call(t, http.MethodGet, f.user.base+"/api/v2/pull-requests/"+path+"?status=ALL&pageSize=100", mtPRList, "", nil)
	if code != http.StatusOK {
		t.Fatalf("listing pull requests of %s: %d %s", path, code, clip(body))
	}
	_ = json.Unmarshal(body, &p)
	for _, e := range p.Embedded.PRs {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	var prs []platformtest.PR
	for _, e := range out {
		prs = append(prs, platformtest.PR{Head: e.Source, Base: e.Target, Draft: e.Status == "DRAFT", Closed: e.Status == "REJECTED"})
	}
	return prs
}

func (f *realFixture) FileAt(path, branch, file string) (string, bool) {
	t := f.t()
	code, _, body := f.user.call(t, http.MethodGet, f.user.base+"/api/v2/repositories/"+path+"/content/"+url.PathEscape(branch)+"/"+file, "*/*", "", nil)
	return string(body), code == http.StatusOK
}

func (f *realFixture) Calls(path string) int { return f.proxy.calls(path) }

func (f *realFixture) RateLimitNextPR() bool {
	f.proxy.mu.Lock()
	f.proxy.limitNext = true
	f.proxy.mu.Unlock()
	return true
}

// SetArchived: the repository object's archived flag, through the update resource.
func (f *realFixture) SetArchived(t *testing.T, _, path string, archived bool) {
	u := f.user.base + "/api/v2/repositories/" + path
	code, _, body := f.user.call(t, http.MethodGet, u, mtRepo, "", nil)
	if code != http.StatusOK {
		t.Fatalf("reading %s: %d %s", path, code, clip(body))
	}
	var repo map[string]any
	_ = json.Unmarshal(body, &repo)
	repo["archived"] = archived
	code, _, body = f.user.call(t, http.MethodPut, u, "", mtRepo, repo)
	t.Logf("FACT archive %s=%v: %d %s", path, archived, code, clip(body))
	if code != http.StatusNoContent {
		t.Fatalf("updating %s: %d %s", path, code, clip(body))
	}
}
