package manager

import (
	"errors"
	"fmt"
	"path"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
)

// GoMod handles go.mod files. Indirect requirements are not proposed.
type GoMod struct {
	// Dir limits the manager to this directory and below; "" means the whole repo.
	Dir string
	// Toolchain is "", GoToolchainLine or GoToolchainDirective: whether the go
	// directive and toolchain line are dependencies too. Off unless asked for.
	Toolchain string
}

func (GoMod) Name() string { return "gomod" }

func (g GoMod) Matches(p string) bool {
	return path.Base(p) == "go.mod" && config.InDirectory(g.Dir, p)
}

func (g GoMod) Extract(p string, content []byte) ([]model.Dependency, error) {
	f, err := modfile.ParseLax(p, content, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	out := g.extractToolchain(p, content)
	for _, r := range f.Require {
		if r.Indirect {
			continue
		}
		out = append(out, model.Dependency{File: p, Manager: "gomod", Datasource: model.DatasourceGo, Package: r.Mod.Path, Version: r.Mod.Version})
	}
	return out, nil
}

func (GoMod) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	f, err := modfile.ParseLax(dep.File, content, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dep.File, err)
	}
	if dep.Datasource == model.DatasourceGoToolchain {
		return applyToolchain(dep, content, newVersion)
	}
	found := false
	for _, r := range f.Require {
		if r.Mod.Path == dep.Package && r.Mod.Version == dep.Version && !r.Indirect {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	if newPackage == dep.Package {
		if err := f.AddRequire(dep.Package, newVersion); err != nil {
			return nil, err
		}
		return f.Format()
	}
	// ParseLax skips replace and exclude directives, which a migration must edit.
	if f, err = modfile.Parse(dep.File, content, nil); err != nil {
		return nil, fmt.Errorf("parse %s: %w", dep.File, err)
	}
	if err := migrate(f, dep.Package, newPackage, newVersion); err != nil {
		return nil, err
	}
	return f.Format()
}

// migrate moves a requirement to a new module path (a major version bump).
// Directives that name the old path follow it when they can: an unversioned
// replace keeps its target and is renamed; a replace or exclude pinned to a version
// of the old major cannot apply to the new path and is dropped.
func migrate(f *modfile.File, oldPath, newPath, newVersion string) error {
	if err := f.DropRequire(oldPath); err != nil {
		return err
	}
	f.AddNewRequire(newPath, newVersion, false)
	// modfile zeroes a dropped entry in place, so copy before dropping.
	reps := make([]modfile.Replace, 0, len(f.Replace))
	for _, r := range f.Replace {
		reps = append(reps, *r)
	}
	for _, r := range reps {
		if r.Old.Path != oldPath {
			continue
		}
		if err := f.DropReplace(r.Old.Path, r.Old.Version); err != nil {
			return err
		}
		if r.Old.Version == "" {
			if err := f.AddReplace(newPath, "", r.New.Path, r.New.Version); err != nil {
				return err
			}
		}
	}
	excl := make([]module.Version, 0, len(f.Exclude))
	for _, x := range f.Exclude {
		excl = append(excl, x.Mod)
	}
	for _, x := range excl {
		if x.Path == oldPath {
			if err := f.DropExclude(x.Path, x.Version); err != nil {
				return err
			}
		}
	}
	f.SortBlocks()
	f.Cleanup()
	return nil
}

// ErrStale means the file no longer contains the dependency at the expected version.
var ErrStale = errors.New("stale dependency")

// ErrUnsupported means the manager cannot perform the requested kind of update.
var ErrUnsupported = errors.New("update not supported by this manager")

// RewriteGoWork follows a major bump in a go.work file: an unversioned replace of
// oldPath moves to newPath, and a replace pinned to a version of the old major is
// dropped, as in a go.mod. changed is false when the file never names oldPath.
func RewriteGoWork(content []byte, oldPath, newPath string) (out []byte, changed bool, err error) {
	f, err := modfile.ParseWork("go.work", content, nil)
	if err != nil {
		return nil, false, fmt.Errorf("parse go.work: %w", err)
	}
	// modfile zeroes a dropped entry in place, so copy before dropping.
	reps := make([]modfile.Replace, 0, len(f.Replace))
	for _, r := range f.Replace {
		reps = append(reps, *r)
	}
	for _, r := range reps {
		if r.Old.Path != oldPath {
			continue
		}
		changed = true
		if err := f.DropReplace(r.Old.Path, r.Old.Version); err != nil {
			return nil, false, err
		}
		if r.Old.Version == "" {
			if err := f.AddReplace(newPath, "", r.New.Path, r.New.Version); err != nil {
				return nil, false, err
			}
		}
	}
	if !changed {
		return content, false, nil
	}
	f.SortBlocks()
	f.Cleanup()
	return modfile.Format(f.Syntax), true, nil
}
