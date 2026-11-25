package manager

import (
	"fmt"
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
// Left alone: a version that is a property (`${junit.version}`), a range (`[1.0,2.0)`), a
// qualified one (`1.0.0-SNAPSHOT`, `1.0-beta`, `1.0.0.RELEASE`), one with fewer or more than three
// numeric segments, a dependency with no version (managed by a parent or a BOM), anything in an
// XML comment, and every dependency of a pom that declares `<repositories>` or
// `<pluginRepositories>` (the artifact may come from there, and ripple reads Maven Central only).
// Maven's own settings.xml mirrors are not read: see docs/maven.md.
type Maven struct {
	Dir string
}

func (Maven) Name() string { return "maven" }

func (m Maven) Matches(p string) bool {
	return path.Base(p) == "pom.xml" && config.InDirectory(m.Dir, p)
}

var mavenSpecRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ParseMavenSpec reports whether s is a version text ripple moves ("X.Y.Z").
func ParseMavenSpec(s string) bool { return mavenSpecRE.MatchString(s) }

type mavenEntry struct {
	pkg, spec  string
	start, end int
}

var (
	mavenMaskRE  = regexp.MustCompile(`(?s)<!--.*?-->|<!\[CDATA\[.*?\]\]>|<exclusions>.*?</exclusions>`)
	mavenReposRE = regexp.MustCompile(`<(repositories|pluginRepositories)\b`)
	mavenDepRE   = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	mavenGroupRE = regexp.MustCompile(`<groupId>\s*([^<\s]*)\s*</groupId>`)
	mavenArtRE   = regexp.MustCompile(`<artifactId>\s*([^<\s]*)\s*</artifactId>`)
	mavenVerRE   = regexp.MustCompile(`<version>\s*([^<\s]*)\s*</version>`)
)

// scanMaven lists the readable declarations. Comments, CDATA and exclusions are blanked to spaces,
// keeping every offset, so none of them is read.
func scanMaven(content string) []mavenEntry {
	masked := mavenMaskRE.ReplaceAllStringFunc(content, func(s string) string {
		b := []byte(s)
		for i, c := range b {
			if c != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
	if mavenReposRE.MatchString(masked) {
		return nil
	}
	var out []mavenEntry
	for _, m := range mavenDepRE.FindAllStringSubmatchIndex(masked, -1) {
		body := masked[m[2]:m[3]]
		g, a, v := mavenGroupRE.FindStringSubmatchIndex(body), mavenArtRE.FindStringSubmatchIndex(body), mavenVerRE.FindStringSubmatchIndex(body)
		if g == nil || a == nil || v == nil {
			continue
		}
		group, artifact, spec := body[g[2]:g[3]], body[a[2]:a[3]], body[v[2]:v[3]]
		if !ParseMavenSpec(spec) || !maven.ValidCoordinate(group, artifact) {
			continue
		}
		start := m[2] + v[2]
		out = append(out, mavenEntry{group + ":" + artifact, spec, start, start + len(spec)})
	}
	return out
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
