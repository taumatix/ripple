package manager

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/terraform"
)

// Terraform moves the version constraints of the registry modules and providers a *.tf file
// declares, the version's digits only, never re-serialised:
//
//	module "vpc" { source = "terraform-aws-modules/vpc/aws"  version = "5.1.0" }
//	terraform { required_providers { aws = { source = "hashicorp/aws"  version = "~> 5.1.0" } } }
//
// A package is "modules/namespace/name/provider" or "providers/namespace/type"; the Dependency's
// Version is the bare X.Y.Z (or X.Y after "~>"), and the constraint's operator (none, "=" or "~>")
// stays as written. `~> 1.2` moves to `~> 1.5` for release 1.5.3: the same shape, and it allows
// what the release needs.
//
// Left alone: a range or any other operator (">= 1.0", "1.0, < 2.0": a floor says what a module
// needs, not which release to use), a two-segment version after no operator or "=", an interpolated
// value, a source with a host, a git, local or `//subdir` address, a module with no version, a
// provider with no `source` (the legacy `aws = "~> 4.0"` form), and anything in a comment or a
// heredoc. Registry mirrors and `.terraformrc` are not read.
type Terraform struct {
	Dir string
}

func (Terraform) Name() string { return "terraform" }

func (t Terraform) Matches(p string) bool {
	return strings.HasSuffix(p, ".tf") && config.InDirectory(t.Dir, p)
}

var terraformSpecRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ParseTerraformSpec reports whether s is a bare version ripple moves ("X.Y.Z").
func ParseTerraformSpec(s string) bool { return terraformSpecRE.MatchString(s) }

var terraformDeclaredRE = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// ParseTerraformDeclared reports whether s is the version text of a declaration ripple moves:
// "X.Y.Z" (after no operator, "=" or "~>") or "X.Y" (after "~>" only).
func ParseTerraformDeclared(s string) bool { return terraformDeclaredRE.MatchString(s) }

var (
	terraformConstraintRE    = regexp.MustCompile(`^(?:=\s*|~>\s*)?([0-9]+\.[0-9]+\.[0-9]+)$`)
	terraformPessimisticTwoRE = regexp.MustCompile(`^~>\s*([0-9]+\.[0-9]+)$`)
)

type tfTok struct {
	kind       byte // 'i' identifier, 's' string, 'p' punctuation
	text       string
	start, end int  // for a string: the bytes between the quotes
	interp     bool // a string holding ${ or %{
}

// tfLex tokenizes HCL just far enough to find blocks and string attributes. Comments and
// heredocs are skipped.
func tfLex(s string) []tfTok {
	var out []tfTok
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '#' || (c == '/' && i+1 < n && s[i+1] == '/'):
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return out
			}
			i += j + 4
		case c == '"':
			start := i + 1
			end, interp := tfString(s, start)
			if end < 0 {
				return out
			}
			out = append(out, tfTok{'s', s[start:end], start, end, interp})
			i = end + 1
		case c == '<' && strings.HasPrefix(s[i:], "<<"):
			j := i + 2
			if j < n && s[j] == '-' {
				j++
			}
			k := j
			for k < n && (s[k] == '_' || s[k] >= '0' && s[k] <= '9' || s[k] >= 'A' && s[k] <= 'Z' || s[k] >= 'a' && s[k] <= 'z') {
				k++
			}
			if k == j {
				out = append(out, tfTok{kind: 'p', text: "<", start: i, end: i + 1})
				i++
				break
			}
			tag := s[j:k]
			for i = k; i < n && s[i] != '\n'; i++ {
			}
			for i < n {
				i++ // past the newline
				e := i
				for e < n && s[e] != '\n' {
					e++
				}
				done := strings.TrimSpace(s[i:e]) == tag
				i = e
				if done {
					break
				}
			}
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < n && (s[j] == '_' || s[j] == '-' || s[j] >= '0' && s[j] <= '9' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			out = append(out, tfTok{'i', s[i:j], i, j, false})
			i = j
		default:
			out = append(out, tfTok{'p', s[i : i+1], i, i + 1, false})
			i++
		}
	}
	return out
}

// tfString returns the index of the closing quote of the string whose contents begin at i, or -1.
// An interpolation ${...} or %{...} may hold quotes and braces of its own.
func tfString(s string, i int) (int, bool) {
	interp := false
	for i < len(s) {
		switch {
		case s[i] == '\\':
			i += 2
		case s[i] == '"':
			return i, interp
		case (s[i] == '$' || s[i] == '%') && i+1 < len(s) && s[i+1] == '{':
			if i > 0 && s[i-1] == s[i] { // $${ and %%{ are escapes
				i++
				continue
			}
			interp = true
			depth := 0
			i++
			for i < len(s) {
				switch s[i] {
				case '{':
					depth++
				case '}':
					depth--
				case '"':
					e, _ := tfString(s, i+1)
					if e < 0 {
						return -1, interp
					}
					i = e
				}
				i++
				if depth == 0 {
					break
				}
			}
		default:
			i++
		}
	}
	return -1, interp
}

type terraformEntry struct {
	pkg, spec  string
	start, end int
}

// tfBlockEnd returns the index of the token after the "}" matching the "{" at toks[i].
func tfBlockEnd(toks []tfTok, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		if toks[i].kind != 'p' {
			continue
		}
		switch toks[i].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(toks)
}

// tfAttrs reads the `name = "string"` attributes directly inside the block whose "{" is at
// toks[open] (not those of nested blocks).
func tfAttrs(toks []tfTok, open int) map[string]tfTok {
	out := map[string]tfTok{}
	end := tfBlockEnd(toks, open)
	depth := 0
	for j := open; j < end; j++ {
		t := toks[j]
		if t.kind == 'p' && t.text == "{" {
			depth++
		} else if t.kind == 'p' && t.text == "}" {
			depth--
		} else if depth == 1 && t.kind == 'i' && j+2 < end && toks[j+1].text == "=" && toks[j+1].kind == 'p' && toks[j+2].kind == 's' {
			if _, dup := out[t.text]; !dup {
				out[t.text] = toks[j+2]
			} else {
				out[t.text] = tfTok{} // declared twice: not safe to edit
			}
		}
	}
	return out
}

func tfEntry(src, ver tfTok, content string, pkgOf func(string) (string, bool)) (terraformEntry, bool) {
	if src.kind != 's' || ver.kind != 's' || src.interp || ver.interp {
		return terraformEntry{}, false
	}
	pkg, ok := pkgOf(src.text)
	if !ok {
		return terraformEntry{}, false
	}
	trimmed := strings.TrimSpace(ver.text)
	m := terraformConstraintRE.FindStringSubmatchIndex(trimmed)
	if m == nil {
		m = terraformPessimisticTwoRE.FindStringSubmatchIndex(trimmed)
	}
	if m == nil {
		return terraformEntry{}, false
	}
	// Offset of the digits within the string: leading blanks plus the operator.
	lead := len(ver.text) - len(strings.TrimLeft(ver.text, " \t"))
	start := ver.start + lead + m[2]
	return terraformEntry{pkg, content[start : start+(m[3]-m[2])], start, start + (m[3] - m[2])}, true
}

func scanTerraform(content string) []terraformEntry {
	toks := tfLex(content)
	var out []terraformEntry
	for i := 0; i+2 < len(toks); {
		t := toks[i]
		switch {
		case t.kind == 'i' && t.text == "module" && toks[i+1].kind == 's' && toks[i+2].text == "{" && toks[i+2].kind == 'p':
			a := tfAttrs(toks, i+2)
			if e, ok := tfEntry(a["source"], a["version"], content, terraform.ModulePackage); ok {
				out = append(out, e)
			}
			i = tfBlockEnd(toks, i+2)
		case t.kind == 'i' && t.text == "terraform" && toks[i+1].text == "{" && toks[i+1].kind == 'p':
			end := tfBlockEnd(toks, i+1)
			for j := i + 2; j+1 < end; j++ {
				if toks[j].kind == 'i' && toks[j].text == "required_providers" && toks[j+1].text == "{" {
					out = append(out, tfProviders(toks, j+1, content)...)
					j = tfBlockEnd(toks, j+1) - 1
				}
			}
			i = end
		default:
			i++
		}
	}
	return out
}

func tfProviders(toks []tfTok, open int, content string) []terraformEntry {
	var out []terraformEntry
	end := tfBlockEnd(toks, open)
	for j := open + 1; j+2 < end; j++ {
		if toks[j].kind == 'i' && toks[j+1].text == "=" && toks[j+2].text == "{" && toks[j+2].kind == 'p' {
			a := tfAttrs(toks, j+2)
			if e, ok := tfEntry(a["source"], a["version"], content, terraform.ProviderPackage); ok {
				out = append(out, e)
			}
			j = tfBlockEnd(toks, j+2) - 1
		}
	}
	return out
}

func (t Terraform) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanTerraform(string(content)) {
		d := model.Dependency{File: p, Manager: t.Name(), Datasource: model.DatasourceTerraform, Package: e.pkg, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (t Terraform) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a module or provider cannot be renamed", ErrUnsupported)
	}
	if !ParseTerraformDeclared(dep.Version) {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if !ParseTerraformDeclared(nv) {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	// `~> 1.2` keeps its two segments: `~> 1.5` allows what release 1.5.3 needs (docs/vendor/terraform/
	// version-constraints.txt, "~>"), so the patch of the release is dropped.
	if strings.Count(dep.Version, ".") == 1 {
		nv = strings.Join(strings.SplitN(nv, ".", 3)[:2], ".")
	} else if strings.Count(nv, ".") != 2 {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	var hits []terraformEntry
	for _, e := range scanTerraform(string(content)) {
		if e.pkg == dep.Package && e.spec == dep.Version {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start > hits[j].start })
	out := string(content)
	for _, e := range hits {
		out = out[:e.start] + nv + out[e.end:]
	}
	return []byte(out), nil
}
