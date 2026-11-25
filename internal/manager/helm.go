package manager

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/helmrepo"
	"github.com/taumatix/ripple/internal/model"
)

// Helm moves the chart dependencies of Chart.yaml (apiVersion v2) and requirements.yaml
// (v1): the `version` of a `dependencies:` entry that names a chart in an https chart
// repository (`repository: https://...`). The files are edited at the version's own
// bytes, never re-serialised, so layout, quoting, comments and key order survive.
//
// Left alone: an entry with no repository (a subchart in charts/), `file://`, `oci://`,
// an `@alias` or `alias:` repository (the URL is in the user's local helm config, not in
// the repo), a plain http URL, and a version that is not a single X.Y.Z release with an
// optional `=`, `^` or `~` (ranges such as ">=1.0.0 <2.0.0", wildcards, prereleases).
// The dependency's Package is "<repository URL>/<chart>".
type Helm struct {
	Dir string
}

func (Helm) Name() string { return "helm" }

func (h Helm) Matches(p string) bool {
	if !config.InDirectory(h.Dir, p) {
		return false
	}
	switch path.Base(p) {
	case "Chart.yaml", "requirements.yaml":
		return true
	}
	return false
}

// HelmPackage is the package name of chart in the repository at repoURL (a
// helmrepo.NormalizeURL form).
func HelmPackage(repoURL, chart string) string { return repoURL + "/" + chart }

// HelmSplit undoes HelmPackage.
func HelmSplit(pkg string) (repoURL, chart string, ok bool) {
	i := strings.LastIndex(pkg, "/")
	if i < 0 || i == len(pkg)-1 {
		return "", "", false
	}
	return pkg[:i], pkg[i+1:], helmrepo.NormalizeURL(pkg[:i]) == pkg[:i]
}

// HelmSpec is a dependency version ripple can move: one release, written as
// [=|^|~][v]X.Y.Z.
type HelmSpec struct {
	Op      string // "", "=", "^" or "~"
	VPrefix bool
	Version string // X.Y.Z, without a v
}

var helmSpecRE = regexp.MustCompile(`^([=^~]?)(v?)([0-9]+\.[0-9]+\.[0-9]+)$`)

// ParseHelmSpec reads a dependency's version text.
func ParseHelmSpec(s string) (HelmSpec, bool) {
	m := helmSpecRE.FindStringSubmatch(s)
	if m == nil {
		return HelmSpec{}, false
	}
	return HelmSpec{Op: m[1], VPrefix: m[2] == "v", Version: m[3]}, true
}

// Text is the spec naming newVersion (with or without a v) in the same form.
func (h HelmSpec) Text(newVersion string) string {
	v := strings.TrimPrefix(newVersion, "v")
	if h.VPrefix {
		v = "v" + v
	}
	return h.Op + v
}

type helmEntry struct {
	name, repo, spec string
	start, end       int // the version's bytes in the file, inside any quotes
}

var (
	helmDepsRE  = regexp.MustCompile(`^dependencies:[ \t]*(#.*)?\r?$`)
	helmDashRE  = regexp.MustCompile(`^( *)-( +)(.*)$`)
	helmKeyRE   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):(?:[ \t]+(.*?))?[ \t]*\r?$`)
	helmChartRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// helmScalar reads the single-line scalar val, which begins at file offset off: its text
// and the span of that text. Quoted scalars with escapes, block scalars, flow
// collections, anchors, aliases and tags are not read.
func helmScalar(val string, off int) (text string, start, end int, ok bool) {
	val = strings.TrimRight(val, " \t\r")
	if val == "" {
		return "", 0, 0, false
	}
	switch val[0] {
	case '"', '\'':
		q := val[0]
		i := strings.IndexByte(val[1:], q)
		if i < 0 {
			return "", 0, 0, false
		}
		inner, rest := val[1:1+i], strings.TrimSpace(val[2+i:])
		if (rest != "" && rest[0] != '#') || strings.ContainsRune(inner, '\\') || (q == '\'' && strings.HasSuffix(val[:2+i], "''")) {
			return "", 0, 0, false
		}
		return inner, off + 1, off + 1 + i, true
	case '|', '>', '[', '{', '&', '*', '!', '%', '@', '`', '#':
		return "", 0, 0, false
	}
	if i := strings.Index(val, " #"); i >= 0 {
		val = strings.TrimRight(val[:i], " \t")
	}
	if strings.Contains(val, ": ") {
		return "", 0, 0, false
	}
	return val, off, off + len(val), true
}

// scanHelm lists the readable entries of every top-level `dependencies:` list.
func scanHelm(content string) []helmEntry {
	var out []helmEntry
	type item struct {
		name, repo, spec string
		start, end       int
		seen             map[string]bool
		bad              bool
	}
	var cur *item
	flush := func() {
		if cur == nil || cur.bad {
			cur = nil
			return
		}
		repo := helmrepo.NormalizeURL(cur.repo)
		if repo != "" && helmChartRE.MatchString(cur.name) {
			if _, ok := ParseHelmSpec(cur.spec); ok {
				out = append(out, helmEntry{cur.name, repo, cur.spec, cur.start, cur.end})
			}
		}
		cur = nil
	}
	inDeps, itemIndent, keyCol := false, -1, 0
	// field records one `key: value` line of the current item.
	field := func(line string, lineOff, col int) {
		m := helmKeyRE.FindStringSubmatchIndex(line[col:])
		if m == nil {
			return
		}
		key := line[col+m[2] : col+m[3]]
		if key != "name" && key != "version" && key != "repository" {
			return
		}
		if cur.seen[key] {
			cur.bad = true
			return
		}
		cur.seen[key] = true
		if m[4] < 0 {
			cur.bad = true
			return
		}
		text, s, e, ok := helmScalar(line[col+m[4]:], lineOff+col+m[4])
		if !ok {
			cur.bad = true
			return
		}
		switch key {
		case "name":
			cur.name = text
		case "repository":
			cur.repo = text
		default:
			cur.spec, cur.start, cur.end = text, s, e
		}
	}
	off := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		lineOff := off
		off += len(line)
		line = strings.TrimRight(line, "\n")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && !strings.HasPrefix(line, "- ") {
			flush()
			inDeps, itemIndent = helmDepsRE.MatchString(line), -1
			continue
		}
		if !inDeps {
			continue
		}
		if m := helmDashRE.FindStringSubmatch(line); m != nil && (itemIndent < 0 || len(m[1]) <= itemIndent) {
			flush()
			itemIndent = len(m[1])
			keyCol = itemIndent + 1 + len(m[2])
			cur = &item{seen: map[string]bool{}}
			if strings.HasPrefix(m[3], "{") || strings.HasPrefix(m[3], "[") {
				cur.bad = true
				continue
			}
			field(line, lineOff, keyCol)
			continue
		}
		if cur != nil && indent == keyCol {
			field(line, lineOff, keyCol)
		}
	}
	flush()
	return out
}

func (h Helm) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanHelm(string(content)) {
		d := model.Dependency{File: p, Manager: h.Name(), Datasource: model.DatasourceHelm, Package: HelmPackage(e.repo, e.name), Version: e.spec}
		if !seen[d] { // Apply rewrites every entry naming the pair, so it is one dependency
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (h Helm) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a chart cannot be renamed", ErrUnsupported)
	}
	spec, ok := ParseHelmSpec(dep.Version)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	if _, ok := ParseHelmSpec(strings.TrimPrefix(newVersion, "v")); !ok {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	var hits []helmEntry
	for _, e := range scanHelm(string(content)) {
		if HelmPackage(e.repo, e.name) == dep.Package && e.spec == dep.Version {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start > hits[j].start })
	out := string(content)
	for _, e := range hits {
		out = out[:e.start] + spec.Text(newVersion) + out[e.end:]
	}
	var doc any
	if yaml.Unmarshal(content, &doc) == nil {
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			return nil, fmt.Errorf("helm: rewriting %s makes %s invalid YAML: %w", dep.Package, dep.File, err)
		}
	}
	return []byte(out), nil
}
