package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/npmreg"
	"github.com/taumatix/ripple/internal/rules"
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
// range ripple can move, and a release newer than the range's floor.
func applicableNpm(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceNpm || d.Package != pkg {
		return "", false
	}
	floor, _ := manager.NpmRangeFloor(d.Version)
	if floor == "" {
		return "", false
	}
	return rules.Classify("v"+floor, "v"+version)
}

// NpmWatcher turns the registry's versions of the packages in use into jobs, like
// DockerWatcher does for images: one read per package per poll through
// Engine.Npm, one job per (repo, package, target version).
type NpmWatcher struct {
	Engine *Engine
}

// Poll returns how many jobs were new. It makes no request while no repo uses an npm
// package, nor while Engine.Npm is nil.
func (w *NpmWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Npm == nil {
		return 0, nil
	}
	raw, err := e.Store.GetCursor(ctx, npmPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range strings.Fields(raw) {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceNpm, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
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
			target := pv.Newest(floor)
			if target == "" {
				continue
			}
			typ, ok := applicable(d, model.DatasourceNpm, pkg, target)
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
			// Abbreviated metadata has no publication dates: the cooldown runs from the
			// first poll that saw the version, and EnqueueJob keeps the first ReleasedAt.
			at := e.now()
			dec, ok := e.decide(rs, d, typ, at)
			if !ok || !dec.Enabled {
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
// The registry ripple asks is registry.npmjs.org. A repository whose .npmrc names
// another registry (for all packages, or for the package's @scope) resolves that
// package somewhere else, and a public package of the same name is then not the one
// the repo uses: proposing its version would be a dependency-confusion step.
func (r *runner) npmGuard(d model.Dependency) string {
	scope := ""
	if strings.HasPrefix(d.Package, "@") {
		scope, _, _ = strings.Cut(d.Package, "/")
	}
	dirs := []string{path.Dir(d.File)}
	if path.Dir(d.File) != "." {
		dirs = append(dirs, "")
	}
	for _, dir := range dirs {
		content, err := r.ws.ReadFile(path.Join(dir, ".npmrc"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
			key, val = strings.TrimSpace(key), strings.TrimSpace(val)
			if !ok || val == "" || strings.Contains(val, "registry.npmjs.org") {
				continue
			}
			if key == "registry" || (scope != "" && key == scope+":registry") {
				return fmt.Sprintf("%s sets a registry for %s; ripple reads only registry.npmjs.org and will not guess a version for a package that may live elsewhere", path.Join(dir, ".npmrc"), d.Package)
			}
		}
	}
	return ""
}

// noteLockfiles records the lockfiles beside a package.json that ripple changed.
func (r *runner) noteLockfiles(d model.Dependency) {
	dir := path.Dir(d.File)
	for _, name := range lockfiles {
		p := path.Join(dir, name)
		if _, err := r.ws.ReadFile(p); err == nil {
			if r.npmLocks == nil {
				r.npmLocks = map[string]bool{}
			}
			r.npmLocks[p] = true
		}
	}
}

// npmNotes is the PR body section for lockfiles ripple could not regenerate.
func (r *runner) npmNotes() string {
	if len(r.npmLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.npmLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## Lockfiles not regenerated\n\nripple edits package.json only: it never runs npm, yarn or pnpm. These lockfiles still hold the old resolution, so run your package manager's install to refresh them before merging (CI with a frozen lockfile will fail until you do):\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
