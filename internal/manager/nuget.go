package manager

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/nuget"
)

// Nuget moves the package versions written as attributes in an MSBuild project file
// (*.csproj, *.fsproj, *.vbproj) and in Directory.Packages.props:
// `<PackageReference Include="Id" Version="1.2.3" />` and `<PackageVersion Include="Id"
// Version="1.2.3" />`, the version's digits only, never re-serialised. A NuGet version
// attribute is a minimum, so a plain `1.2.3` is the whole form.
//
// Left alone: a version that is a range (`[1.0,2.0)`), a floating one (`1.*`), a property
// (`$(Ver)`), a prerelease or four-segment version, a version given as a `<Version>` child
// element, `Update=` and `VersionOverride=` forms, and anything inside an XML comment.
// ripple reads only nuget.org and does not read NuGet.Config, so a package served from a
// private feed under the same id would be looked up on nuget.org: see docs/nuget.md.
type Nuget struct {
	Dir string
}

func (Nuget) Name() string { return "nuget" }

func (n Nuget) Matches(p string) bool {
	b := path.Base(p)
	ext := strings.ToLower(path.Ext(b))
	ok := ext == ".csproj" || ext == ".fsproj" || ext == ".vbproj" || b == "Directory.Packages.props"
	return ok && config.InDirectory(n.Dir, p)
}

var nugetSpecRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ParseNugetSpec reports whether s is a version text ripple moves ("X.Y.Z").
func ParseNugetSpec(s string) bool { return nugetSpecRE.MatchString(s) }

type nugetEntry struct {
	name, spec string
	start, end int
}

var (
	nugetCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	nugetTagRE     = regexp.MustCompile(`(?s)<(PackageReference|PackageVersion)\b([^<>]*?)/?>`)
	nugetAttrRE    = regexp.MustCompile(`(?s)\b([A-Za-z]+)\s*=\s*("([^"]*)"|'([^']*)')`)
)

// scanNuget lists the readable declarations. Comments are blanked to spaces, keeping every
// offset, so a commented-out reference is not read.
func scanNuget(content string) []nugetEntry {
	masked := nugetCommentRE.ReplaceAllStringFunc(content, func(s string) string {
		return strings.Repeat(" ", len(s))
	})
	var out []nugetEntry
	for _, m := range nugetTagRE.FindAllStringSubmatchIndex(masked, -1) {
		attrsOff := m[4]
		attrs := masked[m[4]:m[5]]
		var name, spec string
		specStart, haveName, haveSpec, bad := 0, false, false, false
		for _, a := range nugetAttrRE.FindAllStringSubmatchIndex(attrs, -1) {
			key := attrs[a[2]:a[3]]
			vs, ve := a[6], a[7]
			if vs < 0 {
				vs, ve = a[8], a[9]
			}
			val := attrs[vs:ve]
			switch key {
			case "Include":
				name, haveName = val, true
			case "Version":
				spec, specStart, haveSpec = val, attrsOff+vs, true
			case "Update", "VersionOverride":
				bad = true
			}
		}
		if bad || !haveName || !haveSpec || !ParseNugetSpec(spec) || !nuget.ValidName(name) {
			continue
		}
		out = append(out, nugetEntry{name, spec, specStart, specStart + len(spec)})
	}
	return out
}

func (n Nuget) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanNuget(string(content)) {
		d := model.Dependency{File: p, Manager: n.Name(), Datasource: model.DatasourceNuget, Package: e.name, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (n Nuget) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a package cannot be renamed", ErrUnsupported)
	}
	if !ParseNugetSpec(dep.Version) {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if !ParseNugetSpec(nv) {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	var hits []nugetEntry
	for _, e := range scanNuget(string(content)) {
		if e.name == dep.Package && e.spec == dep.Version {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s: package %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start > hits[j].start })
	out := string(content)
	for _, e := range hits {
		out = out[:e.start] + nv + out[e.end:]
	}
	return []byte(out), nil
}
