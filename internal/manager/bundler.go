package manager

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rubygems"
)

// Bundler moves the gem versions written in a Gemfile: `gem "name", "1.2.3"` and the
// pessimistic `"~> 1.2.3"` (or `"= 1.2.3"`), the version's digits only, never re-serialised.
// The datasource is rubygems.org, so a file naming any other `source`, and every gem
// declared inside a `git`, `path`, `github` or `source` block or with a `git:`, `path:`,
// `github:` or `source:` option, is left alone.
//
// A two-segment "~> 1.2" is ">= 1.2, < 2.0" (docs/vendor/rubygems/gemfile.md, "Gems >
// Version"): every later 1.x is already allowed, so only a release of a later major is an
// update, and it moves the two digits to the new release's major and minor ("~> 2.1" for
// 2.1.3), which that release satisfies. A one-segment "~> 1" is the same ("~> 1.0": real bundler
// resolves it inside 1.x) and moves to the later release's major alone ("~> 2").
//
// Left alone besides: a gem with no version, a range or several constraints (">= 1", "< 2"),
// a single ">=" or ">" (it already allows every later release), a
// version with other than three numeric segments, a prerelease, and a declaration split
// over several lines.
type Bundler struct {
	Dir string
}

func (Bundler) Name() string { return "bundler" }

func (b Bundler) Matches(p string) bool {
	return path.Base(p) == "Gemfile" && config.InDirectory(b.Dir, p)
}

// BundlerSpec is a gem version ripple can move: "[= |~> ]X.Y.Z" or "~> X.Y".
type BundlerSpec struct {
	Prefix  string // "", "= ", "~> " as written
	Version string // "X.Y.Z", or "X.Y" after "~> "
}

var bundlerSpecRE = regexp.MustCompile(`^(?:((?:=|~>)[ \t]*)?([0-9]+\.[0-9]+\.[0-9]+)|(~>[ \t]*)([0-9]+(?:\.[0-9]+)?))$`)

// ParseBundlerSpec reads a gem's version text.
func ParseBundlerSpec(s string) (BundlerSpec, bool) {
	m := bundlerSpecRE.FindStringSubmatch(s)
	switch {
	case m == nil:
		return BundlerSpec{}, false
	case m[4] != "":
		return BundlerSpec{Prefix: m[3], Version: m[4]}, true
	}
	return BundlerSpec{Prefix: m[1], Version: m[2]}, true
}

// TwoSegment reports a "~> X.Y" constraint, which allows every release from X.Y up to the next
// major.
func (s BundlerSpec) TwoSegment() bool { return strings.Count(s.Version, ".") == 1 }

// Partial reports a "~> X" or "~> X.Y" constraint: it allows every later release of its major, so
// only a release of a later major is an update. "~> X" is the same as "~> X.0": the real
// `bundle lock` resolves "~> 1" inside 1.x (TestRealtoolBundlerOneSegment).
func (s BundlerSpec) Partial() bool { return strings.Count(s.Version, ".") < 2 }

// Floor is the constraint's version padded to three segments ("1" and "1.2" become "1.0.0" and
// "1.2.0"), the release it starts from.
func (s BundlerSpec) Floor() string {
	return s.Version + strings.Repeat(".0", 2-strings.Count(s.Version, "."))
}

// partialTarget is the text a "~> X" or "~> X.Y" constraint takes to follow release nv (X.Y.Z): as
// many leading segments of nv as the constraint has, and false when nv is not of a later major
// than the constraint.
func (s BundlerSpec) partialTarget(nv string) (string, bool) {
	cur, _, _ := strings.Cut(s.Version, ".")
	major, rest, _ := strings.Cut(nv, ".")
	minor, _, _ := strings.Cut(rest, ".")
	a, _ := strconv.Atoi(cur)
	b, _ := strconv.Atoi(major)
	if !s.TwoSegment() {
		return major, b > a
	}
	return major + "." + minor, b > a
}

type gemEntry struct {
	name, spec string
	start, end int // the version's digits in the file
}

var (
	gemLineRE   = regexp.MustCompile(`^[ \t]*gem[ \t]+(?:"([^"]+)"|'([^']+)')[ \t]*,[ \t]*(?:"([^"]*)"|'([^']*)')[ \t]*(,.*|#.*)?\r?$`)
	gemSourceRE = regexp.MustCompile(`^[ \t]*source[ \t(]+["']([^"']*)["']`)
	gemBlockRE  = regexp.MustCompile(`^([ \t]*)(?:git|path|github|source|git_source)\b.*\bdo\b`)
	gemEndRE    = regexp.MustCompile(`^([ \t]*)end\b`)
	gemOptRE    = regexp.MustCompile(`(?:\b(?:git|github|gist|bitbucket|path|source|branch|ref|tag|glob|platforms)\b[ \t]*:|:(?:git|github|gist|bitbucket|path|source|branch|ref|tag)[ \t]*=>)`)
)

// scanGemfile lists the readable gem declarations, or nothing when the file reads from
// a source other than rubygems.org.
func scanGemfile(content string) []gemEntry {
	var out []gemEntry
	foreign := -1 // indent of the git/path/source block being skipped
	off := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		lineOff := off
		off += len(line)
		line = strings.TrimRight(line, "\n")
		if m := gemSourceRE.FindStringSubmatch(line); m != nil {
			if u := strings.TrimRight(m[1], "/"); u != "https://rubygems.org" {
				return nil
			}
		}
		if foreign >= 0 {
			if m := gemEndRE.FindStringSubmatch(line); m != nil && len(m[1]) <= foreign {
				foreign = -1
			}
			continue
		}
		if m := gemBlockRE.FindStringSubmatch(line); m != nil {
			foreign = len(m[1])
			continue
		}
		m := gemLineRE.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		ni, si := 2, 6 // the double-quoted group of the name and of the version; the single-quoted is the next one
		if m[ni] < 0 {
			ni += 2
		}
		if m[si] < 0 {
			si += 2
		}
		name, spec, specOff := line[m[ni]:m[ni+1]], line[m[si]:m[si+1]], m[si]
		if m[10] >= 0 {
			rest := line[m[10]:m[11]]
			// a comment is not an option; a trailing comma continues on another line.
			if i := strings.Index(rest, " #"); i >= 0 {
				rest = rest[:i]
			}
			rest = strings.TrimSpace(rest)
			next := strings.TrimSpace(strings.TrimPrefix(rest, ","))
			if strings.HasSuffix(rest, ",") || gemOptRE.MatchString(rest) || strings.HasPrefix(next, `"`) || strings.HasPrefix(next, "'") {
				continue // a continuation, a non-rubygems source, or a second constraint
			}
		}
		s, ok := ParseBundlerSpec(spec)
		if !ok || !rubygems.ValidName(name) {
			continue
		}
		start := lineOff + specOff + len(s.Prefix)
		out = append(out, gemEntry{name, spec, start, start + len(s.Version)})
	}
	return out
}

func (b Bundler) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	seen := map[model.Dependency]bool{}
	for _, e := range scanGemfile(string(content)) {
		d := model.Dependency{File: p, Manager: b.Name(), Datasource: model.DatasourceRubygems, Package: e.name, Version: e.spec}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

func (b Bundler) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != "" && newPackage != dep.Package {
		return nil, fmt.Errorf("%w: a gem cannot be renamed", ErrUnsupported)
	}
	cur, ok := ParseBundlerSpec(dep.Version)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if r, ok := ParseBundlerSpec(nv); !ok || r.Partial() {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
	}
	text := nv
	if cur.Partial() {
		var later bool
		if text, later = cur.partialTarget(nv); !later {
			return nil, fmt.Errorf("%w: %q already allows %s", ErrUnsupported, dep.Version, nv)
		}
	}
	var hits []gemEntry
	for _, e := range scanGemfile(string(content)) {
		if e.name == dep.Package && e.spec == dep.Version {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("%s: gem %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start > hits[j].start })
	out := string(content)
	for _, e := range hits {
		out = out[:e.start] + text + out[e.end:]
	}
	return []byte(out), nil
}
