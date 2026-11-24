package npmreg

import (
	"net/url"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Setting is a registry URL and the file it was read from.
type Setting struct{ URL, File string }

// Rc is the registry routing one directory's npm-family config files state: the
// default registry and a registry per @scope. ripple only reads it to decide whether
// the public registry is where a package lives; it never fetches from, or sends a
// credential to, a URL a repository file names.
type Rc struct {
	Registry Setting
	Scopes   map[string]Setting // keyed by lower-case "@scope"
}

// IsPublic reports whether registry is the public npm registry (or yarn's mirror of
// it): an https URL whose host is exactly registry.npmjs.org or registry.yarnpkg.com.
// A URL that merely mentions the host in its path, one with an unexpanded `${VAR}`,
// or one that does not parse is not public.
func IsPublic(registry string) bool {
	u, err := url.Parse(strings.TrimSpace(registry))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "registry.npmjs.org" || h == "registry.yarnpkg.com"
}

func (r *Rc) setScope(scope, reg, file string) {
	if reg == "" {
		return
	}
	if r.Scopes == nil {
		r.Scopes = map[string]Setting{}
	}
	r.Scopes[strings.ToLower(scope)] = Setting{reg, file}
}

// ParseNpmrc reads the `registry=` and `@scope:registry=` keys of an .npmrc (npm and
// pnpm). Comment lines (`;`, `#`), blank lines, spacing around `=` and quoted values
// are handled; every other key, credentials included, is ignored and never kept.
func ParseNpmrc(data []byte, file string) Rc {
	var rc Rc
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		if !ok || val == "" {
			continue
		}
		switch {
		case strings.EqualFold(key, "registry"):
			rc.Registry = Setting{val, file}
		case strings.HasPrefix(key, "@") && strings.HasSuffix(strings.ToLower(key), ":registry"):
			rc.setScope(key[:len(key)-len(":registry")], val, file)
		}
	}
	return rc
}

// ParseYarnrc reads a classic (v1) .yarnrc: `registry "https://..."` and
// `"@scope:registry" "https://..."`.
func ParseYarnrc(data []byte, file string) Rc {
	var rc Rc
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		key, val = unquote(strings.TrimSpace(key)), unquote(strings.TrimSpace(val))
		if !ok || val == "" {
			continue
		}
		switch {
		case key == "registry":
			rc.Registry = Setting{val, file}
		case strings.HasPrefix(key, "@") && strings.HasSuffix(key, ":registry"):
			rc.setScope(key[:len(key)-len(":registry")], val, file)
		}
	}
	return rc
}

// ParseYarnrcYml reads a Yarn Berry .yarnrc.yml: `npmRegistryServer` and
// `npmScopes.<scope>.npmRegistryServer`. A file that is not YAML yields an empty Rc.
func ParseYarnrcYml(data []byte, file string) Rc {
	var doc struct {
		Server string `yaml:"npmRegistryServer"`
		Scopes map[string]struct {
			Server string `yaml:"npmRegistryServer"`
		} `yaml:"npmScopes"`
	}
	var rc Rc
	if yaml.Unmarshal(data, &doc) != nil {
		return rc
	}
	if doc.Server != "" {
		rc.Registry = Setting{doc.Server, file}
	}
	for scope, s := range doc.Scopes {
		rc.setScope("@"+strings.TrimPrefix(scope, "@"), s.Server, file)
	}
	return rc
}

// pick chooses between two settings of one key that apply at the same level: a
// public one yields to a non-public one, so disagreement is never read as public.
func pick(a, b Setting) Setting {
	switch {
	case a.URL == "":
		return b
	case b.URL == "":
		return a
	case IsPublic(a.URL) && !IsPublic(b.URL):
		return b
	}
	return a
}

// Join combines two Rcs that apply at the same level (files of one directory).
func (r Rc) Join(o Rc) Rc {
	out := Rc{Registry: pick(r.Registry, o.Registry)}
	for _, m := range []map[string]Setting{r.Scopes, o.Scopes} {
		for k, v := range m {
			if out.Scopes == nil {
				out.Scopes = map[string]Setting{}
			}
			out.Scopes[k] = pick(out.Scopes[k], v)
		}
	}
	return out
}

// Over layers r above farther: for each key, r's setting wins when it has one.
func (r Rc) Over(farther Rc) Rc {
	out := Rc{Registry: r.Registry}
	if out.Registry.URL == "" {
		out.Registry = farther.Registry
	}
	for _, m := range []map[string]Setting{farther.Scopes, r.Scopes} { // r last: it overwrites
		for k, v := range m {
			if out.Scopes == nil {
				out.Scopes = map[string]Setting{}
			}
			out.Scopes[k] = v
		}
	}
	return out
}

// For is the registry setting that applies to package name: its scope's, else the
// default. A zero Setting means the public registry applies.
func (r Rc) For(name string) Setting {
	if strings.HasPrefix(name, "@") {
		scope, _, _ := strings.Cut(name, "/")
		if s, ok := r.Scopes[strings.ToLower(scope)]; ok {
			return s
		}
	}
	return r.Registry
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
