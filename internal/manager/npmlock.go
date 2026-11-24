package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrLockDeclined wraps the reason a package-lock.json is left for the owner to
// refresh: ripple rewrites a lockfile only when the change is the same on every
// install path npm would take.
var ErrLockDeclined = errors.New("lockfile not rewritten")

func declined(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrLockDeclined, fmt.Sprintf(format, a...))
}

// NpmLockPlan is a package-lock.json (lockfileVersion 2 or 3) in which one package
// can be moved to another version without running npm: a leaf, hoisted to the top,
// required by nothing but the project itself. It holds the offsets of the strings to
// rewrite, so the rest of the file stays byte for byte.
type NpmLockPlan struct {
	// Version and Resolved are the locked version and its tarball URL.
	Version, Resolved, Integrity string

	content []byte
	spans   map[string][2]int // path -> string literal, quotes included
	roots   []string          // keys of the root-range spans
	v2      bool
}

// leafKeys are the members a lock entry of a leaf package may have for ripple to
// rewrite it: a new version of the same shape changes none of the others, except
// license and funding, which the registry's abbreviated metadata does not carry and
// which are kept as they were.
var leafKeys = map[string]bool{"version": true, "resolved": true, "integrity": true, "dev": true, "optional": true,
	"devOptional": true, "license": true, "funding": true, "engines": true, "bin": true}

var v2Keys = map[string]bool{"version": true, "resolved": true, "integrity": true, "dev": true, "optional": true}

var rootSections = []string{"dependencies", "devDependencies", "optionalDependencies"}

func nul(p ...string) string { return strings.Join(p, "\x00") }

// PlanNpmLock checks that pkg, required as oldSpec by the project's package.json, can be
// moved in lock. An error wrapping ErrLockDeclined says why not.
func PlanNpmLock(lock []byte, pkg, oldSpec string) (NpmLockPlan, error) {
	var doc struct {
		LockfileVersion int                                   `json:"lockfileVersion"`
		Packages        map[string]map[string]json.RawMessage `json:"packages"`
		Dependencies    map[string]map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(lock, &doc); err != nil {
		return NpmLockPlan{}, declined("not valid JSON: %v", err)
	}
	if doc.LockfileVersion != 2 && doc.LockfileVersion != 3 {
		return NpmLockPlan{}, declined("lockfileVersion %d (only 2 and 3 are read)", doc.LockfileVersion)
	}
	root, ok := doc.Packages[""]
	if !ok {
		return NpmLockPlan{}, declined("no root package entry")
	}
	if _, ok := root["workspaces"]; ok {
		return NpmLockPlan{}, declined("the project uses workspaces")
	}
	plan := NpmLockPlan{content: lock, v2: doc.LockfileVersion == 2}
	wants := map[string]bool{}
	for _, sec := range append([]string{"peerDependencies"}, rootSections...) {
		var m map[string]string
		if raw, ok := root[sec]; ok {
			json.Unmarshal(raw, &m)
		}
		spec, has := m[pkg]
		switch {
		case !has:
		case sec == "peerDependencies":
			return NpmLockPlan{}, declined("%s is also a peer dependency", pkg)
		case spec != oldSpec:
			return NpmLockPlan{}, declined("the lockfile's range for %s in %s is %q, not %q", pkg, sec, spec, oldSpec)
		default:
			k := nul("packages", "", sec, pkg)
			wants[k] = true
			plan.roots = append(plan.roots, k)
		}
	}
	if len(plan.roots) == 0 {
		return NpmLockPlan{}, declined("%s is not in the lockfile's root package", pkg)
	}
	key := "node_modules/" + pkg
	entry, ok := doc.Packages[key]
	if !ok {
		return NpmLockPlan{}, declined("%s is not hoisted to the top of node_modules", pkg)
	}
	for k := range entry {
		if !leafKeys[k] {
			return NpmLockPlan{}, declined("%s has %q, so it is not a plain leaf", pkg, k)
		}
	}
	json.Unmarshal(entry["version"], &plan.Version)
	json.Unmarshal(entry["resolved"], &plan.Resolved)
	json.Unmarshal(entry["integrity"], &plan.Integrity)
	if plan.Version == "" || plan.Resolved == "" || plan.Integrity == "" {
		return NpmLockPlan{}, declined("%s has no version, resolved URL and integrity in the lockfile", pkg)
	}
	var others []string
	for k, e := range doc.Packages {
		if k == "" || k == key {
			continue
		}
		for _, sec := range []string{"dependencies", "optionalDependencies", "peerDependencies"} {
			var m map[string]json.RawMessage
			if raw, ok := e[sec]; ok && json.Unmarshal(raw, &m) == nil {
				if _, uses := m[pkg]; uses {
					others = append(others, k)
				}
			}
		}
	}
	if len(others) > 0 {
		sort.Strings(others)
		return NpmLockPlan{}, declined("%s is also required by %s, whose ranges ripple does not check", pkg, others[0])
	}
	for _, f := range []string{"version", "resolved", "integrity"} {
		wants[nul("packages", key, f)] = true
	}
	if plan.v2 {
		d, ok := doc.Dependencies[pkg]
		if !ok {
			return NpmLockPlan{}, declined("%s is missing from the lockfile's dependencies tree", pkg)
		}
		for k := range d {
			if !v2Keys[k] {
				return NpmLockPlan{}, declined("%s has %q in the dependencies tree, so it is not a plain leaf", pkg, k)
			}
		}
		for _, f := range []string{"version", "resolved", "integrity"} {
			wants[nul("dependencies", pkg, f)] = true
		}
	}
	spans := map[string][2]int{}
	prefixes := map[string]bool{}
	for w := range wants {
		parts := strings.Split(w, "\x00")
		for i := 1; i < len(parts); i++ {
			prefixes[nul(parts[:i]...)] = true
		}
	}
	if err := collectSpans(lock, 0, nil, wants, prefixes, spans); err != nil {
		return NpmLockPlan{}, declined("lockfile not readable: %v", err)
	}
	for w := range wants {
		if _, ok := spans[w]; !ok {
			return NpmLockPlan{}, declined("could not locate %s in the lockfile", strings.ReplaceAll(w, "\x00", "."))
		}
	}
	plan.spans = spans
	return plan, nil
}

// Apply returns the lockfile with pkg moved: the root ranges become newSpec, and the
// locked entry names version, its tarball and its integrity.
func (p NpmLockPlan) Apply(pkg, newSpec, version, resolved, integrity string) ([]byte, error) {
	type edit struct {
		span [2]int
		text string
	}
	var edits []edit
	for _, k := range p.roots {
		edits = append(edits, edit{p.spans[k], jsonString(newSpec)})
	}
	vals := map[string]string{"version": version, "resolved": resolved, "integrity": integrity}
	for f, v := range vals {
		edits = append(edits, edit{p.spans[nul("packages", "node_modules/"+pkg, f)], jsonString(v)})
		if p.v2 {
			edits = append(edits, edit{p.spans[nul("dependencies", pkg, f)], jsonString(v)})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].span[0] > edits[j].span[0] })
	out := append([]byte(nil), p.content...)
	for _, e := range edits {
		out = append(out[:e.span[0]], append([]byte(e.text), out[e.span[1]:]...)...)
	}
	if !json.Valid(out) {
		return nil, declined("the rewritten lockfile is not valid JSON")
	}
	return out, nil
}

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// collectSpans records the byte span of each wanted string value, descending only
// into the objects on the way to one.
func collectSpans(data []byte, base int, prefix []string, wants, prefixes map[string]bool, out map[string][2]int) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		path := append(append([]string(nil), prefix...), key)
		before := int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		start := before + bytes.Index(data[before:int(dec.InputOffset())], raw)
		k := nul(path...)
		switch {
		case wants[k] && len(raw) > 0 && raw[0] == '"':
			out[k] = [2]int{base + start, base + start + len(raw)}
		case prefixes[k]:
			if err := collectSpans(raw, base+start, path, wants, prefixes, out); err != nil {
				return err
			}
		}
	}
	return nil
}
