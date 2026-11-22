package manager

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/platform"
)

// memWS is an in-memory platform.Workspace: only the file methods are implemented.
type memWS struct {
	platform.Workspace
	files map[string]string
}

func (m *memWS) ReadFile(p string) ([]byte, error)  { return []byte(m.files[p]), nil }
func (m *memWS) WriteFile(p string, d []byte) error { m.files[p] = string(d); return nil }
func (m *memWS) Walk(fn func(string) error) error {
	var names []string
	for k := range m.files {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if err := fn(k); err != nil {
			return err
		}
	}
	return nil
}

const goMod = `module example.com/app

go 1.22

require (
	example.com/lib v1.0.0
	example.com/ind v0.3.0 // indirect
)

require example.com/single v2.1.0+incompatible // keep me

replace example.com/lib => ../lib
`

func TestGoModExtract(t *testing.T) {
	deps, err := GoMod{}.Extract("svc/go.mod", []byte(goMod))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Dependency{
		{File: "svc/go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/lib", Version: "v1.0.0"},
		{File: "svc/go.mod", Manager: "gomod", Datasource: "go", Package: "example.com/single", Version: "v2.1.0+incompatible"},
	}
	if len(deps) != 2 || deps[0] != want[0] || deps[1] != want[1] {
		t.Fatalf("got %+v", deps)
	}
}

func TestGoModApplyLiteral(t *testing.T) {
	dep := model.Dependency{File: "go.mod", Package: "example.com/lib", Version: "v1.0.0"}
	out, err := GoMod{}.Apply([]byte(goMod), dep, dep.Package, "v1.4.2")
	if err != nil {
		t.Fatal(err)
	}
	want := `module example.com/app

go 1.22

require (
	example.com/lib v1.4.2
	example.com/ind v0.3.0 // indirect
)

require example.com/single v2.1.0+incompatible // keep me

replace example.com/lib => ../lib
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	dep.Version = "v0.9.0"
	if _, err := (GoMod{}).Apply([]byte(goMod), dep, dep.Package, "v1.4.2"); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if _, err := (GoMod{}).Extract("go.mod", []byte("require (\n\texample.com/a v1.0.0\n")); err == nil {
		t.Fatal("expected parse error")
	}
}

func regexMgr(t *testing.T) *Regex {
	r, err := NewRegex("regex#0", config.Update{
		Ecosystem: "regex",
		Files:     []string{"Makefile"},
		Patterns:  []string{`# go:(?P<name>\S+) (?P<version>v\S+)`},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const makefile = "# go:example.com/tool v1.2.3\nTOOL=v1.2.3\n# go:example.com/other v0.1.0\n# go:example.com/tool v1.2.3\n"

func TestRegexExtractAndApply(t *testing.T) {
	r := regexMgr(t)
	if !r.Matches("Makefile") || r.Matches("sub/Makefile") {
		t.Fatal("fileMatch anchoring wrong")
	}
	deps, _ := r.Extract("Makefile", []byte(makefile))
	if len(deps) != 3 || deps[0].Package != "example.com/tool" || deps[0].Version != "v1.2.3" || deps[1].Package != "example.com/other" {
		t.Fatalf("%+v", deps)
	}
	out, err := r.Apply([]byte(makefile), deps[0], deps[0].Package, "v1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "# go:example.com/tool v1.3.0\nTOOL=v1.2.3\n# go:example.com/other v0.1.0\n# go:example.com/tool v1.3.0\n"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
	deps[0].Version = "v9.9.9"
	if _, err := r.Apply([]byte(makefile), deps[0], deps[0].Package, "v1.3.0"); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
}

func TestRegexFixedPackageName(t *testing.T) {
	r, err := NewRegex("regex#0", config.Update{Files: []string{"VERSION"}, Package: "example.com/tool",
		Patterns: []string{`version=(?P<version>v[\d.]+)`}})
	if err != nil {
		t.Fatal(err)
	}
	deps, _ := r.Extract("VERSION", []byte("version=v1.0.0\n"))
	if len(deps) != 1 || deps[0].Package != "example.com/tool" {
		t.Fatalf("%+v", deps)
	}
}

func TestBuildAndRelevant(t *testing.T) {
	c := &config.Repo{Updates: []config.Update{
		{Ecosystem: "gomod", Directory: "/svc"},
		{Ecosystem: "regex", Files: []string{"Makefile"}, Patterns: []string{`(?P<name>a)(?P<version>b)`}},
	}}
	ms, err := Build(c)
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v %v", ms, err)
	}
	for p, want := range map[string]bool{"svc/go.mod": true, "svc/a/go.mod": true, "go.mod": false, "Makefile": true, ".ripple.yaml": true, ".github/ripple.yml": true, "ripple.yml": false, "github/ripple.yml": false, "README.md": false} {
		if Relevant(ms, p) != want {
			t.Errorf("Relevant(%q) != %v", p, want)
		}
	}
	if _, ok := ByName(ms, "regex#1"); !ok {
		t.Fatal("ByName")
	}
}

func TestGoModMigrateLiteral(t *testing.T) {
	in := `module example.com/app

go 1.22

require (
	github.com/a/b/v2 v2.3.0
	github.com/other/x v1.0.0
)

replace github.com/a/b/v2 => github.com/fork/b/v3 v3.0.0

replace github.com/a/b/v2 v2.3.0 => github.com/a/b/v2 v2.3.1

replace github.com/other/x => ./x

exclude github.com/a/b/v2 v2.4.0

exclude github.com/other/x v1.0.1
`
	want := `module example.com/app

go 1.22

require (
	github.com/a/b/v3 v3.0.0
	github.com/other/x v1.0.0
)

replace github.com/other/x => ./x

exclude github.com/other/x v1.0.1

replace github.com/a/b/v3 => github.com/fork/b/v3 v3.0.0
`
	dep := model.Dependency{File: "go.mod", Package: "github.com/a/b/v2", Version: "v2.3.0"}
	out, err := GoMod{}.Apply([]byte(in), dep, "github.com/a/b/v3", "v3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestGoModMigrateShapes(t *testing.T) {
	for _, tc := range []struct{ old, oldV, new string }{
		{"github.com/a/b", "v1.0.0", "github.com/a/b/v2"},
		{"gopkg.in/yaml.v2", "v2.4.0", "gopkg.in/yaml.v3"},
		{"github.com/a/b/v9", "v9.0.0", "github.com/a/b/v10"},
	} {
		in := "module m\n\ngo 1.22\n\nrequire " + tc.old + " " + tc.oldV + "\n"
		out, err := GoMod{}.Apply([]byte(in), model.Dependency{File: "go.mod", Package: tc.old, Version: tc.oldV}, tc.new, "v3.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if want := "module m\n\ngo 1.22\n\nrequire " + tc.new + " v3.0.0\n"; string(out) != want {
			t.Errorf("%s -> %s:\n%s", tc.old, tc.new, out)
		}
	}
}

func TestRegexRefusesRename(t *testing.T) {
	_, err := regexMgr(t).Apply([]byte("x"), model.Dependency{Package: "a"}, "b", "v1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestRewriteImportsLiteral(t *testing.T) {
	src := `package p

import (
	"fmt"
	a "github.com/a/b/v2"
	. "github.com/a/b/v2/dot"
	_ "github.com/a/b/v2/side"
	c "github.com/a/b/v3/other"
	"github.com/a/bc/v2"
	raw ` + "`github.com/a/b/v2/raw`" + `
)

// github.com/a/b/v2 in a comment stays.
var s = "github.com/a/b/v2"

//go:generate go run github.com/a/b/v2/cmd/gen
`
	want := `package p

import (
	"fmt"
	a "github.com/a/b/v3"
	. "github.com/a/b/v3/dot"
	_ "github.com/a/b/v3/side"
	c "github.com/a/b/v3/other"
	"github.com/a/bc/v2"
	raw ` + "`github.com/a/b/v3/raw`" + `
)

// github.com/a/b/v2 in a comment stays.
var s = "github.com/a/b/v2"

//go:generate go run github.com/a/b/v3/cmd/gen
`
	out, n, err := rewriteImports("p.go", []byte(src), "github.com/a/b/v2", "github.com/a/b/v3", nil)
	if err != nil || n != 5 || string(out) != want {
		t.Fatalf("n=%d err=%v\n%s", n, err, out)
	}
}

func TestRewriteImportsUnsuffixedToV2KeepsOtherMajors(t *testing.T) {
	src := "package p\n\nimport (\n\t\"github.com/a/b\"\n\t\"github.com/a/b/sub\"\n\t\"github.com/a/b/v3/x\"\n\t\"github.com/a/b/v2/x\"\n\t\"github.com/a/b/vendor\"\n)\n"
	want := "package p\n\nimport (\n\t\"github.com/a/b/v2\"\n\t\"github.com/a/b/v2/sub\"\n\t\"github.com/a/b/v3/x\"\n\t\"github.com/a/b/v2/x\"\n\t\"github.com/a/b/v2/vendor\"\n)\n"
	out, n, err := rewriteImports("p.go", []byte(src), "github.com/a/b", "github.com/a/b/v2", nil)
	if err != nil || n != 3 || string(out) != want {
		t.Fatalf("n=%d err=%v\n%s", n, err, out)
	}
}

func TestRewriteImportsNestedModuleKeepsItsPackages(t *testing.T) {
	src := "package p\n\nimport (\n\t\"github.com/a/b/v2/core\"\n\t\"github.com/a/b/v2/sub/pkg\"\n)\n"
	out, n, err := rewriteImports("p.go", []byte(src), "github.com/a/b/v2", "github.com/a/b/v3", []string{"github.com/a/b/v2/sub"})
	if err != nil || n != 1 || !strings.Contains(string(out), `"github.com/a/b/v3/core"`) || !strings.Contains(string(out), `"github.com/a/b/v2/sub/pkg"`) {
		t.Fatalf("n=%d err=%v\n%s", n, err, out)
	}
}

func TestRewriteImportsSyntaxError(t *testing.T) {
	if _, _, err := rewriteImports("p.go", []byte("package p\nimport ("), "a", "b", nil); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestRegexSeveralPatternsOutOfOrder(t *testing.T) {
	r, err := NewRegex("regex#0", config.Update{Files: []string{"VERSIONS"}, Package: "example.com/tool", Patterns: []string{`A=(?P<version>v[0-9.]+)`, `B=(?P<version>v[0-9.]+)`}})
	if err != nil {
		t.Fatal(err)
	}
	in := "B=v1.0.0\nA=v1.0.0\n"
	deps, _ := r.Extract("VERSIONS", []byte(in))
	if len(deps) != 2 {
		t.Fatalf("deps = %+v", deps)
	}
	out, err := r.Apply([]byte(in), model.Dependency{File: "VERSIONS", Package: "example.com/tool", Version: "v1.0.0"}, "example.com/tool", "v1.2.0")
	if err != nil || string(out) != "B=v1.2.0\nA=v1.2.0\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestRegexOverlappingMatchesRejected(t *testing.T) {
	r, err := NewRegex("regex#0", config.Update{Files: []string{"V"}, Package: "example.com/tool", Patterns: []string{`(?P<version>1\.1)\.1`, `1\.(?P<version>1\.1)`}})
	if err != nil {
		t.Fatal(err)
	}
	// Two patterns capture different spans of overlapping text: refuse rather than guess.
	in := "1.1.1\n"
	if _, err := r.Apply([]byte(in), model.Dependency{File: "V", Package: "example.com/tool", Version: "1.1"}, "example.com/tool", "2.0"); err == nil {
		t.Fatal("want an error for overlapping matches")
	}
}

func TestMigrateGoWorkReplace(t *testing.T) {
	in := "go 1.22\n\nuse ./a\n\nreplace example.com/lib => ../fork\n\nreplace example.com/lib v1.0.0 => example.com/other v1.0.0\n\nreplace example.com/keep => ../keep\n"
	out, changed, err := RewriteGoWork([]byte(in), "example.com/lib", "example.com/lib/v2")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "go 1.22\n\nuse ./a\n\nreplace example.com/keep => ../keep\n\nreplace example.com/lib/v2 => ../fork\n"
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if _, changed, _ := RewriteGoWork([]byte("go 1.22\n\nuse ./a\n"), "example.com/lib", "example.com/lib/v2"); changed {
		t.Errorf("a go.work that never names the path must be left alone")
	}
}

func rewriteGoWant(t *testing.T, src, want string, wantN int, wantAmbiguous ...string) {
	t.Helper()
	out, n, amb, err := rewriteGo("p.go", []byte(src), "github.com/a/b/v2", "github.com/a/b/v3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != want || n != wantN {
		t.Errorf("n=%d (want %d)\n--- got\n%s\n--- want\n%s", n, wantN, out, want)
	}
	if strings.Join(amb, "|") != strings.Join(wantAmbiguous, "|") {
		t.Errorf("ambiguous = %q, want %q", amb, wantAmbiguous)
	}
}

func TestRewriteGenerateGoRun(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen -o out.go github.com/a/b/v2/arg\n" +
		"//go:generate\tgo   run -tags tools -mod=mod github.com/a/b/v2/cmd/gen\n" +
		"//go:generate -command g go run github.com/a/b/v2/cmd/gen\n" +
		"//go:generate go run ./cmd/local\n" +
		"//go:generate go run github.com/a/b/v2cmd/x\n" +
		"//go:generate go run github.com/a/b/v4/cmd/x\n"
	want := "package p\n\n" +
		"//go:generate go run github.com/a/b/v3/cmd/gen -o out.go github.com/a/b/v2/arg\n" +
		"//go:generate\tgo   run -tags tools -mod=mod github.com/a/b/v3/cmd/gen\n" +
		"//go:generate -command g go run github.com/a/b/v3/cmd/gen\n" +
		"//go:generate go run ./cmd/local\n" +
		"//go:generate go run github.com/a/b/v2cmd/x\n" +
		"//go:generate go run github.com/a/b/v4/cmd/x\n"
	// the program's own argument that names the old path is ambiguous, not rewritten
	rewriteGoWant(t, src, want, 3, "p.go:3")
}

func TestRewriteGenerateAmbiguousLeftAlone(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@latest\n" +
		"//go:generate go run \"github.com/a/b/v2/cmd/gen\"\n" +
		"//go:generate go run $PKG github.com/a/b/v2/x\n" +
		"//go:generate go run -weird github.com/a/b/v2/cmd/gen\n" +
		"//go:generate mockgen github.com/a/b/v2/pkg Iface\n" +
		"//go:generate go tool github.com/a/b/v2/cmd/gen\n"
	rewriteGoWant(t, src, src, 0, "p.go:3", "p.go:4", "p.go:5", "p.go:6", "p.go:7", "p.go:8")
}

func TestRewriteGenerateNotADirective(t *testing.T) {
	src := "package p\n\n" +
		"// //go:generate go run github.com/a/b/v2/cmd/gen\n" +
		"var _ = 1 //go:generate go run github.com/a/b/v2/cmd/gen\n" +
		"/* //go:generate go run github.com/a/b/v2/cmd/gen */\n" +
		"//  go:generate go run github.com/a/b/v2/cmd/gen\n" +
		"var s = \"\\n//go:generate go run github.com/a/b/v2/cmd/gen\"\n" +
		"var r = `\n//go:generate go run github.com/a/b/v2/cmd/gen\n`\n"
	rewriteGoWant(t, src, src, 0)
}

func TestRewriteImportComment(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"line", "package p // import \"github.com/a/b/v2/p\"\n", "package p // import \"github.com/a/b/v3/p\"\n"},
		{"block", "package p /* import \"github.com/a/b/v2\" */\n", "package p /* import \"github.com/a/b/v3\" */\n"},
		{"other module", "package p // import \"github.com/a/c/p\"\n", "package p // import \"github.com/a/c/p\"\n"},
		{"not canonical position", "package p\n\n// import \"github.com/a/b/v2/p\"\nvar x int // import \"github.com/a/b/v2/p\"\n",
			"package p\n\n// import \"github.com/a/b/v2/p\"\nvar x int // import \"github.com/a/b/v2/p\"\n"},
		{"prose", "package p // see import \"github.com/a/b/v2/p\"\n", "package p // see import \"github.com/a/b/v2/p\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			if tc.src != tc.want {
				n = 1
			}
			rewriteGoWant(t, tc.src, tc.want, n)
		})
	}
}

func TestRewriteGoImportsReportsAmbiguous(t *testing.T) {
	ws := &memWS{files: map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.22\n\nrequire github.com/a/b/v2 v2.0.0\n",
		"a.go":   "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen@latest\n",
		"b.go":   "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen\n",
	}}
	r, err := RewriteGo(ws, "go.mod", "github.com/a/b/v2", "github.com/a/b/v3", []byte(ws.files["go.mod"]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Changed, ",") != "b.go" || strings.Join(r.Ambiguous, ",") != "a.go:3" || len(r.Skipped) != 0 {
		t.Fatalf("%+v", r)
	}
	if ws.files["b.go"] != "package p\n\n//go:generate go run github.com/a/b/v3/cmd/gen\n" {
		t.Fatalf("b.go = %q", ws.files["b.go"])
	}
}

func TestRewriteGoFallsBackWhenBodyDoesNotParse(t *testing.T) {
	src := "package p // import \"github.com/a/b/v2/p\"\n\nimport \"github.com/a/b/v2/x\"\n\n//go:generate go run github.com/a/b/v2/cmd/gen\nfunc ( {\n"
	want := "package p // import \"github.com/a/b/v3/p\"\n\nimport \"github.com/a/b/v3/x\"\n\n//go:generate go run github.com/a/b/v2/cmd/gen\nfunc ( {\n"
	rewriteGoWant(t, src, want, 2, "p.go (go:generate lines not examined: file does not parse)")
}
