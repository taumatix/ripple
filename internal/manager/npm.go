package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
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
// Only a range ripple can read with certainty is tracked: a full x.y.z version with
// no operator, `=`, `^` or `~`. Wildcards, comparators, unions, hyphen ranges,
// partial versions, prereleases, dist-tags, git/file/URL specs, `npm:` aliases and
// `workspace:` specs are left alone. A range that is bumped is raised to the new
// version even when it already allowed it, because without a lockfile refresh that is
// the only change a PR can carry.
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

var npmRangeRE = regexp.MustCompile(`^([\^~=]?)(\d+\.\d+\.\d+)$`)

// NpmRangeFloor is the version a trackable range names, with ok false and "" when the
// range is not one ripple moves.
func NpmRangeFloor(spec string) (string, string) {
	m := npmRangeRE.FindStringSubmatch(spec)
	if m == nil {
		return "", ""
	}
	return m[2], m[1]
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
	_, op := NpmRangeFloor(dep.Version)
	out := append([]byte(nil), content...)
	changed := false
	for i := len(entries) - 1; i >= 0; i-- { // back to front keeps earlier offsets valid
		e := entries[i]
		if e.name != dep.Package || e.spec != dep.Version {
			continue
		}
		lit := `"` + op + newVersion + `"`
		out = append(out[:e.start], append([]byte(lit), out[e.end:]...)...)
		changed = true
	}
	if !changed {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	return out, nil
}
