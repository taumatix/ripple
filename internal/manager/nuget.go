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
// (`$(Ver)`), a prerelease or four-segment version, an unreadable `<Version>` child, a
// `VersionOverride=` beside a `Version` or an Update, and anything inside an XML comment. A
// PackageReference's `VersionOverride="1.2.3"` is read like a version (see scanNuget).
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
	nugetCommentRE  = regexp.MustCompile(`(?s)<!--.*?-->`)
	nugetTagRE      = regexp.MustCompile(`(?s)<(PackageReference|PackageVersion)\b([^<>]*?)/?>`)
	nugetAttrRE     = regexp.MustCompile(`(?s)\b([A-Za-z]+)\s*=\s*("([^"]*)"|'([^']*)')`)
	nugetChildRE    = regexp.MustCompile(`<Version>([^<>]*)</Version>`)
	nugetAnyChildRE = regexp.MustCompile(`<Version\b`)
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
// the child is what is read (dotnet restore resolves the child over the attribute,
// TestRealtoolNugetItemForms). An item that a later Update of the same id overrides is dead:
// dotnet restores the Update's version, so editing the Include would change nothing and is
// skipped, and the Update is the one edited (see docs/nuget.md). An item with several or a
// conditional `<Version>` child is unreadable and skipped.
func scanNuget(content string) []nugetEntry {
	items := scanNugetItems(content)
	// per id: the last Update that follows an Include and sets a version overrides everything
	// before it
	liveUpdate := map[string]int{}
	seenInclude := map[string]bool{}
	for i, it := range items {
		key := it.tag + "/" + it.name
		if !it.update {
			seenInclude[key] = true
		} else if seenInclude[key] && it.versioned {
			liveUpdate[key] = i
		}
	}
	var out []nugetEntry
	for i, it := range items {
		if !it.readable {
			continue
		}
		if u, ok := liveUpdate[it.tag+"/"+it.name]; ok && u != i {
			continue // overridden by a later Update
		}
		if nugetOpaqueUpdateAfter(items, i) {
			continue // a list or wildcard Update may override it: the live version is unknown
		}
		out = append(out, nugetEntry{it.name, it.spec, it.start, it.start + len(it.spec)})
	}
	return out
}

// nugetOpaqueUpdateAfter reports whether a later `Update="A;B"` or `Update="A*"` of the same
// element kind names items[i]. Such an Update is not read (a package id has no `;` or `*`),
// but MSBuild applies it to every item it matches, so the Include before it may be dead.
func nugetOpaqueUpdateAfter(items []nugetItem, i int) bool {
	for _, u := range items[i+1:] {
		if !u.opaque || u.tag != items[i].tag {
			continue
		}
		for _, pat := range strings.Split(u.name, ";") {
			if ok, err := path.Match(strings.TrimSpace(pat), items[i].name); ok || err != nil {
				return true
			}
		}
	}
	return false
}

// nugetItem is one PackageReference/PackageVersion element in file order. versioned means it
// sets a version (attribute or child), readable that the version is one ripple can edit.
type nugetItem struct {
	opaque          bool // an Update whose name is a list or a wildcard; only its name is set
	tag, name, spec string
	start           int
	update          bool
	versioned       bool
	readable        bool
}

func scanNugetItems(content string) []nugetItem {
	masked := nugetCommentRE.ReplaceAllStringFunc(content, func(s string) string {
		return strings.Repeat(" ", len(s))
	})
	var out []nugetItem
	included := map[string]bool{} // PackageReference ids with an Include seen so far
	for _, m := range nugetTagRE.FindAllStringSubmatchIndex(masked, -1) {
		tag := masked[m[2]:m[3]]
		attrsOff := m[4]
		attrs := masked[m[4]:m[5]]
		var name, update, spec string
		var override string
		overrideStart := 0
		specStart, haveName, haveSpec, haveOverride := 0, false, false, false
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
				override, overrideStart, haveOverride = val, attrsOff+vs, true
			}
		}
		if haveOverride {
			// Central package management: the override is the version the project restores, so
			// its digits are what moves. It is not read beside a Version attribute or an Update
			// (an error to NuGet, NU1008), on a PackageVersion (where it means nothing), or with
			// a `<Version>` child.
			if tag != "PackageReference" || haveSpec || update != "" || !haveName {
				continue
			}
			if !(m[1] >= 2 && masked[m[1]-2] == '/') {
				if _, _, n := nugetChild(masked, m[1], tag); n != 0 {
					continue
				}
			}
			spec, specStart, haveSpec = override, overrideStart, true
		}
		isUpdate := update != "" && !haveName
		if update != "" && !isUpdate {
			continue // both Include and Update: not read
		}
		if isUpdate {
			name = update
		} else if haveName {
			included[tag+"/"+name] = true
		}
		if isUpdate && strings.ContainsAny(name, ";*?") {
			out = append(out, nugetItem{tag: tag, name: name, update: true, opaque: true})
			continue
		}
		if isUpdate && !included[tag+"/"+name] {
			continue // nothing earlier to update
		}
		if !(haveName || isUpdate) || !nuget.ValidName(name) {
			continue
		}
		it := nugetItem{tag: tag, name: name, update: isUpdate, versioned: haveSpec}
		if tag == "PackageReference" && !(m[1] >= 2 && masked[m[1]-2] == '/') {
			// a `<Version>` child element wins over the attribute
			switch s, st, n := nugetChild(masked, m[1], tag); {
			case n == 1:
				spec, specStart, haveSpec = s, st, true
				it.versioned = true
			case n > 1:
				it.versioned, haveSpec = true, false
			}
		}
		if haveSpec && ParseNugetSpec(spec) {
			it.spec, it.start, it.readable = spec, specStart, true
		}
		if it.versioned || !isUpdate {
			out = append(out, it)
		}
	}
	return out
}

// nugetChild looks at the `<Version>` children of the element whose start tag ends at open. It
// returns the text and offset of the one plain child with n == 1, and n == 0 when there is
// none (or the element is not closed before the next PackageReference or PackageVersion
// starts). n > 1 means several, or one that is not a plain `<Version>X</Version>` (a
// Condition, say): the version cannot be told.
func nugetChild(masked string, open int, tag string) (string, int, int) {
	end := strings.Index(masked[open:], "</"+tag+">")
	if end < 0 {
		return "", 0, 0
	}
	body := masked[open : open+end]
	if strings.Contains(body, "<PackageReference") || strings.Contains(body, "<PackageVersion") {
		return "", 0, 0
	}
	all := nugetAnyChildRE.FindAllStringIndex(body, -1)
	ms := nugetChildRE.FindAllStringSubmatchIndex(body, -1)
	switch {
	case len(all) == 0:
		return "", 0, 0
	case len(all) != 1 || len(ms) != 1:
		return "", 0, 2
	}
	return body[ms[0][2]:ms[0][3]], open + ms[0][2], 1
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
