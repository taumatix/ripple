package manager

import (
	"path"
	"strings"

	"github.com/taumatix/ripple/internal/pypi"
)

// PipPythonFiles are the files, in a manifest's directory and each parent, that can
// declare the Python the repo supports, nearest directory first.
var PipPythonFiles = []string{"pyproject.toml", "setup.cfg", "Pipfile", ".python-version"}

// PipPythonFloor reads the lowest Python a file declares support for: `[project]
// requires-python`, a setup.cfg's `[options] python_requires`, Poetry's `python` dependency, a Pipfile's `[requires]
// python_version`/`python_full_version`, or a `.python-version` first line. floor is ""
// when the file declares none, or none ripple can read with certainty (an upper bound
// only, an interpreter that is not CPython).
func PipPythonFloor(name string, content []byte) (floor string) {
	switch path.Base(name) {
	case "pyproject.toml", "Pipfile":
		leaves, err := tomlScan(string(content))
		if err != nil {
			return ""
		}
		for _, key := range []string{"project.requires-python", "tool.poetry.dependencies.python", "requires.python_full_version", "requires.python_version"} {
			for _, l := range leaves {
				if l.isStr && strings.Join(l.path, ".") == key {
					if f := PipPythonSpecFloor(l.str); f != "" {
						return f
					}
				}
			}
		}
	case "setup.cfg":
		return pipSetupCfgPythonFloor(string(content))
	case ".python-version":
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(tomlStripComment(line))
			if line == "" {
				continue
			}
			if _, ok := pypi.Parse(line); ok {
				return line
			}
			return ""
		}
	}
	return ""
}

// PipPythonSpecFloor is the lowest version a Python specifier allows: the greatest of
// its lower bounds (`>=3.9,<4`, `~=3.9`, Poetry's `^3.9`, `~3.9`, `==3.11.*`, `3.11`),
// or the smallest floor of the `||` alternatives Poetry allows. "" when a clause or
// alternative has no lower bound (`<4`), since the lowest interpreter is then unknown.
func PipPythonSpecFloor(spec string) string {
	var best pypi.Version
	var out string
	for _, alt := range strings.Split(spec, "||") {
		f := pipAltFloor(alt)
		if f == "" {
			return ""
		}
		v, _ := pypi.Parse(f)
		if out == "" || pypi.Compare(v, best) < 0 {
			best, out = v, f
		}
	}
	return out
}

func pipAltFloor(alt string) string {
	var best pypi.Version
	var out string
	for _, c := range strings.FieldsFunc(alt, func(r rune) bool { return r == ',' }) {
		c = strings.TrimSpace(c)
		var text string
		switch {
		case strings.HasPrefix(c, ">="), strings.HasPrefix(c, "~="), strings.HasPrefix(c, "=="):
			text = c[2:]
		case strings.HasPrefix(c, ">"), strings.HasPrefix(c, "^"), strings.HasPrefix(c, "~"):
			text = c[1:]
		case strings.HasPrefix(c, "<"), strings.HasPrefix(c, "!"), c == "*", c == "":
			continue
		default:
			text = c
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), ".*")
		v, ok := pypi.Parse(text)
		if !ok {
			return ""
		}
		if out == "" || pypi.Compare(v, best) > 0 {
			best, out = v, text
		}
	}
	return out
}

// pipSetupCfgPythonFloor reads `python_requires` from a setup.cfg's `[options]` (the
// value is on the key's line; setup.py is never read).
func pipSetupCfgPythonFloor(src string) string {
	section := ""
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if m := pipCfgSectionRE.FindStringSubmatch(line); m != nil {
			section = strings.ToLower(m[1])
			continue
		}
		if section != "options" {
			continue
		}
		if m := pipCfgKeyRE.FindStringSubmatch(line); m != nil && strings.EqualFold(m[1], "python_requires") {
			return PipPythonSpecFloor(strings.TrimSpace(stripRequirementsComment(m[2])))
		}
	}
	return ""
}
