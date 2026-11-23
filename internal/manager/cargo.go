package manager

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"golang.org/x/mod/semver"
)

// Cargo moves the version requirements of a Cargo.toml's dependencies, keeping the
// operator: `serde = "1.0.100"` follows to `"1.0.200"`, `"~1.2.3"` to `"~1.2.9"`.
//
// Read: [dependencies], [dev-dependencies], [build-dependencies], their
// [target.<cfg>.*] forms and [workspace.dependencies]; each as `name = "req"`, as a
// one-line inline table `name = { version = "req", ... }`, or as a
// `[dependencies.name]` table with a `version` key. A `package = "real"` key names the
// crate the index knows.
//
// Only a requirement ripple can read with certainty is tracked: a full x.y.z with no
// operator (cargo's caret default), `^`, `~` or `=`. Wildcards, comparators, partial
// versions ("1.2"), prereleases, multi-part requirements and multi-line inline tables
// are left alone, and so is every dependency with a `path`, `git` or `registry` key or
// `workspace = true` (it has no version of its own). A bumped requirement is raised to
// the new version even when it already allowed it, because without a Cargo.lock
// refresh that is the only change a PR can carry.
//
// The file is edited in place: only the string of the requirement changes. Cargo.lock
// is neither read nor written (see the engine's lockfile note).
//
// The dependency's Package is the crate name and its Version the requirement as
// written. Apply takes the new bare version ("1.0.200").
type Cargo struct {
	Dir string
}

func (Cargo) Name() string { return "cargo" }

func (c Cargo) Matches(p string) bool {
	if path.Base(p) != "Cargo.toml" || !config.InDirectory(c.Dir, p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "target" {
			return false
		}
	}
	return true
}

var cargoReqRE = regexp.MustCompile(`^([\^~=]?)(\d+\.\d+\.\d+)$`)

// CargoReqFloor is the version a trackable requirement names and its operator; both
// "" when the requirement is not one ripple moves.
func CargoReqFloor(spec string) (string, string) {
	m := cargoReqRE.FindStringSubmatch(spec)
	if m == nil {
		return "", ""
	}
	return m[2], m[1]
}

// cargoEntry is one tracked requirement.
type cargoEntry struct {
	crate, spec string
	// start and end delimit the requirement's string literal, quotes included.
	start, end int
}

// cargoField is one `key = value` of an inline or named table.
type cargoField struct {
	str        string
	isStr      bool
	start, end int // the value, quotes included for a string
}

// cargoDepTable classifies a table header's segments: sect says it is a dependency
// table, name is set for `[dependencies.name]`.
func cargoDepTable(segs []string) (sect bool, name string) {
	if len(segs) > 0 && segs[0] == "workspace" {
		segs = segs[1:]
	} else if len(segs) > 1 && segs[0] == "target" {
		segs = segs[2:]
	}
	if len(segs) == 0 {
		return false, ""
	}
	switch segs[0] {
	case "dependencies", "dev-dependencies", "build-dependencies":
	default:
		return false, ""
	}
	switch len(segs) {
	case 1:
		return true, ""
	case 2:
		return true, segs[1]
	}
	return false, ""
}

// tomlSplit splits s on sep outside quotes and brackets.
func tomlSplit(s string, sep byte) []string {
	var out []string
	var quote byte
	depth, from := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == sep && depth == 0:
			out = append(out, s[from:i])
			from = i + 1
		}
	}
	return append(out, s[from:])
}

func tomlUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// tomlStripComment cuts a trailing # comment that is outside quotes.
func tomlStripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}

// tomlValue parses a scalar's text at content[start:end] (trimmed) as a field.
func tomlValue(content string, start, end int) cargoField {
	for start < end && (content[start] == ' ' || content[start] == '\t') {
		start++
	}
	for end > start && (content[end-1] == ' ' || content[end-1] == '\t' || content[end-1] == '\r') {
		end--
	}
	f := cargoField{start: start, end: end}
	if end-start >= 2 && (content[start] == '"' || content[start] == '\'') && content[end-1] == content[start] && !strings.ContainsRune(content[start+1:end-1], rune(content[start])) && !strings.Contains(content[start+1:end-1], `\`) {
		f.isStr, f.str = true, content[start+1:end-1]
	}
	return f
}

// cargoEntries lists the requirements ripple can read, in file order.
func cargoEntries(content []byte) ([]cargoEntry, error) {
	src := string(content)
	var out []cargoEntry
	var (
		inSect   bool
		named    string // crate key of a [dependencies.name] table
		table    map[string]cargoField
		finishCt = func() {
			if named != "" {
				out = appendCargo(out, named, table)
			}
			named, table = "", nil
		}
	)
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
			finishCt()
			inSect = false
			if strings.HasPrefix(trim, "[[") || !strings.HasSuffix(trim, "]") {
				continue
			}
			var segs []string
			for _, s := range tomlSplit(trim[1:len(trim)-1], '.') {
				segs = append(segs, tomlUnquote(s))
			}
			sect, name := cargoDepTable(segs)
			if sect && name == "" {
				inSect = true
			} else if sect {
				named, table = name, map[string]cargoField{}
			}
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = tomlUnquote(key)
		eq := lineStart + strings.IndexByte(line, '=') + 1
		valEnd := lineStart + len(tomlStripComment(line))
		switch {
		case named != "":
			table[key] = tomlValue(src, eq, valEnd)
		case inSect:
			if strings.Contains(key, ".") {
				continue // dotted keys (`foo.version = ...`) are not read
			}
			v := strings.TrimSpace(tomlStripComment(rest))
			if strings.HasPrefix(v, "{") {
				if !strings.HasSuffix(v, "}") {
					continue // a multi-line inline table: not read
				}
				fields := map[string]cargoField{}
				bodyStart := eq + strings.Index(src[eq:valEnd], "{") + 1
				bodyEnd := eq + strings.LastIndex(src[eq:valEnd], "}")
				body := src[bodyStart:bodyEnd]
				pos := bodyStart
				for _, part := range tomlSplit(body, ',') {
					if k, _, ok := strings.Cut(part, "="); ok {
						fields[tomlUnquote(k)] = tomlValue(src, pos+strings.IndexByte(part, '=')+1, pos+len(part))
					}
					pos += len(part) + 1
				}
				out = appendCargo(out, key, fields)
			} else {
				out = appendCargo(out, key, map[string]cargoField{"version": tomlValue(src, eq, valEnd)})
			}
		}
	}
	finishCt()
	return out, nil
}

// appendCargo adds the entry a dependency's fields describe, if it is trackable.
func appendCargo(out []cargoEntry, key string, f map[string]cargoField) []cargoEntry {
	for _, k := range []string{"path", "git", "registry", "workspace"} {
		if _, ok := f[k]; ok {
			return out
		}
	}
	v, ok := f["version"]
	if !ok || !v.isStr {
		return out
	}
	crate := key
	if p, ok := f["package"]; ok {
		if !p.isStr {
			return out
		}
		crate = p.str
	}
	return append(out, cargoEntry{crate: crate, spec: v.str, start: v.start, end: v.end})
}

func (c Cargo) Extract(p string, content []byte) ([]model.Dependency, error) {
	entries, err := cargoEntries(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range entries {
		if v, _ := CargoReqFloor(e.spec); v == "" {
			continue
		}
		d := model.Dependency{File: p, Manager: c.Name(), Datasource: model.DatasourceCargo, Package: e.crate, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (c Cargo) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a crate cannot be renamed", ErrUnsupported)
	}
	if !semver.IsValid("v"+newVersion) || semver.Prerelease("v"+newVersion) != "" || strings.Count(newVersion, ".") != 2 {
		return nil, fmt.Errorf("cargo: %q is not a release version", newVersion)
	}
	entries, err := cargoEntries(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dep.File, err)
	}
	_, op := CargoReqFloor(dep.Version)
	out := append([]byte(nil), content...)
	changed := false
	for i := len(entries) - 1; i >= 0; i-- { // back to front keeps earlier offsets valid
		e := entries[i]
		if e.crate != dep.Package || e.spec != dep.Version {
			continue
		}
		q := string(content[e.start])
		out = append(out[:e.start], append([]byte(q+op+newVersion+q), out[e.end:]...)...)
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	return out, nil
}
