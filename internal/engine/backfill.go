package engine

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

// DefaultMaxBackfill bounds how many jobs one scan may create for dependencies the repo
// did not have before (the server setting RIPPLE_MAX_BACKFILL changes it). The remainder
// is not dropped: it is carried to the repo's next scan (backfillCursor).
const DefaultMaxBackfill = 10

// backfillLookupsPerJob bounds the store reads of one scan to this many per allowed job,
// so a repo that registers thousands of dependencies at once costs a bounded number of reads.
const backfillLookupsPerJob = 10

// backfillMajorProbe is how many majors above the one a dependency is on are looked for on
// its line: a module that skipped further than this is found by the feed when it releases.
const backfillMajorProbe = 3

// backfillProxyLookups bounds the module proxy requests of one scan.
const backfillProxyLookups = 20

// backfillCursor names the per-repo cursor holding the dependencies a scan could not serve
// within its bounds, one "datasource|package" per line.
const backfillCursor = "backfill|"

// ReleaseLookup finds the latest release of one module path the feed never recorded.
type ReleaseLookup interface {
	// Latest reports ok=false, with no error, for a path the source does not know.
	Latest(ctx context.Context, pkg string) (model.Release, bool, error)
}

// backfill gives each dependency that is new to the repo (absent from prev), and each one
// an earlier scan left unserved, the job a release of it would have got had the repo
// imported it when the release came out. The newest release for the line that is an update
// for the dependency is the target: the stored releases of its own path and of the next
// majors on the same line, and, for a module with no stored release at all, what the
// optional Lookup finds (bounded per scan). The cooldown, schedule and ignore rules are
// those of a fed release (decide), and the job key makes a rescan a no-op. What the bounds
// leave unserved is written to the repo's backfill cursor for the next scan.
func (e *Engine) backfill(ctx context.Context, repo string, cfg *config.Repo, prev, deps []model.Dependency) error {
	if !cfg.Backfills() {
		return nil
	}
	limit := e.MaxBackfill
	if limit <= 0 {
		limit = DefaultMaxBackfill
	}
	key := func(d model.Dependency) string { return d.Datasource + "|" + d.Package }
	skip := func(d model.Dependency) bool {
		return d.Datasource == model.DatasourceGoToolchain || d.Datasource == model.DatasourceGitHubTags || d.Datasource == model.DatasourceDocker || d.Datasource == model.DatasourceNpm
	}
	known := map[string]bool{}
	for _, d := range prev {
		known[key(d)] = true
	}
	raw, err := e.Store.GetCursor(ctx, backfillCursor+repo)
	if err != nil {
		return err
	}
	carried := map[string]bool{}
	for _, k := range strings.Split(raw, "\n") {
		if k != "" {
			carried[k] = true
		}
	}
	var todo, fresh []model.Dependency
	seen := map[string]bool{}
	for _, d := range deps {
		k := key(d)
		if skip(d) || seen[k] {
			continue
		}
		seen[k] = true
		switch {
		case carried[k]:
			todo = append(todo, d)
		case !known[k]:
			fresh = append(fresh, d)
		}
	}
	byPkg := func(ds []model.Dependency) {
		sort.Slice(ds, func(i, j int) bool { return ds[i].Package < ds[j].Package })
	}
	byPkg(todo)
	byPkg(fresh)
	todo = append(todo, fresh...)

	rs := &repoState{found: true, cfg: cfg}
	jobs, lookups, proxy := 0, 0, 0
	var rest []model.Dependency
	for i, d := range todo {
		if jobs >= limit || lookups >= limit*backfillLookupsPerJob || proxy >= backfillProxyLookups {
			rest = todo[i:]
			break
		}
		best, typ, err := e.backfillTarget(ctx, d, &lookups, &proxy)
		if err != nil {
			return err
		}
		if best == nil {
			continue
		}
		dec, ok := e.decide(rs, d, typ, best.Time)
		if !ok || !dec.Enabled {
			continue
		}
		jobs++
		if _, err := e.Store.EnqueueJob(ctx, model.Job{
			Key: model.JobKey(repo, d.Datasource, best.Package, best.Version), Repo: repo,
			Datasource: d.Datasource, Package: best.Package, Version: best.Version,
			ReleasedAt: best.Time, State: model.JobPending, NotBefore: dec.NotBefore,
		}); err != nil {
			return err
		}
	}
	var lines []string
	for _, d := range rest {
		lines = append(lines, key(d))
	}
	if next := strings.Join(lines, "\n"); next != raw {
		if err := e.Store.SetCursor(ctx, backfillCursor+repo, next); err != nil {
			return err
		}
	}
	if len(todo) > 0 {
		e.log().Info("backfilled new dependencies", "repo", repo, "new", len(fresh), "considered", len(todo), "jobs", jobs, "carried", len(rest))
	}
	return nil
}

// backfillTarget picks the release d should be brought to: of the stored releases of its
// own path and of the next majors on its line, the newest that is an update, falling back
// to a Lookup only for a dependency whose own path has no stored release at all.
func (e *Engine) backfillTarget(ctx context.Context, d model.Dependency, lookups, proxy *int) (*model.Release, rules.UpdateType, error) {
	paths := []string{d.Package}
	if d.Datasource == model.DatasourceGo && d.Manager == "gomod" {
		paths = append(paths, majorPaths(d.Package, backfillMajorProbe)...)
	}
	var cands []model.Release
	empty := map[string]bool{}
	for _, p := range paths {
		*lookups++
		rels, err := e.Store.ListReleases(ctx, d.Datasource, p)
		if err != nil {
			return nil, "", err
		}
		empty[p] = len(rels) == 0
		cands = append(cands, rels...)
	}
	if empty[d.Package] && e.Lookup != nil && d.Datasource == model.DatasourceGo {
		for _, p := range paths {
			if !empty[p] {
				continue
			}
			*proxy++
			r, ok, err := e.Lookup.Latest(ctx, p)
			if err != nil {
				e.log().Warn("backfill lookup failed", "package", p, "err", err)
				continue
			}
			if ok {
				r.Package = p
				cands = append(cands, r)
			}
		}
	}
	var best *model.Release
	var bestTyp rules.UpdateType
	for i := range cands {
		typ, ok := applicable(d, d.Datasource, cands[i].Package, cands[i].Version)
		if !ok {
			continue
		}
		// The newest major wins, then the newest version within it.
		if best == nil || semver.Compare(cands[i].Version, best.Version) > 0 {
			best, bestTyp = &cands[i], typ
		}
	}
	return best, bestTyp, nil
}

// majorPaths are the module paths of the next n majors above pkg's own, on its line.
func majorPaths(pkg string, n int) []string {
	prefix, _, ok := module.SplitPathVersion(pkg)
	if !ok {
		return nil
	}
	cur := model.PathMajor(model.DatasourceGo, pkg)
	var out []string
	for k := cur + 1; k <= cur+n; k++ {
		p := prefix + "/v" + strconv.Itoa(k)
		if strings.HasPrefix(prefix, "gopkg.in/") {
			p = prefix + ".v" + strconv.Itoa(k)
		}
		if module.CheckPath(p) == nil {
			out = append(out, p)
		}
	}
	return out
}
