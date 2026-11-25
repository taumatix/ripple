package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/packagist"
	"github.com/taumatix/ripple/internal/rules"
)

// packagistPackagesCursor holds the packages some repo uses (newline separated). packagist.org
// has no feed ripple reads, so PackagistWatcher polls only the packages a scanned composer.json names.
const packagistPackagesCursor = "packagist:packages"

// PackagistRegistry is the read side of packagist.org; packagist.Client implements it.
type PackagistRegistry interface {
	Versions(ctx context.Context, pkg string) ([]string, error)
}

// applicablePackagist judges a registry version for a composer.json dependency: the same package, a
// version text ripple can move, and a release newer than it.
func applicablePackagist(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourcePackagist || d.Package != pkg {
		return "", false
	}
	spec, ok := manager.ParseComposerSpec(d.Version)
	if !ok {
		return "", false
	}
	to, ok := manager.ParseComposerSpec(strings.TrimPrefix(version, "v"))
	if !ok {
		return "", false
	}
	return rules.Classify("v"+spec.Version, "v"+to.Version)
}

// packagistTargets lists, newest first, the newest release of each update type that is an
// update of d.
func packagistTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicablePackagist(d, pkg, v)
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

// PackagistWatcher turns the versions on packagist.org of the packages in use into jobs, like
// NpmWatcher does for packages: one read per package per poll through Engine.Packagist, one job
// per (repo, package, target version).
type PackagistWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a package, nor
// while Engine.Packagist is nil.
func (w *PackagistWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Packagist == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, packagistPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourcePackagist, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourcePackagist, packagistPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		vs, err := e.Packagist.Versions(ctx, pkg)
		if err != nil {
			if errors.Is(err, packagist.ErrDenied) || errors.Is(err, packagist.ErrNotFound) {
				e.log().Info("package not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("package not read", "package", pkg, "err", err)
			}
			continue
		}
		releases := packagist.Releases(vs)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range packagistTargets(releases, d, pkg) {
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
				Key: model.JobKey(d.Repo, model.DatasourcePackagist, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourcePackagist, Package: pkg, Version: target,
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

// notePackagistLock records the composer.lock beside an edited composer.json.
func (r *runner) notePackagistLock(d model.Dependency) {
	p := path.Join(path.Dir(d.File), "composer.lock")
	if _, err := r.ws.ReadFile(p); err == nil {
		if r.packagistLocks == nil {
			r.packagistLocks = map[string]bool{}
		}
		r.packagistLocks[p] = true
	}
}

// packagistNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) packagistNotes() string {
	if len(r.packagistLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.packagistLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## composer.lock not regenerated\n\nripple edits the composer.json's version only: it never runs Composer. This lock file still holds the old resolution, so run `composer update <package>` (or `composer update --lock` for a bare metadata change) and push to the branch before merging, since `composer install` warns while the lock and composer.json disagree:\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
