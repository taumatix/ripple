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
// requirement ripple can move, and a release newer than the requirement's floor.
func applicableCargo(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceCargo || d.Package != pkg {
		return "", false
	}
	floor, _ := manager.CargoReqFloor(d.Version)
	if floor == "" {
		return "", false
	}
	return rules.Classify("v"+floor, "v"+version)
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
	raw, err := e.Store.GetCursor(ctx, cargoPackagesCursor)
	if err != nil {
		return 0, err
	}
	n := 0
	repos := map[string]*repoState{}
	for _, pkg := range strings.Fields(raw) {
		deps, err := e.Store.ListDependents(ctx, model.DatasourceCargo, pkg)
		if err != nil {
			return n, fmt.Errorf("list dependents of %s: %w", pkg, err)
		}
		if len(deps) == 0 {
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
			target := cr.Newest(floor)
			if target == "" {
				continue
			}
			typ, ok := applicable(d, model.DatasourceCargo, pkg, target)
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
			// The index has no publication dates: the cooldown runs from the first poll
			// that saw the version, and EnqueueJob keeps the first ReleasedAt.
			at := e.now()
			dec, ok := e.decide(rs, d, typ, at)
			if !ok || !dec.Enabled {
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
