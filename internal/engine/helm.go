package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/helmrepo"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

// helmPackagesCursor holds the charts some repo depends on (newline separated, each
// "<repository URL>/<chart>"). A chart repository has no feed ripple reads, so HelmWatcher
// polls only the repositories a scanned Chart.yaml names, never a registry as a whole.
const helmPackagesCursor = "helm:packages"

// HelmRepos is the read side of chart repositories; helmrepo.Client implements it.
type HelmRepos interface {
	Index(ctx context.Context, repo string) (helmrepo.Index, error)
}

// applicableHelm judges an index version for a Helm dependency: the same chart in the
// same repository, a version text ripple can move, and a release newer than it.
func applicableHelm(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceHelm || d.Package != pkg {
		return "", false
	}
	spec, ok := manager.ParseHelmSpec(d.Version)
	if !ok {
		return "", false
	}
	to, ok := manager.ParseHelmSpec(strings.TrimPrefix(version, "v"))
	if !ok {
		return "", false
	}
	return rules.Classify("v"+spec.Version, "v"+to.Version)
}

// helmTargets lists, newest first, the newest release of each update type that is an
// update of d, so a repo that ignores majors still gets the minor or patch behind one.
func helmTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicableHelm(d, pkg, v)
		if ok && (newest[typ] == "" || semver.Compare("v"+strings.TrimPrefix(v, "v"), "v"+strings.TrimPrefix(newest[typ], "v")) > 0) {
			newest[typ] = v
		}
	}
	var out []cargoTarget
	for typ, v := range newest {
		out = append(out, cargoTarget{v, typ})
	}
	sort.Slice(out, func(i, j int) bool {
		return semver.Compare("v"+strings.TrimPrefix(out[i].version, "v"), "v"+strings.TrimPrefix(out[j].version, "v")) > 0
	})
	return out
}

// HelmWatcher turns the versions in chart repositories of the charts in use into jobs,
// like NpmWatcher does for packages: one index read per repository per poll through
// Engine.Helm, one job per (repo, chart, target version).
type HelmWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo depends on a
// chart, nor while Engine.Helm is nil.
func (w *HelmWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Helm == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, helmPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	type read struct {
		ix  helmrepo.Index
		err error
	}
	indexes := map[string]read{} // one read per repository per poll, whatever it serves
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceHelm, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceHelm, helmPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		repo, chart, ok := manager.HelmSplit(pkg)
		if !ok {
			continue
		}
		rd, have := indexes[repo]
		if !have {
			rd.ix, rd.err = e.Helm.Index(ctx, repo)
			indexes[repo] = rd
		}
		if rd.err != nil {
			if have {
				continue // already reported for this poll
			}
			if errors.Is(rd.err, helmrepo.ErrDenied) || errors.Is(rd.err, helmrepo.ErrNotFound) {
				e.log().Info("chart repository not readable anonymously, left alone", "repository", repo, "err", rd.err)
			} else {
				e.log().Warn("chart repository not read", "repository", repo, "err", rd.err)
			}
			continue
		}
		releases := rd.ix.Releases(chart)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range helmTargets(releases, d, pkg) {
				if rs == nil {
					if rs, err = e.repoState(ctx, repos, d.Repo); err != nil {
						return n, err
					}
					if !rs.found {
						break
					}
				}
				// index.yaml's `created` is when the chart was packaged, not published, and
				// ripple does not read it: the cooldown runs from the first poll that saw
				// the version, and EnqueueJob keeps the first ReleasedAt.
				if x, ok := e.decide(rs, d, c.typ, at); ok && x.Enabled {
					target, dec = c.version, x
					break
				}
			}
			if target == "" {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceHelm, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceHelm, Package: pkg, Version: target,
				ReleasedAt: at, State: model.JobPending, NotBefore: dec.NotBefore,
			})
			if err != nil {
				return n, err
			}
			if inserted {
				n++
			}
		}
	}
	return n, nil
}

// noteHelmLock records the lock file that resolves an edited Chart.yaml or
// requirements.yaml: Chart.lock or requirements.lock beside it.
func (r *runner) noteHelmLock(d model.Dependency) {
	lock := "Chart.lock"
	if path.Base(d.File) == "requirements.yaml" {
		lock = "requirements.lock"
	}
	p := path.Join(path.Dir(d.File), lock)
	if _, err := r.ws.ReadFile(p); err == nil {
		if r.helmLocks == nil {
			r.helmLocks = map[string]bool{}
		}
		r.helmLocks[p] = true
	}
}

// helmNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) helmNotes() string {
	if len(r.helmLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.helmLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## Chart.lock not regenerated\n\nripple edits the dependency's version only: it never runs helm. These lock files still hold the old resolution (and `charts/` the old archives), so run `helm dependency update` to refresh them before merging:\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
