// Package platformtest is the conformance suite every code host must pass: the
// same end-to-end behaviour, driven through the real app (receiver, event queue,
// engine, git) against the host's fake server. A platform runs it from its own
// conformance_test.go with a Fixture over that fake; see docs/platform-authoring.md.
package platformtest

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/platform/gitws"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// PR is a pull request as the fake recorded it.
type PR struct {
	Head, Base       string
	Draft, AutoMerge bool
	// Closed and Comments are what a host that implements platform.PRCloser reports:
	// whether ripple closed the PR and the comments it left. A fixture whose host
	// has no such capability leaves them zero.
	Closed   bool
	Comments []string
}

// Fixture is one host's fake server as the suite drives it. Repository paths are
// the host's own ("acme/app", "group/sub/app", "org/project/app"); Path builds one.
type Fixture interface {
	// Host builds the platform under test against the fake. git carries the
	// shared checkout setup (work dir, module proxy, build cache); now is the
	// suite's clock, for the platform's Retry-After arithmetic.
	Host(t *testing.T, git gitws.Options, now func() time.Time) *platform.Host
	// Path is the host-native path of a test repository called short.
	Path(short string) string
	// AddRepo creates a repository whose default branch, main, holds files. It is
	// not registered with ripple yet.
	AddRepo(t *testing.T, path string, files map[string]string)
	// Register makes ripple watch the repos the way this host does: deliver its
	// install or hook event to webhook (the URL its receiver is served at). A
	// Lister host has nothing to deliver; the suite runs discovery after Register.
	Register(t *testing.T, webhook string, paths ...string)
	// Push commits files to main and delivers the host's push event to webhook.
	Push(t *testing.T, webhook, path string, files map[string]string)
	// PRs lists the pull requests opened in the repository, closed ones included.
	PRs(path string) []PR
	// FileAt is the content of file on branch, ok=false when absent.
	FileAt(path, branch, file string) (string, bool)
	// Calls counts the API requests addressed to the repository, credential
	// minting excluded. Git transport is counted by the suite, not here.
	Calls(path string) int
	// RateLimitNextPR makes the next pull request creation answer with the host's
	// rate-limit response, and reports false when the host has none to fake.
	RateLimitNextPR() bool
}

// FilelessPushes is implemented by a Fixture whose host's push event lists no changed
// files (Bitbucket Cloud). Its platform resolves them with an API call per push, so
// the suite counts an irrelevant push's cost in platform calls (a scan) and not in
// API requests; it must still trigger no scan.
type FilelessPushes interface{ FilelessPushes() }

// PushCreatesProposals is implemented by a Fixture whose host opens the proposal
// when the commit is pushed, not when the platform creates it (Gerrit: a push to
// refs/for/<branch> is the change). A rate limit answered after the push therefore
// leaves the change behind; the suite then holds the host to "one proposal in the
// whole repo set, jobs pending, and no duplicate after the retry".
type PushCreatesProposals interface{ PushCreatesProposals() }

// ReportsRenames is implemented by a Fixture whose host tells ripple that a repository
// was renamed or moved (platform.RepoRenamed). RenameRepo renames the repository in the
// fake, from and to being host-native paths, and delivers the host's event to webhook.
// A host that cannot report it leaves this out and the rename test is skipped; ripple
// then learns of the new name from a listing or an install event, as before.
type ReportsRenames interface {
	RenameRepo(t *testing.T, webhook, from, to string)
}

// ReportsArchives is implemented by a Fixture whose host lets ripple learn that a
// repository was archived or unarchived (an event, or a listing that tells archived
// repos from deleted ones, platform.ArchivedLister). SetArchived changes the fake and,
// for an event host, delivers the event to webhook; the suite runs discovery after it.
// A host that cannot leaves this out: an archive then goes unseen, as before.
type ReportsArchives interface {
	SetArchived(t *testing.T, webhook, path string, archived bool)
}

// ReportsDeletes is implemented by a Fixture whose host lets ripple notice that a
// repository was deleted, by an event or because the listing no longer has it.
// DeleteRepo deletes the repository in the fake (or real instance) and, for an event host,
// delivers the event to webhook; the suite runs discovery after it. A host that cannot
// leaves this out and a deletion goes unseen.
type ReportsDeletes interface {
	DeleteRepo(t *testing.T, webhook, path string)
}

// AnnouncesCreates is implemented by a Fixture whose host announces a created repository
// before its first push (Gitea's and Forgejo's `repository` event), and which can show it
// against a real instance. CreateEmptyRepo creates the repository with no commit and
// delivers the created event to webhook, returning once the receiver has answered;
// FirstPush then makes its first commit. A fixture over a fake leaves this out.
type AnnouncesCreates interface {
	CreateEmptyRepo(t *testing.T, webhook, path string)
	FirstPush(t *testing.T, path string, files map[string]string)
}

// Run runs every conformance test against f.
func Run(t *testing.T, f Fixture) {
	t.Run("ReleaseOpensOnePRInExactlyTheImportingRepos", func(t *testing.T) { releaseFanOut(t, f) })
	t.Run("PushRescansAndAnIrrelevantPushCostsNothing", func(t *testing.T) { pushRescan(t, f) })
	t.Run("RootConfigFileOnEveryHost", func(t *testing.T) { rootConfig(t, f) })
	t.Run("LegacyAliasOnlyWhereTheHostReadsIt", func(t *testing.T) { legacyAlias(t, f) })
	t.Run("AutoMergeRequestedOnlyWhereTheHostCan", func(t *testing.T) { autoMerge(t, f) })
	t.Run("StalePRClosedOnlyWhereTheHostCan", func(t *testing.T) { supersede(t, f) })
	t.Run("RateLimitHoldsBackTheHostThenRetries", func(t *testing.T) { rateLimit(t, f) })
	t.Run("DependencyDashboardOnlyWhereTheHostHasIssues", func(t *testing.T) { dashboard(t, f) })
	t.Run("ArchivePausesAndUnarchiveResumes", func(t *testing.T) { archived(t, f) })
	t.Run("RenameKeepsTheIndexWithoutAScan", func(t *testing.T) { renamed(t, f) })
	t.Run("DeleteForgetsTheRepo", func(t *testing.T) { deleted(t, f) })
	t.Run("CreatedBeforeTheFirstPushStillRegistersOnce", func(t *testing.T) { createdEmpty(t, f) })
}

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
	f     Fixture
	host  *platform.Host
	mux   *platform.Mux
	st    store.Store
	plat  *testutil.CountingPlatform
	app   *app.App
	idx   *testutil.Index
	proxy *testutil.GoProxy
	clock *clock
	hook  string
}

type opts struct {
	workers int
}

func newEnv(t *testing.T, f Fixture, o opts) *env {
	t.Helper()
	e := &env{t: t, f: f, st: store.NewMemory(), idx: testutil.NewIndex(t), proxy: testutil.NewGoProxy(t), clock: &clock{t: t0}}
	e.host = f.Host(t, gitws.Options{WorkDir: t.TempDir(), GoProxy: e.proxy.URL, GoSumDB: "off", GoCacheDir: goCache(t)}, e.clock.Now)
	// GitHub stays the default (unprefixed) host, so every other host is exercised
	// with the "<host>:" names it gets in production.
	def := ""
	if e.host.Name == "github" {
		def = "github"
	}
	var err error
	if e.mux, err = platform.NewMux(def, e.host); err != nil {
		t.Fatal(err)
	}
	e.plat = &testutil.CountingPlatform{Inner: e.mux}
	if o.workers == 0 {
		o.workers = 2
	}
	log := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	eng := &engine.Engine{Store: e.st, Platform: e.plat, Now: e.clock.Now, Workers: o.workers, Log: log,
		Limits: rules.Limits{OpenPRLimit: 5, AllowAutomerge: true}}
	e.app = &app.App{Engine: eng, Log: log, Queue: &app.Queue{}, Hosts: []*platform.Host{e.host}, DefaultHost: "github",
		Watcher: &engine.Watcher{Engine: eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.hook = "http://" + ln.Addr().String() + "/webhook/" + e.host.Name
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

// name is the stored name of the test repository short.
func (e *env) name(short string) string { return e.mux.RepoName(e.host.Name, e.f.Path(short)) }

func (e *env) addRepo(short string, files map[string]string) {
	e.f.AddRepo(e.t, e.f.Path(short), files)
}

// register makes ripple watch the repos, waits for their scans, and lets the
// watcher record its starting cursor.
func (e *env) register(shorts ...string) {
	e.t.Helper()
	var paths []string
	for _, s := range shorts {
		paths = append(paths, e.f.Path(s))
	}
	e.f.Register(e.t, e.hook, paths...)
	e.app.Discover(context.Background())
	for _, s := range shorts {
		name := e.name(s)
		e.eventually("repo "+name+" registered", func() bool {
			_, ok, err := e.st.GetRepo(context.Background(), name)
			return err == nil && ok
		})
		// The repo row exists before its scan stores the dependencies; a poll in between
		// finds no dependent of the release and opens no PR. The scan also holds the repo
		// until it syncs the dashboard, so wait for it to end.
		e.eventually("repo "+name+" scanned", func() bool {
			d, err := e.st.ListRepoDependencies(context.Background(), name)
			return err == nil && len(d) > 0 && e.app.Engine.RepoIdle(name)
		})
	}
	e.tick()
}

func (e *env) push(short string, files map[string]string) {
	e.t.Helper()
	e.f.Push(e.t, e.hook, e.f.Path(short), files)
}

func (e *env) tick() { e.app.Tick(context.Background()) }

func (e *env) publish(pkg, version string) {
	e.clock.Advance(time.Minute)
	e.idx.Publish(pkg, version, e.clock.Now())
}

func (e *env) prs(short string) []PR { return e.f.PRs(e.f.Path(short)) }

// calls is every call the repo cost: platform calls (checkouts included) plus
// the fake's own count of API requests.
func (e *env) calls(short string) int {
	return e.plat.Calls(e.name(short)) + e.f.Calls(e.f.Path(short))
}

func (e *env) deps(short string) []model.Dependency {
	e.t.Helper()
	d, err := e.st.ListRepoDependencies(context.Background(), e.name(short))
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) job(short, pkg, version string) model.Job {
	e.t.Helper()
	j, ok, err := e.st.GetJob(context.Background(), model.JobKey(e.name(short), "go", pkg, version))
	if err != nil || !ok {
		e.t.Fatalf("job %s %s %s: ok=%v err=%v", short, pkg, version, ok, err)
	}
	return j
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

// stub publishes module versions with one empty package, so go mod tidy resolves them.
func (e *env) stub(pkg string, versions ...string) {
	for _, v := range versions {
		e.proxy.Publish(pkg, v, map[string]string{"x.go": "package x\n"})
	}
}

func gomod(module string, reqs ...string) string {
	s := "module " + module + "\n\ngo 1.22\n\nrequire (\n"
	for _, r := range reqs {
		s += "\t" + r + "\n"
	}
	return s + ")\n"
}

func blankImports(pkgs ...string) string {
	s := "package main\n\nimport (\n"
	for _, p := range pkgs {
		s += "\t_ \"" + p + "\"\n"
	}
	return s + ")\n\nfunc main() {}\n"
}

// libRepo is a repository importing example.com/lib at v1.0.0, plus extra files.
func libRepo(module string, extra map[string]string) map[string]string {
	f := map[string]string{"go.mod": gomod(module, "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
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
