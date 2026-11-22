package manager

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/glob"
	"github.com/taumatix/ripple/internal/model"
)

// Regex is a generic manager driven by user supplied RE2 patterns, so a pattern
// cannot cause catastrophic backtracking.
type Regex struct {
	name  string
	files []string
	res   []*regexp.Regexp
	pkg   string
}

// NewRegex builds the manager of an `ecosystem: regex` update. Patterns capture
// (?P<version>...) and, unless package is set, (?P<name>...).
func NewRegex(name string, c config.Update) (*Regex, error) {
	r := &Regex{name: name, pkg: c.Package, files: c.Files}
	for _, m := range c.Patterns {
		re, err := config.CompileRegex(m)
		if err != nil {
			return nil, fmt.Errorf("%s patterns: %w", name, err)
		}
		r.res = append(r.res, re)
	}
	return r, nil
}

func (r *Regex) Name() string { return r.name }

func (r *Regex) Matches(p string) bool {
	for _, g := range r.files {
		if glob.Match(g, p) {
			return true
		}
	}
	return false
}

type hit struct {
	pkg, version string
	start, end   int
}

func (r *Regex) hits(content []byte) []hit {
	var out []hit
	for _, re := range r.res {
		cv, dn := re.SubexpIndex("version"), re.SubexpIndex("name")
		for _, m := range re.FindAllSubmatchIndex(content, -1) {
			if cv < 0 || m[2*cv] < 0 {
				continue
			}
			pkg := r.pkg
			if dn >= 0 && m[2*dn] >= 0 {
				pkg = string(content[m[2*dn]:m[2*dn+1]])
			}
			if pkg == "" {
				continue
			}
			out = append(out, hit{pkg: pkg, version: string(content[m[2*cv]:m[2*cv+1]]), start: m[2*cv], end: m[2*cv+1]})
		}
	}
	return out
}

func (r *Regex) Extract(p string, content []byte) ([]model.Dependency, error) {
	var out []model.Dependency
	for _, h := range r.hits(content) {
		out = append(out, model.Dependency{File: p, Manager: r.name, Datasource: model.DatasourceGo, Package: h.pkg, Version: h.version})
	}
	return out, nil
}

func (r *Regex) Apply(content []byte, dep model.Dependency, newPackage, newVersion string) ([]byte, error) {
	if newPackage != dep.Package {
		return nil, ErrUnsupported
	}
	var spans []hit
	for _, h := range r.hits(content) {
		if h.pkg == dep.Package && h.version == dep.Version {
			spans = append(spans, h)
		}
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("%s: %s %s no longer present: %w", dep.File, dep.Package, dep.Version, ErrStale)
	}
	// Hits come grouped by pattern, not by position: order them, collapse identical
	// spans (two patterns capturing the same bytes), refuse partial overlaps, and
	// replace back to front so earlier offsets stay valid.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})
	uniq := spans[:1]
	for _, s := range spans[1:] {
		prev := uniq[len(uniq)-1]
		switch {
		case s.start == prev.start && s.end == prev.end:
		case s.start < prev.end:
			return nil, fmt.Errorf("%s: patterns match overlapping text at bytes %d-%d and %d-%d", dep.File, prev.start, prev.end, s.start, s.end)
		default:
			uniq = append(uniq, s)
		}
	}
	out := append([]byte(nil), content...)
	for i := len(uniq) - 1; i >= 0; i-- {
		s := uniq[i]
		out = append(out[:s.start:s.start], append(bytes.Clone([]byte(newVersion)), out[s.end:]...)...)
	}
	return out, nil
}
