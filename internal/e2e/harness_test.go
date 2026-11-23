package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform/github"
	"github.com/taumatix/ripple/internal/registry"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/sandbox"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const secret = "It's a Secret to Everybody"

// 2026-11-23 is a Monday.
var t0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t    *testing.T
	gh   *testutil.GitHub
	git  *testutil.GitHTTP // set by opts.gitHTTP
	idx  *testutil.Index
	st   store.Store
	plat *testutil.CountingPlatform
	// client and limits are what a second replica of the server is built from.
	client *github.Client
	limits rules.Limits
	app    *app.App
	clock  *clock
	proxy  *testutil.GoProxy
	hook   string
	ticks  int
	logs   *syncBuf
	// extraHeaders are added to every webhook delivery (a GHES's own headers).
	extraHeaders http.Header

	newApp  func() *app.App
	stopApp func()
}

// syncBuf collects the engine's log so tests can assert on what was warned.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// count returns how many log lines contain every one of subs.
func (e *env) logLines(subs ...string) int {
	n := 0
	for _, l := range strings.Split(e.logs.String(), "\n") {
		ok := l != ""
		for _, s := range subs {
			ok = ok && strings.Contains(l, s)
		}
		if ok {
			n++
		}
	}
	return n
}

func defaultLimits() rules.Limits {
	return rules.Limits{MinimumReleaseAge: 0, OpenPRLimit: 5, AllowAutomerge: true}
}

// opts are the knobs a few tests turn; the zero value is what newEnv uses.
type opts struct {
	// pollEvery runs the app's own poll and job loops; zero leaves them to e.tick.
	pollEvery time.Duration
	// queue configures the event queue; nil keeps its defaults.
	queue func(q *app.Queue)
	// sandbox confines the go and git commands run on checkouts; nil is unconfined.
	sandbox sandbox.Backend
	// goDL, when set, is the Go release list the app polls for go-toolchain updates.
	goDL *testutil.GoDL
	// ghes, when set, serves the host from a fake GHES (/api/v3 and /api/graphql only).
	ghes *testutil.GHES
	// tweak edits each App newApp builds, a restart included, before it runs.
	tweak func(a *app.App)
	// gitHTTP serves the repos over git smart-HTTP (env.git) instead of file://, so a
	// test can count what a clone downloaded. Repos must be added before the env is built? No:
	// the server reads the same bare repos, so adding them later is fine.
	gitHTTP bool
	// registry, when set, is the fake container registry every image host resolves to.
	registry *testutil.Registry
	// engine adjusts the engine before it runs (server-side go settings).
	engine func(e *engine.Engine)
}

func newEnv(t *testing.T, st store.Store, lim rules.Limits) *env {
	t.Helper()
	return newEnvOpts(t, st, lim, opts{})
}

func newEnvOpts(t *testing.T, st store.Store, lim rules.Limits, o opts) *env {
	t.Helper()
	e := &env{t: t, st: st, idx: testutil.NewIndex(t), clock: &clock{t: t0}, proxy: testutil.NewGoProxy(t), logs: &syncBuf{}}
	apiBase, tokens, versions := "", github.TokenSource(nil), (*github.Versions)(nil)
	if o.ghes != nil {
		e.gh, apiBase, tokens = o.ghes.GitHub, o.ghes.APIURL, o.ghes.Auth
		versions = &github.Versions{}
		o.ghes.Auth.Versions = versions
		e.extraHeaders = http.Header{"User-Agent": {"GitHub-Hookshot/abc1234"}, "X-GitHub-Enterprise-Version": {"3.22.0"},
			"X-GitHub-Enterprise-Host": {"ghes.example.com"}, "X-Hub-Signature": {"sha1=0000000000000000000000000000000000000000"}}
	} else {
		e.gh = testutil.NewGitHub(t)
		apiBase, tokens = e.gh.APIURL, e.gh.Auth
	}
	if o.gitHTTP {
		e.git = e.gh.ServeGit()
		e.gh.GitBase = e.git.URL
	}
	gh := &github.Client{APIBase: apiBase, GitBase: e.gh.GitBase, Tokens: tokens, Versions: versions, WorkDir: t.TempDir(),
		GoProxy: e.proxy.URL, GoSumDB: "off", GoCacheDir: goCache(t), Sandbox: o.sandbox, Now: e.clock.Now,
		Log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	e.plat = &testutil.CountingPlatform{Inner: gh}
	e.client, e.limits = gh, lim
	eng := &engine.Engine{Store: st, Platform: e.plat, Limits: lim, Now: e.clock.Now, Workers: 2, Log: slog.New(slog.NewTextHandler(io.MultiWriter(testWriter{t}, e.logs), nil))}
	if o.engine != nil {
		o.engine(eng)
	}
	if o.registry != nil {
		eng.Registry = &registry.Client{HTTP: http.DefaultClient, Insecure: true, BaseURL: func(string) string { return o.registry.URL }}
	}
	e.newApp = func() *app.App {
		a := &app.App{Engine: eng, Secret: []byte(secret), Log: slog.New(slog.NewTextHandler(io.MultiWriter(testWriter{t}, e.logs), nil)),
			PollEvery: o.pollEvery, Queue: &app.Queue{},
			Watcher: &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}},
			Actions: &engine.ActionsWatcher{Engine: eng}, Docker: &engine.DockerWatcher{Engine: eng}}
		if o.goDL != nil {
			a.Toolchain = &engine.ToolchainWatcher{Engine: eng, Source: &feed.GoDL{BaseURL: o.goDL.URL}}
		}
		if o.queue != nil {
			o.queue(a.Queue)
		}
		if o.tweak != nil {
			o.tweak(a)
		}
		return a
	}
	e.startApp()
	return e
}

// startApp serves a fresh App over the env's store, platform and clock; stopApp ends
// it the way a SIGTERM does. Together they are a process restart.
func (e *env) startApp() {
	t := e.t
	e.app = e.newApp()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.hook = "http://" + ln.Addr().String() + "/webhook"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.app.Run(ctx, ln) }()
	var once sync.Once
	e.stopApp = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("app.Run: %v", err)
			}
		})
	}
	t.Cleanup(e.stopApp)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

// sign computes the header independently of the production code: HMAC-SHA256 of the
// body keyed with the secret, hex, prefixed "sha256=" (GitHub docs, "Validating webhook deliveries").
func sign(body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (e *env) deliver(event string, payload any, sig string) int {
	e.t.Helper()
	body, _ := json.Marshal(payload)
	if sig == "" {
		sig = sign(body)
	}
	req, _ := http.NewRequest(http.MethodPost, e.hook, bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.extraHeaders {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func repoList(names ...string) []map[string]string {
	var out []map[string]string
	for _, n := range names {
		out = append(out, map[string]string{"full_name": n})
	}
	return out
}

func (e *env) install(names ...string) {
	e.t.Helper()
	code := e.deliver("installation", map[string]any{"action": "created", "installation": map[string]any{"id": 7}, "repositories": repoList(names...)}, "")
	if code != http.StatusAccepted {
		e.t.Fatalf("installation webhook: %d", code)
	}
	for _, n := range names {
		e.waitRepo(n)
	}
}

// waitQueueIdle waits until the receiver has finished every delivery it accepted. A repo is
// registered before its first scan and dependency backfill complete, so a test that polls
// the feed right after waitRepo can see no dependents yet and enqueue no job.
func (e *env) waitQueueIdle() {
	e.t.Helper()
	e.eventually("event queue idle", func() bool { return e.app.Queue.Len() == 0 })
}

// waitRegistered waits for the repo row only: its scan or backfill may still be running.
func (e *env) waitRegistered(name string) {
	e.t.Helper()
	e.eventually(fmt.Sprintf("repo %s registered", name), func() bool {
		_, ok, err := e.st.GetRepo(context.Background(), name)
		return err == nil && ok
	})
}

// waitRepo waits until the repo is registered and every accepted delivery has been handled,
// so its first scan and dependency backfill are over and the test can act on its
// dependencies. Tests that hold a checkout open (so the queue never drains) use
// waitRegistered.
func (e *env) waitRepo(name string) {
	e.t.Helper()
	e.waitRegistered(name)
	e.waitQueueIdle()
}

func (e *env) eventually(what string, f func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for: %s", what)
}

// push commits files to main and delivers the matching push webhook; changed names
// the files GitHub would list in the push payload.
func (e *env) push(repo string, files map[string]string) {
	e.t.Helper()
	e.gh.Commit(repo, files)
	var changed []string
	for p := range files {
		changed = append(changed, p)
	}
	code := e.deliver("push", map[string]any{
		"ref": "refs/heads/main", "installation": map[string]any{"id": 7},
		"repository": map[string]any{"full_name": repo, "default_branch": "main"},
		"commits":    []map[string]any{{"modified": changed}},
	}, "")
	if code != http.StatusAccepted {
		e.t.Fatalf("push webhook: %d", code)
	}
}

func (e *env) tick() {
	e.t.Helper()
	e.ticks++
	e.app.Tick(context.Background())
}

func (e *env) publish(pkg, version string) { e.idx.Publish(pkg, version, e.clock.Now()) }

func (e *env) deps(repo string) []model.Dependency {
	e.t.Helper()
	d, err := e.st.ListRepoDependencies(context.Background(), repo)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) job(repo, pkg, version string) model.Job {
	e.t.Helper()
	j, ok, err := e.st.GetJob(context.Background(), model.JobKey(repo, "go", pkg, version))
	if err != nil || !ok {
		e.t.Fatalf("job %s %s %s: ok=%v err=%v", repo, pkg, version, ok, err)
	}
	return j
}

func gomod(module string, reqs ...string) string {
	s := "module " + module + "\n\ngo 1.22\n\nrequire (\n"
	for _, r := range reqs {
		s += "\t" + r + "\n"
	}
	return s + ")\n"
}

var (
	goCacheOnce sync.Once
	goCacheDir  string
)

// goCache shares the host's build cache between tests: compiling the standard
// library for every checkout would dominate the run time.
func goCache(t *testing.T) string {
	goCacheOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOCACHE").Output()
		if err == nil {
			goCacheDir = strings.TrimSpace(string(out))
		}
	})
	if goCacheDir == "" {
		return t.TempDir()
	}
	return goCacheDir
}

// rerun puts a finished job back to pending, due now, as a re-delivered release would.
func (e *env) rerun(repo, pkg, version string) {
	e.t.Helper()
	j := e.job(repo, pkg, version)
	j.State, j.NotBefore, j.PRURL, j.Reason = model.JobPending, e.clock.Now(), "", ""
	if err := e.st.UpdateJob(context.Background(), j); err != nil {
		e.t.Fatal(err)
	}
}
