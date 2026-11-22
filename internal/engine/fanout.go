package engine

import (
	"context"
	"fmt"
	"time"

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
	for _, d := range deps {
		if done[d.Repo] {
			continue
		}
		typ, ok := applicable(d, rel.Datasource, rel.Package, rel.Version)
		if !ok {
			continue
		}
		repo, found, err := e.Store.GetRepo(ctx, d.Repo)
		if err != nil {
			return n, err
		}
		if !found {
			continue
		}
		dec, ok := e.decide(repo, d, typ, rel.Time)
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

// decide evaluates the repo's update entry for one dependency. ok is false when the
// repo file is invalid or no entry covers the dependency.
func (e *Engine) decide(repo model.Repo, d model.Dependency, typ rules.UpdateType, released time.Time) (rules.Decision, bool) {
	cfg, err := RepoConfig(repo.Config)
	if err != nil {
		return rules.Decision{}, false
	}
	u, ok := cfg.UpdateFor(d.Manager, d.File)
	if !ok {
		return rules.Decision{}, false
	}
	return rules.Evaluate(e.Limits, u, d.Package, typ, released, e.now()), true
}
