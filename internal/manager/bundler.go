package manager

import (
	"fmt"
	"path"
	"regexp"
	"sort"
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
// Left alone besides: a gem with no version, a range or several constraints (">= 1", "< 2"),
// a two-segment "~> 1.2" (the Gemfile already allows every later 1.x, so a newer release
// needs only a lock refresh), a version with other than three numeric segments, a
// prerelease, and a declaration split over several lines.
type Bundler struct {
	Dir string
}

func (Bundler) Name() string { return "bundler" }

func (b Bundler) Matches(p string) bool {
	return path.Base(p) == "Gemfile" && config.InDirectory(b.Dir, p)
}

// BundlerSpec is a gem version ripple can move: "[= |~> ]X.Y.Z".
type BundlerSpec struct {
	Prefix  string // "", "= ", "~> " as written
	Version string
}

var bundlerSpecRE = regexp.MustCompile(`^((?:=|~>)[ \t]*)?([0-9]+\.[0-9]+\.[0-9]+)$`)

// ParseBundlerSpec reads a gem's version text.
func ParseBundlerSpec(s string) (BundlerSpec, bool) {
	m := bundlerSpecRE.FindStringSubmatch(s)
	if m == nil {
		return BundlerSpec{}, false
	}
	return BundlerSpec{Prefix: m[1], Version: m[2]}, true
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
	if _, ok := ParseBundlerSpec(dep.Version); !ok {
		return nil, fmt.Errorf("%w: %q is not a version ripple moves", ErrUnsupported, dep.Version)
	}
	nv := strings.TrimPrefix(newVersion, "v")
	if _, ok := ParseBundlerSpec(nv); !ok {
		return nil, fmt.Errorf("%w: %q is not a release version", ErrUnsupported, newVersion)
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
		out = out[:e.start] + nv + out[e.end:]
	}
	return []byte(out), nil
}
