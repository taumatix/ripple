// Package glob is the one pattern syntax used in .ripple.yaml: * matches any run of
// characters (including "/"), everything else is literal.
package glob

import (
	"regexp"
	"strings"
)

func Match(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$").MatchString(s)
}
