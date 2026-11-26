package manager

import (
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/maven"
	"github.com/taumatix/ripple/internal/model"
)

// Maven moves the literal versions of the `<dependency>` elements of a pom.xml (in
// `<dependencies>` and in `<dependencyManagement>`, including those of a plugin), the version's
// digits only, never re-serialised. A package is "groupId:artifactId".
//
// A version is read when it has two or three numeric segments (`4.13`, `3.12.0`), or is a
// `${name}` whose value is such a number in the pom's own `<properties>`; in that case the
// property's value is the text that moves (see scanMavenProperties for when it is safe to).
//
// Left alone: a range (`[1.0,2.0)`), a qualified version (`1.0.0-SNAPSHOT`, `1.0-beta`,
// `1.0.0.RELEASE`), one with one or more than three numeric segments, a property that is not
// defined once in the pom's own `<properties>` or is used for anything but the version of one
// package, a dependency with no version (managed by a parent or a BOM), anything in an XML
// comment, and every dependency of a pom that declares `<repositories>` or `<pluginRepositories>`
// (the artifact may come from there, and ripple reads Maven Central only). Parents and BOMs are
// not followed and Maven's own settings.xml mirrors are not read: see docs/maven.md.
type Maven struct {
	Dir string
}

func (Maven) Name() string { return "maven" }

func (m Maven) Matches(p string) bool {
	return path.Base(p) == "pom.xml" && config.InDirectory(m.Dir, p)
}

var mavenSpecRE = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// ParseMavenSpec reports whether s is a version text ripple moves ("X.Y" or "X.Y.Z").
func ParseMavenSpec(s string) bool { return mavenSpecRE.MatchString(s) }

type mavenEntry struct {
	pkg, spec  string
	start, end int
}

var (
	mavenCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	mavenMaskRE    = regexp.MustCompile(`(?s)<!--.*?-->|<!\[CDATA\[.*?\]\]>|<exclusions>.*?</exclusions>`)
	mavenReposRE   = regexp.MustCompile(`<(repositories|pluginRepositories)\b`)
	mavenDepRE     = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	mavenGroupRE   = regexp.MustCompile(`<groupId>\s*([^<\s]*)\s*</groupId>`)
	mavenArtRE     = regexp.MustCompile(`<artifactId>\s*([^<\s]*)\s*</artifactId>`)
	mavenVerRE     = regexp.MustCompile(`<version>\s*([^<\s]*)\s*</version>`)
	mavenPropUseRE = regexp.MustCompile(`^\$\{([^${}\s<>]+)\}$`)
)

// blank replaces every match with spaces (newlines kept), so every offset survives.
func blank(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		b := []byte(m)
		for i, c := range b {
			if c != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
}

// scanMaven lists the readable declarations in source order. Comments, CDATA and exclusions are
// blanked to spaces, keeping every offset, so none of them is read.
func scanMaven(content string) []mavenEntry {
	masked := blank(mavenMaskRE, content)
	if mavenReposRE.MatchString(masked) {
		return nil
	}
	type propUse struct{ pkg, name string }
	var out []mavenEntry
	var uses []propUse
	for _, m := range mavenDepRE.FindAllStringSubmatchIndex(masked, -1) {
		body := masked[m[2]:m[3]]
		g, a, v := mavenGroupRE.FindStringSubmatchIndex(body), mavenArtRE.FindStringSubmatchIndex(body), mavenVerRE.FindStringSubmatchIndex(body)
		if g == nil || a == nil || v == nil {
			continue
		}
		group, artifact, spec := body[g[2]:g[3]], body[a[2]:a[3]], body[v[2]:v[3]]
		if !maven.ValidCoordinate(group, artifact) {
			continue
		}
		if ParseMavenSpec(spec) {
			start := m[2] + v[2]
			out = append(out, mavenEntry{group + ":" + artifact, spec, start, start + len(spec)})
		} else if p := mavenPropUseRE.FindStringSubmatch(spec); p != nil {
			uses = append(uses, propUse{group + ":" + artifact, p[1]})
		}
	}
	if len(uses) > 0 {
		props := scanMavenProperties(content)
		// Every `${name}` outside a comment, CDATA included: a use that is not a dependency's
		// version (a plugin, a description, another property) is a reason to leave it alone.
		text := blank(mavenCommentRE, content)
		pkgs := map[string]map[string]bool{}
		count := map[string]int{}
		for _, u := range uses {
			if pkgs[u.name] == nil {
				pkgs[u.name] = map[string]bool{}
			}
			pkgs[u.name][u.pkg] = true
			count[u.name]++
		}
		for name, ps := range pkgs {
			p, ok := props[name]
			if !ok || len(ps) != 1 || strings.Count(text, "${"+name+"}") != count[name] {
				continue
			}
			for pkg := range ps {
				out = append(out, mavenEntry{pkg, p.value, p.start, p.end})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

type mavenProperty struct {
	value      string
	start, end int
}

// mavenReservedProps are the prefixes under which Maven resolves a name itself (docs/vendor/maven/
// pom-reference.txt, "Properties": env.X, project.x, settings.x and the Java system properties),
// so a `<properties>` entry of that name is not what `${name}` means.
var mavenReservedProps = []string{"project.", "pom.", "env.", "settings.", "java.", "os.", "user.", "file.", "line.", "path.", "sun.", "jdk."}

// scanMavenProperties returns the properties that `${name}` can be replaced by: declared exactly
// once in the whole pom, as a direct child of `<project><properties>` (a profile's properties are a
// different set), whose whole text is a version ripple moves. Well-formed XML is required; on any
// parse error there are none, so a malformed pom keeps the literal-only reading.
func scanMavenProperties(content string) map[string]mavenProperty {
	type open struct {
		name       string
		start, end int // the one text token, -1 when none yet
		bad        bool
	}
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = true
	var stack []string
	var cur *open
	seen := map[string]int{}
	found := map[string]mavenProperty{}
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if cur != nil {
				cur.bad = true // a child element: not a plain value
			}
			if n := len(stack); n > 0 && stack[n-1] == "properties" {
				seen[t.Name.Local]++
				if n == 2 && stack[0] == "project" {
					cur = &open{name: t.Name.Local, start: -1}
				}
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if cur != nil && len(stack) == 3 {
				if !cur.bad && cur.start >= 0 {
					raw := content[cur.start:cur.end]
					v := strings.TrimSpace(raw)
					lead := strings.Index(raw, v)
					if ParseMavenSpec(v) {
						found[cur.name] = mavenProperty{v, cur.start + lead, cur.start + lead + len(v)}
					}
				}
				cur = nil
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if cur != nil && len(stack) == 3 {
				end := int(dec.InputOffset())
				if cur.start >= 0 || content[before:end] != string(t) {
					cur.bad = true // two text tokens, or CDATA or an entity: not a plain value
				} else {
					cur.start, cur.end = before, end
				}
			}
		}
	}
	for name := range found {
		if seen[name] != 1 || reservedMavenProp(name) {
			delete(found, name)
		}
	}
	return found
}

func reservedMavenProp(name string) bool {
	for _, p := range mavenReservedProps {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (m Maven) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanMaven(string(content)) {
		d := model.Dependency{File: p, Manager: m.Name(), Datasource: model.DatasourceMaven, Package: e.pkg, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (m Maven) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: an artifact cannot be renamed", ErrUnsupported)
	}
	if !ParseMavenSpec(dep.Version) {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if !ParseMavenSpec(nv) {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	var hits []mavenEntry
	for _, e := range scanMaven(string(content)) {
		if e.pkg == dep.Package && e.spec == dep.Version {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s: dependency %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start > hits[j].start })
	out := string(content)
	for _, e := range hits {
		out = out[:e.start] + nv + out[e.end:]
	}
	return []byte(out), nil
}
