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
	"github.com/taumatix/ripple/internal/rules"
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
	t     *testing.T
	gh    *testutil.GitHub
	idx   *testutil.Index
	st    store.Store
	plat  *testutil.CountingPlatform
	app   *app.App
	clock *clock
	proxy *testutil.GoProxy
	hook  string
	ticks int
}

func defaultLimits() rules.Limits {
	return rules.Limits{MinimumReleaseAge: 0, OpenPRLimit: 5, AllowAutomerge: true}
}

func newEnv(t *testing.T, st store.Store, lim rules.Limits) *env {
	t.Helper()
	e := &env{t: t, st: st, gh: testutil.NewGitHub(t), idx: testutil.NewIndex(t), clock: &clock{t: t0}, proxy: testutil.NewGoProxy(t)}
	gh := &github.Client{APIBase: e.gh.APIURL, GitBase: e.gh.GitBase, Tokens: e.gh.Auth, WorkDir: t.TempDir(),
		GoProxy: e.proxy.URL, GoSumDB: "off", GoCacheDir: goCache(t),
		Log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	e.plat = &testutil.CountingPlatform{Inner: gh}
	eng := &engine.Engine{Store: st, Platform: e.plat, Limits: lim, Now: e.clock.Now, Workers: 2, Log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	e.app = &app.App{Engine: eng, Secret: []byte(secret), Log: slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		Watcher: &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.hook = "http://" + ln.Addr().String() + "/webhook"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.app.Run(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("app.Run: %v", err)
		}
	})
	return e
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

func (e *env) waitRepo(name string) {
	e.t.Helper()
	e.eventually(fmt.Sprintf("repo %s registered", name), func() bool {
		_, ok, err := e.st.GetRepo(context.Background(), name)
		return err == nil && ok
	})
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
