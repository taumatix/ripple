package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/nuget"
	"github.com/taumatix/ripple/internal/rules"
)

// nugetPackagesCursor holds the packages some repo uses (newline separated). nuget.org
// has no feed ripple reads, so NugetWatcher polls only the packages a scanned project file names.
const nugetPackagesCursor = "nuget:packages"

// NugetRegistry is the read side of nuget.org; nuget.Client implements it.
type NugetRegistry interface {
	Versions(ctx context.Context, pkg string) ([]string, error)
}

// applicableNuget judges a registry version for a project-file package reference: the same package, a
// version text ripple can move, and a release newer than it.
func applicableNuget(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceNuget || d.Package != pkg {
		return "", false
	}
	if !manager.ParseNugetSpec(d.Version) || !manager.ParseNugetSpec(version) {
		return "", false
	}
	return rules.Classify("v"+d.Version, "v"+version)
}

// nugetTargets lists, newest first, the newest release of each update type that is an
// update of d.
func nugetTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicableNuget(d, pkg, v)
		if ok && (newest[typ] == "" || semver.Compare("v"+v, "v"+newest[typ]) > 0) {
			newest[typ] = v
		}
	}
	var out []cargoTarget
	for typ, v := range newest {
		out = append(out, cargoTarget{v, typ})
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare("v"+out[i].version, "v"+out[j].version) > 0 })
	return out
}

// NugetWatcher turns the versions on nuget.org of the packages in use into jobs, like
// NpmWatcher does for packages: one read per package per poll through Engine.Nuget, one job
// per (repo, package, target version).
type NugetWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a package, nor
// while Engine.Nuget is nil.
func (w *NugetWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Nuget == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, nugetPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceNuget, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceNuget, nugetPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		vs, err := e.Nuget.Versions(ctx, pkg)
		if err != nil {
			if errors.Is(err, nuget.ErrDenied) || errors.Is(err, nuget.ErrNotFound) {
				e.log().Info("package not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("package not read", "package", pkg, "err", err)
			}
			continue
		}
		releases := nuget.Releases(vs)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range nugetTargets(releases, d, pkg) {
				if rs == nil {
					if rs, err = e.repoState(ctx, repos, d.Repo); err != nil {
						return n, err
					}
					if !rs.found {
						break
					}
				}
				// The cooldown runs from the first poll that saw the version, and EnqueueJob
				// keeps the first ReleasedAt (created_at is not read).
				if x, ok := e.decide(rs, d, c.typ, at); ok && x.Enabled {
					target, dec = c.version, x
					break
				}
			}
			if target == "" {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceNuget, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceNuget, Package: pkg, Version: target,
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

// noteNugetLock records the packages.lock.json beside an edited project file (the lock a project
// opts into with RestorePackagesWithLockFile).
func (r *runner) noteNugetLock(d model.Dependency) {
	p := path.Join(path.Dir(d.File), "packages.lock.json")
	if _, err := r.ws.ReadFile(p); err == nil {
		if r.nugetLocks == nil {
			r.nugetLocks = map[string]bool{}
		}
		r.nugetLocks[p] = true
	}
}

// nugetNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) nugetNotes() string {
	if len(r.nugetLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.nugetLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## packages.lock.json not regenerated\n\nripple edits the project file's version only: it never runs the .NET SDK. This lock file still holds the old resolution, so run `dotnet restore --force-evaluate` and push to the branch before merging, since a locked-mode restore fails while the lock and the project disagree:\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
