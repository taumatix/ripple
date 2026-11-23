// Package advisory reads OSV (https://osv.dev) records for the Go ecosystem and says,
// for one module at one version, whether an advisory affects it and which version
// fixes it. It speaks the OSV schema as vendored in docs/vendor/osv.
package advisory

import (
	"sort"

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

// FixFor says which version fixes the advisory for module at cur (a go.mod version
// such as "v1.2.3"). ok is false when the advisory does not affect cur, or affects it
// with no fixed event to move to (unfixed, or only a last_affected ceiling).
//
// The affected test is the Evaluation algorithm of the OSV schema. A record with
// several ranges (backported fixes) gives the fix of the range cur lies in, and of
// those the lowest, so a v1 user is moved to the v1 backport, not to v2.
func (v *Vuln) FixFor(mod, cur string) (fixed string, ok bool) {
	if !semver.IsValid(cur) {
		return "", false
	}
	best := ""
	for i := range v.Affected {
		a := &v.Affected[i]
		if !a.isModule() || a.Package.Name != mod {
			continue
		}
		for _, r := range a.Ranges {
			if r.Type != "SEMVER" {
				continue
			}
			if f, hit := r.fixFor(cur); hit && (best == "" || semver.Compare(f, best) < 0) {
				best = f
			}
		}
	}
	return best, best != ""
}

// sv turns an OSV SEMVER value (no leading "v") into a Go semantic version.
func sv(s string) string { return "v" + s }

// fixFor evaluates one range at cur and returns the lowest fixed event above cur.
func (r Range) fixFor(cur string) (string, bool) {
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
		return "", false
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
		return "", false
	}
	best := ""
	for _, e := range evs {
		if e.Fixed == "" || !semver.IsValid(sv(e.Fixed)) || semver.Compare(sv(e.Fixed), cur) <= 0 {
			continue
		}
		if best == "" || semver.Compare(sv(e.Fixed), best) < 0 {
			best = sv(e.Fixed)
		}
	}
	return best, best != ""
}
