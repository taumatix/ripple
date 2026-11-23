package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

// restarted is a server process that has just started on e's store: a fresh engine with
// none of the previous one's memory.
func (e *env) restarted(name string) *replica {
	r := e.newReplica(name, e.st, nil)
	return r
}

// ListOpenJobs lets a replica's wrapped store keep the optional listing the dashboard needs.
func (m *mortalStore) ListOpenJobs(ctx context.Context, repo string) ([]model.Job, error) {
	return m.Store.(store.OpenJobLister).ListOpenJobs(ctx, repo)
}

func (r *replica) poll(e *env) {
	e.t.Helper()
	w := &engine.Watcher{Engine: r.eng, Feed: &feed.GoIndex{BaseURL: e.idx.URL}}
	if _, err := w.Poll(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func TestDashboardRefreshSurvivesARestart(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		a := e.restarted("a")
		rel := model.Release{Datasource: "go", Package: "example.com/lib", Version: "v1.1.0", Time: e.clock.Now()}
		if n, err := a.eng.FanOut(context.Background(), rel); err != nil || n != 1 {
			t.Fatalf("fan out: %d %v", n, err)
		}
		// The process dies here, before it flushed the dashboards it marked.
		b := e.restarted("b")
		b.poll(e)
		// The environment's own job loop may be the one that takes the stored set, so
		// wait for the issue rather than for this process in particular.
		e.eventually("the refresh to reach the issue", func() bool { return len(e.gh.Issues(repo)) == 1 })
		is := e.gh.Issues(repo)
		if len(is) != 1 || !strings.Contains(is[0].Body, "`example.com/lib` `v1.1.0`") {
			t.Fatalf("the restarted process did not refresh the dashboard: %+v", is)
		}
		// And it is not refreshed again: the set was consumed.
		writes := e.gh.IssueWrites(repo)
		b.poll(e)
		c := e.restarted("c")
		c.poll(e)
		if got := e.gh.IssueWrites(repo); got != writes || c.plat.Calls(repo) != 0 {
			t.Fatalf("a consumed refresh set was acted on again: writes %d->%d, calls %d", writes, got, c.plat.Calls(repo))
		}
	})
}

func TestDashboardEditByAPersonIsUndone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		is := e.gh.Issues(repo)
		if len(is) != 1 {
			t.Fatalf("no dashboard: %+v", is)
		}
		want := is[0].Body
		e.gh.HumanEditIssue(repo, is[0].Number, "I replaced the dashboard with my own text")
		calls := e.plat.Calls(repo)
		e.tick()
		if e.plat.Calls(repo) != calls {
			t.Fatal("an unchanged dashboard was re-read on every pass")
		}
		e.clock.Advance(2 * time.Hour)
		e.tick()
		if got := e.gh.Issues(repo)[0].Body; got != want {
			t.Fatalf("the edit was not noticed and undone:\n%s", got)
		}
		writes := e.gh.IssueWrites(repo)
		e.clock.Advance(2 * time.Hour)
		e.tick()
		if got := e.gh.IssueWrites(repo); got != writes {
			t.Fatalf("an intact dashboard was written again")
		}
	})
}

func TestDashboardIsClosedWhenTheKeyIsTurnedOff(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if is := e.gh.Issues(repo); len(is) != 1 || is[0].Closed {
			t.Fatalf("setup: %+v", is)
		}
		e.push(repo, map[string]string{".ripple.yaml": "dependency-dashboard: false\n"})
		e.eventually("the dashboard to be closed", func() bool { is := e.gh.Issues(repo); return len(is) == 1 && is[0].Closed })
		// Once closed it is left alone, so a later pass costs nothing.
		writes := e.gh.IssueWrites(repo)
		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.tick()
		e.clock.Advance(2 * time.Hour)
		e.tick()
		if got := e.gh.Issues(repo); !got[0].Closed || e.gh.IssueWrites(repo) != writes || len(got) != 1 {
			t.Fatalf("a closed dashboard was touched again: %+v", got)
		}
	})
}

func TestDashboardBackoffSurvivesARestart(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.gh.DisableIssues(repo)
		mark := func(r *replica, version string) {
			if _, err := r.eng.FanOut(context.Background(), model.Release{Datasource: "go", Package: "example.com/lib", Version: version, Time: e.clock.Now()}); err != nil {
				t.Fatal(err)
			}
			r.poll(e)
		}
		// The environment's own job loop shares the store and may be the process that
		// tries first, so count the requests the fake saw, whoever made them.
		total := func() int { return e.gh.IssueRequests(repo) }
		start := e.restarted
		mark(start("a"), "v1.1.0")
		e.eventually("a process to try the issues API", func() bool { return total() > 0 })
		tried := total()
		e.clock.Advance(10 * time.Minute)
		mark(start("b"), "v1.2.0")
		time.Sleep(200 * time.Millisecond)
		if n := total(); n != tried {
			t.Fatalf("a restarted process retried inside the hour: %d new calls", n-tried)
		}
		e.clock.Advance(2 * time.Hour)
		mark(start("c"), "v1.3.0")
		e.eventually("the retry after the hour", func() bool { return total() > tried })
	})
}
