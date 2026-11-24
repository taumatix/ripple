package manager

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"golang.org/x/mod/semver"
)

// Cargo moves the version requirements of a Cargo.toml's dependencies, keeping the
// operator: `serde = "1.0.100"` follows to `"1.0.200"`, `"~1.2.3"` to `"~1.2.9"`.
//
// Read: [dependencies], [dev-dependencies], [build-dependencies], their
// [target.<cfg>.*] forms and [workspace.dependencies], written in any way TOML allows:
// `name = "req"`, an inline table `name = { version = "req", ... }` (on one line or
// several), a dotted key `name.version = "req"`, or a `[dependencies.name]` table with a
// `version` key. A `package = "real"` key names the crate the index knows.
//
// A requirement ripple can read with certainty is tracked (see CargoReq): one to three
// numeric parts with no operator (cargo's caret default), `^`, `~`, `=` or `>=`
// ("1", "1.2", "1.2.3"), optionally followed by upper bounds (`>=1.2, <2`), and a
// trailing wildcard (`1.*`, `1.2.*`). A partial requirement keeps its precision when it
// moves ("1.2" follows 1.5.3 to "1.5"). Prereleases, `>`, `<` alone, a bare `*` and a
// requirement with more than one lower bound are left alone, and so is every dependency
// with a `path`, `git` or `registry` key or `workspace = true` (it has no version of its
// own). A bumped requirement is raised to the new version even when it already allowed
// it, because without a Cargo.lock refresh that is the only change a PR can carry (a
// partial requirement that already names the new version at its precision is not
// touched; a wildcard moves only to a version it does not already match).
//
// The file is read with a TOML scanner that keeps byte offsets and edited in place: only
// the string of the requirement changes, so comments, layout and key order stay. A file
// that is not valid TOML is not read. Cargo.lock is neither read nor written (see the
// engine's lockfile note).
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

var (
	cargoPrimaryRE = regexp.MustCompile(`^(\^|~|=|>=)?\s*(\d+)(?:\.(\d+))?(?:\.(\d+))?$`)
	cargoWildRE    = regexp.MustCompile(`^(\d+)(?:\.(\d+))?\.\*$`)
	cargoUpperRE   = regexp.MustCompile(`^(<=|<)\s*(\d+)(?:\.(\d+))?(?:\.(\d+))?$`)
)

// CargoReq is a requirement ripple moves: one lower bound, written as an optional `^`,
// `~`, `=` or `>=` and one to three numeric parts ("1", "1.2", "1.2.3"), or as a
// wildcard with one or two parts before it ("1.*", "1.2.*"); a lower bound may be
// followed by upper bounds ("<2", "<=1.5"), which only limit the versions it moves to.
// A bare `*`, a `>` or `<` on its own and several lower bounds are not CargoReqs.
type CargoReq struct {
	Op    string // "", "^", "~", "=" or ">="
	Parts []int  // 1 to 3 numbers of the lower bound, as written
	// Wild says the lower bound is a wildcard: Parts are the numbers before the `.*`.
	Wild bool

	raw    string // the requirement as written
	vs, ve int    // the span of raw that holds the lower bound's version text
	upper  []cargoBound
}

// cargoBound is an upper bound: `<parts` or `<=parts`.
type cargoBound struct {
	orEqual bool
	parts   []int
}

func cargoNums(m []string) ([]int, bool) {
	var out []int
	for _, p := range m {
		if p == "" {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// ParseCargoReq reads spec as a CargoReq; false when it is something else.
func ParseCargoReq(spec string) (CargoReq, bool) {
	r := CargoReq{raw: spec}
	var primary bool
	off := 0
	for _, comp := range strings.Split(spec, ",") {
		start := off
		off += len(comp) + 1
		lead := len(comp) - len(strings.TrimLeft(comp, " \t"))
		comp = strings.TrimSpace(comp)
		if comp == "" {
			return CargoReq{}, false
		}
		if m := cargoUpperRE.FindStringSubmatch(comp); m != nil {
			nums, ok := cargoNums(m[2:])
			if !ok {
				return CargoReq{}, false
			}
			r.upper = append(r.upper, cargoBound{orEqual: m[1] == "<=", parts: nums})
			continue
		}
		if primary {
			return CargoReq{}, false // a second lower bound
		}
		primary = true
		start += lead
		if m := cargoWildRE.FindStringSubmatch(comp); m != nil {
			nums, ok := cargoNums(m[1:])
			if !ok {
				return CargoReq{}, false
			}
			r.Parts, r.Wild = nums, true
			r.vs, r.ve = start, start+len(comp)
			continue
		}
		m := cargoPrimaryRE.FindStringSubmatch(comp)
		if m == nil {
			return CargoReq{}, false
		}
		nums, ok := cargoNums(m[2:])
		if !ok {
			return CargoReq{}, false
		}
		r.Op, r.Parts = m[1], nums
		rest := comp[len(m[1]):]
		r.vs, r.ve = start+len(comp)-len(strings.TrimLeft(rest, " \t")), start+len(comp)
	}
	if !primary || (r.Wild && len(r.upper) > 0) {
		return CargoReq{}, false
	}
	return r, true
}

// Floor is the lowest version the requirement allows, padded to x.y.z ("1.2" is "1.2.0").
func (r CargoReq) Floor() string {
	v := [3]int{}
	copy(v[:], r.Parts)
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// Text is the requirement that follows version at the precision written: the operator,
// spacing and upper bounds are kept and a partial requirement stays partial ("1.2" and
// 1.5.3 give "1.5", "1.*" and 2.4.0 give "2.*").
func (r CargoReq) Text(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) > len(r.Parts) {
		parts = parts[:len(r.Parts)]
	}
	moved := strings.Join(parts, ".")
	if r.Wild {
		moved += ".*"
	}
	return r.raw[:r.vs] + moved + r.raw[r.ve:]
}

// Applicable says whether version, a release, is something the requirement can move to:
// newer than its floor, inside its upper bounds, not already the version at the
// precision written ("1.2" for 1.2.9), and for a wildcard outside what it matches
// already ("1.*" moves only to 2.0.0 or later).
func (r CargoReq) Applicable(version string) bool {
	v, ok := cargoVersion(version)
	if !ok {
		return false
	}
	floor, _ := cargoVersion(r.Floor())
	if cargoCompare(v[:], floor[:]) <= 0 {
		return false
	}
	if r.Wild {
		return cargoCompare(v[:len(r.Parts)], r.Parts) != 0
	}
	for _, b := range r.upper {
		if !b.allows(v) {
			return false
		}
	}
	return len(r.Parts) == 3 || r.Text(version) != r.raw
}

func (b cargoBound) allows(v [3]int) bool {
	c := cargoCompare(v[:len(b.parts)], b.parts)
	switch {
	case b.orEqual:
		return c <= 0 // `<=1.5` takes all of 1.5.x
	case len(b.parts) < 3:
		padded := append(append([]int(nil), b.parts...), make([]int, 3-len(b.parts))...)
		return cargoCompare(v[:], padded) < 0
	}
	return c < 0
}

func cargoVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func cargoCompare(a, b []int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Breaking is the position (0 major, 1 minor, 2 patch) of the leftmost nonzero number
// written in the requirement, which is where cargo's caret rule puts compatibility: 1.2.3,
// 1.2 and 1 break on the major, 0.2.3 and 0.2 on the minor, 0.0.3 on the patch. All zeros
// break on the last part written. It is a property of the version, so the operator does
// not change it: ~0.2.3 and 0.2.3 both treat 0.3.0 as incompatible.
func (r CargoReq) Breaking() int {
	for i, n := range r.Parts {
		if n != 0 {
			return i
		}
	}
	return len(r.Parts) - 1
}

// CargoReqFloor is the version a trackable requirement names and its operator; both
// "" when the requirement is not one ripple moves.
func CargoReqFloor(spec string) (string, string) {
	r, ok := ParseCargoReq(spec)
	if !ok {
		return "", ""
	}
	return r.Floor(), r.Op
}

// cargoEntry is one tracked requirement.
type cargoEntry struct {
	crate, spec string
	// start and end delimit the requirement's string literal, quotes included.
	start, end int
}

// cargoDepSections are the tables whose members are dependencies.
var cargoDepSections = map[string]bool{"dependencies": true, "dev-dependencies": true, "build-dependencies": true}

// cargoEntries lists the requirements ripple can read, in file order.
func cargoEntries(content []byte) ([]cargoEntry, error) {
	leaves, err := tomlScan(string(content))
	if err != nil {
		return nil, err
	}
	type dep struct {
		key    string
		fields map[string]tomlLeaf
	}
	var order []*dep
	byKey := map[string]*dep{}
	for _, l := range leaves {
		i := cargoDepIndex(l.path)
		if i < 0 || len(l.path) < i+2 || len(l.path) > i+3 {
			continue
		}
		id := strings.Join(l.path[:i+2], "\x01")
		d := byKey[id]
		if d == nil {
			d = &dep{key: l.path[i+1], fields: map[string]tomlLeaf{}}
			byKey[id] = d
			order = append(order, d)
		}
		field := "version"
		if len(l.path) == i+3 {
			field = l.path[i+2]
		} else if len(l.path) == i+2 && !l.isStr {
			continue // `name = [..]` or a number: not a requirement
		}
		d.fields[field] = l
	}
	var out []cargoEntry
	for _, d := range order {
		out = appendCargo(out, d.key, d.fields)
	}
	return out, nil
}

// cargoDepIndex is the position of the dependency-section segment in a key path
// (`dependencies`, `workspace.dependencies`, `target.<cfg>.dev-dependencies`), or -1.
func cargoDepIndex(path []string) int {
	i := 0
	switch {
	case len(path) > 0 && path[0] == "workspace":
		i = 1
	case len(path) > 1 && path[0] == "target":
		i = 2
	}
	if i < len(path) && cargoDepSections[path[i]] {
		return i
	}
	return -1
}

// appendCargo adds the entry a dependency's fields describe, if it is trackable.
func appendCargo(out []cargoEntry, key string, f map[string]tomlLeaf) []cargoEntry {
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
	req, ok := ParseCargoReq(dep.Version)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a requirement ripple moves", ErrUnsupported, dep.Version)
	}
	out := append([]byte(nil), content...)
	changed := false
	for i := len(entries) - 1; i >= 0; i-- { // back to front keeps earlier offsets valid
		e := entries[i]
		if e.crate != dep.Package || e.spec != dep.Version {
			continue
		}
		q := string(content[e.start])
		out = append(out[:e.start], append([]byte(q+req.Text(newVersion)+q), out[e.end:]...)...)
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	return out, nil
}

// CargoRustVersion reads the compiler a manifest declares it supports: `[package]
// rust-version`, or `[workspace.package] rust-version` in the same file. inherits is
// true when the package says `rust-version.workspace = true`, so the value lives in a
// parent manifest. ver is "" when the file declares none.
func CargoRustVersion(content []byte) (ver string, inherits bool, err error) {
	leaves, err := tomlScan(string(content))
	if err != nil {
		return "", false, err
	}
	for _, l := range leaves {
		switch strings.Join(l.path, ".") {
		case "package.rust-version":
			if l.isStr {
				return l.str, false, nil
			}
		case "package.rust-version.workspace":
			inherits = true
		case "workspace.package.rust-version":
			if l.isStr && ver == "" {
				ver = l.str
			}
		}
	}
	if inherits {
		return "", true, nil
	}
	return ver, false, nil
}

// RustVersionNewer reports whether the compiler need ("1.80", "1.80.1") is newer than
// the one have declares. A value that is not a rust version never counts as newer.
func RustVersionNewer(need, have string) bool {
	pad := func(s string) ([3]int, bool) {
		var v [3]int
		parts := strings.Split(s, ".")
		if len(parts) == 0 || len(parts) > 3 {
			return v, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	n, ok1 := pad(need)
	h, ok2 := pad(have)
	return ok1 && ok2 && cargoCompare(n[:], h[:]) > 0
}
