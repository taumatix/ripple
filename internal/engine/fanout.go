package engine

import (
	"context"
	"fmt"
	"time"

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
		if !ok {
			continue
		}
		rs, err := e.repoState(ctx, repos, d.Repo)
		if err != nil {
			return n, err
		}
		if !rs.found {
			continue
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
