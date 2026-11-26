package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/maven"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

// mavenPackagesCursor holds the packages some repo uses (newline separated). Maven Central
// has no feed ripple reads, so MavenWatcher polls only the packages a scanned pom.xml names.
const mavenPackagesCursor = "maven:packages"

// MavenRegistry is the read side of Maven Central; maven.Client implements it.
type MavenRegistry interface {
	Versions(ctx context.Context, pkg string) ([]string, error)
}

// applicableMaven judges a registry version for a pom.xml dependency: the same package, a
// version text ripple can move, and a release newer than it.
func applicableMaven(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceMaven || d.Package != pkg {
		return "", false
	}
	if !manager.ParseMavenSpec(d.Version) || !manager.ParseMavenSpec(version) {
		return "", false
	}
	// A three-segment declaration keeps moving to three-segment releases only, as before two-segment
	// ones were read at all; a two-segment one (4.13) may move to either (4.13.2, 4.14).
	if strings.Count(d.Version, ".") == 2 && strings.Count(version, ".") != 2 {
		return "", false
	}
	return rules.Classify("v"+d.Version, "v"+version)
}

// mavenTargets lists, newest first, the newest release of each update type that is an
// update of d.
func mavenTargets(releases []string, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range releases {
		typ, ok := applicableMaven(d, pkg, v)
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

// MavenWatcher turns the versions on Maven Central of the packages in use into jobs, like
// NpmWatcher does for packages: one read per package per poll through Engine.Maven, one job
// per (repo, package, target version).
type MavenWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a package, nor
// while Engine.Maven is nil.
func (w *MavenWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Maven == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, mavenPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceMaven, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceMaven, mavenPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		vs, err := e.Maven.Versions(ctx, pkg)
		if err != nil {
			if errors.Is(err, maven.ErrDenied) || errors.Is(err, maven.ErrNotFound) {
				e.log().Info("package not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("package not read", "package", pkg, "err", err)
			}
			continue
		}
		releases := maven.Releases(vs)
		for _, d := range deps {
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range mavenTargets(releases, d, pkg) {
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
				Key: model.JobKey(d.Repo, model.DatasourceMaven, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceMaven, Package: pkg, Version: target,
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
