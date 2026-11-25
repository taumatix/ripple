package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
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
// constraints (`">=1.0 <2.0"`, `"^1.0 || ^2.0"`), a wildcard, a two-segment `"^1.2"` (the
// constraint already allows every later 1.x, so a new release needs a lock refresh and no
// edit), a stability flag, a `dev-` branch, a version with other than three numeric
// segments, and a string with a JSON escape in it.
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

var composerSpecRE = regexp.MustCompile(`^((?:[\^~=][ \t]*)?v?)([0-9]+\.[0-9]+\.[0-9]+)$`)

// ParseComposerSpec reads a package's version text.
func ParseComposerSpec(s string) (ComposerSpec, bool) {
	m := composerSpecRE.FindStringSubmatch(s)
	if m == nil {
		return ComposerSpec{}, false
	}
	return ComposerSpec{Prefix: m[1], Version: m[2]}, true
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
	if _, ok := ParseComposerSpec(dep.Version); !ok {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if _, ok := ParseComposerSpec(nv); !ok {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
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
		out = out[:e.start] + nv + out[e.end:]
	}
	return []byte(out), nil
}
