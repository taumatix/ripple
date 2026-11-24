package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"golang.org/x/mod/semver"
)

// Npm moves the semver ranges of a package.json's dependencies, devDependencies and
// optionalDependencies, keeping the range operator: `^1.2.3` follows to `^1.5.0`,
// `~1.2.3` to `~1.2.9`, `1.2.3` to `1.5.0`. peerDependencies are not touched (a peer
// range is a promise to users, not a pin).
//
// Only a range ripple can read with certainty is tracked: one to three numeric parts
// with no operator, `=`, `^` or `~`, and for a partial one an x-range tail (`1.2`,
// `^1.2`, `1.x`, `1.2.x`). A partial range keeps its precision when it moves (`1.2`
// follows 1.5.3 to `1.5`). A `>=` floor with `<`/`<=` upper bounds (`>=1.2.3 <2`) moves
// only to a version under them. A bare `*`, other comparators, hyphen ranges,
// prereleases, dist-tags, git/file/URL specs, `npm:` aliases and `workspace:` specs
// are left alone. A full range that is bumped is raised to the new version even when
// it already allowed it, because without a lockfile refresh that is the only change a
// PR can carry; a partial one that already names the new version is not touched.
//
// The file is edited in place: only the string of the range changes, so indentation,
// key order and everything else in the file stay byte for byte. Lockfiles are not
// read or written (see the engine's lockfile note).
//
// The dependency's Package is the npm name and its Version the range as written.
// Apply takes the new bare version ("1.5.0").
type Npm struct {
	Dir string
}

func (Npm) Name() string { return "npm" }

func (n Npm) Matches(p string) bool {
	if path.Base(p) != "package.json" || !config.InDirectory(n.Dir, p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "node_modules" {
			return false
		}
	}
	return true
}

var npmSections = map[string]bool{"dependencies": true, "devDependencies": true, "optionalDependencies": true}

var npmRangeRE = regexp.MustCompile(`^(>=[ \t]*|[\^~=]?)(\d+)((?:\.\d+){0,2})((?:\.[xX*]){0,2})$`)

// NpmRange is a single-comparator range ripple moves: an optional `^`, `~`, `=` or
// `>=` (with the spacing it was written with), one to three numeric parts, and for a
// partial one optionally the wildcard tail it was written with ("1.2", "^1.2", "1.x",
// "1.2.x", "~1.*", ">= 1.2"), and a `>=` floor may be followed by upper bounds
// (">=1.2.3 <2", ">=1 <=1.5"), which only limit the versions it moves to. `*`, a lone
// `<`, `<=` or `>`, other compound ranges and hyphen ranges are not NpmRanges; a `||`
// union is an NpmSpec.
type NpmRange struct {
	Op     string
	Parts  []int  // the numbers, as written
	Suffix string // the wildcard tail as written (".x", ".x.x", ".*"), or ""
	text   string // the whole range as written
	// rest is what follows a `>=` floor in a compound range, as written (" <2"); upper
	// are its bounds. Both are empty for a single comparator.
	rest  string
	upper []npmBound
}

// npmBound is an upper bound of a compound range: `<parts` or `<=parts`.
type npmBound struct {
	orEqual bool
	parts   []int
}

// npmUpperRE is the tail of a compound range: one or more `<` / `<=` comparators, each
// after whitespace, on a numeric version of one to three parts.
var npmUpperRE = regexp.MustCompile(`^(?:[ \t]+(<=?)[ \t]*(\d+(?:\.\d+){0,2}))+$`)
var npmUpperOneRE = regexp.MustCompile(`[ \t]+(<=?)[ \t]*(\d+(?:\.\d+){0,2})`)

// allows says whether v (x.y.z) is inside the bound.
func (b npmBound) allows(v [3]int) bool {
	cmp := func(a, c []int) int {
		for i := range a {
			if a[i] != c[i] {
				if a[i] < c[i] {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	if b.orEqual {
		return cmp(v[:len(b.parts)], b.parts) <= 0 // `<=1.5` takes all of 1.5.x
	}
	padded := append(append([]int(nil), b.parts...), make([]int, 3-len(b.parts))...)
	return cmp(v[:], padded) < 0
}

// Allows says whether version (a release, x.y.z) is inside the upper bounds of a
// compound range; a single comparator has none and allows every version.
func (r NpmRange) Allows(version string) bool {
	var v [3]int
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		v[i] = n
	}
	for _, b := range r.upper {
		if !b.allows(v) {
			return false
		}
	}
	return true
}

// ParseNpmRange reads spec as an NpmRange; false when it is something else.
func ParseNpmRange(spec string) (NpmRange, bool) {
	head, rest := spec, ""
	// A space inside `>= 1.2` belongs to the floor; one before a `<` starts the bounds.
	if j := npmUpperStart(spec); j >= 0 {
		head, rest = spec[:j], spec[j:]
	}
	m := npmRangeRE.FindStringSubmatch(head)
	if m == nil {
		return NpmRange{}, false
	}
	r := NpmRange{Op: m[1], Suffix: m[4], text: spec, rest: rest}
	if rest != "" {
		// Only a `>=` floor takes upper bounds: `^1.2 <1.5` and `1.2.3 <2` say something else.
		if !strings.HasPrefix(m[1], ">=") || !npmUpperRE.MatchString(rest) {
			return NpmRange{}, false
		}
		for _, bm := range npmUpperOneRE.FindAllStringSubmatch(rest, -1) {
			var nums []int
			for _, p := range strings.Split(bm[2], ".") {
				n, _ := strconv.Atoi(p)
				nums = append(nums, n)
			}
			r.upper = append(r.upper, npmBound{orEqual: bm[1] == "<=", parts: nums})
		}
	}
	nums := m[2] + m[3]
	for _, p := range strings.Split(nums, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return NpmRange{}, false
		}
		r.Parts = append(r.Parts, n)
	}
	// "1.2.3.x" has no wildcard room: the tail only fills missing parts.
	if len(r.Parts)+strings.Count(r.Suffix, ".") > 3 || (r.Suffix != "" && len(r.Parts) == 3) {
		return NpmRange{}, false
	}
	// ">=1.x" is legal but says nothing a floor does not: not read.
	if strings.HasPrefix(r.Op, ">") && r.Suffix != "" {
		return NpmRange{}, false
	}
	return r, true
}

// Floor is the lowest version the range allows, padded to x.y.z ("1.2" is "1.2.0").
func (r NpmRange) Floor() string {
	v := [3]int{}
	copy(v[:], r.Parts)
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// Text is the range that follows version at the precision written: operator and
// wildcard tail are kept ("1.2" and 1.5.3 give "1.5", "1.x" and 2.0.1 give "2.x").
func (r NpmRange) Text(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) > len(r.Parts) {
		parts = parts[:len(r.Parts)]
	}
	return r.Op + strings.Join(parts, ".") + r.Suffix + r.rest
}

// npmUpperStart is the index where the first `<` comparator's leading whitespace begins,
// or -1 when the spec has none.
func npmUpperStart(spec string) int {
	i := strings.IndexByte(spec, '<')
	if i < 0 {
		return -1
	}
	for i > 0 && (spec[i-1] == ' ' || spec[i-1] == '\t') {
		i--
	}
	if i == 0 {
		return -1
	}
	return i
}

// NpmSpec is a dependency's whole range string: one NpmRange, or a `||` union of
// them (`^1.2.3 || ^2.0.0`). A union is read only when every alternative is an
// NpmRange. Only its newest alternative (highest floor, the last on a tie) moves;
// the others are kept as written, because an older alternative states a range the
// owner still wants to allow.
type NpmSpec struct {
	Alts []NpmRange
	lead []string // whitespace before each alternative
	tail []string // whitespace after each alternative
}

// ParseNpmSpec reads spec as an NpmSpec; false when any part is something else.
func ParseNpmSpec(spec string) (NpmSpec, bool) {
	var s NpmSpec
	for _, alt := range strings.Split(spec, "||") {
		t := strings.TrimSpace(alt)
		r, ok := ParseNpmRange(t)
		if !ok {
			return NpmSpec{}, false
		}
		i := strings.Index(alt, t)
		s.Alts = append(s.Alts, r)
		s.lead = append(s.lead, alt[:i])
		s.tail = append(s.tail, alt[i+len(t):])
	}
	return s, true
}

// target is the index of the alternative that moves.
func (s NpmSpec) target() int {
	best := 0
	for i, a := range s.Alts {
		if semver.Compare("v"+a.Floor(), "v"+s.Alts[best].Floor()) >= 0 {
			best = i
		}
	}
	return best
}

// Moving is the alternative that follows a new version.
func (s NpmSpec) Moving() NpmRange { return s.Alts[s.target()] }

// Floor is the floor of the moving alternative.
func (s NpmSpec) Floor() string { return s.Moving().Floor() }

// Text is the spec that follows version: the moving alternative is rewritten, every
// other byte of the spec is kept.
func (s NpmSpec) Text(version string) string {
	t := s.target()
	var b strings.Builder
	for i, a := range s.Alts {
		if i > 0 {
			b.WriteString("||")
		}
		b.WriteString(s.lead[i])
		if i == t {
			b.WriteString(a.Text(version))
		} else {
			b.WriteString(a.raw())
		}
		b.WriteString(s.tail[i])
	}
	return b.String()
}

// raw is the range as it was written.
func (r NpmRange) raw() string { return r.text }

// NpmRangeFloor is the version a trackable spec names (the moving alternative's) and
// that alternative's operator; both "" when the spec is not one ripple moves.
func NpmRangeFloor(spec string) (string, string) {
	s, ok := ParseNpmSpec(spec)
	if !ok {
		return "", ""
	}
	return s.Floor(), s.Moving().Op
}

// npmEntry is one `"name": "range"` member of a dependency section.
type npmEntry struct {
	name, spec string
	// start and end delimit the range's string literal, quotes included.
	start, end int
}

// npmEntries lists the string-valued members of the three sections, in file order.
func npmEntries(content []byte) ([]npmEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("package.json is not a JSON object: %v", err)
	}
	var out []npmEntry
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		if !npmSections[key] {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if tok != json.Delim('{') { // null or a malformed section: nothing to read
			continue
		}
		for dec.More() {
			nt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, _ := nt.(string)
			before := int(dec.InputOffset())
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, err
			}
			after := int(dec.InputOffset())
			var spec string
			if json.Unmarshal(raw, &spec) != nil {
				continue
			}
			start := before + bytes.IndexByte(content[before:after], '"')
			out = append(out, npmEntry{name: name, spec: spec, start: start, end: after})
		}
		if _, err := dec.Token(); err != nil { // the section's closing brace
			return nil, err
		}
	}
	return out, nil
}

func (n Npm) Extract(p string, content []byte) ([]model.Dependency, error) {
	entries, err := npmEntries(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range entries {
		if v, _ := NpmRangeFloor(e.spec); v == "" {
			continue
		}
		d := model.Dependency{File: p, Manager: n.Name(), Datasource: model.DatasourceNpm, Package: e.name, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (n Npm) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: an npm package cannot be renamed", ErrUnsupported)
	}
	if !semver.IsValid("v"+newVersion) || semver.Prerelease("v"+newVersion) != "" || strings.Count(newVersion, ".") != 2 {
		return nil, fmt.Errorf("npm: %q is not a release version", newVersion)
	}
	entries, err := npmEntries(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", dep.File, err)
	}
	rng, ok := ParseNpmSpec(dep.Version)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a range ripple moves", ErrUnsupported, dep.Version)
	}
	out := append([]byte(nil), content...)
	changed := false
	for i := len(entries) - 1; i >= 0; i-- { // back to front keeps earlier offsets valid
		e := entries[i]
		if e.name != dep.Package || e.spec != dep.Version {
			continue
		}
		lit := `"` + rng.Text(newVersion) + `"`
		out = append(out[:e.start], append([]byte(lit), out[e.end:]...)...)
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	return out, nil
}
