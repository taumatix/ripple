package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// replica is a second (or third) server process: its own engine and job runner over
// the shared store, the shared fake GitHub, and its own platform handle.
type replica struct {
	name   string
	eng    *engine.Engine
	plat   *testutil.CountingPlatform
	st     *mortalStore
	runner engine.JobRunner
	cancel context.CancelFunc
	done   chan struct{}
}

const replicaTTL = 600 * time.Millisecond

func (e *env) newReplica(name string, st store.Store, wrap func(platform.Platform) platform.Platform) *replica {
	e.t.Helper()
	ms := &mortalStore{Store: st}
	r := &replica{name: name, st: ms, plat: &testutil.CountingPlatform{Inner: e.client}}
	var plat platform.Platform = r.plat
	if wrap != nil {
		plat = wrap(plat)
	}
	r.eng = &engine.Engine{Store: ms, Platform: plat, Limits: e.limits, Now: e.clock.Now, Workers: 2,
		Replica: name, LeaseTTL: replicaTTL, Log: slog.New(slog.NewTextHandler(io.MultiWriter(testWriter{e.t}, e.logs), nil))}
	r.runner.Engine = r.eng
	return r
}

// start runs the replica's job loop until stop or kill.
func (r *replica) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan struct{})
	go func() { defer close(r.done); r.runner.Run(ctx, 50*time.Millisecond) }()
}

func (r *replica) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}
}

// kill is a process that is gone: it stops on the spot and writes nothing more to the
// store, so it neither renews nor releases what it holds, and its jobs are not touched.
func (r *replica) kill() {
	r.st.dead.Store(true)
	r.stop()
}

// mortalStore refuses every write once dead, as a crashed process cannot make any.
type mortalStore struct {
	store.Store
	dead atomic.Bool
}

var errDead = errors.New("replica is dead")

func (m *mortalStore) UpdateJob(ctx context.Context, j model.Job) error {
	if m.dead.Load() {
		return errDead
	}
	return m.Store.UpdateJob(ctx, j)
}

func (m *mortalStore) AcquireLease(ctx context.Context, n, o string, ttl time.Duration) (store.Lease, bool, error) {
	if m.dead.Load() {
		return store.Lease{}, false, errDead
	}
	return m.Store.AcquireLease(ctx, n, o, ttl)
}

func (m *mortalStore) RenewLease(ctx context.Context, l store.Lease, ttl time.Duration) (store.Lease, bool, error) {
	if m.dead.Load() {
		return store.Lease{}, false, errDead
	}
	return m.Store.RenewLease(ctx, l, ttl)
}

func (m *mortalStore) ReleaseLease(ctx context.Context, l store.Lease) error {
	if m.dead.Load() {
		return errDead
	}
	return m.Store.ReleaseLease(ctx, l)
}

// replicaRepos registers n repos that import example.com/lib v1.0.0, then publishes
// v1.1.0 and polls the feed once, so each repo has one pending job.
func replicaRepos(t *testing.T, e *env, n int) []string {
	t.Helper()
	stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0")
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("acme/r%02d", i)
		e.gh.AddRepo(name, map[string]string{"go.mod": gomod("example.com/"+name, "example.com/lib v1.0.0"), "main.go": blankImports("example.com/lib")})
		names = append(names, name)
	}
	e.install(names...)
	if _, err := e.app.Watcher.Poll(context.Background()); err != nil { // seeds the feed cursor
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	e.publish("example.com/lib", "v1.1.0")
	if got, err := e.app.Watcher.Poll(context.Background()); err != nil || got != n {
		t.Fatalf("poll enqueued %d jobs, err %v; want %d", got, err, n)
	}
	return names
}

func (e *env) jobDone(repo string) bool {
	j, ok, err := e.st.GetJob(context.Background(), model.JobKey(repo, "go", "example.com/lib", "v1.1.0"))
	return err == nil && ok && j.State == model.JobDone
}

// Two replicas on one store and one GitHub run the same N pending jobs: every repo gets
// exactly one PR, and no job is lost.
func TestTwoReplicasOpenEachPullRequestOnce(t *testing.T) {
	testutil.ForEachStoreWithFake(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		repos := replicaRepos(t, e, 6)
		a, b := e.newReplica("a", st, nil), e.newReplica("b", st, nil)
		a.start()
		b.start()
		defer a.stop()
		defer b.stop()
		e.eventually("every job done", func() bool {
			for _, r := range repos {
				if !e.jobDone(r) {
					return false
				}
			}
			return true
		})
		a.stop()
		b.stop()
		for _, r := range repos {
			if n := len(e.gh.PRs(r)); n != 1 {
				t.Errorf("%s: %d PRs, want exactly 1", r, n)
			}
		}
		t.Logf("replica a made %d platform calls, b made %d", callsOf(a, repos), callsOf(b, repos))
	})
}

func callsOf(r *replica, repos []string) int {
	n := 0
	for _, name := range repos {
		n += r.plat.Calls(name)
	}
	return n
}

// diePlatform kills its replica at the first CreatePR: the branch is pushed and the PR
// is not opened, the moment the roadmap names.
type diePlatform struct {
	platform.Platform
	once sync.Once
	die  func()
	hit  chan struct{}
}

func (d *diePlatform) CreatePR(ctx context.Context, r model.Repo, s platform.PRSpec) (platform.PR, error) {
	d.once.Do(func() { d.die(); close(d.hit) })
	return platform.PR{}, errDead
}

// A replica that dies after pushing its branch and before opening the PR leaves the job
// claimed; once its lease lapses another replica takes the repo over, finds the branch
// already pushed, and opens the one PR.
func TestKilledReplicaIsTakenOverWithOnePullRequest(t *testing.T) {
	testutil.ForEachStoreWithFake(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		repos := replicaRepos(t, e, 1)
		repo := repos[0]
		var a *replica
		dp := &diePlatform{hit: make(chan struct{})}
		a = e.newReplica("a", st, func(p platform.Platform) platform.Platform { dp.Platform = p; return dp })
		dp.die = func() { a.st.dead.Store(true) }
		a.start()
		defer a.stop()
		select {
		case <-dp.hit:
		case <-time.After(30 * time.Second):
			t.Fatal("replica a never reached CreatePR")
		}
		a.kill()
		if n := len(e.gh.PRs(repo)); n != 0 {
			t.Fatalf("%d PRs before takeover, want 0", n)
		}
		if len(e.gh.Branches(repo)) < 1 {
			t.Fatalf("branch was not pushed before the kill: %v", e.gh.Branches(repo))
		}
		if e.jobDone(repo) {
			t.Fatal("the dead replica finished the job")
		}
		killed := time.Now()
		b := e.newReplica("b", st, nil)
		b.start()
		defer b.stop()
		e.eventually("job taken over", func() bool { return e.jobDone(repo) })
		if d := time.Since(killed); d < replicaTTL/3 {
			t.Errorf("taken over after %v, before the dead replica's lease could have lapsed", d)
		}
		b.stop()
		if n := len(e.gh.PRs(repo)); n != 1 {
			t.Fatalf("%d PRs after takeover, want exactly 1", n)
		}
	})
}
