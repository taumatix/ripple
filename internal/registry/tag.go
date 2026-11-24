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

// TagFloor is the lexically smallest string a tag newer than cur and of cur's shape can
// have, "" when cur is not tracked. Registries list tags in lexical order, which is not
// version order: "1.100.0" is newer than "1.25.3" and sorts before it. So the floor is not
// cur but the smallest string any newer tag of the shape can take, where a component with
// more digits than cur's ("100" against "25") is the lowest it can sort.
func TagFloor(cur string) string {
	m := tagRE.FindStringSubmatch(cur)
	if m == nil {
		return ""
	}
	floor := cur
	comps := strings.Split(m[2], ".")
	for i, c := range comps {
		cand := m[1] + strings.Join(comps[:i], ".")
		if i > 0 {
			cand += "."
		}
		cand += "1" + strings.Repeat("0", len(c))
		if cand < floor {
			floor = cand
		}
	}
	return floor
}

// lastBefore is the `last=` value that makes a registry list from floor on, floor included
// (last is exclusive): the string with floor's final byte lowered by one.
func lastBefore(floor string) string {
	return floor[:len(floor)-1] + string(floor[len(floor)-1]-1)
}
