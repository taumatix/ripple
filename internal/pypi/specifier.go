package pypi

import (
	"regexp"
	"strings"
)

var clauseRE = regexp.MustCompile(`^(===|==|~=|!=|<=|>=|<|>)\s*(\S+)$`)

// Satisfies reports whether v meets every clause of a comma-separated PEP 440
// specifier (`>=1.2,<2`, `!=1.5.*`). ok is false when the specifier has a clause
// this function does not understand (`===`, a malformed version), so a caller can
// leave the dependency alone. Pre-release matching rules are not applied: ripple only
// asks about final and post releases.
func Satisfies(spec string, v Version) (satisfied, ok bool) {
	v.Local = ""
	for _, part := range strings.Split(spec, ",") {
		m := clauseRE.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil || m[1] == "===" {
			return false, false
		}
		op, text := m[1], m[2]
		prefix := strings.HasSuffix(text, ".*")
		text = strings.TrimSuffix(text, ".*")
		w, good := Parse(text)
		if !good || (prefix && op != "==" && op != "!=") {
			return false, false
		}
		var pass bool
		switch op {
		case "==", "!=":
			if prefix {
				pass = hasPrefix(v, w)
			} else {
				w.Local = ""
				pass = Compare(v, w) == 0
			}
			if op == "!=" {
				pass = !pass
			}
		case "<":
			// <V excludes the pre-releases of V, which sort below it, unless V is one.
			pass = Compare(v, w) < 0 && !((v.Pre != "" || v.Dev) && w.Pre == "" && !w.Dev && sameRelease(v, w))
		case "<=":
			pass = Compare(v, w) <= 0
		case ">":
			pass = Compare(v, w) > 0 && !(v.Post && !w.Post && sameRelease(v, w))
		case ">=":
			pass = Compare(v, w) >= 0
		case "~=":
			if len(w.Release) < 2 {
				return false, false
			}
			pre := Version{Epoch: w.Epoch, Release: w.Release[:len(w.Release)-1]}
			pass = Compare(v, w) >= 0 && hasPrefix(v, pre)
		}
		if !pass {
			return false, true
		}
	}
	return true, true
}

// hasPrefix is the `==X.*` match: same epoch and X's release segments lead v's.
func hasPrefix(v, p Version) bool {
	if v.Epoch != p.Epoch {
		return false
	}
	for i, n := range p.Release {
		x := 0
		if i < len(v.Release) {
			x = v.Release[i]
		}
		if x != n {
			return false
		}
	}
	return true
}

func sameRelease(a, b Version) bool {
	return a.Epoch == b.Epoch && Compare(Version{Epoch: a.Epoch, Release: a.Release}, Version{Epoch: b.Epoch, Release: b.Release}) == 0
}
