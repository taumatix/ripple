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
