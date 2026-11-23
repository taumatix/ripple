package engine

import (
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

// applicable reports whether the release (pkg, version) is an update for dependency
// d, and of which kind. Releases of the same package are classified by version. A
// release of another path on the same line (example.com/m/v60 -> example.com/m/v61,
// or example.com/m -> example.com/m/v2) is a major update when the new path claims a
// higher major than the current one: the module path is the identity of a major
// version in Go, so this is how a major bump is discovered at all. Only the gomod
// manager can rewrite a name; the path never moves backwards.
func applicable(d model.Dependency, ds, pkg, version string) (rules.UpdateType, bool) {
	if ds == model.DatasourceGoToolchain {
		return applicableToolchain(d, pkg, version)
	}
	if d.Datasource != ds || model.Line(ds, d.Package) != model.Line(ds, pkg) {
		return "", false
	}
	typ, ok := rules.Classify(d.Version, version)
	if !ok {
		return "", false
	}
	if d.Package == pkg {
		return typ, true
	}
	if d.Manager != "gomod" || model.PathMajor(ds, pkg) <= model.PathMajor(ds, d.Package) {
		return "", false
	}
	if _, pm, ok := module.SplitPathVersion(pkg); !ok || module.CheckPathMajor(version, pm) != nil {
		return "", false // a release must carry the major its path claims
	}
	return rules.Major, true
}

// applicableToolchain judges a job that ToolchainWatcher already chose a target for:
// it must be newer, and a go directive moves only to another minor, never to a patch.
func applicableToolchain(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceGoToolchain || d.Package != pkg {
		return "", false
	}
	typ, ok := rules.Classify(d.Version, version)
	if !ok || (pkg == model.PackageGoDirective && typ == rules.Patch) {
		return "", false
	}
	return typ, true
}

// applicablePseudoFix judges a security job whose target is a pseudo-version, the only
// fix an advisory has. Classify refuses those (a pseudo-version is no release), so this
// is the one place they are accepted, and only for the same module path, from a release
// of the same major, to a later version. It is never a major update.
func applicablePseudoFix(d model.Dependency, pkg, version string) (rules.UpdateType, bool) {
	if d.Datasource != model.DatasourceGo || d.Package != pkg || !module.IsPseudoVersion(version) {
		return "", false
	}
	if !semver.IsValid(d.Version) || module.IsPseudoVersion(d.Version) || semver.Prerelease(d.Version) != "" {
		return "", false
	}
	if semver.Major(d.Version) != semver.Major(version) || semver.Compare(version, d.Version) <= 0 {
		return "", false
	}
	if semver.MajorMinor(version) != semver.MajorMinor(d.Version) {
		return rules.Minor, true
	}
	return rules.Patch, true
}
