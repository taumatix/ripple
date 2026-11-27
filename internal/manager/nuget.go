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
	nugetChildRE   = regexp.MustCompile(`<Version>([^<>]*)</Version>`)
)

// scanNuget lists the readable declarations. Comments are blanked to spaces, keeping every
// offset, so a commented-out reference is not read.
//
// Besides the `Version` attribute of an Include, a PackageReference's `<Version>` child
// element is read (dotnet restore resolves it, TestRealtoolNugetItemForms), and so is the
// `Version` of a `PackageReference Update="Id"` that comes after an `Include="Id"` of the
// same file: MSBuild evaluates items in order of appearance and an Update changes the
// metadata of items evaluated earlier, so an Update before its Include, or one with no
// Include, does nothing and is not read. When an Include has both an attribute and a child
// the attribute is still what is read, as before (see docs/nuget.md).
func scanNuget(content string) []nugetEntry {
	masked := nugetCommentRE.ReplaceAllStringFunc(content, func(s string) string {
		return strings.Repeat(" ", len(s))
	})
	var out []nugetEntry
	included := map[string]bool{} // PackageReference ids with an Include seen so far
	for _, m := range nugetTagRE.FindAllStringSubmatchIndex(masked, -1) {
		tag := masked[m[2]:m[3]]
		attrsOff := m[4]
		attrs := masked[m[4]:m[5]]
		var name, update, spec string
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
			case "Update":
				update = val
			case "VersionOverride":
				bad = true
			}
		}
		if bad {
			continue
		}
		isUpdate := tag == "PackageReference" && update != "" && !haveName
		if update != "" && !isUpdate {
			continue // an Update of a PackageVersion, or both Include and Update: not read
		}
		if isUpdate {
			name = update
		} else if tag == "PackageReference" && haveName {
			included[name] = true
		}
		selfClosing := m[1] >= 2 && masked[m[1]-2] == '/'
		if !haveSpec && tag == "PackageReference" && (haveName || isUpdate) && !selfClosing {
			// a `<Version>` child element: the one child, a plain release
			if s, st, ok := nugetChild(masked, m[1], tag); ok {
				spec, specStart, haveSpec = s, st, true
			}
		}
		if isUpdate && !included[name] {
			continue // nothing earlier to update
		}
		if !(haveName || isUpdate) || !haveSpec || !ParseNugetSpec(spec) || !nuget.ValidName(name) {
			continue
		}
		out = append(out, nugetEntry{name, spec, specStart, specStart + len(spec)})
	}
	return out
}

// nugetChild reads the single `<Version>X</Version>` child of the element whose start tag ends
// at open: its text and offset. It reports false when the element has none or several, or
// is not closed before the next PackageReference or PackageVersion starts.
func nugetChild(masked string, open int, tag string) (string, int, bool) {
	end := strings.Index(masked[open:], "</"+tag+">")
	if end < 0 {
		return "", 0, false
	}
	body := masked[open : open+end]
	if strings.Contains(body, "<PackageReference") || strings.Contains(body, "<PackageVersion") {
		return "", 0, false
	}
	ms := nugetChildRE.FindAllStringSubmatchIndex(body, -1)
	if len(ms) != 1 {
		return "", 0, false
	}
	return body[ms[0][2]:ms[0][3]], open + ms[0][2], true
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
