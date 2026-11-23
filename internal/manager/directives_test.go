package manager

import (
	"strings"
	"testing"
)

var extended = GoRewriteOptions{Extended: true, NewVersion: "v3.1.0"}

func rewriteExt(t *testing.T, src, want string, wantN int, wantAmbiguous ...string) {
	t.Helper()
	out, n, amb, err := rewriteGoOpts("p.go", []byte(src), "github.com/a/b/v2", "github.com/a/b/v3", nil, extended)
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

func TestExtendedGoRunVersionSuffix(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@v2.0.4 -o out.go\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@latest\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@v2\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@master\n"
	want := "package p\n\n" +
		"//go:generate go run github.com/a/b/v3/cmd/gen@v3.1.0 -o out.go\n" +
		"//go:generate go run github.com/a/b/v3/cmd/gen@latest\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@v2\n" +
		"//go:generate go run github.com/a/b/v2/cmd/gen@master\n"
	// a branch or a major-only query is not a version we can translate: left, reported
	rewriteExt(t, src, want, 2, "p.go:5", "p.go:6")
}

func TestExtendedGoRunVersionNeedsNewVersion(t *testing.T) {
	src := "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen@v2.0.4\n"
	out, n, amb, err := rewriteGoOpts("p.go", []byte(src), "github.com/a/b/v2", "github.com/a/b/v3", nil, GoRewriteOptions{Extended: true})
	if err != nil || n != 0 || string(out) != src || len(amb) != 1 {
		t.Errorf("without a new version the directive is left alone: n=%d amb=%q err=%v\n%s", n, amb, err, out)
	}
}

func TestExtendedGoTool(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate go tool github.com/a/b/v2/cmd/gen -o out.go\n" +
		"//go:generate go tool -n github.com/a/b/v2/cmd/gen\n" +
		"//go:generate go tool gen\n" +
		"//go:generate go tool -weird github.com/a/b/v2/cmd/gen\n"
	want := "package p\n\n" +
		"//go:generate go tool github.com/a/b/v3/cmd/gen -o out.go\n" +
		"//go:generate go tool -n github.com/a/b/v3/cmd/gen\n" +
		"//go:generate go tool gen\n" +
		"//go:generate go tool -weird github.com/a/b/v2/cmd/gen\n"
	rewriteExt(t, src, want, 2, "p.go:6")
}

func TestExtendedLeavesTheDefaultAloneWhenOff(t *testing.T) {
	src := "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen@v2.0.4\n//go:generate go tool github.com/a/b/v2/cmd/gen\n"
	rewriteGoWant(t, src, src, 0, "p.go:3", "p.go:4")
}

func TestExtendedBodyThatDoesNotParse(t *testing.T) {
	// cmd/go reads //go:generate as lines starting at column 1, without parsing the file.
	src := "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen\n//go:generate go tool github.com/a/b/v2/cmd/gen\nfunc ( {\n\nvar r = `\n//go:generate go run github.com/a/b/v2/cmd/gen\n`\n"
	want := strings.Replace(src, "b/v2/cmd/gen", "b/v3/cmd/gen", -1)
	// Go's own scanner does not tell a raw string from a line, so neither do we: the
	// last directive is rewritten too, exactly as `go generate` would run it.
	rewriteExt(t, src, want, 3)
}

func TestExtendedBodyThatDoesNotParseAmbiguousStillReported(t *testing.T) {
	src := "package p\n\n//go:generate go run github.com/a/b/v2/cmd/gen@master\nfunc ( {\n"
	rewriteExt(t, src, src, 0, "p.go:3")
}

// mockgen's documented command line (go.uber.org/mock README, docs/vendor/mockgen):
// package and archive modes take "<import path> <symbols>" as the two non-flag
// arguments, and -imports takes comma-separated foo=bar/baz elements.
func TestExtendedMockgenPackageMode(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate mockgen -destination=m.go github.com/a/b/v2/store Reader,Writer\n" +
		"//go:generate mockgen -package mocks -destination m.go -typed github.com/a/b/v2/store Reader\n" +
		"//go:generate mockgen --destination=m.go github.com/a/b/v2 Client\n" +
		"//go:generate mockgen -archive=pkg.a github.com/a/b/v2/store Reader\n" +
		"//go:generate mockgen -destination=m.go . Reader\n" +
		"//go:generate mockgen -destination=m.go database/sql/driver Conn\n"
	want := "package p\n\n" +
		"//go:generate mockgen -destination=m.go github.com/a/b/v3/store Reader,Writer\n" +
		"//go:generate mockgen -package mocks -destination m.go -typed github.com/a/b/v3/store Reader\n" +
		"//go:generate mockgen --destination=m.go github.com/a/b/v3 Client\n" +
		"//go:generate mockgen -archive=pkg.a github.com/a/b/v3/store Reader\n" +
		"//go:generate mockgen -destination=m.go . Reader\n" +
		"//go:generate mockgen -destination=m.go database/sql/driver Conn\n"
	rewriteExt(t, src, want, 4)
}

func TestExtendedMockgenImportsFlag(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate mockgen -source=foo.go -imports=st=github.com/a/b/v2/store,x=other/pkg -destination=m.go\n" +
		"//go:generate mockgen -source=foo.go -imports other=github.com/a/b/v2 -destination=m.go\n" +
		"//go:generate mockgen -source=foo.go -imports=github.com/a/b/v2/store -destination=m.go\n"
	want := "package p\n\n" +
		"//go:generate mockgen -source=foo.go -imports=st=github.com/a/b/v3/store,x=other/pkg -destination=m.go\n" +
		"//go:generate mockgen -source=foo.go -imports other=github.com/a/b/v3 -destination=m.go\n" +
		"//go:generate mockgen -source=foo.go -imports=github.com/a/b/v2/store -destination=m.go\n"
	// an element without foo= is not the documented form: left, and reported
	rewriteExt(t, src, want, 2, "p.go:5")
}

func TestExtendedMockgenViaGoRun(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate go run go.uber.org/mock/mockgen -destination=m.go github.com/a/b/v2/store Reader\n" +
		"//go:generate go run github.com/a/b/v2/mockgen@v2.0.4 -destination=m.go github.com/a/b/v2/store Reader\n"
	want := "package p\n\n" +
		"//go:generate go run go.uber.org/mock/mockgen -destination=m.go github.com/a/b/v3/store Reader\n" +
		"//go:generate go run github.com/a/b/v3/mockgen@v3.1.0 -destination=m.go github.com/a/b/v3/store Reader\n"
	rewriteExt(t, src, want, 3)
}

func TestExtendedMockgenAfterCommandAlias(t *testing.T) {
	src := "package p\n\n//go:generate -command mock mockgen -destination=m.go github.com/a/b/v2/store Reader\n"
	want := strings.Replace(src, "b/v2/store", "b/v3/store", 1)
	rewriteExt(t, src, want, 1)
}

func TestExtendedMockgenLeavesWhatItCannotReadAlone(t *testing.T) {
	src := "package p\n\n" +
		"//go:generate mockgen -unknown_flag github.com/a/b/v2/store Reader\n" +
		"//go:generate mockgen github.com/a/b/v2/store\n" +
		"//go:generate mockgen -source=foo.go github.com/a/b/v2/store Reader\n" +
		"//go:generate mockgen -destination=m.go \"github.com/a/b/v2/store\" Reader\n" +
		"//go:generate mockgen -destination=m.go $PKG Reader\n" +
		"//go:generate mockgen -mock_names=A=github.com/a/b/v2 -destination=m.go . Reader\n"
	// unknown flag, one positional, -source with positionals, quoting and a variable
	// are not shapes mockgen documents for an import path; a name in -mock_names is not one
	rewriteExt(t, src, src, 0, "p.go:3", "p.go:4", "p.go:5", "p.go:6", "p.go:8")
}

func TestMockgenIsLeftAloneWithoutExtended(t *testing.T) {
	src := "package p\n\n//go:generate mockgen -destination=m.go github.com/a/b/v2/store Reader\n"
	rewriteGoWant(t, src, src, 0, "p.go:3")
}
