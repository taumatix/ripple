package manager

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/taumatix/ripple/internal/platform"
)

// GoRewrite is what RewriteGo did: the files it changed, the files it could not
// parse, and the places it found a reference to the old path that it could not
// safely rewrite and left as it was ("file:line").
type GoRewrite struct {
	Changed, Skipped, Ambiguous []string
}

// RewriteGoImports is RewriteGo without the ambiguous report.
func RewriteGoImports(ws platform.Workspace, goModFile, oldPath, newPath string, goMod []byte) (changed, skipped []string, err error) {
	r, err := RewriteGo(ws, goModFile, oldPath, newPath, goMod)
	return r.Changed, r.Skipped, err
}

// RewriteGo rewrites, in every .go file owned by the module whose go.mod is
// goModFile, the references to packages under oldPath so they sit under newPath:
// import specs, canonical import comments (package p // import "...") and the
// package argument of "go run" in //go:generate directives. Files are parsed with
// go/parser and only the quoted path (or the one directive word) is replaced, byte
// for byte, so other comments, string constants, alias names and "." and "_"
// imports are untouched. A directive whose reference is not plainly the package
// of a "go run" (a version suffix, quoting, a variable, another command, a
// program argument) is left alone and reported in Ambiguous. It must run on the go.mod as it was before the migration:
// requirements of the same module that live below oldPath keep the packages they
// own. Directories the go tool ignores (testdata, and names starting with "_" or
// ".") are left alone. A file that does not parse is skipped, not fatal: it is
// returned in skipped so the caller can tell a human. It returns the files it
// changed.
func RewriteGo(ws platform.Workspace, goModFile, oldPath, newPath string, goMod []byte) (res GoRewrite, err error) {
	return RewriteGoWith(ws, goModFile, oldPath, newPath, goMod, GoRewriteOptions{})
}

// GoRewriteOptions are the opt-in extensions of RewriteGo; the zero value is RewriteGo.
type GoRewriteOptions struct {
	// Extended also rewrites the //go:generate forms that are not a plain "go run
	// <package>": "go run <package>@<version>" (the version becomes NewVersion when it is
	// a full semantic version, and stays for latest, upgrade and patch) and
	// "go tool <package>", and reads the directives of a file whose body does not parse
	// the way cmd/go does, as lines that start at column 1.
	Extended bool
	// NewVersion is the version of the new module path being bumped to.
	NewVersion string
}

// RewriteGoWith is RewriteGo with the extensions in opts.
func RewriteGoWith(ws platform.Workspace, goModFile, oldPath, newPath string, goMod []byte, opts GoRewriteOptions) (res GoRewrite, err error) {
	f, err := modfile.ParseLax(goModFile, goMod, nil)
	if err != nil {
		return res, fmt.Errorf("parse %s: %w", goModFile, err)
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
		case strings.HasSuffix(p, ".go") && !ignoredByGo(p):
			gofiles = append(gofiles, p)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	sort.Strings(gofiles)
	for _, p := range gofiles {
		if owner(p, modRoots) != root {
			continue
		}
		src, err := ws.ReadFile(p)
		if err != nil {
			return res, err
		}
		out, n, amb, err := rewriteGoOpts(p, src, oldPath, newPath, nested, opts)
		if err != nil {
			res.Skipped = append(res.Skipped, p)
			continue
		}
		res.Ambiguous = append(res.Ambiguous, amb...)
		if n == 0 {
			continue
		}
		if err := ws.WriteFile(p, out); err != nil {
			return res, err
		}
		res.Changed = append(res.Changed, p)
	}
	return res, nil
}

// ignoredByGo reports whether the go tool skips the directory chain of p: testdata,
// or a directory whose name starts with "_" or ".".
func ignoredByGo(p string) bool {
	dirs := strings.Split(path.Dir(p), "/")
	for _, d := range dirs {
		if d == "." && len(dirs) == 1 {
			continue
		}
		if d == "testdata" || strings.HasPrefix(d, "_") || strings.HasPrefix(d, ".") {
			return true
		}
	}
	return false
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
	out, n, _, err := rewriteGo(name, src, oldPath, newPath, nested)
	return out, n, err
}

type edit struct {
	start, end int
	text       string
}

// rewriteGo returns src with every safe reference to oldPath moved to newPath, how
// many edits it made, and the "name:line" of references it left alone. A file whose
// body does not parse still gets its imports and import comment rewritten when its
// header parses; its directives are then not examined, which is reported.
func rewriteGo(name string, src []byte, oldPath, newPath string, nested []string) ([]byte, int, []string, error) {
	return rewriteGoOpts(name, src, oldPath, newPath, nested, GoRewriteOptions{})
}

func rewriteGoOpts(name string, src []byte, oldPath, newPath string, nested []string, opts GoRewriteOptions) ([]byte, int, []string, error) {
	fset := token.NewFileSet()
	full := true
	af, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		full = false
		fset = token.NewFileSet()
		af, err = parser.ParseFile(fset, name, src, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("parse %s: %w", name, err)
		}
	}
	var edits []edit
	var ambiguous []string
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
	rewrite := func(s string) (string, bool) { return mapImport(s, oldPath, newPath, nested) }
	for _, cg := range af.Comments {
		for _, c := range cg.List {
			if e, ok := importCommentEdit(fset, af, c, rewrite); ok {
				edits = append(edits, e)
			}
			if !full {
				continue
			}
			if fset.Position(c.Pos()).Column != 1 {
				continue
			}
			es, amb := generateEdits(c.Text, fset.Position(c.Pos()).Offset, oldPath, rewrite, opts)
			edits = append(edits, es...)
			if amb {
				ambiguous = append(ambiguous, fmt.Sprintf("%s:%d", name, fset.Position(c.Pos()).Line))
			}
		}
	}
	if !full && opts.Extended {
		es, amb := scanDirectives(name, src, oldPath, rewrite, opts)
		edits = append(edits, es...)
		ambiguous = append(ambiguous, amb...)
	} else if !full && bytes.Contains(src, []byte("//go:generate")) {
		ambiguous = append(ambiguous, name+" (go:generate lines not examined: file does not parse)")
	}
	if len(edits) == 0 {
		return src, 0, ambiguous, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	out := make([]byte, 0, len(src)+len(edits)*4)
	prev := 0
	for _, e := range edits {
		out = append(out, src[prev:e.start]...)
		out = append(out, e.text...)
		prev = e.end
	}
	out = append(out, src[prev:]...)
	return out, len(edits), ambiguous, nil
}

// importCommentEdit finds the canonical import comment: a comment that is exactly
// import "path", on the line of the package clause and before any declaration.
func importCommentEdit(fset *token.FileSet, af *ast.File, c *ast.Comment, rewrite func(string) (string, bool)) (edit, bool) {
	if c.Pos() < af.Name.End() || fset.Position(c.Pos()).Line != fset.Position(af.Name.End()).Line {
		return edit{}, false
	}
	if len(af.Decls) > 0 && af.Decls[0].Pos() < c.Pos() {
		return edit{}, false
	}
	var inner string
	switch {
	case strings.HasPrefix(c.Text, "//"):
		inner = c.Text[2:]
	case strings.HasPrefix(c.Text, "/*") && strings.HasSuffix(c.Text, "*/") && len(c.Text) >= 4:
		inner = c.Text[2 : len(c.Text)-2]
	default:
		return edit{}, false
	}
	inner = strings.TrimSpace(inner)
	lit, ok := strings.CutPrefix(inner, "import")
	if !ok || lit == "" || (lit[0] != ' ' && lit[0] != '\t') {
		return edit{}, false
	}
	lit = strings.TrimSpace(lit)
	val, err := strconv.Unquote(lit)
	if err != nil || lit[0] != '"' {
		return edit{}, false
	}
	nv, ok := rewrite(val)
	if !ok {
		return edit{}, false
	}
	off := strings.Index(c.Text, lit)
	start := fset.Position(c.Pos()).Offset + off
	return edit{start, start + len(lit), `"` + nv + `"`}, true
}

// goRunValueFlags take a separate value; goRunBoolFlags do not. Any other flag
// before the package makes the directive ambiguous.
var (
	goRunValueFlags = map[string]bool{"-tags": true, "-mod": true, "-o": true, "-C": true, "-exec": true,
		"-p": true, "-ldflags": true, "-gcflags": true, "-asmflags": true, "-gccgoflags": true,
		"-pkgdir": true, "-toolexec": true, "-modfile": true, "-overlay": true, "-pgo": true, "-buildmode": true, "-compiler": true}
	goRunBoolFlags = map[string]bool{"-race": true, "-msan": true, "-asan": true, "-a": true, "-n": true,
		"-x": true, "-v": true, "-work": true, "-trimpath": true, "-linkshared": true, "-modcacherw": true}
)

type word struct {
	start, end int // offsets in the comment text
	text       string
	plain      bool // no quote, escape or variable: the text is what go generate passes on
}

// splitWords tokenises a go:generate argument line the way cmd/go does: words are
// separated by spaces and tabs, and a double-quoted section (Go string syntax)
// belongs to the word it appears in.
func splitWords(s string) []word {
	var ws []word
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		start, plain := i, true
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			switch s[i] {
			case '"':
				plain = false
				i++
				for i < len(s) && s[i] != '"' {
					if s[i] == '\\' && i+1 < len(s) {
						i++
					}
					i++
				}
			case '$', '\\', '\'':
				plain = false
			}
			i++
		}
		if i > len(s) {
			i = len(s)
		}
		ws = append(ws, word{start, i, s[start:i], plain})
	}
	return ws
}

// mentions reports whether w holds oldPath as a whole path (not as the prefix of a
// longer element such as .../v2cmd).
func mentions(w, oldPath string) bool {
	isPath := func(b byte) bool {
		return b == '.' || b == '-' || b == '_' || b == '/' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for off := 0; ; {
		i := strings.Index(w[off:], oldPath)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(oldPath)
		if (i == 0 || !isPath(w[i-1])) && (end == len(w) || w[end] == '/' || !isPath(w[end])) {
			return true
		}
		off = i + 1
	}
}

// scanDirectives reads the //go:generate lines of a file that does not parse the way
// cmd/go does: every line that starts, at column 1, with the directive.
func scanDirectives(name string, src []byte, oldPath string, rewrite func(string) (string, bool), opts GoRewriteOptions) (edits []edit, ambiguous []string) {
	for off, line := 0, 1; off < len(src); line++ {
		end := bytes.IndexByte(src[off:], '\n')
		next := len(src)
		if end >= 0 {
			end, next = off+end, off+end+1
		} else {
			end = len(src)
		}
		text := strings.TrimSuffix(string(src[off:end]), "\r")
		if strings.HasPrefix(text, "//go:generate") {
			es, amb := generateEdits(text, off, oldPath, rewrite, opts)
			edits = append(edits, es...)
			if amb {
				ambiguous = append(ambiguous, fmt.Sprintf("%s:%d", name, line))
			}
		}
		off = next
	}
	return edits, ambiguous
}

// rewriteArg is the edit for one word that names a package, as "go run" and "go tool"
// take it: "path" or, with opts.Extended, "path@version".
func rewriteArg(w word, rewrite func(string) (string, bool), opts GoRewriteOptions) (string, bool) {
	if !w.plain {
		return "", false
	}
	pkg, ver, versioned := strings.Cut(w.text, "@")
	if versioned && !opts.Extended {
		return "", false
	}
	nv, ok := rewrite(pkg)
	if !ok {
		return "", false
	}
	if !versioned {
		return nv, true
	}
	switch {
	case ver == "latest" || ver == "upgrade" || ver == "patch":
		return nv + "@" + ver, true
	case opts.NewVersion != "" && semver.IsValid(ver) && semver.Canonical(ver) == ver:
		return nv + "@" + opts.NewVersion, true
	}
	return "", false
}

// generateEdits handles one comment (its text, at offset base of the file): if it is a
// //go:generate directive it returns the edit for the package of a plain "go run"
// (and with opts.Extended "go run pkg@version" and "go tool pkg"), and whether the
// directive mentions oldPath anywhere it did not rewrite.
func generateEdits(text string, base int, oldPath string, rewrite func(string) (string, bool), opts GoRewriteOptions) (edits []edit, ambiguous bool) {
	const prefix = "//go:generate"
	if !strings.HasPrefix(text, prefix) {
		return nil, false
	}
	if rest := text[len(prefix):]; rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return nil, false
	}
	ws := splitWords(text)[1:] // [0] is the directive itself
	if len(ws) >= 2 && ws[0].text == "-command" {
		ws = ws[2:] // //go:generate -command name args...: args is a command too
	}
	used := -1
	switch {
	case len(ws) >= 2 && ws[0].text == "go" && ws[1].text == "run":
		used = goRunPackage(ws)
	case opts.Extended && len(ws) >= 3 && ws[0].text == "go" && ws[1].text == "tool":
		used = goToolPackage(ws)
	}
	handled := map[int]bool{}
	if used >= 0 {
		if nv, ok := rewriteArg(ws[used], rewrite, opts); ok {
			edits = append(edits, edit{base + ws[used].start, base + ws[used].end, nv})
			handled[used] = true
		}
	}
	if opts.Extended {
		// The tool's own arguments: after "go run <pkg>", or the command word itself.
		tool := -1
		switch {
		case used >= 0:
			if p, _, _ := strings.Cut(ws[used].text, "@"); path.Base(p) == "mockgen" {
				tool = used
			}
		case len(ws) > 0 && ws[0].text == "mockgen":
			tool = 0
		}
		if tool >= 0 {
			es, hs := mockgenEdits(ws[tool+1:], rewrite)
			for _, e := range es {
				edits = append(edits, edit{base + e.start, base + e.end, e.text})
			}
			for _, i := range hs {
				handled[tool+1+i] = true
			}
		}
	}
	for i, w := range ws {
		if handled[i] {
			continue
		}
		if mentions(w.text, oldPath) {
			ambiguous = true
		}
	}
	return edits, ambiguous
}

// goToolPackage returns the index in ws of the command of "go tool": the only flag
// before it that go tool has is -n (go help tool).
func goToolPackage(ws []word) int {
	for i := 2; i < len(ws); i++ {
		switch t := ws[i].text; {
		case t == "-n":
		case strings.HasPrefix(t, "-"):
			return -1
		default:
			return i
		}
	}
	return -1
}

// goRunPackage returns the index in ws of the package argument of "go run", or -1
// when a flag before it is not one we know the shape of.
func goRunPackage(ws []word) int {
	for i := 2; i < len(ws); i++ {
		t := ws[i].text
		switch {
		case !strings.HasPrefix(t, "-"):
			return i
		case strings.Contains(t, "="), goRunBoolFlags[t]:
		case goRunValueFlags[t]:
			i++
		default:
			return -1
		}
	}
	return -1
}

// mockgenValueFlags and mockgenBoolFlags are the flags the mockgen documentation lists
// (go.uber.org/mock README, docs/vendor/mockgen/README.md). A flag not listed there
// stops the reading: ripple does not guess at its grammar.
var mockgenValueFlags = map[string]bool{
	"archive": true, "source": true, "destination": true, "package": true, "imports": true,
	"aux_files": true, "build_flags": true, "mock_names": true, "self_package": true,
	"copyright_file": true, "exclude_interfaces": true,
}
var mockgenBoolFlags = map[string]bool{
	"debug_parser": true, "write_package_comment": true, "write_generate_directive": true,
	"write_source_comment": true, "typed": true,
}

// mockgenArgs is what mockgen's command line holds for an import path.
type mockgenArgs struct {
	imports     []int // index of the word whose value is -imports, and where in it: see importsValue
	importsAt   []int // offset of the value inside that word
	positionals []int // words from the first non-flag argument on
	hasSource   bool  // -source given: source mode takes no import path
	ok          bool  // the line was read to the end with documented flags only
}

// readMockgen reads ws (the words after the command) the way Go's flag package does:
// -f, --f, -f=v and, for a value flag, -f v; the first non-flag word and all after it
// are positional.
func readMockgen(ws []word) (a mockgenArgs) {
	for i := 0; i < len(ws); i++ {
		t := ws[i].text
		if !strings.HasPrefix(t, "-") || t == "-" {
			for j := i; j < len(ws); j++ {
				a.positionals = append(a.positionals, j)
			}
			a.ok = true
			return a
		}
		if t == "--" {
			for j := i + 1; j < len(ws); j++ {
				a.positionals = append(a.positionals, j)
			}
			a.ok = true
			return a
		}
		name := strings.TrimPrefix(strings.TrimPrefix(t, "-"), "-")
		name, _, inline := strings.Cut(name, "=")
		switch {
		case mockgenBoolFlags[name]:
		case mockgenValueFlags[name]:
			vi, off := i, strings.Index(t, "=")+1
			if !inline {
				i++
				if i >= len(ws) {
					return a
				}
				vi, off = i, 0
			}
			switch name {
			case "source":
				a.hasSource = true
			case "imports":
				if ws[vi].plain {
					a.imports, a.importsAt = append(a.imports, vi), append(a.importsAt, off)
				}
			}
		default:
			return a
		}
	}
	a.ok = true
	return a
}

// mockgenEdits are the edits for the import paths on a mockgen command line: the
// package of package and archive mode (the first of exactly two non-flag arguments,
// the second being the symbols) and the package of each foo=bar/baz element of -imports.
// It also returns the words it rewrote, which the caller does not report as left alone.
func mockgenEdits(ws []word, rewrite func(string) (string, bool)) (edits []edit, handled []int) {
	a := readMockgen(ws)
	if !a.ok {
		return nil, nil
	}
	if !a.hasSource && len(a.positionals) == 2 && ws[a.positionals[0]].plain {
		w := ws[a.positionals[0]]
		if nv, ok := rewrite(w.text); ok {
			edits = append(edits, edit{w.start, w.end, nv})
			handled = append(handled, a.positionals[0])
		}
	}
	for k, vi := range a.imports {
		w, off := ws[vi], a.importsAt[k]
		pos := w.start + off
		before := len(edits)
		for _, el := range strings.Split(w.text[off:], ",") {
			if name, pkg, ok := strings.Cut(el, "="); ok {
				if nv, ok := rewrite(pkg); ok {
					start := pos + len(name) + 1
					edits = append(edits, edit{start, start + len(pkg), nv})
				}
			}
			pos += len(el) + 1
		}
		if len(edits) > before {
			handled = append(handled, vi)
		}
	}
	return edits, handled
}
