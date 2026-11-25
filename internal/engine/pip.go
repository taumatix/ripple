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
	"github.com/taumatix/ripple/internal/pypi"
	"github.com/taumatix/ripple/internal/rules"
)

// pipPackagesCursor holds the PyPI projects some repo uses (newline separated). PyPI
// has no feed ripple reads, so PipWatcher polls only the projects a scanned
// requirements file or pyproject.toml names, never the index as a whole.
const pipPackagesCursor = "pip:packages"

// PyPIIndex is the read side of PyPI; pypi.Client implements it.
type PyPIIndex interface {
	Project(ctx context.Context, name string) (pypi.Project, error)
}

// pipClassify classifies a move between two PEP 440 releases by their first three
// release segments, which is how ignore and automerge rules see it (2.31 -> 2.32 is
// "minor", 2.31.0.1 -> 2.31.0.2 is "patch").
func pipClassify(cur, next string) (rules.UpdateType, bool) {
	a, ok1 := pypi.Parse(cur)
	b, ok2 := pypi.Parse(next)
	if !ok1 || !ok2 || !a.IsRelease() || !b.IsRelease() || pypi.Compare(b, a) <= 0 {
		return "", false
	}
	seg := func(v pypi.Version, i int) int {
		if i < len(v.Release) {
			return v.Release[i]
		}
		return 0
	}
	switch {
	case a.Epoch != b.Epoch || seg(a, 0) != seg(b, 0):
		return rules.Major, true
	case seg(a, 1) != seg(b, 1):
		return rules.Minor, true
	}
	return rules.Patch, true
}

// applicablePip judges an index version for a pip dependency: the same project, a
// specifier ripple can move, and a release newer than the specifier's version.
func applicablePip(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourcePip || d.Package != pkg {
		return "", false
	}
	floor, _ := manager.PipSpecFloor(d.Version)
	if floor == "" {
		return "", false
	}
	if allow := manager.PipSpecAllows(d.Version); allow != nil {
		if v, ok := pypi.Parse(version); !ok || !allow(v) {
			return "", false
		}
	}
	return pipClassify(floor, version)
}

// PipWatcher turns PyPI's versions of the projects in use into jobs, like CargoWatcher
// does for crates: one read per project per poll through Engine.PyPI, one job per
// (repo, project, target version).
type PipWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a
// project, nor while Engine.PyPI is nil.
func (w *PipWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.PyPI == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, pipPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourcePip, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourcePip, pipPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		pr, err := e.PyPI.Project(ctx, pkg)
		if err != nil {
			if errors.Is(err, pypi.ErrDenied) || errors.Is(err, pypi.ErrNotFound) {
				e.log().Info("project not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("project not read", "package", pkg, "err", err)
			}
			continue
		}
		for _, d := range deps {
			floor, _ := manager.PipSpecFloor(d.Version)
			if floor == "" {
				continue
			}
			target := pr.NewestWhere(floor, manager.PipSpecAllows(d.Version))
			if target == "" {
				continue
			}
			typ, ok := applicable(d, model.DatasourcePip, pkg, target)
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
			// The JSON API's upload times are not read: the cooldown runs from the first
			// poll that saw the version, and EnqueueJob keeps the first ReleasedAt.
			at := e.now()
			dec, ok := e.decide(rs, d, typ, at)
			if !ok || !dec.Enabled {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourcePip, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourcePip, Package: pkg, Version: target,
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

// pipIndexFiles are the files, in the manifest's directory and each parent, that can
// point resolution at an index other than PyPI.
var pipIndexFiles = []string{"pip.conf", "pip.ini", "uv.toml", "requirements.txt", "pyproject.toml", "Pipfile"}

// pipGuard is called before a pip dependency is rewritten. A non-empty reason means
// the update must not be proposed.
//
// The index ripple asks is pypi.org's. A file that sets `--index-url` or
// `--extra-index-url` (or a pip.conf, uv.toml or pyproject source) resolves projects
// from somewhere else, and a PyPI project of the same name could be a different
// package: the dependency-confusion step. Environment variables and user-level
// configuration are not visible to ripple.
func (r *runner) pipGuard(d model.Dependency, target string) string {
	if why := r.pipPythonGuard(d, target); why != "" {
		return why
	}
	check := func(p string) string {
		content, err := r.ws.ReadFile(p)
		if err == nil && manager.PipIndexOverride(p, content) {
			return fmt.Sprintf("%s names a package index other than PyPI; ripple reads only PyPI and will not guess a version for a project that may live elsewhere", p)
		}
		return ""
	}
	if why := check(d.File); why != "" {
		return why
	}
	for dir := path.Dir(d.File); ; dir = path.Dir(dir) {
		base := dir
		if base == "." {
			base = ""
		}
		for _, name := range pipIndexFiles {
			if why := check(path.Join(base, name)); why != "" {
				return why
			}
		}
		if dir == "." || dir == "/" {
			return ""
		}
	}
}

// pipLockNames are lock files a resolver writes beside or above a manifest.
var pipLockNames = []string{"poetry.lock", "uv.lock", "pdm.lock", "Pipfile.lock", "pylock.toml"}

// notePipLocks records the lock files that resolve an edited file: in its directory
// and any parent (a workspace keeps one at its root).
func (r *runner) notePipLocks(d model.Dependency) {
	for dir := path.Dir(d.File); ; dir = path.Dir(dir) {
		base := dir
		if base == "." {
			base = ""
		}
		for _, name := range pipLockNames {
			p := path.Join(base, name)
			if _, err := r.ws.ReadFile(p); err == nil {
				if r.pipLocks == nil {
					r.pipLocks = map[string]bool{}
				}
				r.pipLocks[p] = true
			}
		}
		// A requirements.in beside a pip-compile output: the output is the lock.
		if f := path.Base(d.File); dir == path.Dir(d.File) && strings.HasSuffix(f, ".in") {
			p := path.Join(base, strings.TrimSuffix(f, ".in")+".txt")
			if content, err := r.ws.ReadFile(p); err == nil && manager.PipGenerated(content) {
				if r.pipLocks == nil {
					r.pipLocks = map[string]bool{}
				}
				r.pipLocks[p] = true
			}
		}
		if dir == "." || dir == "/" {
			return
		}
	}
}

// pipNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) pipNotes() string {
	if len(r.pipLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.pipLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## Lock files not regenerated\n\nripple edits requirements and pyproject.toml only: it never runs pip, poetry, uv or pdm. These lock files still hold the old resolution, so re-lock before merging (a CI that installs from the lock or checks it is current will fail until you do):\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}

// pipPythonGuard says why target, a release of d's project, cannot run on the Python
// the repo declares: the release's Requires-Python (PyPI reports it per file) against
// the lowest Python the nearest pyproject.toml, Pipfile or .python-version names. A repo
// that declares none, a specifier ripple cannot read or an index that cannot be read
// means no objection. The job is skipped, not retargeted to an older release (as for
// a Cargo rust-version).
func (r *runner) pipPythonGuard(d model.Dependency, target string) string {
	if r.e.PyPI == nil {
		return ""
	}
	have, from := r.pipPython(d.File)
	if have == "" {
		return ""
	}
	pr, err := r.e.PyPI.Project(r.ctx, d.Package)
	if err != nil {
		return ""
	}
	rel, ok := pr.Release(target)
	if !ok || rel.RunsOn(have) {
		return ""
	}
	return fmt.Sprintf("%s %s declares Requires-Python %s, which excludes Python %s declared in %s; ripple will not propose a release the repo's Python cannot install", d.Package, target, strings.Join(rel.RequiresPython, " or "), have, from)
}

// pipPython is the lowest Python declared for the file p, and the file that declares it.
func (r *runner) pipPython(p string) (string, string) {
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		base := dir
		if base == "." {
			base = ""
		}
		for _, name := range manager.PipPythonFiles {
			pp := path.Join(base, name)
			if content, err := r.ws.ReadFile(pp); err == nil {
				if f := manager.PipPythonFloor(pp, content); f != "" {
					return f, pp
				}
			}
		}
		if dir == "." || dir == "/" {
			return "", ""
		}
	}
}
