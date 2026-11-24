package manager

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/pypi"
)

// Pip moves the version specifiers of Python dependencies, keeping the operator and
// the rest of the file byte for byte: `requests==2.31.0` follows to `requests==2.32.3`,
// `"click~=8.1.0"` to `"click~=8.1.7"`.
//
// Read: requirements*.txt (and *.txt under a directory named requirements), one
// requirement per line, and a pyproject.toml's `[project] dependencies`,
// `[project.optional-dependencies]` and `[dependency-groups]` arrays. A requirement is
// tracked when its specifier is a single clause `==X`, `~=X` or `>=X` with a plain
// release X (1 to 4 numeric segments; `~=` needs 2). Everything else is left alone:
// several clauses, `!=`, `<`, `===`, wildcards, prereleases, `name @ url`, `-e` and
// other option lines, and any requirement carrying `--hash` (the hashes would go stale).
// A requirements file that pip-compile or uv generated is a lock file and is not read.
//
// The dependency's Package is the PEP 503 normalized project name and its Version the
// clause as normalized text ("==2.31.0"). Apply takes the new bare version.
type Pip struct {
	Dir string
}

func (Pip) Name() string { return "pip" }

func (p Pip) Matches(f string) bool {
	if !config.InDirectory(p.Dir, f) {
		return false
	}
	for _, seg := range strings.Split(f, "/") {
		switch seg {
		case "node_modules", ".venv", "venv", "site-packages", ".tox":
			return false
		}
	}
	base := path.Base(f)
	switch {
	case base == "pyproject.toml" || base == "Pipfile":
		return true
	case !strings.HasSuffix(base, ".txt") && !strings.HasSuffix(base, ".in"):
		return false
	case strings.HasPrefix(base, "requirements") || strings.HasPrefix(base, "constraints"):
		return true
	}
	return path.Base(path.Dir(f)) == "requirements"
}

var (
	pipClauseRE = regexp.MustCompile(`^(==|~=|>=)\s*(\d+(?:\.\d+){0,3})$`)
	// pipPoetryRE is Poetry's caret and tilde requirement and its bare exact version.
	pipPoetryRE = regexp.MustCompile(`^(\^|~|)\s*(\d+(?:\.\d+){0,3})$`)
	// pipBoundRE is a clause that may accompany a `>=` floor: an upper bound or an exclusion.
	pipBoundRE = regexp.MustCompile(`^(?:<=|<|!=)\s*\d+(?:\.\d+)*(?:\.\*)?$`)
	pipReqRE   = regexp.MustCompile(`^\s*([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(?:\[[^\]]*\])?\s*` +
		`((?:===|==|~=|!=|<=|>=|<|>)\s*[^\s,;()]+(?:\s*,\s*(?:===|==|~=|!=|<=|>=|<|>)\s*[^\s,;()]+)*)?\s*(?:;.*)?$`)
)

// pipSpecParts splits a specifier on commas into trimmed clauses.
func pipSpecParts(spec string) []string {
	parts := strings.Split(spec, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// pipFloorPart is the index of the clause whose version ripple edits in a tracked
// specifier, or -1. Tracked: one clause `==X`, `~=X` or `>=X`; Poetry's `^X`, `~X` and
// bare `X`; or a `>=X` floor with upper bounds and exclusions (`>=1.20,<2,!=1.5`), where
// only the floor moves.
func pipFloorPart(parts []string) (idx int, floor, op string) {
	if len(parts) == 1 {
		if m := pipClauseRE.FindStringSubmatch(parts[0]); m != nil {
			if m[1] == "~=" && !strings.Contains(m[2], ".") {
				return -1, "", ""
			}
			return 0, m[2], m[1]
		}
		if m := pipPoetryRE.FindStringSubmatch(parts[0]); m != nil {
			return 0, m[2], m[1]
		}
		return -1, "", ""
	}
	idx = -1
	for i, p := range parts {
		switch m := pipClauseRE.FindStringSubmatch(p); {
		case m != nil && m[1] == ">=" && idx < 0:
			idx, floor, op = i, m[2], ">="
		case pipBoundRE.MatchString(p):
		default:
			return -1, "", ""
		}
	}
	return idx, floor, op
}

// PipSpecFloor is the version a trackable specifier names and its operator ("" for a
// bare Poetry version); both "" when the specifier is not one ripple moves.
func PipSpecFloor(spec string) (string, string) {
	idx, floor, op := pipFloorPart(pipSpecParts(spec))
	if idx < 0 {
		return "", ""
	}
	return floor, op
}

// PipSpecAllows is the test a new version must pass for the specifier: a `>=` floor with
// bounds keeps to them (the bounds are not edited, so a release outside them would make
// the PR a different change); every other tracked specifier accepts any newer release.
// nil accepts everything.
func PipSpecAllows(spec string) func(pypi.Version) bool {
	if !strings.Contains(spec, ",") {
		return nil
	}
	return func(v pypi.Version) bool {
		ok, known := pypi.Satisfies(spec, v)
		return ok && known
	}
}

// pipEntry is one tracked requirement.
type pipEntry struct {
	pkg, spec string
	// start and end delimit the version number inside the file.
	start, end int
}

// pipSpecEntry reads a specifier; base is the offset of s in the file.
func pipSpecEntry(pkg, s string, base int) (pipEntry, bool) {
	parts := pipSpecParts(s)
	idx, _, _ := pipFloorPart(parts)
	if idx < 0 {
		return pipEntry{}, false
	}
	off := 0
	for i := 0; i < idx; i++ {
		off += strings.IndexByte(s[off:], ',') + 1
	}
	rel := strings.IndexFunc(s[off:], func(r rune) bool { return r >= '0' && r <= '9' })
	end := rel + off
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	return pipEntry{pkg: pkg, spec: strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, strings.Join(parts, ",")), start: base + off + rel, end: base + end}, true
}

// parsePipReq reads one PEP 508 requirement; base is the offset of s in the file.
func parsePipReq(s string, base int) (pipEntry, bool) {
	m := pipReqRE.FindStringSubmatchIndex(s)
	if m == nil || m[4] < 0 {
		return pipEntry{}, false
	}
	return pipSpecEntry(pypi.NormalizeName(s[m[2]:m[3]]), s[m[4]:m[5]], base+m[4])
}

var pipOptionCut = regexp.MustCompile(`\s--?[A-Za-z]`)

// PipGenerated reports a requirements file a resolver wrote (a lock file).
func PipGenerated(content []byte) bool { return pipGenerated(string(content)) }

// pipGenerated reports a requirements file a resolver wrote: it is a lock file.
func pipGenerated(src string) bool {
	for _, l := range strings.SplitN(src, "\n", 40) {
		l = strings.ToLower(l)
		if strings.HasPrefix(strings.TrimSpace(l), "#") && (strings.Contains(l, "autogenerated by pip-compile") || strings.Contains(l, "autogenerated by uv")) {
			return true
		}
	}
	return false
}

func stripRequirementsComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func pipRequirementsEntries(src string) []pipEntry {
	if pipGenerated(src) {
		return nil
	}
	var out []pipEntry
	off := 0
	for off < len(src) {
		start := off
		var group []string
		for {
			nl := strings.IndexByte(src[off:], '\n')
			end := len(src)
			if nl >= 0 {
				end = off + nl
			}
			line := strings.TrimSuffix(src[off:end], "\r")
			group = append(group, line)
			off = end + 1
			if !strings.HasSuffix(line, `\`) || off >= len(src) {
				break
			}
		}
		hashed := false
		for _, l := range group {
			if strings.Contains(l, "--hash") {
				hashed = true
			}
		}
		text := strings.TrimSuffix(stripRequirementsComment(group[0]), `\`)
		if loc := pipOptionCut.FindStringIndex(text); loc != nil {
			text = text[:loc[0]]
		}
		if hashed || strings.HasPrefix(strings.TrimSpace(text), "-") || strings.TrimSpace(text) == "" {
			continue
		}
		if e, ok := parsePipReq(text, start); ok {
			out = append(out, e)
		}
	}
	return out
}

// tomlArray reads the array whose '[' is at src[i]: the plain strings at its top level
// (with the offset of their first character) and the offset just past its ']'.
func tomlArray(src string, i int) (strs []struct {
	s     string
	start int
}, end int) {
	depth := 0
	for ; i < len(src); i++ {
		switch c := src[i]; c {
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return strs, i + 1
			}
		case '"', '\'':
			if strings.HasPrefix(src[i:], strings.Repeat(string(c), 3)) {
				j := strings.Index(src[i+3:], strings.Repeat(string(c), 3))
				if j < 0 {
					return strs, len(src)
				}
				i += 3 + j + 2
				continue
			}
			j, plain := i+1, true
			for ; j < len(src) && src[j] != c && src[j] != '\n'; j++ {
				if c == '"' && src[j] == '\\' {
					plain = false
					j++
				}
			}
			if depth == 1 && plain && j < len(src) && src[j] == c {
				strs = append(strs, struct {
					s     string
					start int
				}{src[i+1 : j], i + 1})
			}
			i = j
		}
	}
	return strs, len(src)
}

var pipInlineVersionRE = regexp.MustCompile(`[{,]\s*version\s*=\s*("[^"\\]*"|'[^']*')`)
var pipInlineSourceRE = regexp.MustCompile(`[{,]\s*(?:git|path|url|file|develop)\s*=`)

// pipDepTable says what a TOML table holds: "array" tables list PEP 508 strings under
// the keys named, "map" tables are `name = spec` dependency tables (Poetry, Pipfile),
// "detail" is one dependency's own table (`[tool.poetry.dependencies.name]`).
func pipDepTable(table string, pipfile bool) (kind, name string) {
	if pipfile {
		if table == "packages" || table == "dev-packages" {
			return "map", ""
		}
		return "", ""
	}
	const poetry = "tool.poetry."
	switch {
	case table == "project" || table == "project.optional-dependencies" || table == "dependency-groups" || table == "build-system":
		return "array", ""
	case table == poetry+"dependencies" || table == poetry+"dev-dependencies":
		return "map", ""
	case strings.HasPrefix(table, poetry+"group.") && strings.HasSuffix(table, ".dependencies"):
		return "map", ""
	case strings.HasPrefix(table, poetry+"dependencies."):
		return "detail", tomlUnquote(strings.TrimPrefix(table, poetry+"dependencies."))
	case strings.HasPrefix(table, poetry+"dev-dependencies."):
		return "detail", tomlUnquote(strings.TrimPrefix(table, poetry+"dev-dependencies."))
	case strings.HasPrefix(table, poetry+"group."):
		if rest := strings.TrimPrefix(table, poetry+"group."); strings.Contains(rest, ".dependencies.") {
			return "detail", tomlUnquote(rest[strings.Index(rest, ".dependencies.")+len(".dependencies."):])
		}
	}
	return "", ""
}

// pipQuoted reads the plain quoted string at the start of s: its text and offset.
func pipQuoted(s string) (text string, at int, ok bool) {
	if s == "" || (s[0] != '"' && s[0] != '\'') {
		return "", 0, false
	}
	j := strings.IndexByte(s[1:], s[0])
	if j < 0 || strings.ContainsAny(s[1:1+j], "\\\n") {
		return "", 0, false
	}
	return s[1 : 1+j], 1, true
}

func pipPyprojectEntries(src string) []pipEntry { return pipTomlEntries(src, false) }

func pipTomlEntries(src string, pipfile bool) []pipEntry {
	var out []pipEntry
	table := ""
	for off := 0; off < len(src); {
		nl := strings.IndexByte(src[off:], '\n')
		lineEnd := len(src)
		if nl >= 0 {
			lineEnd = off + nl
		}
		line, lineStart := src[off:lineEnd], off
		off = lineEnd + 1
		trim := strings.TrimSpace(tomlStripComment(line))
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			table = ""
			if !strings.HasPrefix(trim, "[[") && strings.HasSuffix(trim, "]") {
				table = strings.TrimSpace(trim[1 : len(trim)-1])
			}
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kind, detail := pipDepTable(table, pipfile)
		key = tomlUnquote(key)
		valOff := lineStart + strings.Index(line, "=") + 1
		value := strings.TrimLeft(rest, " \t")
		valOff += len(rest) - len(value)
		switch {
		case kind == "array" && strings.HasPrefix(value, "["):
			if !(table == "project" && key == "dependencies") && table != "project.optional-dependencies" && table != "dependency-groups" && !(table == "build-system" && key == "requires") {
				continue
			}
			strs, end := tomlArray(src, valOff)
			for _, s := range strs {
				if e, ok := parsePipReq(s.s, s.start); ok {
					out = append(out, e)
				}
			}
			if end > off {
				off = end
			}
		case kind == "map" && key != "python" && pypi.ValidName(key):
			if text, at, ok := pipQuoted(value); ok {
				if e, ok := pipSpecEntry(pypi.NormalizeName(key), text, valOff+at); ok {
					out = append(out, e)
				}
			} else if strings.HasPrefix(value, "{") && !pipInlineSourceRE.MatchString(value) {
				if m := pipInlineVersionRE.FindStringSubmatchIndex(value); m != nil {
					if text, at, ok := pipQuoted(value[m[2]:m[3]]); ok {
						if e, ok := pipSpecEntry(pypi.NormalizeName(key), text, valOff+m[2]+at); ok {
							out = append(out, e)
						}
					}
				}
			}
		case kind == "detail" && key == "version" && pypi.ValidName(detail):
			if text, at, ok := pipQuoted(value); ok {
				if e, ok := pipSpecEntry(pypi.NormalizeName(detail), text, valOff+at); ok {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

func pipEntries(file string, content []byte) []pipEntry {
	switch path.Base(file) {
	case "pyproject.toml":
		return pipPyprojectEntries(string(content))
	case "Pipfile":
		return pipTomlEntries(string(content), true)
	}
	return pipRequirementsEntries(string(content))
}

func (p Pip) Extract(f string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range pipEntries(f, content) {
		d := model.Dependency{File: f, Manager: p.Name(), Datasource: model.DatasourcePip, Package: e.pkg, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (p Pip) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a project cannot be renamed", ErrUnsupported)
	}
	if v, ok := pypi.Parse(newVersion); !ok || !v.IsRelease() || strings.ContainsAny(newVersion, "+!") {
		return nil, fmt.Errorf("pip: %q is not a release version", newVersion)
	}
	entries := pipEntries(dep.File, content)
	out := append([]byte(nil), content...)
	changed := false
	for i := len(entries) - 1; i >= 0; i-- { // back to front keeps earlier offsets valid
		e := entries[i]
		if e.pkg != dep.Package || e.spec != dep.Version {
			continue
		}
		out = append(out[:e.start], append([]byte(newVersion), out[e.end:]...)...)
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("%s: %s%s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	return out, nil
}

var (
	pipReqOptionRE  = regexp.MustCompile(`^(?:-i\s*\S|-f\s*\S|--(?:index-url|extra-index-url|find-links)\b|--no-index\b)`)
	pipConfKeyRE    = regexp.MustCompile(`(?m)^\s*(?:index-url|extra-index-url|find-links)\s*[=:]`)
	pipPyprojectKey = regexp.MustCompile(`(?m)^\s*(?:index-url|extra-index-url|find-links|index|default-index)\s*=`)
	pipPyprojectHdr = regexp.MustCompile(`(?m)^\s*\[\[?tool\.(?:poetry\.source|uv\.index|pdm\.source|hatch\.index)\b`)
)

// PipIndexOverride reports whether a file, by its name, points pip (or a tool that
// reads the same files) at an index other than PyPI: a requirements file with an
// index option, a pip.conf or uv.toml naming one, or a pyproject.toml declaring a
// source. Dependencies resolved through one are not PyPI's: the same name on PyPI
// could be someone else's package (dependency confusion).
func PipIndexOverride(name string, content []byte) bool {
	switch base := path.Base(name); {
	case base == "pyproject.toml":
		return pipPyprojectHdr.Match(content) || pipPyprojectKey.Match(content)
	case base == "Pipfile":
		return pipfileOverride(content)
	case base == "pip.conf" || base == "pip.ini" || base == "uv.toml":
		return pipConfKeyRE.Match(content) || pipPyprojectKey.Match(content)
	case strings.HasSuffix(base, ".txt"):
		for _, l := range strings.Split(string(content), "\n") {
			if pipReqOptionRE.MatchString(strings.TrimSpace(stripRequirementsComment(l))) {
				return true
			}
		}
	}
	return false
}

var pipfileURLRE = regexp.MustCompile(`(?m)^\s*url\s*=\s*["']([^"']*)["']`)

// pipfileOverride reports a [[source]] other than pypi.org's. A Pipfile names the
// default index explicitly, so that one is not an override.
func pipfileOverride(content []byte) bool {
	for _, m := range pipfileURLRE.FindAllSubmatch(content, -1) {
		if !pipIsPyPIURL(string(m[1])) {
			return true
		}
	}
	return false
}

// pipIsPyPIURL reports the pypi.org index, whichever of its spellings.
func pipIsPyPIURL(u string) bool {
	u = strings.TrimRight(strings.ToLower(strings.TrimSpace(u)), "/")
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return u == "pypi.org/simple" || u == "pypi.org" || u == "pypi.python.org/simple"
}
