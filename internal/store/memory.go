package store

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/taumatix/ripple/internal/model"
)

// Memory is an in-process Store for fast unit tests.
type Memory struct {
	mu       sync.Mutex
	repos    map[string]model.Repo
	deps     map[string][]model.Dependency
	releases map[string]model.Release
	jobs     map[string]model.Job
	cursors  map[string]string
	events   map[string]model.Event
}

func NewMemory() *Memory {
	return &Memory{
		repos: map[string]model.Repo{}, deps: map[string][]model.Dependency{},
		releases: map[string]model.Release{}, jobs: map[string]model.Job{}, cursors: map[string]string{},
	}
}

func (m *Memory) UpsertRepo(_ context.Context, r model.Repo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repos[r.FullName] = r
	return nil
}

func (m *Memory) GetRepo(_ context.Context, n string) (model.Repo, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.repos[n]
	return r, ok, nil
}

func (m *Memory) DeleteRepo(_ context.Context, n string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repos, n)
	delete(m.deps, n)
	return nil
}

func (m *Memory) ReplaceDependencies(_ context.Context, repo string, deps []model.Dependency) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deps[repo] = append([]model.Dependency(nil), deps...)
	return nil
}

func (m *Memory) ListRepoDependencies(_ context.Context, repo string) ([]model.Dependency, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]model.Dependency(nil), m.deps[repo]...)
	SortDependencies(out)
	return out, nil
}

func (m *Memory) ListDependents(_ context.Context, ds, pkg string) ([]model.Dependency, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	line := model.Line(ds, pkg)
	var out []model.Dependency
	for _, deps := range m.deps {
		for _, d := range deps {
			if d.Datasource == ds && model.Line(ds, d.Package) == line {
				out = append(out, d)
			}
		}
	}
	SortDependencies(out)
	return out, nil
}

func (m *Memory) RecordRelease(_ context.Context, r model.Release) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := r.Datasource + "|" + r.Package + "|" + r.Version
	if _, ok := m.releases[k]; ok {
		return false, nil
	}
	m.releases[k] = r
	return true, nil
}

func (m *Memory) ListReleases(_ context.Context, ds, pkg string) ([]model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Release
	for _, r := range m.releases {
		if r.Datasource == ds && r.Package == pkg {
			r.Time = r.Time.UTC()
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (m *Memory) EnqueueJob(_ context.Context, j model.Job) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.Key]; ok {
		return false, nil
	}
	m.jobs[j.Key] = j
	return true, nil
}

func (m *Memory) GetJob(_ context.Context, k string) (model.Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[k]
	return j, ok, nil
}

func (m *Memory) UpdateJob(_ context.Context, j model.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.Key] = j
	return nil
}

func (m *Memory) ListDueJobs(ctx context.Context, now time.Time, limit int) ([]model.Job, error) {
	return m.ListDueJobsExcluding(ctx, now, limit, nil)
}

func (m *Memory) ListDueJobsExcluding(_ context.Context, now time.Time, limit int, repos []string) ([]model.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Job
	for _, j := range m.jobs {
		if j.State == model.JobPending && !j.NotBefore.After(now) && !slices.Contains(repos, j.Repo) {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if !out[i].NotBefore.Equal(out[k].NotBefore) {
			return out[i].NotBefore.Before(out[k].NotBefore)
		}
		return out[i].Key < out[k].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) GetCursor(_ context.Context, n string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[n], nil
}

func (m *Memory) SetCursor(_ context.Context, n, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors[n] = v
	return nil
}

func (m *Memory) Close() error { return nil }

// SortDependencies orders by repo, file, datasource, package for stable output.
func SortDependencies(d []model.Dependency) {
	sort.Slice(d, func(i, j int) bool {
		a, b := d[i], d[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Datasource != b.Datasource {
			return a.Datasource < b.Datasource
		}
		return a.Package < b.Package
	})
}
