// Package pypi reads project versions from PyPI's JSON API, anonymously, and orders
// them by PEP 440 (docs/vendor/pip).
package pypi

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed PEP 440 version.
type Version struct {
	Epoch   int
	Release []int
	// Pre is "a", "b", "rc" or "".
	Pre    string
	PreN   int
	Post   bool
	PostN  int
	Dev    bool
	DevN   int
	Local  string
	Source string // the text that was parsed
}

// Appendix B of PEP 440, with the alternate spellings the normalization rules allow.
var versionRE = regexp.MustCompile(`(?i)^\s*v?(?:(\d+)!)?(\d+(?:\.\d+)*)` +
	`(?:[-_.]?(alpha|a|beta|b|preview|pre|c|rc)[-_.]?(\d+)?)?` +
	`(?:(?:-(\d+))|(?:[-_.]?(post|rev|r)[-_.]?(\d+)?))?` +
	`(?:[-_.]?(dev)[-_.]?(\d+)?)?` +
	`(?:\+([a-z0-9]+(?:[-_.][a-z0-9]+)*))?\s*$`)

// Parse reads s as a PEP 440 version.
func Parse(s string) (Version, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	v := Version{Source: s}
	v.Epoch, _ = strconv.Atoi(m[1])
	for _, p := range strings.Split(m[2], ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, false
		}
		v.Release = append(v.Release, n)
	}
	if m[3] != "" {
		switch strings.ToLower(m[3]) {
		case "alpha", "a":
			v.Pre = "a"
		case "beta", "b":
			v.Pre = "b"
		default:
			v.Pre = "rc"
		}
		v.PreN, _ = strconv.Atoi(m[4])
	}
	if m[5] != "" || m[6] != "" {
		v.Post = true
		v.PostN, _ = strconv.Atoi(m[5] + m[7])
	}
	if m[8] != "" {
		v.Dev = true
		v.DevN, _ = strconv.Atoi(m[9])
	}
	v.Local = strings.ToLower(m[10])
	return v, true
}

// IsRelease reports whether v is a final or post release: no pre-release, no dev
// release and no local segment.
func (v Version) IsRelease() bool { return v.Pre == "" && !v.Dev && v.Local == "" }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Compare orders two versions: -1, 0 or 1. A local segment sorts after the same
// public version.
func Compare(a, b Version) int {
	if c := cmpInt(a.Epoch, b.Epoch); c != 0 {
		return c
	}
	n := len(a.Release)
	if len(b.Release) > n {
		n = len(b.Release)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a.Release) {
			x = a.Release[i]
		}
		if i < len(b.Release) {
			y = b.Release[i]
		}
		if c := cmpInt(x, y); c != 0 {
			return c
		}
	}
	// Segment keys: dev-only sorts before any pre-release; no pre/post/dev is the
	// final release; post sorts after it.
	if c := cmpInt(preRank(a), preRank(b)); c != 0 {
		return c
	}
	if c := cmpInt(a.PreN, b.PreN); c != 0 && a.Pre != "" {
		return c
	}
	if c := cmpInt(postRank(a), postRank(b)); c != 0 {
		return c
	}
	if c := cmpInt(devRank(a), devRank(b)); c != 0 {
		return c
	}
	if c := cmpInt(a.DevN, b.DevN); c != 0 && a.Dev {
		return c
	}
	return strings.Compare(a.Local, b.Local)
}

// preRank: a release with only a dev segment (1.0.dev1) sorts below every
// pre-release of 1.0; a pre-release sorts below the final.
func preRank(v Version) int {
	switch {
	case v.Pre == "" && v.Dev && !v.Post:
		return -1
	case v.Pre == "a":
		return 0
	case v.Pre == "b":
		return 1
	case v.Pre == "rc":
		return 2
	}
	return 3
}

func postRank(v Version) int {
	if !v.Post {
		return -1
	}
	return v.PostN
}

// devRank: within the same pre/post key a dev release sorts before the one without.
func devRank(v Version) int {
	if v.Dev {
		return 0
	}
	return 1
}

// NormalizeName is the PEP 503 normalized project name.
func NormalizeName(name string) string {
	return strings.ToLower(nameSep.ReplaceAllString(name, "-"))
}

var nameSep = regexp.MustCompile(`[-_.]+`)
