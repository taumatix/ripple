package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// A Go release directory is named go<version> and holds bin/go, the layout of an
// unpacked go.dev download renamed after its version (go1.25.3/, or go1.25/ for a
// minor only). Nothing is downloaded: an operator installs the releases they want.
var goReleaseDir = regexp.MustCompile(`^go(1\.[0-9]+)(?:\.([0-9]+))?$`)
var goLineVersion = regexp.MustCompile(`^(?:go)?(1\.[0-9]+)(?:\.([0-9]+))?$`)

// FindGo returns the go binary under dir for the Go release a go.mod names, and the
// release's version without the "go" prefix. The toolchain line wins and must match
// exactly; without one the go line selects the newest installed patch of its minor
// that is not older than the line. Both empty when dir is empty or holds no such
// release, so the caller falls back to the server's own Go.
func FindGo(dir, goLine, toolchainLine string) (bin, release string) {
	if dir == "" {
		return "", ""
	}
	exact := toolchainLine != ""
	line := goLine
	if exact {
		line = toolchainLine
	}
	m := goLineVersion.FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	minor, patch := m[1], -1
	if m[2] != "" {
		patch, _ = strconv.Atoi(m[2])
	}
	if exact {
		patch = max(patch, 0)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	best := -2
	for _, e := range entries {
		d := goReleaseDir.FindStringSubmatch(e.Name())
		if d == nil || d[1] != minor {
			continue
		}
		have := 0
		if d[2] != "" {
			have, _ = strconv.Atoi(d[2])
		}
		switch {
		case exact && have != patch:
			continue
		case !exact && have < max(patch, 0):
			continue
		case have <= best:
			continue
		}
		cand := filepath.Join(dir, e.Name(), "bin", "go")
		if st, err := os.Stat(cand); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		best, bin, release = have, cand, e.Name()[len("go"):]
	}
	return bin, release
}
