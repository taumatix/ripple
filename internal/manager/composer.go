package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/packagist"
)

// Composer moves the package versions written in the `require` and `require-dev` objects of
// a composer.json: `"vendor/name": "1.2.3"`, `"^1.2.3"`, `"~1.2.3"` or `"=1.2.3"` (with an
// optional `v`), the version's digits only, never re-serialised. The datasource is
// Packagist, so a file with any `repositories` entry (a private or VCS source, or Packagist
// switched off) is left alone entirely.
//
// Left alone besides: platform requirements (php, ext-*, lib-*), a range, several
// constraints (`">=1.0 <2.0"`, `"^1.0 || ^2.0"`), a wildcard, a stability flag, a `dev-`
// branch, a bare-major `"1"`, a four-segment version and a string with a JSON escape in it.
//
// A two-segment `"^1.2"`, `"~1.2"`, `"~0.3"` or `"^0.3"` is read too: it already allows every
// later release of its line, so only a release beyond the line (a later major, or for `^0.Y` a
// later minor) is an update, and the text becomes the new release's first two segments (`"^2.1"`).
// A one-segment `"^1"`, `"~1"` or `"^0"` is read the same way, moves on a later major only
// and becomes the new major alone (`"^2"`).
type Composer struct {
	Dir string
}

func (Composer) Name() string { return "composer" }

func (c Composer) Matches(p string) bool {
	return path.Base(p) == "composer.json" && config.InDirectory(c.Dir, p)
}

// ComposerSpec is a package version ripple can move: "[^|~|=][v]X.Y.Z".
type ComposerSpec struct {
	Prefix  string // "", "^", "~", "=", with an optional "v", as written
	Version string
}

var composerSpecRE = regexp.MustCompile(`^((?:[\^~=][ \t]*)?v?)(?:([0-9]+\.[0-9]+\.[0-9]+)|([0-9]+\.[0-9]+)|([0-9]+))$`)

// ParseComposerSpec reads a package's version text. A two-segment text is read for "^X.Y" and
// "~X.Y", a one-segment text for "^X" and "~X" only. Composer 2 (run in CI, TestRealtoolComposerConstraintMeaning)
// locks "~0.3" to mockery 0.9.11, "^1" and "~1" to monolog 1.27.1 and "^0" to mockery 0.9.11:
// "~0.3" is >=0.3 <1.0, and a bare major X is >=X <X+1 even for 0.
func ParseComposerSpec(s string) (ComposerSpec, bool) {
	m := composerSpecRE.FindStringSubmatch(s)
	if m == nil {
		return ComposerSpec{}, false
	}
	if m[2] != "" {
		return ComposerSpec{Prefix: m[1], Version: m[2]}, true
	}
	if !strings.HasPrefix(m[1], "^") && !strings.HasPrefix(m[1], "~") {
		return ComposerSpec{}, false
	}
	return ComposerSpec{Prefix: m[1], Version: m[3] + m[4]}, true
}

// TwoSegment reports a "^X.Y" or "~X.Y" constraint, which already allows every later release
// of its line, so only a release beyond the line is an update.
func (s ComposerSpec) TwoSegment() bool { return strings.Count(s.Version, ".") == 1 }

// Partial reports a constraint of fewer than three segments ("^1.2", "~0.3", "^1"): it already
// allows every later release inside its major (inside its minor for "^0.Y"), so only a release
// beyond that is an update.
func (s ComposerSpec) Partial() bool { return strings.Count(s.Version, ".") < 2 }

// Floor is the version padded to three segments ("1" is "1.0.0"), the release the constraint starts at.
func (s ComposerSpec) Floor() string {
	v := s.Version
	for strings.Count(v, ".") < 2 {
		v += ".0"
	}
	return v
}

func atoiParts(v string) (major, minor int) {
	a, rest, _ := strings.Cut(v, ".")
	b, _, _ := strings.Cut(rest, ".")
	major, _ = strconv.Atoi(a)
	minor, _ = strconv.Atoi(b)
	return major, minor
}

// Moves reports whether release X.Y.Z (a leading "v" allowed) is outside a two-segment
// constraint and newer than it: of a later major, or for "^0.Y" of a later minor.
func (s ComposerSpec) Moves(release string) bool {
	cm, cn := atoiParts(s.Version)
	rm, rn := atoiParts(strings.TrimPrefix(release, "v"))
	if rm != cm {
		return rm > cm
	}
	return s.TwoSegment() && cm == 0 && strings.Contains(s.Prefix, "^") && rn > cn
}

// partialTarget is the text a partial constraint takes to follow release nv: its first two
// segments (the major alone for "^1"), and false when the constraint already allows nv or nv is older.
func (s ComposerSpec) partialTarget(nv string) (string, bool) {
	major, rest, _ := strings.Cut(nv, ".")
	if !s.TwoSegment() {
		return major, s.Moves(nv)
	}
	minor, _, _ := strings.Cut(rest, ".")
	return major + "." + minor, s.Moves(nv)
}

type composerEntry struct {
	name, spec string
	start, end int // the version's digits in the file
}

// scanComposerJSON lists the readable requirements, or nothing when the file names
// repositories or is not a JSON object. Offsets come from the decoder, not from a re-encode.
func scanComposerJSON(content []byte) []composerEntry {
	dec := json.NewDecoder(bytes.NewReader(content))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var out []composerEntry
	for dec.More() {
		kt, err := dec.Token()
		key, _ := kt.(string)
		if err != nil {
			return nil
		}
		switch key {
		case "repositories":
			var raw json.RawMessage
			if dec.Decode(&raw) != nil {
				return nil
			}
			if s := strings.TrimSpace(string(raw)); s != "{}" && s != "[]" && s != "null" {
				return nil
			}
		case "require", "require-dev":
			t, err := dec.Token()
			if err != nil {
				return nil
			}
			if t != json.Delim('{') {
				continue // null or another type: nothing read, nothing consumed
			}
			for dec.More() {
				nt, err := dec.Token()
				name, _ := nt.(string)
				if err != nil {
					return nil
				}
				vt, err := dec.Token()
				if err != nil {
					return nil
				}
				spec, isStr := vt.(string)
				if !isStr {
					if d, ok := vt.(json.Delim); ok && (d == '{' || d == '[') {
						if skipComposerValue(dec) != nil {
							return nil
						}
					}
					continue
				}
				end := int(dec.InputOffset())
				lit := `"` + spec + `"`
				if end < len(lit) || string(content[end-len(lit):end]) != lit {
					continue // an escape in the string: not edited byte for byte
				}
				s, ok := ParseComposerSpec(spec)
				if !ok || !packagist.ValidName(name) {
					continue
				}
				start := end - 1 - len(spec) + len(s.Prefix)
				out = append(out, composerEntry{name, spec, start, start + len(s.Version)})
			}
			if _, err := dec.Token(); err != nil { // the closing brace
				return nil
			}
		default:
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil
			}
		}
	}
	return out
}

// skipComposerValue consumes the rest of an object or array whose opening delimiter was read.
func skipComposerValue(dec *json.Decoder) error {
	for depth := 1; depth > 0; {
		t, err := dec.Token()
		if err == io.EOF {
			return err
		}
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

func (c Composer) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanComposerJSON(content) {
		d := model.Dependency{File: p, Manager: c.Name(), Datasource: model.DatasourcePackagist, Package: e.name, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (c Composer) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a package cannot be renamed", ErrUnsupported)
	}
	cur, ok := ParseComposerSpec(dep.Version)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if r, ok := ParseComposerSpec(nv); !ok || r.Partial() {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	text := nv
	if cur.Partial() {
		var later bool
		if text, later = cur.partialTarget(nv); !later {
			return nil, fmt.Errorf("%w: %q already allows %s", ErrUnsupported, dep.Version, nv)
		}
	}
	var hits []composerEntry
	for _, e := range scanComposerJSON(content) {
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
		out = out[:e.start] + text + out[e.end:]
	}
	return []byte(out), nil
}
