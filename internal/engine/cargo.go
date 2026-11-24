package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/cratesindex"
	"github.com/taumatix/ripple/internal/manager"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
	"golang.org/x/mod/semver"
)

// cargoPackagesCursor holds the crates some repo uses (newline separated). The index
// has no feed ripple reads, so CargoWatcher polls only the crates a scanned Cargo.toml
// names, never the registry as a whole.
const cargoPackagesCursor = "cargo:packages"

// CratesIndex is the read side of the Cargo sparse index; cratesindex.Client
// implements it.
type CratesIndex interface {
	Crate(ctx context.Context, name string) (cratesindex.Crate, error)
}

// applicableCargo judges an index version for a Cargo dependency: the same crate, a
// requirement ripple can move, and a release it can move to (CargoReq.Applicable: newer
// than the floor, inside any upper bound, and outside a wildcard). The
// type follows cargo's compatibility rule, not the number position: ^0.2.3 to 0.3.0 and
// ^0.0.3 to 0.0.4 are "major" because cargo treats them as breaking. A partial
// requirement that already names the version at its precision ("1.2" for 1.2.9) has
// nothing to change and is not applicable.
func applicableCargo(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceCargo || d.Package != pkg {
		return "", false
	}
	req, ok := manager.ParseCargoReq(d.Version)
	if !ok {
		return "", false
	}
	if !req.Applicable(version) {
		return "", false
	}
	floor := req.Floor()
	return cargoClassify(req, floor, version), true
}

// cargoClassify is the update type of floor -> version under req's compatibility rule.
func cargoClassify(req manager.CargoReq, floor, version string) rules.UpdateType {
	from, to := strings.Split(floor, "."), strings.Split(version, ".")
	for i := range from {
		if from[i] == to[i] {
			continue
		}
		switch {
		case i <= req.Breaking():
			return rules.Major
		case i == 1:
			return rules.Minor
		}
		return rules.Patch
	}
	return rules.Patch
}

// CargoWatcher turns the index's versions of the crates in use into jobs, like
// NpmWatcher does for packages: one read per crate per poll through Engine.Crates, one
// job per (repo, crate, target version).
type CargoWatcher struct {
	Engine *Engine

	elect election // PollElected's claim
}

// Poll returns how many jobs were new. It makes no request while no repo uses a crate,
// nor while Engine.Crates is nil.
func (w *CargoWatcher) Poll(ctx context.Context) (int, error) {
	e := w.Engine
	if e.Crates == nil {
		return 0, nil
	}
	pkgs, err := e.polledPackages(ctx, cargoPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range pkgs {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceCargo, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
			if err := e.dropPackage(ctx, model.DatasourceCargo, cargoPackagesCursor, pkg); err != nil {
				e.log().Warn("unused package not pruned", "package", pkg, "err", err)
			}
			continue
		}
		cr, err := e.Crates.Crate(ctx, pkg)
		if err != nil {
			if errors.Is(err, cratesindex.ErrDenied) || errors.Is(err, cratesindex.ErrNotFound) {
				e.log().Info("crate not readable anonymously, left alone", "package", pkg, "err", err)
			} else {
				e.log().Warn("crate not read", "package", pkg, "err", err)
			}
			continue
		}
		for _, d := range deps {
			floor, _ := manager.CargoReqFloor(d.Version)
			if floor == "" {
				continue
			}
			// The newest release of each update type, newest first: a repo that ignores
			// majors (every 0.x minor bump is one) must still get the patch behind it.
			var target string
			var rs *repoState
			var dec rules.Decision
			at := e.now()
			for _, c := range cargoTargets(cr, d, pkg) {
				if rs == nil {
					if rs, err = e.repoState(ctx, repos, d.Repo); err != nil {
						return n, err
					}
					if !rs.found {
						break
					}
				}
				// The index has no publication dates: the cooldown runs from the first poll
				// that saw the version, and EnqueueJob keeps the first ReleasedAt.
				if x, ok := e.decide(rs, d, c.typ, at); ok && x.Enabled {
					target, dec = c.version, x
					break
				}
			}
			if target == "" {
				continue
			}
			inserted, err := e.Store.EnqueueJob(ctx, model.Job{
				Key: model.JobKey(d.Repo, model.DatasourceCargo, pkg, target), Repo: d.Repo,
				Datasource: model.DatasourceCargo, Package: pkg, Version: target,
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

type cargoTarget struct {
	version string
	typ     rules.UpdateType
}

// cargoTargets lists, newest first, the newest release of each update type that is an
// update of d.
func cargoTargets(cr cratesindex.Crate, d model.Dependency, pkg string) []cargoTarget {
	newest := map[rules.UpdateType]string{}
	for _, v := range cr.Versions {
		if v.Yanked || semver.Prerelease("v"+v.Version) != "" {
			continue
		}
		typ, ok := applicableCargo(d, pkg, v.Version)
		if ok && (newest[typ] == "" || semver.Compare("v"+v.Version, "v"+newest[typ]) > 0) {
			newest[typ] = v.Version
		}
	}
	var out []cargoTarget
	for typ, v := range newest {
		out = append(out, cargoTarget{v, typ})
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare("v"+out[i].version, "v"+out[j].version) > 0 })
	return out
}

// cargoGuard is called before a Cargo dependency is rewritten. A non-empty reason
// means the update must not be proposed.
//
// The index ripple asks is crates.io's. A repository whose .cargo/config.toml
// replaces a source (`replace-with`, a mirror or a vendored directory) resolves crates
// somewhere else, and a crates.io crate of the same name is then possibly not the one
// the repo uses.
func (r *runner) cargoGuard(d model.Dependency) string {
	for dir := path.Dir(d.File); ; dir = path.Dir(dir) {
		base := dir
		if base == "." {
			base = ""
		}
		for _, name := range []string{".cargo/config.toml", ".cargo/config"} {
			p := path.Join(base, name)
			content, err := r.ws.ReadFile(p)
			if err == nil && strings.Contains(string(content), "replace-with") {
				return fmt.Sprintf("%s replaces a registry source; ripple reads only crates.io's index and will not guess a version for a crate that may live elsewhere", p)
			}
		}
		if dir == "." || dir == "/" {
			return ""
		}
	}
}

// noteCargoLock records the Cargo.lock files that resolve an edited Cargo.toml: the
// one beside it and any in a parent directory (a workspace keeps one at its root).
func (r *runner) noteCargoLock(d model.Dependency) {
	for dir := path.Dir(d.File); ; dir = path.Dir(dir) {
		base := dir
		if base == "." {
			base = ""
		}
		p := path.Join(base, "Cargo.lock")
		if _, err := r.ws.ReadFile(p); err == nil {
			if r.cargoLocks == nil {
				r.cargoLocks = map[string]bool{}
			}
			r.cargoLocks[p] = true
		}
		if dir == "." || dir == "/" {
			return
		}
	}
}

// cargoNotes is the PR body section for lock files ripple could not regenerate.
func (r *runner) cargoNotes() string {
	if len(r.cargoLocks) == 0 {
		return ""
	}
	var files []string
	for f := range r.cargoLocks {
		files = append(files, f)
	}
	sort.Strings(files)
	s := "\n## Cargo.lock not regenerated\n\nripple edits Cargo.toml only: it never runs cargo. These lock files still hold the old resolution, so run `cargo update -p <crate>` (or `cargo build`) to refresh them before merging (CI with `--locked` will fail until you do):\n\n"
	for _, f := range files {
		s += "- `" + f + "`\n"
	}
	return s
}
