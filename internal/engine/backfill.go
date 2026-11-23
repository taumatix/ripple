package engine

import (
	"context"
	"sort"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

// DefaultMaxBackfill bounds how many jobs one scan may create for dependencies the repo
// did not have before. The remainder is not queued later: the next release of each
// such module reaches the repo through the normal fan-out.
const DefaultMaxBackfill = 10

// backfillLookupsPerJob bounds the store reads of one scan to this many per allowed job,
// so a repo that registers thousands of dependencies at once costs a bounded number of reads.
const backfillLookupsPerJob = 10

// backfill gives each dependency that is new to the repo (absent from prev) the job a
// release of it would have got had the repo imported it when the release came out. The
// newest release already stored for the exact package that is an update for the new
// dependency is the target; the cooldown, schedule and ignore rules are those of a
// fed release (decide), and the job key makes a rescan a no-op. It reads only the
// store: a release the feed never recorded is not found here.
func (e *Engine) backfill(ctx context.Context, repo string, cfg *config.Repo, prev, deps []model.Dependency) error {
	if !cfg.Backfills() {
		return nil
	}
	limit := e.MaxBackfill
	if limit <= 0 {
		limit = DefaultMaxBackfill
	}
	known := map[string]bool{}
	for _, d := range prev {
		known[d.Datasource+"|"+d.Package] = true
	}
	var fresh []model.Dependency
	for _, d := range deps {
		k := d.Datasource + "|" + d.Package
		if known[k] || d.Datasource == model.DatasourceGoToolchain {
			continue
		}
		known[k] = true
		fresh = append(fresh, d)
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Package < fresh[j].Package })
	rs := &repoState{found: true, cfg: cfg}
	jobs, lookups := 0, 0
	for _, d := range fresh {
		if jobs >= limit || lookups >= limit*backfillLookupsPerJob {
			break
		}
		lookups++
		rels, err := e.Store.ListReleases(ctx, d.Datasource, d.Package)
		if err != nil {
			return err
		}
		var best *model.Release
		for i := range rels {
			if _, ok := applicable(d, d.Datasource, d.Package, rels[i].Version); !ok {
				continue
			}
			if best == nil || semver.Compare(rels[i].Version, best.Version) > 0 {
				best = &rels[i]
			}
		}
		if best == nil {
			continue
		}
		typ, _ := applicable(d, d.Datasource, d.Package, best.Version)
		dec, ok := e.decide(rs, d, typ, best.Time)
		if !ok || !dec.Enabled {
			continue
		}
		jobs++
		if _, err := e.Store.EnqueueJob(ctx, model.Job{
			Key: model.JobKey(repo, d.Datasource, d.Package, best.Version), Repo: repo,
			Datasource: d.Datasource, Package: d.Package, Version: best.Version,
			ReleasedAt: best.Time, State: model.JobPending, NotBefore: dec.NotBefore,
		}); err != nil {
			return err
		}
	}
	if len(fresh) > 0 {
		e.log().Info("backfilled new dependencies", "repo", repo, "new", len(fresh), "jobs", jobs)
	}
	return nil
}
