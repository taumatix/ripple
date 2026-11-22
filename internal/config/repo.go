// Package config parses and validates .github/ripple.yml and the server settings.
//
// Simplicity is a design goal: the per-repo schema is a short Dependabot-style
// updates: list with no rule precedence, presets or inheritance, and a repo file
// can only tighten what the server allows, never widen it.
package config

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	// FileName is where a repo keeps its (optional) configuration.
	FileName      = ".github/ripple.yml"
	maxRegexBytes = 4096
	maxCooldown   = 365 * 24 * time.Hour
)

var (
	durationPart = regexp.MustCompile(`^(\d+)([dhm])`)
	hhmm         = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	validDays    = map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}
	validTypes   = map[string]bool{"major": true, "minor": true, "patch": true}
)

// ParseDuration reads "3d", "12h", "90m", "1d12h" or "0".
func ParseDuration(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if s == "" {
		return 0, errors.New(`empty duration; use e.g. "3d", "12h", "90m" or "0"`)
	}
	rest, total := s, time.Duration(0)
	for rest != "" {
		m := durationPart.FindStringSubmatch(rest)
		if m == nil {
			return 0, fmt.Errorf("invalid duration %q; use e.g. \"3d\", \"12h\", \"90m\" or \"0\"", s)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n > 100000 {
			return 0, fmt.Errorf("duration %q is too large", s)
		}
		total += time.Duration(n) * map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute}[m[2]]
		rest = rest[len(m[0]):]
	}
	if total > maxCooldown {
		return 0, fmt.Errorf("duration %q exceeds the 365d maximum", s)
	}
	return total, nil
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDuration(n.Value)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type Schedule struct {
	Days     []string `yaml:"days"`
	From     string   `yaml:"from"`
	To       string   `yaml:"to"`
	Timezone string   `yaml:"timezone"`
}

type Ignore struct {
	Dependency  string   `yaml:"dependency"`
	UpdateTypes []string `yaml:"update-types"`
}

type Group struct {
	Name        string   `yaml:"-"`
	Patterns    []string `yaml:"patterns"`
	UpdateTypes []string `yaml:"update-types"`
}

type Update struct {
	Ecosystem string    `yaml:"ecosystem"`
	Directory string    `yaml:"directory"`
	Schedule  *Schedule `yaml:"schedule"`
	Cooldown  *Duration `yaml:"cooldown"`
	Ignore    []Ignore  `yaml:"ignore"`
	Groups    []Group   `yaml:"-"`
	// Automerge is "", "patch" (patch updates), "minor" (minor and patch) or "major" (all).
	// Major updates are never merged unless the file says "major".
	Automerge string `yaml:"automerge"`
	// regex ecosystem only
	Files    []string `yaml:"files"`
	Patterns []string `yaml:"patterns"`
	Package  string   `yaml:"package"`
}

// Repo is the content of .github/ripple.yml.
type Repo struct {
	Version     int      `yaml:"version"`
	OpenPRLimit int      `yaml:"open-pull-requests-limit"`
	Updates     []Update `yaml:"updates"`
}

// Default is what a repo without a file, or with an empty one, gets.
func Default() *Repo {
	return &Repo{Version: 1, Updates: []Update{{Ecosystem: "gomod", Directory: "/"}}}
}

// ManagerName is the name recorded on dependencies found through update i.
func (r *Repo) ManagerName(i int) string {
	if r.Updates[i].Ecosystem == "regex" {
		return fmt.Sprintf("regex#%d", i)
	}
	return r.Updates[i].Ecosystem
}

// UpdateFor returns the update entry that governs a dependency: for gomod the
// entry with the deepest matching directory, for regex the entry that found it.
func (r *Repo) UpdateFor(manager, file string) (*Update, bool) {
	if idx, ok := strings.CutPrefix(manager, "regex#"); ok {
		i, err := strconv.Atoi(idx)
		if err != nil || i < 0 || i >= len(r.Updates) {
			return nil, false
		}
		return &r.Updates[i], true
	}
	var best *Update
	bestLen := -1
	for i := range r.Updates {
		u := &r.Updates[i]
		if u.Ecosystem != manager || !InDirectory(u.Directory, file) {
			continue
		}
		if l := len(u.Directory); l > bestLen {
			best, bestLen = u, l
		}
	}
	return best, best != nil
}

// InDirectory reports whether file is in dir or below it ("/" is the whole repo).
func InDirectory(dir, file string) bool {
	if dir == "/" || dir == "" {
		return true
	}
	return strings.HasPrefix(file, strings.TrimPrefix(dir, "/")+"/")
}

// Example is a valid file that exercises every key; printed by `config validate` on errors.
const Example = `version: 1
open-pull-requests-limit: 3        # at most this many ripple PRs open at once
updates:
  - ecosystem: gomod               # gomod, or regex (see README)
    directory: /                   # this directory and everything below it
    cooldown: 7d                   # wait this long after a release (never less than the server minimum)
    schedule:
      days: [mon, tue]
      from: "22:00"
      to: "05:00"
      timezone: Europe/Paris
    ignore:
      - dependency: github.com/legacy/*
      - dependency: github.com/foo/bar
        update-types: [major]
    groups:
      aws:
        patterns: ["github.com/aws/*"]
    automerge: patch               # patch, minor or major (default: never); needs auto-merge on the repo
`

// DefaultFile is committed by the onboarding PR: valid, equal to the defaults, with hints.
func DefaultFile() []byte {
	return []byte(`# ripple updates your dependencies when a new version is published.
# This file is optional: without it ripple behaves exactly as written here.
# Docs: https://github.com/taumatix/ripple#configuration
version: 1
updates:
  - ecosystem: gomod
    directory: /
    # cooldown: 7d              # wait longer than the server default before opening a PR
    # automerge: patch          # merge patch updates once checks pass
`)
}

// Problem is one validation finding.
type Problem struct {
	Line int
	Msg  string
}

// ValidationError lists every problem found.
type ValidationError struct {
	File     string
	Problems []Problem
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid %s:", e.File)
	for _, p := range e.Problems {
		if p.Line > 0 {
			fmt.Fprintf(&b, "\n  - line %d: %s", p.Line, p.Msg)
		} else {
			fmt.Fprintf(&b, "\n  - %s", p.Msg)
		}
	}
	return b.String()
}

type checker struct{ problems []Problem }

func (c *checker) add(n *yaml.Node, format string, a ...any) {
	line := 0
	if n != nil {
		line = n.Line
	}
	c.problems = append(c.problems, Problem{line, fmt.Sprintf(format, a...)})
}

var (
	rootKeys     = []string{"version", "open-pull-requests-limit", "updates"}
	updateKeys   = []string{"ecosystem", "directory", "schedule", "cooldown", "ignore", "groups", "automerge", "files", "patterns", "package"}
	scheduleKeys = []string{"days", "from", "to", "timezone"}
	ignoreKeys   = []string{"dependency", "update-types"}
	groupKeys    = []string{"patterns", "update-types"}
)

// mapping returns the key -> value pairs of a mapping node, reporting unknown keys.
func (c *checker) mapping(n *yaml.Node, allowed []string, where string) map[string]*yaml.Node {
	if n.Kind != yaml.MappingNode {
		c.add(n, "%s must be a mapping of keys to values", where)
		return nil
	}
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		ok := false
		for _, a := range allowed {
			if a == k.Value {
				ok = true
			}
		}
		if !ok {
			msg := fmt.Sprintf("unknown key %q in %s", k.Value, where)
			if s := closest(k.Value, allowed); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			} else {
				msg += "; allowed keys: " + strings.Join(allowed, ", ")
			}
			c.add(k, "%s", msg)
			continue
		}
		out[k.Value] = v
	}
	return out
}

func closest(name string, options []string) string {
	best, bd := "", 4
	for _, o := range options {
		if d := levenshtein(strings.ToLower(name), o); d < bd {
			best, bd = o, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Parse decodes and strictly validates a repo file. Empty input yields Default().
func Parse(data []byte) (*Repo, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &ValidationError{File: FileName, Problems: []Problem{{Msg: strings.TrimPrefix(err.Error(), "yaml: ")}}}
	}
	if doc.Kind == 0 {
		return Default(), nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return Default(), nil
	}
	c := &checker{}
	top := c.mapping(root, rootKeys, "the top level")
	if top == nil && root.Kind == yaml.MappingNode && len(root.Content) == 0 {
		return Default(), nil
	}
	var r Repo
	if len(c.problems) == 0 || top != nil {
		if err := decodeSafe(root, &r); err != nil {
			c.problems = append(c.problems, Problem{Msg: err.Error()})
		}
	}
	if v := top["version"]; v != nil && r.Version != 1 {
		c.add(v, "version must be 1 (the only schema version)")
	}
	if v := top["open-pull-requests-limit"]; v != nil && r.OpenPRLimit < 1 {
		c.add(v, "open-pull-requests-limit must be 1 or more")
	}
	updates := top["updates"]
	if updates == nil {
		r.Updates = Default().Updates
	} else if updates.Kind != yaml.SequenceNode {
		c.add(updates, "updates must be a list")
	} else {
		if len(updates.Content) == 0 {
			c.add(updates, "updates is empty; remove the key to get the default gomod update")
		}
		for i, un := range updates.Content {
			if i < len(r.Updates) {
				c.update(un, &r.Updates[i], fmt.Sprintf("updates[%d]", i))
			}
		}
	}
	if len(c.problems) > 0 {
		sort.SliceStable(c.problems, func(i, j int) bool { return c.problems[i].Line < c.problems[j].Line })
		return nil, &ValidationError{File: FileName, Problems: c.problems}
	}
	if r.Version == 0 {
		r.Version = 1
	}
	for i := range r.Updates {
		if r.Updates[i].Ecosystem == "gomod" && r.Updates[i].Directory == "" {
			r.Updates[i].Directory = "/"
		}
	}
	return &r, nil
}

func decodeSafe(n *yaml.Node, out any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("cannot decode: %v", p)
		}
	}()
	if err := n.Decode(out); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			return errors.New(strings.TrimPrefix(strings.Join(te.Errors, "; "), "yaml: "))
		}
		return err
	}
	return nil
}

func (c *checker) update(n *yaml.Node, u *Update, where string) {
	f := c.mapping(n, updateKeys, where)
	if f == nil {
		return
	}
	switch u.Ecosystem {
	case "gomod":
		for _, k := range []string{"files", "patterns", "package"} {
			if v := f[k]; v != nil {
				c.add(v, "%s.%s is only valid with ecosystem: regex", where, k)
			}
		}
		if d := f["directory"]; d != nil {
			if !strings.HasPrefix(u.Directory, "/") || strings.Contains(u.Directory, "..") || path.Clean(u.Directory) != strings.TrimRight(u.Directory, "/") && u.Directory != "/" {
				c.add(d, "%s.directory must be an absolute path inside the repo such as / or /services/api", where)
			}
		}
	case "regex":
		if v := f["directory"]; v != nil {
			c.add(v, "%s.directory does not apply to ecosystem: regex; use files", where)
		}
		if len(u.Files) == 0 {
			c.add(n, "%s.files: at least one file pattern is required for ecosystem: regex", where)
		}
		if len(u.Patterns) == 0 {
			c.add(n, "%s.patterns: at least one regular expression is required for ecosystem: regex", where)
		}
		for j, p := range u.Patterns {
			re, err := CompileRegex(p)
			if err != nil {
				c.add(f["patterns"], "%s.patterns[%d]: %v", where, j, err)
				continue
			}
			if re.SubexpIndex("version") < 0 {
				c.add(f["patterns"], "%s.patterns[%d] must contain a named group (?P<version>...)", where, j)
			}
			if re.SubexpIndex("name") < 0 && u.Package == "" {
				c.add(f["patterns"], "%s.patterns[%d] needs a (?P<name>...) group, or set package:", where, j)
			}
		}
	case "":
		c.add(n, "%s.ecosystem is required (gomod or regex)", where)
	default:
		c.add(f["ecosystem"], "%s.ecosystem %q is not supported; use gomod or regex", where, u.Ecosystem)
	}
	switch u.Automerge {
	case "", "patch", "minor", "major":
	default:
		c.add(f["automerge"], "%s.automerge must be patch, minor or major (omit it to never auto-merge)", where)
	}
	if s := f["schedule"]; s != nil {
		c.schedule(s, u.Schedule, where+".schedule")
	}
	if ig := f["ignore"]; ig != nil && ig.Kind == yaml.SequenceNode {
		for j, in := range ig.Content {
			m := c.mapping(in, ignoreKeys, fmt.Sprintf("%s.ignore[%d]", where, j))
			if m == nil {
				continue
			}
			if m["dependency"] == nil {
				c.add(in, "%s.ignore[%d].dependency is required", where, j)
			}
			if j < len(u.Ignore) {
				c.types(m["update-types"], u.Ignore[j].UpdateTypes, fmt.Sprintf("%s.ignore[%d]", where, j))
			}
		}
	}
	if g := f["groups"]; g != nil {
		c.groups(g, u, where)
	}
}

func (c *checker) types(n *yaml.Node, types []string, where string) {
	for _, t := range types {
		if !validTypes[t] {
			c.add(n, "%s.update-types: %q is not one of major, minor, patch", where, t)
		}
	}
}

func (c *checker) groups(g *yaml.Node, u *Update, where string) {
	if g.Kind != yaml.MappingNode {
		c.add(g, "%s.groups must be a mapping of group name to settings", where)
		return
	}
	for i := 0; i+1 < len(g.Content); i += 2 {
		name, body := g.Content[i].Value, g.Content[i+1]
		at := fmt.Sprintf("%s.groups.%s", where, name)
		m := c.mapping(body, groupKeys, at)
		var grp Group
		if m != nil {
			if err := decodeSafe(body, &grp); err != nil {
				c.add(body, "%s: %v", at, err)
			}
			if len(grp.Patterns) == 0 {
				c.add(body, "%s.patterns: at least one pattern is required", at)
			}
			c.types(m["update-types"], grp.UpdateTypes, at)
		}
		grp.Name = name
		u.Groups = append(u.Groups, grp)
	}
}

func (c *checker) schedule(n *yaml.Node, s *Schedule, where string) {
	if c.mapping(n, scheduleKeys, where) == nil || s == nil {
		return
	}
	for _, d := range s.Days {
		if !validDays[d] {
			c.add(n, "%s.days: %q is not one of mon, tue, wed, thu, fri, sat, sun", where, d)
		}
	}
	for name, v := range map[string]string{"from": s.From, "to": s.To} {
		if v != "" && !hhmm.MatchString(v) {
			c.add(n, "%s.%s: %q is not a 24h time such as \"22:00\" (quote it)", where, name, v)
		}
	}
	if (s.From == "") != (s.To == "") {
		c.add(n, "%s: set both from and to, or neither", where)
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			c.add(n, "%s.timezone: unknown IANA time zone %q", where, s.Timezone)
		}
	}
}

// CompileRegex compiles a user supplied RE2 pattern with a size cap.
func CompileRegex(s string) (*regexp.Regexp, error) {
	if len(s) > maxRegexBytes {
		return nil, fmt.Errorf("pattern longer than %d bytes", maxRegexBytes)
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression (Go RE2 syntax): %v", err)
	}
	return re, nil
}
