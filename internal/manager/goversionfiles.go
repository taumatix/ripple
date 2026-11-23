package manager

import (
	"bytes"
	"path"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// A Go version written outside go.mod follows the go.mod bump, but only where its
// spelling is unambiguous. Each rule matches one whole line shape and replaces only
// the version in it; everything else (a matrix expression, an alias such as
// `stable`, an image digest, a list of versions, a comment) is left as written, so a
// miss costs a line the reviewer edits by hand and a wrong hit never happens.
var (
	versionToken    = `[0-9]+\.[0-9]+(?:\.[0-9]+)?`
	goVersionFileRE = regexp.MustCompile(`^(\s*)(` + versionToken + `)(\s*)$`)
	toolVersionsRE  = regexp.MustCompile(`^(\s*golang\s+)(` + versionToken + `)(\s*(?:#.*)?)$`)
	workflowRE      = regexp.MustCompile(`^(\s*(?:-\s+)?go-version:\s*["']?)(` + versionToken + `)(["']?\s*(?:#.*)?)$`)
	miseGoRE        = regexp.MustCompile(`^(\s*go\s*=\s*["'])(` + versionToken + `)(["']\s*(?:#.*)?)$`)
	tomlSectionRE   = regexp.MustCompile(`^\s*\[+\s*([^\]\s]+)\s*\]+`)
	devcontainerRE  = regexp.MustCompile(`^(\s*"image"\s*:\s*"(?:mcr\.microsoft\.com/devcontainers/go|golang):)(` + versionToken + `)((?:-[A-Za-z0-9._]+)?"\s*,?\s*)$`)
	goWorkGoRE      = regexp.MustCompile(`^(go\s+)(` + versionToken + `)(\s*(?://.*)?)$`)
	goWorkToolRE    = regexp.MustCompile(`^(toolchain\s+go)(` + versionToken + `)(\s*(?://.*)?)$`)
	makeVarRE       = regexp.MustCompile(`^(\s*(?:export\s+)?(?:GO|GOLANG)_VERSION\s*(?:\?=|:=|::=|=)\s*)(` + versionToken + `)(\s*(?:#.*)?)$`)
	dockerFromRE    = regexp.MustCompile(`(?i)^(\s*FROM\s+(?:--platform=\S+\s+)?golang:)(` + versionToken + `)((?:-[A-Za-z0-9._]+)?(?:\s+AS\s+\S+)?\s*)$`)
)

// FollowGoVersion rewrites the Go version in a file that names one next to go.mod:
// `.go-version`, the golang line of `.tool-versions`, `go-version:` in
// .github/workflows/*.yml, `FROM golang:` in Dockerfiles, `go` under `[tools]` in
// `.mise.toml`, the image of a devcontainer.json, the `go` and `toolchain` lines of
// go.work, and a `GO_VERSION` / `GOLANG_VERSION` variable in a Makefile. old is the semver the
// module's toolchain line or go directive had before the bump and target the release
// it moves to. A file moves only when it names the same minor as old and an older
// release than target; a version written without a patch keeps that spelling and
// moves only when the minor does. The second result is false when nothing changed.
func FollowGoVersion(p string, content []byte, old, target string) ([]byte, bool) {
	if !semver.IsValid(old) || !semver.IsValid(target) || semver.Compare(target, old) <= 0 {
		return content, false
	}
	var re, re2 *regexp.Regexp
	whole, mise := false, false
	switch base := path.Base(p); {
	case base == ".go-version":
		re, whole = goVersionFileRE, true
	case base == ".tool-versions":
		re = toolVersionsRE
	case isWorkflow(p):
		re = workflowRE
	case isDockerfile(base):
		re = dockerFromRE
	case base == ".mise.toml" || base == "mise.toml":
		re, mise = miseGoRE, true
	case isDevcontainer(p):
		re = devcontainerRE
	case base == "go.work":
		re, re2 = goWorkGoRE, goWorkToolRE
	case isMakefile(base):
		re = makeVarRE
	default:
		return content, false
	}
	lines := strings.Split(string(content), "\n")
	if whole {
		// A version file names one version; two lines means something else is going on.
		n := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				n++
			}
		}
		if n != 1 {
			return content, false
		}
	}
	changed := false
	section := ""
	for i, l := range lines {
		cr := strings.HasSuffix(l, "\r")
		l = strings.TrimSuffix(l, "\r")
		if mise {
			if sm := tomlSectionRE.FindStringSubmatch(l); sm != nil {
				section = sm[1]
				continue
			}
			if section != "tools" {
				continue
			}
		}
		m := re.FindStringSubmatch(l)
		if m == nil && re2 != nil {
			m = re2.FindStringSubmatch(l)
		}
		if m == nil {
			continue
		}
		nv, ok := followVersion(m[2], old, target)
		if !ok {
			continue
		}
		l = m[1] + nv + m[3]
		if cr {
			l += "\r"
		}
		lines[i] = l
		changed = true
	}
	if !changed {
		return content, false
	}
	return bytes.NewBufferString(strings.Join(lines, "\n")).Bytes(), true
}

// HoldsGoVersion reports whether FollowGoVersion looks at the file at p at all, so a
// caller reads only those.
func HoldsGoVersion(p string) bool {
	base := path.Base(p)
	return base == ".go-version" || base == ".tool-versions" || isWorkflow(p) || isDockerfile(base) ||
		base == ".mise.toml" || base == "mise.toml" || base == "go.work" || isDevcontainer(p) || isMakefile(base)
}

// HoldsGoVersionRepoWide reports whether the file at p is one a toolchain bump reads
// wherever it sits in the repo, not only beside the module: workflows and devcontainer
// configs belong to the repo, not to one module.
func HoldsGoVersionRepoWide(p string) bool {
	return isWorkflow(p) || isDevcontainer(p)
}

func isMakefile(base string) bool {
	return base == "Makefile" || base == "GNUmakefile" || base == "makefile" || strings.HasSuffix(base, ".mk")
}

// isDevcontainer matches .devcontainer.json and any devcontainer.json under a root
// .devcontainer directory (the spec allows one level of subdirectories).
func isDevcontainer(p string) bool {
	if p == ".devcontainer.json" {
		return true
	}
	return path.Base(p) == "devcontainer.json" && strings.HasPrefix(p, ".devcontainer/")
}

func isDockerfile(base string) bool {
	return base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || strings.HasSuffix(base, ".Dockerfile")
}

func isWorkflow(p string) bool {
	dir, base := path.Split(p)
	if dir != ".github/workflows/" {
		return false
	}
	ext := path.Ext(base)
	return ext == ".yml" || ext == ".yaml"
}

func followVersion(cur, old, target string) (string, bool) {
	cv := "v" + cur
	if semver.MajorMinor(cv) != semver.MajorMinor(old) {
		return "", false
	}
	if strings.Count(cur, ".") == 1 {
		if semver.MajorMinor(target) == semver.MajorMinor(cv) {
			return "", false
		}
		return strings.TrimPrefix(semver.MajorMinor(target), "v"), true
	}
	if semver.Compare(cv, target) >= 0 {
		return "", false
	}
	return strings.TrimPrefix(target, "v"), true
}
