// Package storetest is the conformance suite every store.Store must pass.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
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
		r := model.Repo{FullName: "acme/web", InstallationID: 42, DefaultBranch: "main", RepoID: 777, Config: []byte(`{"prLimit":3}`)}
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

	t.Run("one package at two versions in one file", func(t *testing.T) {
		// `actions/checkout@v4` and `actions/checkout@v5.0.0` in one workflow, two FROM lines
		// naming one image, a dependency repeated in devDependencies: each is its own row.
		s := mk(t)
		v4 := dep("acme/web", "ci.yml", "actions/checkout", "v4")
		v5 := dep("acme/web", "ci.yml", "actions/checkout", "v5.0.0")
		other := dep("acme/web", "ci.yml", "actions/cache", "v4")
		for _, d := range []*model.Dependency{&v4, &v5, &other} {
			d.Datasource = "github-tags"
		}
		if err := s.ReplaceDependencies(ctx, "acme/web", []model.Dependency{v5, v4, other, v4}); err != nil {
			t.Fatal(err)
		}
		want := []model.Dependency{other, v4, v5}
		own, err := s.ListRepoDependencies(ctx, "acme/web")
		if err != nil || !sameDeps(own, want) {
			t.Fatalf("repo deps %v (%v), want %v", own, err, want)
		}
		got, err := s.ListDependents(ctx, "github-tags", "actions/checkout")
		if err != nil || !sameDeps(got, []model.Dependency{v4, v5}) {
			t.Fatalf("dependents %v (%v), want v4 and v5.0.0", got, err)
		}
		// Moving one of them leaves the other alone; dropping one drops only it.
		v4b := v4
		v4b.Version = "v4.1"
		_ = s.ReplaceDependencies(ctx, "acme/web", []model.Dependency{v5, v4b})
		got, _ = s.ListDependents(ctx, "github-tags", "actions/checkout")
		if !sameDeps(got, []model.Dependency{v4b, v5}) {
			t.Fatalf("after a move: %v", got)
		}
		_ = s.ReplaceDependencies(ctx, "acme/web", []model.Dependency{v5})
		got, _ = s.ListDependents(ctx, "github-tags", "actions/checkout")
		if !sameDeps(got, []model.Dependency{v5}) {
			t.Fatalf("after a drop: %v", got)
		}
		_ = s.ReplaceDependencies(ctx, "acme/web", nil)
		if got, _ = s.ListDependents(ctx, "github-tags", "actions/checkout"); len(got) != 0 {
			t.Fatalf("after clearing: %v", got)
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

	t.Run("repos in pages", func(t *testing.T) {
		s := mk(t)
		rl, ok := s.(store.RepoLister)
		if !ok {
			t.Log("store has no repo listing")
			return
		}
		for _, n := range []string{"acme/c", "acme/a", "acme/b", "beta/z"} {
			if err := s.UpsertRepo(ctx, model.Repo{FullName: n, DefaultBranch: "main"}); err != nil {
				t.Fatal(err)
			}
		}
		var all []string
		after := ""
		for i := 0; i < 5; i++ {
			page, err := rl.ListRepos(ctx, after, 3)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page...)
			if len(page) < 3 {
				break
			}
			after = page[len(page)-1]
		}
		if !reflect.DeepEqual(all, []string{"acme/a", "acme/b", "acme/c", "beta/z"}) {
			t.Fatalf("repos: %v", all)
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

	t.Run("get event", func(t *testing.T) {
		s := mk(t)
		e := model.Event{ID: "g1", Seq: 5, InstallationID: 1, Kind: "push", Repo: "acme/web", Files: []string{"go.mod"}}
		if _, ok, err := s.GetEvent(ctx, "g1"); err != nil || ok {
			t.Fatalf("absent event: ok=%v err=%v", ok, err)
		}
		_, _ = s.EnqueueEvent(ctx, e)
		got, ok, err := s.GetEvent(ctx, "g1")
		if err != nil || !ok || got.ID != "g1" || got.Repo != "acme/web" || len(got.Files) != 1 {
			t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
		}
		e.Attempts, e.DeadAt = 3, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		_ = s.UpdateEvent(ctx, e)
		if got, ok, _ = s.GetEvent(ctx, "g1"); !ok || got.Attempts != 3 || !got.Dead() {
			t.Fatalf("dead event not returned: %+v ok=%v", got, ok)
		}
		_ = s.DeleteEvent(ctx, "g1")
		if _, ok, _ = s.GetEvent(ctx, "g1"); ok {
			t.Fatal("deleted event still returned")
		}
	})

	t.Run("lease exclusion, renewal and takeover", func(t *testing.T) {
		s := mk(t)
		const ttl = 400 * time.Millisecond
		a, ok, err := s.AcquireLease(ctx, "repo:acme/web", "A", ttl)
		if err != nil || !ok || a.Owner != "A" || a.Token < 1 || a.Name != "repo:acme/web" {
			t.Fatalf("first acquire: %+v ok=%v err=%v", a, ok, err)
		}
		if _, ok, err := s.AcquireLease(ctx, "repo:acme/web", "B", ttl); err != nil || ok {
			t.Fatalf("B acquired a held lease: ok=%v err=%v", ok, err)
		}
		if _, ok, _ := s.AcquireLease(ctx, "repo:acme/other", "B", ttl); !ok {
			t.Fatal("a different name is independent")
		}
		// The holder asking again extends and keeps the token.
		again, ok, _ := s.AcquireLease(ctx, "repo:acme/web", "A", ttl)
		if !ok || again.Token != a.Token {
			t.Fatalf("re-acquire by holder: %+v ok=%v", again, ok)
		}
		// Renewal keeps it from lapsing.
		for i := 0; i < 3; i++ {
			time.Sleep(ttl / 2)
			if a, ok, err = s.RenewLease(ctx, a, ttl); err != nil || !ok {
				t.Fatalf("renew %d: ok=%v err=%v", i, ok, err)
			}
		}
		if _, ok, _ := s.AcquireLease(ctx, "repo:acme/web", "B", ttl); ok {
			t.Fatal("B took over a renewed lease")
		}
		// Left alone it lapses and B takes over with a higher token; A's renewal is then refused.
		time.Sleep(ttl + 300*time.Millisecond)
		b, ok, err := s.AcquireLease(ctx, "repo:acme/web", "B", ttl)
		if err != nil || !ok || b.Token <= a.Token {
			t.Fatalf("takeover: %+v ok=%v err=%v (old token %d)", b, ok, err, a.Token)
		}
		if _, ok, _ := s.RenewLease(ctx, a, ttl); ok {
			t.Fatal("the displaced holder renewed")
		}
		if err := s.ReleaseLease(ctx, a); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.AcquireLease(ctx, "repo:acme/web", "A", ttl); ok {
			t.Fatal("a stale release freed B's lease")
		}
		if err := s.ReleaseLease(ctx, b); err != nil {
			t.Fatal(err)
		}
		if c, ok, _ := s.AcquireLease(ctx, "repo:acme/web", "A", ttl); !ok || c.Owner != "A" {
			t.Fatalf("released lease not claimable: %+v ok=%v", c, ok)
		}
	})

	t.Run("lease race has one winner", func(t *testing.T) {
		s := mk(t)
		const racers = 8
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, ok, err := s.AcquireLease(ctx, "event:e1", fmt.Sprintf("r%d", i), time.Minute); err != nil {
					t.Error(err)
				} else if ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d replicas acquired the same lease, want 1", wins.Load())
		}
	})

	t.Run("older events of a repo", func(t *testing.T) {
		s := mk(t)
		ev := func(id string, seq int64, repo string) model.Event {
			return model.Event{ID: id, Seq: seq, InstallationID: 7, Kind: "push", Repo: repo}
		}
		old, mid, api := ev("a", 10, "acme/web"), ev("b", 20, "acme/web"), ev("c", 5, "acme/api")
		dead := ev("d", 1, "acme/web")
		for _, e := range []model.Event{old, mid, api, dead} {
			if _, err := s.EnqueueEvent(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		dead.DeadAt = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		if err := s.UpdateEvent(ctx, dead); err != nil {
			t.Fatal(err)
		}
		older := func(e model.Event) bool {
			t.Helper()
			ok, err := s.HasOlderEvent(ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if !older(mid) {
			t.Error("acme/web seq 20 has an older live event (seq 10)")
		}
		if older(old) {
			t.Error("a dead-lettered event (seq 1) must not hold back seq 10")
		}
		if older(api) || !older(ev("z", 99, "acme/api")) {
			t.Error("events of another repo must not count, and a later one must see seq 5")
		}
		if !older(ev("a0", 10, "acme/web")) {
			t.Error("same seq: event a is older than a0 by id")
		}
		if older(ev("0", 10, "acme/web")) {
			t.Error("same seq: nothing is older than id 0")
		}
		if err := s.DeleteEvent(ctx, old.ID); err != nil {
			t.Fatal(err)
		}
		if older(mid) {
			t.Error("the older event was handled and deleted")
		}
	})

	t.Run("store-assigned event sequence", func(t *testing.T) {
		s := mk(t)
		q, ok := s.(store.EventSequencer)
		if !ok {
			t.Fatal("store has no EventSequencer")
		}
		// An event stored before the sequence existed carries a clock Seq; new ones sort after it.
		before := model.Event{ID: "old", Seq: time.Now().Add(-time.Hour).UnixNano(), InstallationID: 7, Kind: "push", Repo: "acme/web"}
		if _, err := s.EnqueueEvent(ctx, before); err != nil {
			t.Fatal(err)
		}
		in := func(id, repo string) model.Event {
			// A caller's own Seq is ignored: this one claims to be first of all.
			return model.Event{ID: id, Seq: 1, InstallationID: 7, Kind: "push", Repo: repo}
		}
		a, okA, err := q.EnqueueSequenced(ctx, in("a", "acme/web"))
		if err != nil || !okA {
			t.Fatalf("a: ok=%v err=%v", okA, err)
		}
		b, okB, err := q.EnqueueSequenced(ctx, in("b", "acme/web"))
		if err != nil || !okB {
			t.Fatalf("b: ok=%v err=%v", okB, err)
		}
		if a.Seq <= before.Seq || b.Seq <= a.Seq {
			t.Fatalf("seq old=%d a=%d b=%d; want strictly increasing and above the earlier row", before.Seq, a.Seq, b.Seq)
		}
		if dup, ok, err := q.EnqueueSequenced(ctx, in("a", "acme/other")); err != nil || ok {
			t.Fatalf("duplicate id: ok=%v err=%v (seq %d)", ok, err, dup.Seq)
		}
		got, err := s.ListEvents(ctx)
		if err != nil || len(got) != 3 || got[0].ID != "old" || got[1].ID != "a" || got[1].Seq != a.Seq || got[1].Repo != "acme/web" || got[2].ID != "b" || got[2].Seq != b.Seq {
			t.Fatalf("list = %+v, err=%v; want old, a, b with the returned Seq and a's first Repo", got, err)
		}
		if ok, err := s.HasOlderEvent(ctx, b); err != nil || !ok {
			t.Fatalf("b has an older event (a): %v %v", ok, err)
		}
		// Racing writers all get distinct numbers and every event is stored.
		const writers = 8
		seqs := make(chan int64, writers)
		errs := make(chan error, writers)
		for i := range writers {
			go func() {
				e, ok, err := q.EnqueueSequenced(ctx, in(fmt.Sprintf("r%d", i), "acme/api"))
				if err == nil && !ok {
					err = fmt.Errorf("r%d not new", i)
				}
				seqs <- e.Seq
				errs <- err
			}()
		}
		seen := map[int64]bool{}
		for range writers {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			seen[<-seqs] = true
		}
		if len(seen) != writers {
			t.Fatalf("%d distinct numbers for %d racing events", len(seen), writers)
		}
		if got, _ := s.ListEvents(ctx); len(got) != 3+writers {
			t.Fatalf("%d events stored, want %d", len(got), 3+writers)
		}
	})

	t.Run("fenced job update", func(t *testing.T) {
		s := mk(t)
		const ttl = 400 * time.Millisecond
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		j := model.Job{Key: model.JobKey("acme/web", "go", "example.com/a", "v1.1.0"), Repo: "acme/web", Datasource: "go",
			Package: "example.com/a", Version: "v1.1.0", ReleasedAt: base, State: model.JobPending, NotBefore: base}
		if _, err := s.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		a, ok, err := s.AcquireLease(ctx, "repo:acme/web", "A", ttl)
		if err != nil || !ok {
			t.Fatalf("acquire: %v %v", ok, err)
		}
		j.Attempts, j.Reason = 1, "first"
		if err := s.UpdateJobFenced(ctx, j, a); err != nil {
			t.Fatalf("the holder's write: %v", err)
		}
		if got, _, _ := s.GetJob(ctx, j.Key); got.Attempts != 1 {
			t.Fatalf("holder's write lost: %+v", got)
		}
		// A stalls past its ttl; B takes over and finishes the job.
		time.Sleep(ttl + 300*time.Millisecond)
		b, ok, err := s.AcquireLease(ctx, "repo:acme/web", "B", ttl)
		if err != nil || !ok {
			t.Fatalf("takeover: %v %v", ok, err)
		}
		done := j
		done.State, done.PRURL = model.JobDone, "https://example.test/pr/1"
		if err := s.UpdateJobFenced(ctx, done, b); err != nil {
			t.Fatalf("the new holder's write: %v", err)
		}
		// A wakes up and writes its stale view of the job: refused, and nothing changes.
		stale := j
		stale.State, stale.Reason = model.JobFailed, "stale"
		if err := s.UpdateJobFenced(ctx, stale, a); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("the displaced holder's write: %v, want ErrLeaseLost", err)
		}
		if got, _, _ := s.GetJob(ctx, j.Key); got.State != model.JobDone || got.PRURL != done.PRURL {
			t.Fatalf("stale write landed: %+v", got)
		}
		if lister, ok := s.(store.OpenJobLister); ok {
			if open, _ := lister.ListOpenJobs(ctx, "acme/web"); len(open) != 0 {
				t.Fatalf("stale write reopened the job: %+v", open)
			}
		}
		if err := s.ReleaseLease(ctx, b); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateJobFenced(ctx, stale, b); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("write under a released lease: %v, want ErrLeaseLost", err)
		}
	})

	t.Run("fenced cursor and event writes", func(t *testing.T) {
		s := mk(t)
		const ttl = 400 * time.Millisecond
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		evt := model.Event{ID: "e1", Seq: 1, Kind: "push", Repo: "acme/web", NotBefore: at}
		if _, err := s.EnqueueEvent(ctx, evt); err != nil {
			t.Fatal(err)
		}
		a, ok, err := s.AcquireLease(ctx, "event:e1", "A", ttl)
		if err != nil || !ok {
			t.Fatalf("acquire: %v %v", ok, err)
		}
		// The holder's writes land.
		if err := s.SetCursorFenced(ctx, "paused:acme/web", "1", a); err != nil {
			t.Fatalf("holder's cursor write: %v", err)
		}
		if v, _ := s.GetCursor(ctx, "paused:acme/web"); v != "1" {
			t.Fatalf("cursor = %q", v)
		}
		retry := evt
		retry.Attempts, retry.LastError, retry.NotBefore = 1, "boom", at.Add(time.Minute)
		if err := s.UpdateEventFenced(ctx, retry, a); err != nil {
			t.Fatalf("holder's event update: %v", err)
		}
		if got, _, _ := s.GetEvent(ctx, "e1"); got.Attempts != 1 || got.LastError != "boom" {
			t.Fatalf("holder's update lost: %+v", got)
		}
		dead := retry
		dead.DeadAt = at.Add(time.Hour)
		if err := s.UpdateEventFenced(ctx, dead, a); err != nil {
			t.Fatalf("holder's dead-letter: %v", err)
		}
		if live, _ := s.ListEvents(ctx); len(live) != 0 {
			t.Fatalf("a dead-lettered event is still live: %+v", live)
		}
		if d, _ := s.ListDeadEvents(ctx); len(d) != 1 {
			t.Fatalf("dead events = %+v", d)
		}
		// An event that is already gone is a no-op for the holder, as in UpdateEvent.
		if err := s.UpdateEventFenced(ctx, model.Event{ID: "gone", Seq: 2, Repo: "acme/web", NotBefore: at}, a); err != nil {
			t.Fatalf("update of a missing event: %v", err)
		}
		if _, ok, _ := s.GetEvent(ctx, "gone"); ok {
			t.Fatal("update of a missing event created it")
		}
		// A stalls past its ttl; B takes the lease over. A's late writes are refused.
		time.Sleep(ttl + 300*time.Millisecond)
		b, ok, err := s.AcquireLease(ctx, "event:e1", "B", ttl)
		if err != nil || !ok {
			t.Fatalf("takeover: %v %v", ok, err)
		}
		if err := s.SetCursorFenced(ctx, "paused:acme/web", "stale", a); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("displaced cursor write: %v, want ErrLeaseLost", err)
		}
		if v, _ := s.GetCursor(ctx, "paused:acme/web"); v != "1" {
			t.Fatalf("stale cursor write landed: %q", v)
		}
		stale := dead
		stale.Attempts, stale.DeadAt = 9, time.Time{}
		if err := s.UpdateEventFenced(ctx, stale, a); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("displaced event update: %v, want ErrLeaseLost", err)
		}
		if live, _ := s.ListEvents(ctx); len(live) != 0 {
			t.Fatalf("stale update revived the event: %+v", live)
		}
		if err := s.DeleteEventFenced(ctx, "e1", a); !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("displaced delete: %v, want ErrLeaseLost", err)
		}
		if _, ok, _ := s.GetEvent(ctx, "e1"); !ok {
			t.Fatal("stale delete removed the event")
		}
		if err := s.DeleteEventFenced(ctx, "e1", b); err != nil {
			t.Fatalf("new holder's delete: %v", err)
		}
		if _, ok, _ := s.GetEvent(ctx, "e1"); ok {
			t.Fatal("holder's delete did not remove the event")
		}
		// Deleting what is gone is not an error for the holder.
		if err := s.DeleteEventFenced(ctx, "e1", b); err != nil {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("lease contention", func(t *testing.T) {
		s := mk(t)
		const racers, rounds = 6, 8
		base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		j := model.Job{Key: model.JobKey("acme/web", "go", "example.com/a", "v1.1.0"), Repo: "acme/web", Datasource: "go",
			Package: "example.com/a", Version: "v1.1.0", ReleasedAt: base, State: model.JobPending, NotBefore: base}
		if _, err := s.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		// Every racer takes the lease, reads the job, bumps Attempts under the fence and
		// releases: no two are inside at once, and no increment is lost.
		var inside atomic.Int32
		var wg sync.WaitGroup
		for r := 0; r < racers; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				owner := fmt.Sprintf("r%d", r)
				for n := 0; n < rounds; n++ {
					var l store.Lease
					for {
						var ok bool
						var err error
						if l, ok, err = s.AcquireLease(ctx, "repo:acme/web", owner, time.Minute); err != nil {
							t.Error(err)
							return
						} else if ok {
							break
						}
						time.Sleep(time.Millisecond)
					}
					if inside.Add(1) != 1 {
						t.Errorf("%s entered while another replica held the lease", owner)
					}
					cur, _, err := s.GetJob(ctx, j.Key)
					if err != nil {
						t.Error(err)
					}
					cur.Attempts++
					if err := s.UpdateJobFenced(ctx, cur, l); err != nil {
						t.Errorf("%s: fenced write under its own lease: %v", owner, err)
					}
					inside.Add(-1)
					if err := s.ReleaseLease(ctx, l); err != nil {
						t.Error(err)
					}
				}
			}()
		}
		wg.Wait()
		if got, _, _ := s.GetJob(ctx, j.Key); got.Attempts != racers*rounds {
			t.Fatalf("Attempts=%d, want %d: an update was lost", got.Attempts, racers*rounds)
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

	t.Run("live replicas", func(t *testing.T) {
		r, ok := mk(t).(store.ReplicaTable)
		if !ok {
			t.Fatal("store has no ReplicaTable")
		}
		if got, err := r.LiveReplicas(ctx); err != nil || len(got) != 0 {
			t.Fatalf("empty table = %v, %v", got, err)
		}
		for _, in := range []store.ReplicaInfo{{ID: "b", Takeovers: 2, FeedPoller: true}, {ID: "a"}} {
			if err := r.Heartbeat(ctx, in, time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Heartbeat(ctx, store.ReplicaInfo{ID: "b", Takeovers: 3}, time.Minute); err != nil { // a later beat replaces the earlier one
			t.Fatal(err)
		}
		if err := r.Heartbeat(ctx, store.ReplicaInfo{ID: "gone"}, 50*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1200 * time.Millisecond) // past the short ttl; Postgres judges it by its own clock
		got, err := r.LiveReplicas(ctx)
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || got[1].Takeovers != 3 || got[1].FeedPoller || got[1].Seen.IsZero() {
			t.Fatalf("live = %+v, %v; want a and b (b at 3 takeovers, not polling), not the lapsed one", got, err)
		}
		if err := r.RetireReplica(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if err := r.RetireReplica(ctx, "never-beat"); err != nil {
			t.Fatalf("retiring an absent replica: %v", err)
		}
		if got, _ := r.LiveReplicas(ctx); len(got) != 1 || got[0].ID != "b" {
			t.Fatalf("after retire = %+v", got)
		}
	})

	t.Run("delivery table", func(t *testing.T) {
		d, ok := mk(t).(store.DeliveryTable)
		if !ok {
			t.Fatal("store has no DeliveryTable")
		}
		at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		if got, err := d.ClaimDelivery(ctx, "github/d1", at, time.Hour); err != nil || !got {
			t.Fatalf("first claim = %v, %v", got, err)
		}
		if got, err := d.ClaimDelivery(ctx, "github/d1", at.Add(30*time.Minute), time.Hour); err != nil || got {
			t.Fatalf("repeat inside the ttl = %v, %v; want false", got, err)
		}
		if got, _ := d.ClaimDelivery(ctx, "github/d2", at, time.Hour); !got {
			t.Fatal("another key was refused")
		}
		if got, err := d.ClaimDelivery(ctx, "github/d1", at.Add(time.Hour), time.Hour); err != nil || !got {
			t.Fatalf("claim at expiry = %v, %v; want true", got, err)
		}
		if err := d.ReleaseDelivery(ctx, "github/d1"); err != nil {
			t.Fatal(err)
		}
		if got, _ := d.ClaimDelivery(ctx, "github/d1", at.Add(time.Hour), time.Hour); !got {
			t.Fatal("a released key was refused")
		}
		if err := d.ReleaseDelivery(ctx, "never-claimed"); err != nil {
			t.Fatalf("releasing an absent key: %v", err)
		}
	})

	t.Run("delivery table races", func(t *testing.T) {
		d := mk(t).(store.DeliveryTable)
		at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		var won atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got, err := d.ClaimDelivery(ctx, "github/race", at, time.Hour); err == nil && got {
					won.Add(1)
				}
			}()
		}
		wg.Wait()
		if won.Load() != 1 {
			t.Fatalf("%d of 8 racing claims won, want exactly 1", won.Load())
		}
	})

	t.Run("delivery table purge", func(t *testing.T) {
		d := mk(t).(store.DeliveryTable)
		at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		_, _ = d.ClaimDelivery(ctx, "old", at, time.Hour)
		_, _ = d.ClaimDelivery(ctx, "new", at, 3*time.Hour)
		n, err := d.PurgeDeliveries(ctx, at.Add(2*time.Hour))
		if err != nil || n != 1 {
			t.Fatalf("purged %d, %v; want 1 (only the expired key)", n, err)
		}
		if got, _ := d.ClaimDelivery(ctx, "new", at.Add(2*time.Hour), time.Hour); got {
			t.Fatal("the live key was purged")
		}
		if got, _ := d.ClaimDelivery(ctx, "old", at.Add(2*time.Hour), time.Hour); !got {
			t.Fatal("the purged key was still held")
		}
	})

	t.Run("cursor listing", func(t *testing.T) {
		s := mk(t)
		cl, ok := s.(store.CursorLister)
		if !ok {
			t.Fatalf("%T does not list cursors", s)
		}
		for n, v := range map[string]string{
			"pkgs#actions/checkout": "1", "pkgs#actions/cache": "1", "pkgs#registry:5000/img": "2",
			"other#x": "9", "pkgs": "legacy", "plain:one": "p1", "plain:two": "p2", "unplain": "u",
		} {
			if err := s.SetCursor(ctx, n, v); err != nil {
				t.Fatal(err)
			}
		}
		names := func(prefix string) []string {
			t.Helper()
			got, err := cl.ListCursors(ctx, prefix)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, c := range got {
				if v, _ := s.GetCursor(ctx, c.Name); v != c.Value {
					t.Errorf("%s listed with %q, stored %q", c.Name, c.Value, v)
				}
				out = append(out, c.Name)
			}
			sort.Strings(out)
			return out
		}
		eq := func(prefix string, want ...string) {
			t.Helper()
			if got := names(prefix); !reflect.DeepEqual(got, want) {
				t.Errorf("ListCursors(%q) = %v, want %v", prefix, got, want)
			}
		}
		eq("pkgs#", "pkgs#actions/cache", "pkgs#actions/checkout", "pkgs#registry:5000/img")
		eq("pkgs#actions/", "pkgs#actions/cache", "pkgs#actions/checkout")
		eq("other#", "other#x")
		eq("pkgs#nothing")
		eq("plain:", "plain:one", "plain:two") // a prefix with no group is a scan, same answer
		eq("un", "unplain")
		if err := cl.DeleteCursor(ctx, "pkgs#actions/cache"); err != nil {
			t.Fatal(err)
		}
		if err := cl.DeleteCursor(ctx, "never-set"); err != nil {
			t.Fatalf("deleting an absent cursor: %v", err)
		}
		eq("pkgs#", "pkgs#actions/checkout", "pkgs#registry:5000/img")
		if v, _ := s.GetCursor(ctx, "pkgs#actions/cache"); v != "" {
			t.Errorf("deleted cursor reads %q", v)
		}
		if err := s.SetCursor(ctx, "pkgs#actions/checkout", "3"); err != nil {
			t.Fatal(err)
		}
		if got, _ := cl.ListCursors(ctx, "pkgs#actions/checkout"); len(got) != 1 || got[0].Value != "3" {
			t.Errorf("an overwritten cursor lists once with its new value: %v", got)
		}
	})

	t.Run("cursor purge by group", func(t *testing.T) {
		s := mk(t)
		c := s.(store.CursorPurger)
		for k, v := range map[string]string{"removed#a/x": "100|0", "removed#a/y": "900|0", "removed#a/cleared": "", "other#a/z": "100|0", "removed|a/legacy": "100|0"} {
			if err := s.SetCursor(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		n, err := c.PurgeCursors(ctx, "removed#", func(v string) bool { return v == "" || v == "100|0" })
		if err != nil || n != 2 {
			t.Fatalf("purged %d, %v; want 2", n, err)
		}
		for k, want := range map[string]string{"removed#a/x": "", "removed#a/cleared": "", "removed#a/y": "900|0", "other#a/z": "100|0", "removed|a/legacy": "100|0"} {
			if got, _ := s.GetCursor(ctx, k); got != want {
				t.Fatalf("cursor %q = %q, want %q", k, got, want)
			}
		}
	})

	t.Run("cursor purge", func(t *testing.T) {
		s := mk(t)
		c, ok := s.(store.CursorPurger)
		if !ok {
			t.Fatal("store has no CursorPurger")
		}
		for k, v := range map[string]string{"removed|a/x": "100|0", "removed|a/y": "900|0", "other|a/z": "100|0", "removed|a/under_score": "100|0"} {
			if err := s.SetCursor(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		n, err := c.PurgeCursors(ctx, "removed|", func(v string) bool { return v == "100|0" })
		if err != nil || n != 2 {
			t.Fatalf("purged %d, %v; want 2", n, err)
		}
		for k, want := range map[string]string{"removed|a/x": "", "removed|a/under_score": "", "removed|a/y": "900|0", "other|a/z": "100|0"} {
			if got, _ := s.GetCursor(ctx, k); got != want {
				t.Fatalf("cursor %q = %q, want %q", k, got, want)
			}
		}
	})

}

// sameDeps compares two dependency sets regardless of order.
func sameDeps(a, b []model.Dependency) bool {
	key := func(d model.Dependency) string { return d.File + "|" + d.Package + "|" + d.Version }
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, d := range a {
		m[key(d)]++
	}
	for _, d := range b {
		m[key(d)]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}
