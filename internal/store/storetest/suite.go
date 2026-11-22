// Package storetest is the conformance suite every store.Store must pass.
package storetest

import (
	"context"
	"fmt"
	"reflect"
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
