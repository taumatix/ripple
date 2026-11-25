package engine

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

// FanOut turns one released version into jobs, but only in repos that import the
// package: one reverse-index lookup, no platform calls. It returns how many jobs
// were newly enqueued. It is idempotent, which is what makes the feed cursor safe
// to replay.
func (e *Engine) FanOut(ctx context.Context, rel model.Release) (int, error) {
	deps, err := e.Store.ListDependents(ctx, rel.Datasource, rel.Package)
	if err != nil {
		return 0, fmt.Errorf("list dependents of %s: %w", rel.Package, err)
	}
	n := 0
	done := map[string]bool{}
	repos := map[string]*repoState{}
	for _, d := range deps {
		if done[d.Repo] {
			continue
		}
		typ, ok := applicable(d, rel.Datasource, rel.Package, rel.Version)
		if !ok && !module.IsPseudoVersion(d.Version) {
			continue
		}
		rs, err := e.repoState(ctx, repos, d.Repo)
		if err != nil {
			return n, err
		}
		if !rs.found {
			continue
		}
		if !ok {
			// A dependency at a pseudo-version moves on a release only where the entry opts in.
			if rs.cfgErr != nil || rs.cfg == nil {
				continue
			}
			if u, have := rs.cfg.UpdateFor(d.Manager, d.File); !have || !u.FromPseudoVersions {
				continue
			}
			if typ, ok = applicablePseudoRelease(d, rel.Package, rel.Version, rel.Time); !ok {
				continue
			}
			if lower, err := e.lowerPseudoRelease(ctx, d, rel); err != nil {
				return n, err
			} else if lower {
				continue // an earlier release can already contain the commit: that one is proposed
			}
		}
		dec, ok := e.decide(rs, d, typ, rel.Time)
		if !ok || !dec.Enabled {
			continue
		}
		done[d.Repo] = true
		inserted, err := e.Store.EnqueueJob(ctx, model.Job{
			Key: model.JobKey(d.Repo, rel.Datasource, rel.Package, rel.Version), Repo: d.Repo,
			Datasource: rel.Datasource, Package: rel.Package, Version: rel.Version,
			ReleasedAt: rel.Time, State: model.JobPending, NotBefore: dec.NotBefore,
		})
		if err != nil {
			return n, err
		}
		if inserted {
			n++
			e.markDashboard(d.Repo)
		}
	}
	return n, nil
}

// repoState is what FanOut needs to know about one repo, loaded once per release
// however many dependency rows the repo has: the stored repo and its parsed file.
type repoState struct {
	found  bool
	cfg    *config.Repo
	cfgErr error
}

func (e *Engine) repoState(ctx context.Context, cache map[string]*repoState, name string) (*repoState, error) {
	if rs, ok := cache[name]; ok {
		return rs, nil
	}
	repo, found, err := e.Store.GetRepo(ctx, name)
	if err != nil {
		return nil, err
	}
	rs := &repoState{found: found}
	if found {
		parse := e.parseConfig
		if parse == nil {
			parse = RepoConfig
		}
		rs.cfg, rs.cfgErr = parse(repo.Config)
	}
	cache[name] = rs
	return rs, nil
}

// decide evaluates the repo's update entry for one dependency. ok is false when the
// repo file is invalid or no entry covers the dependency.
func (e *Engine) decide(rs *repoState, d model.Dependency, typ rules.UpdateType, released time.Time) (rules.Decision, bool) {
	if rs.cfgErr != nil {
		return rules.Decision{}, false
	}
	u, ok := rs.cfg.UpdateFor(d.Manager, d.File)
	if !ok {
		return rules.Decision{}, false
	}
	return rules.Evaluate(e.Limits, u, d.Package, typ, released, e.now()), true
}

// lowerPseudoRelease reports whether a stored release below rel would also move d off its
// pseudo-version (the "first release above the commit" rule of from-pseudo-versions).
func (e *Engine) lowerPseudoRelease(ctx context.Context, d model.Dependency, rel model.Release) (bool, error) {
	rels, err := e.Store.ListReleases(ctx, rel.Datasource, rel.Package)
	if err != nil {
		return false, fmt.Errorf("list releases of %s: %w", rel.Package, err)
	}
	for _, o := range rels {
		if semver.Compare(o.Version, rel.Version) >= 0 {
			continue
		}
		if _, ok := applicablePseudoRelease(d, o.Package, o.Version, o.Time); ok {
			return true, nil
		}
	}
	return false, nil
}
