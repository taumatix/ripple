package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

const optIn = "dependency-dashboard: true\n"

// dashEnv registers acme/app importing example.com/lib v1.0.0 behind a 72h minimum
// release age, so a release is deferred rather than turned into a PR.
func dashEnv(t *testing.T, st store.Store, cfg string) *env {
	t.Helper()
	lim := defaultLimits()
	lim.MinimumReleaseAge = 72 * time.Hour
	e := newEnv(t, st, lim)
	stubModule(e, "example.com/lib", "v1.0.0", "v1.1.0", "v1.2.0")
	stubModule(e, "example.com/other", "v1.0.0", "v1.1.0")
	files := map[string]string{"go.mod": gomod("example.com/app", "example.com/lib v1.0.0", "example.com/other v1.0.0"),
		"main.go": blankImports("example.com/lib", "example.com/other")}
	if cfg != "" {
		files[".ripple.yaml"] = cfg
	}
	e.gh.AddRepo(repo, files)
	start(t, e, repo)
	e.clock.Advance(time.Minute)
	return e
}

func TestDashboardIsCreatedOnTheFirstDeferredJob(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.tick()
		if got := e.gh.Issues(repo); len(got) != 0 {
			t.Fatalf("an issue before anything is pending: %+v", got)
		}
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		is := e.gh.Issues(repo)
		if len(is) != 1 {
			t.Fatalf("want 1 dashboard issue, got %+v", is)
		}
		d := is[0]
		if !strings.HasPrefix(d.Body, "<!-- ripple:dependency-dashboard -->") || len(d.Labels) != 1 || d.Labels[0] != "ripple" {
			t.Errorf("marker or label missing: %+v", d)
		}
		if !strings.Contains(d.Body, "## Deferred (1)") || !strings.Contains(d.Body, "`example.com/lib` `v1.1.0` until ") {
			t.Errorf("body does not list the deferred job:\n%s", d.Body)
		}
		if n := len(e.gh.PRs(repo)); n != 0 {
			t.Errorf("the cooldown was ignored: %d PRs", n)
		}
	})
}

func TestDashboardUpdatesOnChangeAndIsQuietOtherwise(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		writes := e.gh.IssueWrites(repo)
		if writes == 0 {
			t.Fatal("no dashboard written")
		}
		for i := 0; i < 3; i++ {
			e.tick()
		}
		if got := e.gh.IssueWrites(repo); got != writes {
			t.Fatalf("unchanged dashboard cost %d API writes", got-writes)
		}
		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.tick()
		is := e.gh.Issues(repo)
		if len(is) != 1 || !strings.Contains(is[0].Body, "## Deferred (2)") || e.gh.IssueWrites(repo) != writes+1 {
			t.Fatalf("want the same issue updated once: writes=%d\n%+v", e.gh.IssueWrites(repo), is)
		}
		// The cooldown ends: both become PRs and the dashboard says nothing is pending.
		e.clock.Advance(80 * time.Hour)
		e.tick()
		is = e.gh.Issues(repo)
		if len(is) != 1 || !strings.Contains(is[0].Body, "Nothing is pending") {
			t.Fatalf("dashboard after the PRs opened:\n%+v", is)
		}
	})
}

func TestDashboardIsOptIn(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, "")
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		if got := e.gh.Issues(repo); len(got) != 0 || e.gh.IssueWrites(repo) != 0 {
			t.Fatalf("a repo that did not opt in got an issue: %+v", got)
		}
	})
}

func TestDashboardNeverFailsAJobWhenIssuesCannotBeWritten(t *testing.T) {
	for name, block := range map[string]func(e *env){
		"issues disabled": func(e *env) { e.gh.DisableIssues(repo) },
		"no permission":   func(e *env) { e.gh.ForbidIssues(repo) },
	} {
		t.Run(name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := dashEnv(t, st, optIn)
				block(e)
				e.publish("example.com/lib", "v1.1.0")
				e.tick()
				e.tick()
				e.clock.Advance(80 * time.Hour)
				e.tick()
				if n := len(e.gh.PRs(repo)); n != 1 {
					t.Fatalf("the dashboard must not stop the PR: %d PRs", n)
				}
				if j := e.job(repo, "example.com/lib", "v1.1.0"); j.Attempts != 0 {
					t.Errorf("the dashboard failure counted against the job: %+v", j)
				}
				if n := e.logLines("dependency dashboard skipped"); n != 1 {
					t.Errorf("want one log line about it, got %d", n)
				}
			})
		})
	}
}

func TestDashboardLeavesAPersonsIssueAloneAndNeverReopens(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		mine := e.gh.HumanIssue(repo, "Release notes", "my own notes", "ripple")
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		is := e.gh.Issues(repo)
		if len(is) != 2 || is[0].Body != "my own notes" || !strings.Contains(is[1].Body, "ripple:dependency-dashboard") {
			t.Fatalf("the person's issue must be untouched and a separate dashboard made: %+v", is)
		}
		e.gh.HumanCloseIssue(repo, is[1].Number)
		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.tick()
		is = e.gh.Issues(repo)
		if len(is) != 2 || !is[1].Closed || !strings.Contains(is[1].Body, "## Deferred (2)") || is[0].Number != mine || is[0].Body != "my own notes" {
			t.Fatalf("a closed dashboard keeps its updates and stays closed: %+v", is)
		}
	})
}

func TestDashboardIsRecreatedWhenDeleted(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := dashEnv(t, st, optIn)
		e.publish("example.com/lib", "v1.1.0")
		e.tick()
		e.gh.HumanDeleteIssue(repo, 1)
		e.clock.Advance(time.Minute)
		e.publish("example.com/other", "v1.1.0")
		e.tick()
		is := e.gh.Issues(repo)
		if len(is) != 2 || !strings.Contains(is[1].Body, "## Deferred (2)") {
			t.Fatalf("want a fresh dashboard after the old one was deleted: %+v", is)
		}
	})
}
