// Package advisory reads OSV (https://osv.dev) records for the Go ecosystem and says,
// for one module at one version, whether an advisory affects it and which version
// fixes it. It speaks the OSV schema as vendored in docs/vendor/osv.
package advisory

import (
	"sort"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Ecosystem is the OSV ecosystem name for Go modules; the package name is the module path.
const Ecosystem = "Go"

// Vuln is the part of an OSV record ripple reads.
type Vuln struct {
	ID          string   `json:"id"`
	Aliases     []string `json:"aliases"`
	Summary     string   `json:"summary"`
	WithdrawnAt string   `json:"withdrawn"`
	Severity    []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
	Affected []Affected `json:"affected"`
}

// Affected is one package entry of a record.
type Affected struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Ranges   []Range  `json:"ranges"`
	Versions []string `json:"versions"`
}

// Range is one timeline of events.
type Range struct {
	Type   string  `json:"type"`
	Events []Event `json:"events"`
}

// Event is one object of a range's timeline; exactly one field is set.
type Event struct {
	Introduced   string `json:"introduced"`
	Fixed        string `json:"fixed"`
	LastAffected string `json:"last_affected"`
	Limit        string `json:"limit"`
}

// Withdrawn reports whether the record was withdrawn; a withdrawn advisory proposes nothing.
func (v *Vuln) Withdrawn() bool { return v.WithdrawnAt != "" }

// URL is the human page of the record.
func (v *Vuln) URL() string { return "https://osv.dev/vulnerability/" + v.ID }

// SeverityLabel is the GitHub advisory rating (LOW, MODERATE, HIGH, CRITICAL) when the
// record carries one; Go database records do not.
func (v *Vuln) SeverityLabel() string { return v.DatabaseSpecific.Severity }

// CVSS is the first CVSS v3 vector string of the record, or "".
func (v *Vuln) CVSS() string {
	for _, s := range v.Severity {
		if s.Type == "CVSS_V3" {
			return s.Score
		}
	}
	return ""
}

// isModule reports whether an entry names a go.mod module: the Go ecosystem also
// lists "stdlib" and "toolchain", which are not.
func (a *Affected) isModule() bool {
	n := a.Package.Name
	return a.Package.Ecosystem == Ecosystem && n != "" && n != "stdlib" && n != "toolchain"
}

// Modules lists the distinct module paths the record names.
func (v *Vuln) Modules() []string {
	var out []string
	seen := map[string]bool{}
	for i := range v.Affected {
		a := &v.Affected[i]
		if a.isModule() && !seen[a.Package.Name] {
			seen[a.Package.Name] = true
			out = append(out, a.Package.Name)
		}
	}
	return out
}

// isToolchain reports whether an entry names the Go standard library or the toolchain.
func (a *Affected) isToolchain() bool {
	n := a.Package.Name
	return a.Package.Ecosystem == Ecosystem && (n == "stdlib" || n == "toolchain")
}

// HasToolchain reports whether the record names stdlib or toolchain.
func (v *Vuln) HasToolchain() bool {
	for i := range v.Affected {
		if v.Affected[i].isToolchain() {
			return true
		}
	}
	return false
}

// Verdict is what a record says about one package at one version.
//
// Affected with neither Fixed nor Pseudo set means the advisory has no fix to move to
// (unfixed, or only a last_affected ceiling).
type Verdict struct {
	// Affected reports whether the version lies in an affected range.
	Affected bool
	// Fixed is the lowest release (not pseudo-version) above the version that fixes it.
	Fixed string
	// Pseudo is the lowest pseudo-version fix above the version; set even when Fixed is,
	// because the caller decides whether a pseudo-version is any use.
	Pseudo string
}

// FixFor says which release fixes the advisory for module at cur (a go.mod version
// such as "v1.2.3"). ok is false when the advisory does not affect cur, or affects it
// with no release to move to (unfixed, only a last_affected ceiling, or only a
// pseudo-version fix: see Assess).
//
// The affected test is the Evaluation algorithm of the OSV schema. A record with
// several ranges (backported fixes) gives the fix of the range cur lies in, and of
// those the lowest, so a v1 user is moved to the v1 backport, not to v2.
func (v *Vuln) FixFor(mod, cur string) (fixed string, ok bool) {
	a := v.Assess(mod, cur)
	return a.Fixed, a.Fixed != ""
}

// Assess evaluates the record for module mod at cur.
func (v *Vuln) Assess(mod, cur string) Verdict {
	return v.assess(cur, func(a *Affected) bool { return a.isModule() && a.Package.Name == mod })
}

// AssessToolchain evaluates the record for the Go toolchain at cur (a version such as
// "v1.25.3"), reading the stdlib and toolchain entries.
func (v *Vuln) AssessToolchain(cur string) Verdict {
	return v.assess(cur, func(a *Affected) bool { return a.isToolchain() })
}

func (v *Vuln) assess(cur string, match func(*Affected) bool) Verdict {
	var out Verdict
	if !semver.IsValid(cur) {
		return out
	}
	lower := func(best, f string) string {
		if best == "" || semver.Compare(f, best) < 0 {
			return f
		}
		return best
	}
	for i := range v.Affected {
		a := &v.Affected[i]
		if !match(a) {
			continue
		}
		for _, r := range a.Ranges {
			if r.Type != "SEMVER" {
				continue
			}
			hit, fixed, pseudo := r.assess(cur)
			if !hit {
				continue
			}
			out.Affected = true
			if fixed != "" {
				out.Fixed = lower(out.Fixed, fixed)
			}
			if pseudo != "" {
				out.Pseudo = lower(out.Pseudo, pseudo)
			}
		}
	}
	return out
}

// sv turns an OSV SEMVER value (no leading "v") into a Go semantic version.
func sv(s string) string { return "v" + s }

// assess evaluates one range at cur: whether cur is affected, the lowest release fix
// above it and the lowest pseudo-version fix above it.
func (r Range) assess(cur string) (affected bool, fixed, pseudo string) {
	hasLimit, belowLimit := false, false
	for _, e := range r.Events {
		if e.Limit != "" {
			hasLimit = true
			if e.Limit == "*" || semver.Compare(cur, sv(e.Limit)) < 0 {
				belowLimit = true
			}
		}
	}
	if hasLimit && !belowLimit {
		return false, "", ""
	}
	evs := append([]Event(nil), r.Events...)
	key := func(e Event) string {
		switch {
		case e.Introduced == "0":
			return "v0.0.0-0" // sorts before every release and prerelease of 0.0.0
		case e.Introduced != "":
			return sv(e.Introduced)
		case e.Fixed != "":
			return sv(e.Fixed)
		case e.LastAffected != "":
			return sv(e.LastAffected)
		}
		return ""
	}
	sort.SliceStable(evs, func(i, j int) bool { return semver.Compare(key(evs[i]), key(evs[j])) < 0 })
	vulnerable := false
	for _, e := range evs {
		switch {
		case e.Introduced != "" && (e.Introduced == "0" || semver.Compare(cur, sv(e.Introduced)) >= 0):
			vulnerable = true
		case e.Fixed != "" && semver.Compare(cur, sv(e.Fixed)) >= 0:
			vulnerable = false
		case e.LastAffected != "" && semver.Compare(cur, sv(e.LastAffected)) > 0:
			vulnerable = false
		}
	}
	if !vulnerable {
		return false, "", ""
	}
	for _, e := range evs {
		if e.Fixed == "" || !semver.IsValid(sv(e.Fixed)) || semver.Compare(sv(e.Fixed), cur) <= 0 {
			continue
		}
		f := sv(e.Fixed)
		best := &fixed
		if module.IsPseudoVersion(f) {
			best = &pseudo
		}
		if *best == "" || semver.Compare(f, *best) < 0 {
			*best = f
		}
	}
	return true, fixed, pseudo
}
