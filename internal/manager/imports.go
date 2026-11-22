package manager

import (
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/taumatix/ripple/internal/platform"
)

// RewriteGoImports rewrites, in every .go file owned by the module whose go.mod is
// goModFile, the import paths under oldPath so they sit under newPath. Files are
// parsed with go/parser and only the string literal of an import spec is replaced,
// byte for byte, so comments, string constants, alias names and "." and "_" imports
// are untouched. It must run on the go.mod as it was before the migration:
// requirements of the same module that live below oldPath keep the packages they
// own. It returns the files it changed.
func RewriteGoImports(ws platform.Workspace, goModFile, oldPath, newPath string, goMod []byte) ([]string, error) {
	f, err := modfile.ParseLax(goModFile, goMod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", goModFile, err)
	}
	var nested []string // other modules required below oldPath: they own their packages
	for _, r := range f.Require {
		if r.Mod.Path != oldPath && strings.HasPrefix(r.Mod.Path, oldPath+"/") {
			nested = append(nested, r.Mod.Path)
		}
	}
	root := path.Dir(goModFile)
	var gofiles []string
	modRoots := map[string]bool{}
	err = ws.Walk(func(p string) error {
		switch {
		case path.Base(p) == "go.mod":
			modRoots[path.Dir(p)] = true
		case strings.HasSuffix(p, ".go"):
			gofiles = append(gofiles, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(gofiles)
	var changed []string
	for _, p := range gofiles {
		if owner(p, modRoots) != root {
			continue
		}
		src, err := ws.ReadFile(p)
		if err != nil {
			return changed, err
		}
		out, n, err := rewriteImports(p, src, oldPath, newPath, nested)
		if err != nil {
			return changed, err
		}
		if n == 0 {
			continue
		}
		if err := ws.WriteFile(p, out); err != nil {
			return changed, err
		}
		changed = append(changed, p)
	}
	return changed, nil
}

// owner is the directory of the nearest go.mod above file p.
func owner(p string, modRoots map[string]bool) string {
	for d := path.Dir(p); ; d = path.Dir(d) {
		if modRoots[d] {
			return d
		}
		if d == "." || d == "/" {
			return "."
		}
	}
}

func underPath(s, base string) bool { return s == base || strings.HasPrefix(s, base+"/") }

func mapImport(s, oldPath, newPath string, nested []string) (string, bool) {
	if !underPath(s, oldPath) || underPath(s, newPath) {
		return "", false
	}
	for _, n := range nested {
		if underPath(s, n) {
			return "", false
		}
	}
	if isMajorElem(strings.TrimPrefix(s[len(oldPath):], "/")) {
		return "", false // another major of the same module, not a package of this one
	}
	return newPath + s[len(oldPath):], true
}

// isMajorElem reports whether the first element of rest looks like /vN (N >= 2).
func isMajorElem(rest string) bool {
	el, _, _ := strings.Cut(rest, "/")
	if len(el) < 2 || el[0] != 'v' || el == "v1" {
		return false
	}
	for _, c := range el[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func rewriteImports(name string, src []byte, oldPath, newPath string, nested []string) ([]byte, int, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", name, err)
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	for _, spec := range af.Imports {
		lit := spec.Path
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		nv, ok := mapImport(val, oldPath, newPath, nested)
		if !ok {
			continue
		}
		q := string(lit.Value[0]) // keep "..." or `...`
		edits = append(edits, edit{fset.Position(lit.Pos()).Offset, fset.Position(lit.End()).Offset, q + nv + q})
	}
	if len(edits) == 0 {
		return src, 0, nil
	}
	out := make([]byte, 0, len(src)+len(edits)*4)
	prev := 0
	for _, e := range edits {
		out = append(out, src[prev:e.start]...)
		out = append(out, e.text...)
		prev = e.end
	}
	out = append(out, src[prev:]...)
	return out, len(edits), nil
}
