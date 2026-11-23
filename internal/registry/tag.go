package registry

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var tagRE = regexp.MustCompile(`^(v?)((?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){0,2})(-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

// ParseTag splits an image tag into the semver ripple compares and its shape: the v
// prefix, how many numeric components it spells and its variant suffix. "1.25",
// "1.25.3", "1.25-alpine" and "v1.25" are four shapes; a tag moves only to a newer
// tag of its own shape, so a floating `3.19` stays floating and `-alpine` stays
// alpine. Tags with no leading version (latest, bookworm) or a fourth component are
// not tracked.
func ParseTag(tag string) (sv, shape string, ok bool) {
	m := tagRE.FindStringSubmatch(tag)
	if m == nil {
		return "", "", false
	}
	sv = "v" + m[2]
	suffix := m[3]
	if suffix == "" {
		suffix = "-"
	}
	return sv, fmt.Sprintf("%s%d|%s", m[1], strings.Count(m[2], ".")+1, suffix), true
}

// NewestOfShape is the newest tag of the same shape as cur that is newer than it, or "".
func NewestOfShape(cur string, tags []string) string {
	curSV, curShape, ok := ParseTag(cur)
	if !ok {
		return ""
	}
	best, bestSV := "", curSV
	for _, t := range tags {
		sv, shape, ok := ParseTag(t)
		if ok && shape == curShape && semver.Compare(sv, bestSV) > 0 {
			best, bestSV = t, sv
		}
	}
	return best
}
