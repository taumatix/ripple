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
	"github.com/taumatix/ripple/internal/rubygems"
	"github.com/taumatix/ripple/internal/rules"
)

// rubygemsPackagesCursor holds the gems some repo uses (newline separated). rubygems.org
// has no feed ripple reads, so RubygemsWatcher polls only the gems a scanned Gemfile names.
const rubygemsPackagesCursor = "rubygems:packages"

// RubygemsRegistry is the read side of rubygems.org; rubygems.Client implements it.
type RubygemsRegistry interface {
	Versions(ctx context.Context, gem string) ([]rubygems.Version, error)
}

// applicableRubygems judges a registry version for a Gemfile dependency: the same gem, a
// version text ripple can move, and a release newer than it.
func applicableRubygems(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceRubygems || d.Package != pkg {
		return "", false
	}
	spec, ok := manager.ParseBundlerSpec(d.Version)
	if !ok {
		return "", false
	}
	to, ok := manager.ParseBundlerSpec(strings.TrimPrefix(version, "v"))
	if !ok {
		return "", false
	}
	typ, ok := rules.Classify("v"+spec.Floor(), "v"+to.Version)
	if ok && spec.Partial() && typ != rules.Major {
		return "", false // "~> 1.2" and "~> 1" already allow every later 1.x: nothing to edit
	}
	return typ, ok
}

// rubygemsTargets lists, newest first, the newest release of each update type that is an
// update of d.
func rubygemsTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicableRubygems(d, pkg, v)
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

// RubygemsWatcher turns the versions on rubygems.org of the gems in use into jobs, like
// NpmWatcher does for packages: one read per gem per poll through Engine.Rubygems, one job
// per (repo, gem, target version).
type RubygemsWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a gem, nor
// while Engine.Rubygems is nil.
func (w *RubygemsWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Rubygems == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, rubygemsPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceRubygems, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceRubygems, rubygemsPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		vs, err := e.Rubygems.Versions(ctx, pkg)
		if err != nil {
			if errors.Is(err, rubygems.ErrDenied) || errors.Is(err, rubygems.ErrNotFound) {
				e.log().Info("gem not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("gem not read", "package", pkg, "err", err)
			}
			continue
		}
		releases := rubygems.Releases(vs)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range rubygemsTargets(releases, d, pkg) {
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
				Key: model.JobKey(d.Repo, model.DatasourceRubygems, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceRubygems, Package: pkg, Version: target,
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

// noteGemLock records the Gemfile.lock beside an edited Gemfile.
func (r *runner) noteGemLock(d model.Dependency) {
	p := path.Join(path.Dir(d.File), "Gemfile.lock")
	if _, err := r.ws.ReadFile(p); err == nil {
		if r.gemLocks == nil {
			r.gemLocks = map[string]bool{}
		}
		r.gemLocks[p] = true
	}
}

// gemNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) gemNotes() string {
	if len(r.gemLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.gemLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## Gemfile.lock not regenerated\n\nripple edits the Gemfile's version only: it never runs bundler. These lock files still hold the old resolution, so run `bundle install` to refresh them before merging (a frozen bundle fails until you do):\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
