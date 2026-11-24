package engine

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/npmreg"
	"github.com/taumatix/ripple/internal/rules"
	"golang.org/x/mod/semver"
)

// npmPackagesCursor holds the npm packages some repo uses (newline separated). The
// registry has no feed ripple reads, so NpmWatcher polls only the packages a scanned
// package.json names, never the registry as a whole.
const npmPackagesCursor = "npm:packages"

// NpmRegistry is the read side of the npm registry; npmreg.Client implements it.
type NpmRegistry interface {
	Packument(ctx context.Context, name string) (npmreg.Packument, error)
}

// applicableNpm judges a registry version for an npm dependency: the same package, a
// range ripple can move, and a release newer than the range's floor. A partial range
// that already names the version at its precision ("1.2" for 1.2.9) has nothing to
// change and is not applicable.
func applicableNpm(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceNpm || d.Package != pkg {
		return "", false
	}
	rng, ok := manager.ParseNpmSpec(d.Version)
	if !ok {
		return "", false
	}
	if !rng.Moving().Allows(version) {
		return "", false // over an upper bound of a compound range
	}
	typ, ok := rules.Classify("v"+rng.Floor(), "v"+version)
	if !ok || (rng.Text(version) == d.Version && len(rng.Moving().Parts) < 3) {
		return "", false
	}
	return typ, true
}

// npmTargets lists, newest first, the newest release of each update type that is an
// update of d, so a repo that ignores majors still gets the minor or patch behind one.
func npmTargets(pv npmreg.Packument, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range pv.Releases() {
		typ, ok := applicableNpm(d, pkg, v)
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

// NpmWatcher turns the registry's versions of the packages in use into jobs, like
// DockerWatcher does for images: one read per package per poll through
// Engine.Npm, one job per (repo, package, target version).
type NpmWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses an npm
// package, nor while Engine.Npm is nil.
func (w *NpmWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Npm == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, npmPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceNpm, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceNpm, npmPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		pv, err := e.Npm.Packument(ctx, pkg)
		if err != nil {
			if errors.Is(err, npmreg.ErrDenied) || errors.Is(err, npmreg.ErrNotFound) {
				e.log().Info("npm package not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("npm package not read", "package", pkg, "err", err)
			}
			continue
		}
		for _, d := range deps {
			floor, _ := manager.NpmRangeFloor(d.Version)
			if floor == "" {
				continue
			}
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range npmTargets(pv, d, pkg) {
				if rs == nil {
					if rs, err = e.repoState(ctx, repos, d.Repo); err != nil {
						return n, err
					}
					if !rs.found {
						break
					}
				}
				// Abbreviated metadata has no publication dates: the cooldown runs from the
				// first poll that saw the version, and EnqueueJob keeps the first ReleasedAt.
				if x, ok := e.decide(rs, d, c.typ, at); ok && x.Enabled {
					target, dec = c.version, x
					break
				}
			}
			if target == "" {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceNpm, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceNpm, Package: pkg, Version: target,
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

// lockfiles are the files npm-family tools resolve a package.json into. ripple cannot
// regenerate any of them (it never runs npm or node), so a PR that moves a range
// beside one says so.
var lockfiles = []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"}

// npmGuard is called before an npm dependency is rewritten. A non-empty reason means
// the update must not be proposed.
//
// The registry ripple asks is registry.npmjs.org. A repository whose npm-family config
// (.npmrc, .yarnrc, .yarnrc.yml in the package.json's directory or any parent up to
// the root; the nearest file that sets a key wins) names another registry, for all
// packages or for the package's @scope, resolves that package somewhere else, and a
// public package of the same name is then not the one the repo uses: proposing its
// version would be a dependency-confusion step. A URL is public only when its host is
// exactly the public registry's, and one ripple cannot read (an unexpanded ${VAR}) is
// not public. ripple never fetches from a registry a repository names.
func (r *runner) npmGuard(d model.Dependency) string {
	var rc npmreg.Rc
	dir := path.Dir(d.File)
	for {
		var level npmreg.Rc
		for _, f := range []struct {
			name  string
			parse func([]byte, string) npmreg.Rc
		}{{".npmrc", npmreg.ParseNpmrc}, {".yarnrc.yml", npmreg.ParseYarnrcYml}, {".yarnrc", npmreg.ParseYarnrc}} {
			p := path.Join(dir, f.name)
			if content, err := r.ws.ReadFile(p); err == nil {
				level = level.Join(f.parse(content, p))
			}
		}
		rc = rc.Over(level) // rc is the nearer layers, level the next one out
		if dir == "." || dir == "/" || dir == "" {
			break
		}
		dir = path.Dir(dir)
	}
	if s := rc.For(d.Package); s.URL != "" && !npmreg.IsPublic(s.URL) {
		return fmt.Sprintf("%s sets a registry for %s; ripple reads only registry.npmjs.org and will not guess a version for a package that may live elsewhere", s.File, d.Package)
	}
	return ""
}

// noteLockfiles handles the lockfiles beside a package.json that ripple changed: a
// package-lock.json or npm-shrinkwrap.json that can be moved without npm is rewritten,
// every other one is recorded for the PR body.
func (r *runner) noteLockfiles(d model.Dependency, version string) {
	dir := path.Dir(d.File)
	for _, name := range lockfiles {
		p := path.Join(dir, name)
		if _, err := r.ws.ReadFile(p); err != nil {
			continue
		}
		why := "ripple rewrites only package-lock.json and npm-shrinkwrap.json"
		if name == "package-lock.json" || name == "npm-shrinkwrap.json" {
			var err error
			if why, err = r.refreshNpmLock(p, d, version); err != nil {
				r.e.log().Warn("npm lockfile not rewritten", "file", p, "err", err)
				why = "the file could not be written: " + err.Error()
			}
		}
		if why == "" {
			if r.npmLocksDone == nil {
				r.npmLocksDone = map[string]bool{}
			}
			r.npmLocksDone[p] = true
			continue
		}
		if r.npmLocks == nil {
			r.npmLocks, r.npmLockWhy = map[string]bool{}, map[string]string{}
		}
		r.npmLocks[p] = true
		if r.npmLockWhy[p] == "" {
			r.npmLockWhy[p] = d.Package + ": " + why
		}
	}
}

// refreshNpmLock rewrites the locked entry of d.Package in the lockfile at p to
// version, from the registry's metadata, when that is exactly what npm would do: a
// leaf package (see manager.PlanNpmLock) whose new version has the same dependencies,
// bin, engines and install shape as the locked one, comes from the same registry host
// and is newer. It returns "" when it rewrote the file, else the reason it did not.
func (r *runner) refreshNpmLock(p string, d model.Dependency, version string) (string, error) {
	content, err := r.ws.ReadFile(p)
	if err != nil {
		return "", err
	}
	plan, err := manager.PlanNpmLock(content, d.Package, d.Version)
	if err != nil {
		return strings.TrimPrefix(err.Error(), manager.ErrLockDeclined.Error()+": "), nil
	}
	if r.e.Npm == nil {
		return "no registry client to read the package's metadata from", nil
	}
	pv, err := r.e.Npm.Packument(r.ctx, d.Package)
	if err != nil {
		return "the registry's metadata could not be read: " + err.Error(), nil
	}
	old, okOld := pv.Lookup(plan.Version)
	nw, okNew := pv.Lookup(version)
	switch {
	case !okOld || !okNew:
		return fmt.Sprintf("the registry does not list both %s and %s", plan.Version, version), nil
	case semver.Compare("v"+version, "v"+plan.Version) <= 0:
		return fmt.Sprintf("the lockfile already holds %s", plan.Version), nil
	case old.Integrity != "" && old.Integrity != plan.Integrity:
		return "the locked integrity is not the registry's for " + plan.Version, nil
	case nw.Integrity == "" || nw.Tarball == "":
		return "the registry gave no tarball and integrity for " + version, nil
	case old.Shape != nw.Shape:
		return fmt.Sprintf("%s and %s differ in dependencies or install metadata", plan.Version, version), nil
	case !sameOrigin(plan.Resolved, nw.Tarball):
		return "the locked tarball and the registry's are on different hosts", nil
	}
	newSpec := d.Version
	if spec, ok := manager.ParseNpmSpec(d.Version); ok {
		newSpec = spec.Text(version)
	}
	out, err := plan.Apply(d.Package, newSpec, version, nw.Tarball, nw.Integrity)
	if err != nil {
		return strings.TrimPrefix(err.Error(), manager.ErrLockDeclined.Error()+": "), nil
	}
	return "", r.ws.WriteFile(p, out)
}

func sameOrigin(a, b string) bool {
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	return ea == nil && eb == nil && ua.Scheme != "" && ua.Scheme == ub.Scheme && strings.EqualFold(ua.Host, ub.Host)
}

// npmNotes is the PR body for the lockfiles: those ripple rewrote, and those it could
// not regenerate.
func (r *runner) npmNotes() string {
	list := func(m map[string]bool, why map[string]string) string {
		var files []string
		for f := range m {
			files = append(files, f)
		}
		sort.Strings(files)
		s := ""
		for _, f := range files {
			s += "- `" + f + "`"
			if why != nil && why[f] != "" {
				s += ": " + why[f]
			}
			s += "\n"
		}
		return s
	}
	s := ""
	if len(r.npmLocksDone) > 0 {
		s += "\n## Lockfiles rewritten without npm\n\nripple never runs npm. For these lockfiles it changed only the moved package's own entry (version, tarball URL and integrity from the registry's metadata) and the project's range, because the new version has the same dependencies and install metadata as the locked one. License and funding fields of the entry were kept as they were. Run `npm ci` or your package manager's install to confirm before merging:\n\n" + list(r.npmLocksDone, nil)
	}
	if len(r.npmLocks) > 0 {
		s += "\n## Lockfiles not regenerated\n\nripple edits package.json only: it never runs npm, yarn or pnpm. These lockfiles still hold the old resolution, so run your package manager's install to refresh them before merging (CI with a frozen lockfile will fail until you do):\n\n" + list(r.npmLocks, r.npmLockWhy)
	}
	return s
}
