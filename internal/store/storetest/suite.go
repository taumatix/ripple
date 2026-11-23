// Package storetest is the conformance suite every store.Store must pass.
package storetest

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

func dep(repo, file, pkg, ver string) model.Dependency {
	return model.Dependency{Repo: repo, File: file, Manager: "gomod", Datasource: "go", Package: pkg, Version: ver}
}

// Run executes the suite; mk must return a fresh, empty store.
func Run(t *testing.T, mk func(t *testing.T) store.Store) {
	ctx := context.Background()
	t.Run("repo roundtrip and delete", func(t *testing.T) {
		s := mk(t)
		r := model.Repo{FullName: "acme/web", InstallationID: 42, DefaultBranch: "main", Config: []byte(`{"prLimit":3}`)}
		if err := s.UpsertRepo(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.GetRepo(ctx, "acme/web")
		if err != nil || !ok || !reflect.DeepEqual(got, r) {
			t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
		}
		r.Config = nil
		if err := s.UpsertRepo(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, _, _ = s.GetRepo(ctx, "acme/web")
		if len(got.Config) != 0 {
			t.Fatalf("upsert did not overwrite: %+v", got)
		}
		if _, ok, _ := s.GetRepo(ctx, "acme/none"); ok {
			t.Fatal("unexpected repo")
		}
		_ = s.ReplaceDependencies(ctx, "acme/web", []model.Dependency{dep("acme/web", "go.mod", "example.com/a", "v1.0.0")})
		if err := s.DeleteRepo(ctx, "acme/web"); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.GetRepo(ctx, "acme/web"); ok {
			t.Fatal("repo survived delete")
		}
		if d, _ := s.ListDependents(ctx, "go", "example.com/a"); len(d) != 0 {
			t.Fatalf("dependencies survived delete: %v", d)
		}
	})

	t.Run("reverse index", func(t *testing.T) {
		s := mk(t)
		_ = s.ReplaceDependencies(ctx, "acme/web", []model.Dependency{
			dep("acme/web", "go.mod", "example.com/lib", "v1.0.0"),
			dep("acme/web", "go.mod", "example.com/other", "v2.0.0"),
		})
		_ = s.ReplaceDependencies(ctx, "acme/api", []model.Dependency{
			dep("acme/api", "go.mod", "example.com/lib", "v1.2.0"),
			dep("acme/api", "tools/go.mod", "example.com/lib", "v1.1.0"),
		})
		got, err := s.ListDependents(ctx, "go", "example.com/lib")
		if err != nil {
			t.Fatal(err)
		}
		want := []model.Dependency{
			dep("acme/api", "go.mod", "example.com/lib", "v1.2.0"),
			dep("acme/api", "tools/go.mod", "example.com/lib", "v1.1.0"),
			dep("acme/web", "go.mod", "example.com/lib", "v1.0.0"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
		if d, _ := s.ListDependents(ctx, "npm", "example.com/lib"); len(d) != 0 {
			t.Fatalf("datasource not part of key: %v", d)
		}

		// Replace drops what is no longer present and updates versions.
		_ = s.ReplaceDependencies(ctx, "acme/api", []model.Dependency{dep("acme/api", "go.mod", "example.com/lib", "v1.3.0")})
		got, _ = s.ListDependents(ctx, "go", "example.com/lib")
		want = []model.Dependency{dep("acme/api", "go.mod", "example.com/lib", "v1.3.0"), dep("acme/web", "go.mod", "example.com/lib", "v1.0.0")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("after replace got %v want %v", got, want)
		}
		own, _ := s.ListRepoDependencies(ctx, "acme/web")
		if len(own) != 2 || own[0].Package != "example.com/lib" || own[1].Package != "example.com/other" {
			t.Fatalf("repo deps: %v", own)
		}
		_ = s.ReplaceDependencies(ctx, "acme/api", nil)
		got, _ = s.ListDependents(ctx, "go", "example.com/lib")
		if len(got) != 1 {
			t.Fatalf("replace with empty set: %v", got)
		}
	})

	t.Run("reverse index spans major versions", func(t *testing.T) {
		s := mk(t)
		_ = s.ReplaceDependencies(ctx, "acme/a", []model.Dependency{dep("acme/a", "go.mod", "example.com/gh/v60", "v60.0.0")})
		_ = s.ReplaceDependencies(ctx, "acme/b", []model.Dependency{dep("acme/b", "go.mod", "example.com/gh/v61", "v61.0.0")})
		_ = s.ReplaceDependencies(ctx, "acme/c", []model.Dependency{dep("acme/c", "go.mod", "gopkg.in/yaml.v2", "v2.4.0")})
		_ = s.ReplaceDependencies(ctx, "acme/d", []model.Dependency{dep("acme/d", "go.mod", "example.com/gh", "v1.9.0")})
		// A lookup by any path of the line returns the importers of every major.
		for _, q := range []string{"example.com/gh/v62", "example.com/gh/v60", "example.com/gh"} {
			got, err := s.ListDependents(ctx, "go", q)
			if err != nil {
				t.Fatal(err)
			}
			var repos []string
			for _, d := range got {
				repos = append(repos, d.Repo)
			}
			if !reflect.DeepEqual(repos, []string{"acme/a", "acme/b", "acme/d"}) {
				t.Fatalf("lookup %s: %v", q, repos)
			}
		}
		got, _ := s.ListDependents(ctx, "go", "gopkg.in/yaml.v3")
		if len(got) != 1 || got[0].Package != "gopkg.in/yaml.v2" {
			t.Fatalf("gopkg.in line: %v", got)
		}
	})

	t.Run("many dependencies", func(t *testing.T) {
		s := mk(t)
		var deps []model.Dependency
		for i := 0; i < 130; i++ {
			deps = append(deps, dep("acme/big", "go.mod", fmt.Sprintf("example.com/m%03d", i), "v1.0.0"))
		}
		if err := s.ReplaceDependencies(ctx, "acme/big", deps); err != nil {
			t.Fatal(err)
		}
		got, err := s.ListRepoDependencies(ctx, "acme/big")
		if err != nil || len(got) != 130 {
			t.Fatalf("len=%d err=%v", len(got), err)
		}
		if err := s.ReplaceDependencies(ctx, "acme/big", deps[:5]); err != nil {
			t.Fatal(err)
		}
		got, _ = s.ListRepoDependencies(ctx, "acme/big")
		if len(got) != 5 {
			t.Fatalf("after shrink len=%d", len(got))
		}
	})

	t.Run("releases", func(t *testing.T) {
		s := mk(t)
		r := model.Release{Datasource: "go", Package: "example.com/lib", Version: "v1.1.0", Time: time.Now().UTC().Truncate(time.Microsecond)}
		if n, err := s.RecordRelease(ctx, r); err != nil || !n {
			t.Fatalf("first: new=%v err=%v", n, err)
		}
		if n, err := s.RecordRelease(ctx, r); err != nil || n {
			t.Fatalf("second: new=%v err=%v", n, err)
		}
		r.Version = "v1.2.0"
		if n, _ := s.RecordRelease(ctx, r); !n {
			t.Fatal("different version must be new")
		}
	})

	t.Run("list releases", func(t *testing.T) {
		s := mk(t)
		at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
		for _, r := range []model.Release{
			{Datasource: "go", Package: "example.com/lib", Version: "v1.1.0", Time: at},
			{Datasource: "go", Package: "example.com/lib", Version: "v1.0.0", Time: at.Add(-time.Hour)},
			{Datasource: "go", Package: "example.com/lib/v2", Version: "v2.0.0", Time: at},
			{Datasource: "go", Package: "example.com/library", Version: "v9.0.0", Time: at},
			{Datasource: "other", Package: "example.com/lib", Version: "v5.0.0", Time: at},
		} {
			if _, err := s.RecordRelease(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.ListReleases(ctx, "go", "example.com/lib")
		if err != nil {
			t.Fatal(err)
		}
		want := []model.Release{
			{Datasource: "go", Package: "example.com/lib", Version: "v1.0.0", Time: at.Add(-time.Hour)},
			{Datasource: "go", Package: "example.com/lib", Version: "v1.1.0", Time: at},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("exactly this package, ordered by version string:\n got %+v\nwant %+v", got, want)
		}
		if got, err := s.ListReleases(ctx, "go", "example.com/none"); err != nil || len(got) != 0 {
			t.Fatalf("unknown package: %v %v", got, err)
		}
	})

	t.Run("jobs", func(t *testing.T) {
		s := mk(t)
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		mkJob := func(pkg string, nb time.Time) model.Job {
			return model.Job{Key: model.JobKey("acme/web", "go", pkg, "v1.1.0"), Repo: "acme/web", Datasource: "go", Package: pkg,
				Version: "v1.1.0", ReleasedAt: base.Add(-time.Hour), State: model.JobPending, NotBefore: nb}
		}
		a, b, c := mkJob("example.com/a", base.Add(-2*time.Minute)), mkJob("example.com/b", base.Add(-time.Minute)), mkJob("example.com/c", base.Add(time.Hour))
		for _, j := range []model.Job{b, a, c} {
			if ok, err := s.EnqueueJob(ctx, j); err != nil || !ok {
				t.Fatalf("enqueue: ok=%v err=%v", ok, err)
			}
		}
		if ok, _ := s.EnqueueJob(ctx, a); ok {
			t.Fatal("duplicate key must not insert")
		}
		due, err := s.ListDueJobs(ctx, base, 10)
		if err != nil || len(due) != 2 || due[0].Key != a.Key || due[1].Key != b.Key {
			t.Fatalf("due=%v err=%v", due, err)
		}
		if !reflect.DeepEqual(due[0], a) {
			t.Fatalf("job roundtrip:\n got %+v\nwant %+v", due[0], a)
		}
		if due, _ = s.ListDueJobs(ctx, base, 1); len(due) != 1 {
			t.Fatalf("limit ignored: %v", due)
		}
		a.State, a.PRURL, a.Attempts, a.Reason = model.JobDone, "https://example.test/pr/1", 1, "ok"
		if err := s.UpdateJob(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, ok, _ := s.GetJob(ctx, a.Key)
		if !ok || !reflect.DeepEqual(got, a) {
			t.Fatalf("got %+v", got)
		}
		due, _ = s.ListDueJobs(ctx, base, 10)
		if len(due) != 1 || due[0].Key != b.Key {
			t.Fatalf("done job still due: %v", due)
		}
		c.NotBefore = base
		if err := s.UpdateJob(ctx, c); err != nil {
			t.Fatal(err)
		}
		if due, _ = s.ListDueJobs(ctx, base, 10); len(due) != 2 {
			t.Fatalf("deferral not lifted: %v", due)
		}
		if _, ok, _ := s.GetJob(ctx, "nope"); ok {
			t.Fatal("unexpected job")
		}
	})

	t.Run("open jobs of a repo", func(t *testing.T) {
		s := mk(t)
		ol, ok := s.(store.OpenJobLister)
		if !ok {
			t.Fatal("store does not implement store.OpenJobLister")
		}
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		mkJob := func(repo, pkg string, st model.JobState, nb time.Time) model.Job {
			return model.Job{Key: model.JobKey(repo, "go", pkg, "v1.1.0"), Repo: repo, Datasource: "go", Package: pkg,
				Version: "v1.1.0", ReleasedAt: base, State: st, NotBefore: nb}
		}
		jobs := map[string]model.Job{
			"due":      mkJob("acme/web", "example.com/due", model.JobPending, base.Add(-time.Hour)),
			"deferred": mkJob("acme/web", "example.com/deferred", model.JobPending, base.Add(48*time.Hour)),
			"failed":   mkJob("acme/web", "example.com/failed", model.JobFailed, base),
			"done":     mkJob("acme/web", "example.com/done", model.JobDone, base),
			"skipped":  mkJob("acme/web", "example.com/skipped", model.JobSkipped, base),
			"other":    mkJob("acme/api", "example.com/other", model.JobPending, base),
		}
		for _, j := range jobs {
			if _, err := s.EnqueueJob(ctx, j); err != nil {
				t.Fatal(err)
			}
		}
		keys := func() []string {
			got, err := ol.ListOpenJobs(ctx, "acme/web")
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, j := range got {
				out = append(out, j.Package)
			}
			sort.Strings(out)
			return out
		}
		if got := keys(); !reflect.DeepEqual(got, []string{"example.com/deferred", "example.com/due", "example.com/failed"}) {
			t.Fatalf("open jobs of acme/web: %v", got)
		}
		got, _ := ol.ListOpenJobs(ctx, "acme/web")
		for _, j := range got {
			if j.Package == "example.com/failed" && !reflect.DeepEqual(j, jobs["failed"]) {
				t.Fatalf("job roundtrip:\n got %+v\nwant %+v", j, jobs["failed"])
			}
		}
		// A job that finishes leaves the list; one that fails or is deferred joins or stays.
		d := jobs["due"]
		d.State = model.JobDone
		if err := s.UpdateJob(ctx, d); err != nil {
			t.Fatal(err)
		}
		f := jobs["deferred"]
		f.State, f.Attempts, f.Reason = model.JobFailed, 5, "boom"
		if err := s.UpdateJob(ctx, f); err != nil {
			t.Fatal(err)
		}
		if got := keys(); !reflect.DeepEqual(got, []string{"example.com/deferred", "example.com/failed"}) {
			t.Fatalf("after updates: %v", got)
		}
		if got, _ := ol.ListOpenJobs(ctx, "acme/none"); len(got) != 0 {
			t.Fatalf("unknown repo: %v", got)
		}
	})

	// Spread over many repos and jobs, the order and the limit must be the same as for a
	// few: stores that shard their indexes (DynamoDB) merge several partitions here.
	t.Run("many jobs in order", func(t *testing.T) {
		s := mk(t)
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		const n = 120
		for i := 0; i < n; i++ {
			// Not-before times descend with i, so insertion order is the reverse of due order.
			j := model.Job{Key: model.JobKey(fmt.Sprintf("acme/r%03d", i), "go", "example.com/lib", "v1.0.0"), Repo: fmt.Sprintf("acme/r%03d", i),
				Datasource: "go", Package: "example.com/lib", Version: "v1.0.0", ReleasedAt: base, State: model.JobPending,
				NotBefore: base.Add(-time.Duration(i) * time.Second)}
			if ok, err := s.EnqueueJob(ctx, j); err != nil || !ok {
				t.Fatalf("enqueue %d: ok=%v err=%v", i, ok, err)
			}
		}
		for _, limit := range []int{1, 7, 50, n, n + 30} {
			due, err := s.ListDueJobs(ctx, base, limit)
			want := min(limit, n)
			if err != nil || len(due) != want {
				t.Fatalf("limit %d: got %d jobs, err=%v", limit, len(due), err)
			}
			for k, j := range due {
				if wantRepo := fmt.Sprintf("acme/r%03d", n-1-k); j.Repo != wantRepo {
					t.Fatalf("limit %d: position %d is %s, want %s", limit, k, j.Repo, wantRepo)
				}
			}
		}
		// Finishing the oldest drops it from the queue wherever it lives.
		due, _ := s.ListDueJobs(ctx, base, 1)
		due[0].State = model.JobDone
		if err := s.UpdateJob(ctx, due[0]); err != nil {
			t.Fatal(err)
		}
		if next, _ := s.ListDueJobs(ctx, base, 1); len(next) != 1 || next[0].Key == due[0].Key {
			t.Fatalf("done job still first: %v", next)
		}
	})

	// Repos whose run is in flight are left out before the limit applies, so a repo with
	// more due jobs than one batch cannot hold back the jobs of every other repo.
	t.Run("due jobs excluding repos", func(t *testing.T) {
		s := mk(t)
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		add := func(repo string, i int, nb time.Time) {
			j := model.Job{Key: model.JobKey(repo, "go", fmt.Sprintf("example.com/p%d", i), "v1.0.0"), Repo: repo, Datasource: "go",
				Package: fmt.Sprintf("example.com/p%d", i), Version: "v1.0.0", ReleasedAt: base, State: model.JobPending, NotBefore: nb}
			if ok, err := s.EnqueueJob(ctx, j); err != nil || !ok {
				t.Fatalf("enqueue: ok=%v err=%v", ok, err)
			}
		}
		for i := 0; i < 5; i++ {
			add("acme/big", i, base.Add(-time.Hour+time.Duration(i)*time.Second))
		}
		add("acme/small", 0, base.Add(-time.Minute))
		add("acme/other", 0, base.Add(-30*time.Second))
		due, err := s.ListDueJobsExcluding(ctx, base, 3, []string{"acme/big"})
		if err != nil || len(due) != 2 || due[0].Repo != "acme/small" || due[1].Repo != "acme/other" {
			t.Fatalf("excluding big: %v err=%v", due, err)
		}
		if due, _ = s.ListDueJobsExcluding(ctx, base, 3, nil); len(due) != 3 || due[0].Repo != "acme/big" {
			t.Fatalf("no exclusion is ListDueJobs: %v", due)
		}
		if due, _ = s.ListDueJobsExcluding(ctx, base, 10, []string{"acme/big", "acme/small"}); len(due) != 1 || due[0].Repo != "acme/other" {
			t.Fatalf("two excluded: %v", due)
		}
		if due, _ = s.ListDueJobsExcluding(ctx, base, 4, []string{"acme/small", "acme/other"}); len(due) != 4 {
			t.Fatalf("limit applies after the exclusion: %v", due)
		}
	})

	t.Run("many dependents of one package", func(t *testing.T) {
		s := mk(t)
		const n = 60
		for i := 0; i < n; i++ {
			r := fmt.Sprintf("acme/r%03d", i)
			if err := s.ReplaceDependencies(ctx, r, []model.Dependency{dep(r, "go.mod", "example.com/popular", "v1.0.0"), dep(r, "go.mod", "example.com/rare", "v1.0.0")}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.ListDependents(ctx, "go", "example.com/popular")
		if err != nil || len(got) != n {
			t.Fatalf("got %d dependents, err=%v", len(got), err)
		}
		for i, d := range got {
			if want := fmt.Sprintf("acme/r%03d", i); d.Repo != want {
				t.Fatalf("position %d is %s, want %s", i, d.Repo, want)
			}
		}
	})

	t.Run("events", func(t *testing.T) {
		s := mk(t)
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		push := model.Event{ID: "d1/0", Seq: 20, InstallationID: 7, Kind: "push", Repo: "acme/web", Ref: "refs/heads/main", Files: []string{"go.mod", "a/go.mod"}}
		added := model.Event{ID: "d0/0", Seq: 10, InstallationID: 8, Kind: "repos_added", Repo: "acme/api"}
		later := model.Event{ID: "d2/0", Seq: 30, InstallationID: 7, Kind: "repos_removed", Repo: "acme/web"}
		if evs, err := s.ListEvents(ctx); err != nil || len(evs) != 0 {
			t.Fatalf("empty store: %v err=%v", evs, err)
		}
		for _, ev := range []model.Event{push, later, added} {
			if ok, err := s.EnqueueEvent(ctx, ev); err != nil || !ok {
				t.Fatalf("enqueue %s: ok=%v err=%v", ev.ID, ok, err)
			}
		}
		dup := push
		dup.Repo = "acme/other"
		if ok, err := s.EnqueueEvent(ctx, dup); err != nil || ok {
			t.Fatalf("duplicate id: ok=%v err=%v", ok, err)
		}
		evs, err := s.ListEvents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := []model.Event{added, push, later}; !reflect.DeepEqual(evs, want) {
			t.Fatalf("list, oldest first:\n got %+v\nwant %+v", evs, want)
		}
		push.Attempts, push.NotBefore = 2, at
		if err := s.UpdateEvent(ctx, push); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteEvent(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteEvent(ctx, "absent"); err != nil {
			t.Fatalf("deleting an absent event: %v", err)
		}
		evs, _ = s.ListEvents(ctx)
		if want := []model.Event{push, later}; !reflect.DeepEqual(evs, want) {
			t.Fatalf("after update and delete:\n got %+v\nwant %+v", evs, want)
		}
	})

	t.Run("event typed fields", func(t *testing.T) {
		s := mk(t)
		ctx := context.Background()
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		rename := model.Event{ID: "r/0", Seq: 1, InstallationID: 7, Kind: "repo_renamed", Repo: "acme/old", Ref: "acme/new", NewRepo: "acme/new", RepoID: 99}
		branch := model.Event{ID: "b/0", Seq: 2, InstallationID: 7, Kind: "default_branch_changed", Repo: "acme/web", Ref: "refs/heads/trunk", DefaultBranch: "trunk"}
		push := model.Event{ID: "p/0", Seq: 3, InstallationID: 7, Kind: "push", Repo: "acme/web", Ref: "refs/heads/main", DefaultBranch: "main", RepoID: 5, Files: []string{"go.mod"}}
		for _, ev := range []model.Event{rename, branch, push} {
			if ok, err := s.EnqueueEvent(ctx, ev); err != nil || !ok {
				t.Fatalf("EnqueueEvent %s: %v %v", ev.ID, ok, err)
			}
		}
		check := func(got []model.Event, want ...model.Event) {
			t.Helper()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v\nwant %+v", got, want)
			}
		}
		live, err := s.ListEvents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check(live, rename, branch, push)
		// A failed attempt and dead-lettering keep the typed fields.
		rename.Attempts, rename.DeadAt, rename.LastAttemptAt, rename.LastError = 3, at, at, "boom"
		if err := s.UpdateEvent(ctx, rename); err != nil {
			t.Fatal(err)
		}
		dead, err := s.ListDeadEvents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		check(dead, rename)
	})

	t.Run("dead events", func(t *testing.T) {
		s := mk(t)
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		a := model.Event{ID: "a/0", Seq: 10, InstallationID: 7, Kind: "push", Repo: "acme/a", Ref: "refs/heads/main", Files: []string{"go.mod"}}
		b := model.Event{ID: "b/0", Seq: 20, InstallationID: 7, Kind: "repos_added", Repo: "acme/b"}
		c := model.Event{ID: "c/0", Seq: 30, InstallationID: 8, Kind: "repos_added", Repo: "acme/c"}
		for _, ev := range []model.Event{a, b, c} {
			if ok, err := s.EnqueueEvent(ctx, ev); err != nil || !ok {
				t.Fatalf("enqueue %s: ok=%v err=%v", ev.ID, ok, err)
			}
		}
		if dead, err := s.ListDeadEvents(ctx); err != nil || len(dead) != 0 {
			t.Fatalf("no dead events yet: %v err=%v", dead, err)
		}
		// A failed attempt is recorded on the live event.
		b.Attempts, b.NotBefore, b.LastError, b.LastAttemptAt = 1, at, "boom", at.Add(-time.Second)
		if err := s.UpdateEvent(ctx, b); err != nil {
			t.Fatal(err)
		}
		// b dies first, then a.
		b.Attempts, b.LastError, b.LastAttemptAt, b.DeadAt = 3, "boom 3", at, at
		a.Attempts, a.LastError, a.LastAttemptAt, a.DeadAt = 3, "later", at.Add(time.Hour), at.Add(time.Hour)
		for _, ev := range []model.Event{a, b} {
			if err := s.UpdateEvent(ctx, ev); err != nil {
				t.Fatal(err)
			}
		}
		live, _ := s.ListEvents(ctx)
		if len(live) != 1 || live[0].ID != c.ID {
			t.Fatalf("dead events must leave the live list: %+v", live)
		}
		dead, err := s.ListDeadEvents(ctx)
		if err != nil || !reflect.DeepEqual(dead, []model.Event{b, a}) {
			t.Fatalf("dead, earliest first:\n got %+v err=%v\nwant %+v", dead, err, []model.Event{b, a})
		}
		if ok, err := s.RequeueDeadEvent(ctx, "nope"); err != nil || ok {
			t.Fatalf("requeue of an unknown id: ok=%v err=%v", ok, err)
		}
		if ok, err := s.RequeueDeadEvent(ctx, c.ID); err != nil || ok {
			t.Fatalf("requeue of a live event: ok=%v err=%v", ok, err)
		}
		if ok, err := s.RequeueDeadEvent(ctx, a.ID); err != nil || !ok {
			t.Fatalf("requeue: ok=%v err=%v", ok, err)
		}
		if ok, _ := s.RequeueDeadEvent(ctx, a.ID); ok {
			t.Fatal("a requeued event is no longer dead")
		}
		a.Attempts, a.DeadAt, a.NotBefore = 0, time.Time{}, time.Time{}
		live, _ = s.ListEvents(ctx)
		if want := []model.Event{a, c}; !reflect.DeepEqual(live, want) {
			t.Fatalf("after requeue:\n got %+v\nwant %+v", live, want)
		}
		if dead, _ = s.ListDeadEvents(ctx); !reflect.DeepEqual(dead, []model.Event{b}) {
			t.Fatalf("dead after requeue: %+v", dead)
		}
		// Purge removes only dead events older than the cut-off.
		if n, err := s.PurgeDeadEvents(ctx, at); err != nil || n != 0 {
			t.Fatalf("nothing older than at: n=%d err=%v", n, err)
		}
		if n, err := s.PurgeDeadEvents(ctx, at.Add(time.Minute)); err != nil || n != 1 {
			t.Fatalf("purge: n=%d err=%v", n, err)
		}
		if dead, _ = s.ListDeadEvents(ctx); len(dead) != 0 {
			t.Fatalf("purged event still listed: %+v", dead)
		}
		if live, _ = s.ListEvents(ctx); len(live) != 2 {
			t.Fatalf("purge touched live events: %+v", live)
		}
		// A dead event can be deleted by id like a live one.
		c.DeadAt = at
		_ = s.UpdateEvent(ctx, c)
		if err := s.DeleteEvent(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
		if dead, _ = s.ListDeadEvents(ctx); len(dead) != 0 {
			t.Fatalf("deleted dead event still listed: %+v", dead)
		}
	})

	t.Run("many events", func(t *testing.T) {
		s := mk(t)
		for i := 0; i < 130; i++ {
			if _, err := s.EnqueueEvent(ctx, model.Event{ID: fmt.Sprintf("e%03d", i), Seq: int64(1000 - i), InstallationID: 1, Kind: "push", Repo: "acme/web"}); err != nil {
				t.Fatal(err)
			}
		}
		evs, err := s.ListEvents(ctx)
		if err != nil || len(evs) != 130 || evs[0].ID != "e129" || evs[129].ID != "e000" {
			t.Fatalf("len=%d err=%v", len(evs), err)
		}
	})

	t.Run("cursors", func(t *testing.T) {
		s := mk(t)
		if v, err := s.GetCursor(ctx, "feed:go"); err != nil || v != "" {
			t.Fatalf("v=%q err=%v", v, err)
		}
		_ = s.SetCursor(ctx, "feed:go", "2026-11-22T10:00:00Z")
		_ = s.SetCursor(ctx, "feed:go", "2026-11-22T11:00:00Z")
		if v, _ := s.GetCursor(ctx, "feed:go"); v != "2026-11-22T11:00:00Z" {
			t.Fatalf("v=%q", v)
		}
	})
}
