package manager

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
)

// Values of the go-toolchain key of an update entry. The empty value is off.
const (
	// GoToolchainLine moves an existing `toolchain` line to the newest patch of its
	// own Go release. It cannot raise what a dependant needs: the go command reads
	// the toolchain line of the main module only.
	GoToolchainLine = "toolchain"
	// GoToolchainDirective also moves the `go` directive once its release is out of
	// support, which raises the Go version every dependant of the module needs.
	GoToolchainDirective = "directive"
)

var goVersionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// GoSemver turns a Go version as go.mod spells it ("1.25", "1.25.3", with or
// without a "go" prefix) into semver ("v1.25", "v1.25.3"). Release candidates,
// betas and the words default and local are not versions ripple proposes from.
func GoSemver(v string) (string, bool) {
	v = strings.TrimPrefix(v, "go")
	if !goVersionRE.MatchString(v) {
		return "", false
	}
	return "v" + v, true
}

// GoDirective is the spelling the go directive takes when it moves from old to the
// release target (both semver): "1.26" when old named no patch, "1.26.0" when it did.
// The first release of the minor is the lowest requirement that names it, so a bump
// asks no more of dependants than moving to that minor already does.
func GoDirective(old, target string) string {
	mm := strings.TrimPrefix(semver.MajorMinor(target), "v")
	if strings.Count(strings.TrimPrefix(old, "v"), ".") == 2 {
		return mm + ".0"
	}
	return mm
}

// extractToolchain reads the go and toolchain lines. It parses strictly: the lax
// parse used for dependencies drops the toolchain line, the very reason a dependency's
// toolchain line cannot matter to its dependants. A file strict parsing rejects has no
// toolchain dependencies, and the require lines are still extracted.
func (g GoMod) extractToolchain(p string, content []byte) []model.Dependency {
	if g.Toolchain == "" {
		return nil
	}
	f, err := modfile.Parse(p, content, nil)
	if err != nil {
		return nil
	}
	var out []model.Dependency
	add := func(pkg, raw string) {
		if v, ok := GoSemver(raw); ok {
			out = append(out, model.Dependency{File: p, Manager: "gomod", Datasource: model.DatasourceGoToolchain, Package: pkg, Version: v})
		}
	}
	if g.Toolchain == GoToolchainDirective && f.Go != nil {
		add(model.PackageGoDirective, f.Go.Version)
	}
	if (g.Toolchain == GoToolchainLine || g.Toolchain == GoToolchainDirective) && f.Toolchain != nil {
		add(model.PackageGoToolchain, f.Toolchain.Name)
	}
	return out
}

func applyToolchain(dep model.Dependency, content []byte, newVersion string) ([]byte, error) {
	f, err := modfile.Parse(dep.File, content, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dep.File, err)
	}
	if !semver.IsValid(newVersion) || semver.Compare(newVersion, dep.Version) <= 0 {
		return nil, fmt.Errorf("%s: %s is not newer than %s", dep.File, newVersion, dep.Version)
	}
	switch dep.Package {
	case model.PackageGoDirective:
		cur, _ := GoSemver(goLine(f))
		if cur != dep.Version {
			return nil, fmt.Errorf("%s: go directive is no longer %s: %w", dep.File, dep.Version, ErrStale)
		}
		if err := f.AddGoStmt(GoDirective(dep.Version, newVersion)); err != nil {
			return nil, err
		}
		// A toolchain line below the new go line would be a contradiction; the
		// newest patch of the new minor is what `go get go@` would write too.
		if f.Toolchain != nil {
			if tv, ok := GoSemver(f.Toolchain.Name); ok && semver.Compare(tv, newVersion) < 0 {
				if err := f.AddToolchainStmt("go" + strings.TrimPrefix(newVersion, "v")); err != nil {
					return nil, err
				}
			}
		}
	case model.PackageGoToolchain:
		cur := ""
		if f.Toolchain != nil {
			cur, _ = GoSemver(f.Toolchain.Name)
		}
		if cur != dep.Version {
			return nil, fmt.Errorf("%s: toolchain line is no longer %s: %w", dep.File, dep.Version, ErrStale)
		}
		if err := f.AddToolchainStmt("go" + strings.TrimPrefix(newVersion, "v")); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%s: unknown go toolchain package %q", dep.File, dep.Package)
	}
	return f.Format()
}

func goLine(f *modfile.File) string {
	if f.Go == nil {
		return ""
	}
	return f.Go.Version
}
