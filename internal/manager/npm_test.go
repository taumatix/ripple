package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

func TestNpmMatches(t *testing.T) {
	m := Npm{Dir: "/"}
	for p, want := range map[string]bool{
		"package.json": true, "web/package.json": true, "node_modules/x/package.json": false,
		"web/node_modules/x/package.json": false, "package-lock.json": false, "tsconfig.json": false,
	} {
		if got := m.Matches(p); got != want {
			t.Errorf("Matches(%q) = %v", p, got)
		}
	}
	if (Npm{Dir: "/web"}).Matches("package.json") {
		t.Error("matched outside its directory")
	}
}

const pkgJSON = `{
  "name": "app",
  "version": "1.0.0",
  "dependencies": {
    "left-pad": "^1.2.3",
    "@types/node": "~20.1.0",
    "exact": "4.0.1",
    "eq": "=2.0.0",
    "star": "*",
    "range": ">=1.0.0 <2",
    "pre": "^1.0.0-beta.1",
    "partial": "^1.2",
    "git": "github:a/b#v1",
    "alias": "npm:other@^1.0.0",
    "ws": "workspace:*",
    "tag": "latest"
  },
  "devDependencies": { "left-pad": "^1.2.3", "jest": "^29.0.0" },
  "optionalDependencies": {"fsevents": "~2.3.2"},
  "peerDependencies": {"react": "^18.0.0"},
  "scripts": {"left-pad": "^9.9.9"}
}
`

func TestNpmExtract(t *testing.T) {
	deps, err := Npm{Dir: "/"}.Extract("package.json", []byte(pkgJSON))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range deps {
		if d.Manager != "npm" || d.Datasource != model.DatasourceNpm || d.File != "package.json" {
			t.Errorf("%+v", d)
		}
		got[d.Package] = d.Version
	}
	want := map[string]string{"left-pad": "^1.2.3", "@types/node": "~20.1.0", "exact": "4.0.1", "eq": "=2.0.0", "jest": "^29.0.0", "fsevents": "~2.3.2"}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(deps) != 6 {
		t.Errorf("left-pad in two sections with one range is one dependency, got %d", len(deps))
	}
}

func TestNpmExtractRejectsBadJSON(t *testing.T) {
	if _, err := (Npm{}).Extract("package.json", []byte(`{"dependencies": {`)); err == nil {
		t.Error("want an error")
	}
	if deps, err := (Npm{}).Extract("package.json", []byte(`{"name":"x"}`)); err != nil || len(deps) != 0 {
		t.Errorf("%v %v", deps, err)
	}
}

func TestNpmApplyKeepsOperatorAndLayout(t *testing.T) {
	m := Npm{Dir: "/"}
	dep := model.Dependency{File: "package.json", Manager: "npm", Datasource: model.DatasourceNpm, Package: "left-pad", Version: "^1.2.3"}
	out, err := m.Apply([]byte(pkgJSON), dep, "left-pad", "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	want := pkgJSON
	want = replaceAll(want, `"left-pad": "^1.2.3"`, `"left-pad": "^1.5.0"`)
	if string(out) != want {
		t.Errorf("both sections move, nothing else does (scripts.left-pad stays):\n%s", out)
	}
	for _, c := range []struct{ pkg, from, to, line string }{
		{"@types/node", "~20.1.0", "20.2.1", `"@types/node": "~20.2.1"`},
		{"exact", "4.0.1", "4.1.0", `"exact": "4.1.0"`},
		{"eq", "=2.0.0", "3.0.0", `"eq": "=3.0.0"`},
		{"fsevents", "~2.3.2", "2.3.9", `"fsevents": "~2.3.9"`},
	} {
		d := model.Dependency{File: "package.json", Package: c.pkg, Version: c.from}
		out, err := m.Apply([]byte(pkgJSON), d, c.pkg, c.to)
		if err != nil || !contains(string(out), c.line) {
			t.Errorf("%s: %v\n%s", c.pkg, err, out)
		}
	}
}

func TestNpmApplyStaleAndRename(t *testing.T) {
	m := Npm{}
	d := model.Dependency{File: "package.json", Package: "left-pad", Version: "^1.0.0"}
	if _, err := m.Apply([]byte(pkgJSON), d, "left-pad", "1.5.0"); !errors.Is(err, ErrStale) {
		t.Errorf("err = %v, want ErrStale", err)
	}
	d.Version = "^1.2.3"
	if _, err := m.Apply([]byte(pkgJSON), d, "other", "1.5.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	if _, err := m.Apply([]byte(pkgJSON), d, "left-pad", "1.5.0-beta"); err == nil {
		t.Error("a prerelease target must be refused")
	}
}

func TestNpmApplyEscapedKey(t *testing.T) {
	in := "{\"dependencies\":{\"a\\u002db\":\"^1.0.0\"}}"
	d := model.Dependency{File: "package.json", Package: "a-b", Version: "^1.0.0"}
	out, err := (Npm{}).Apply([]byte(in), d, "a-b", "1.1.0")
	if err != nil || string(out) != "{\"dependencies\":{\"a\\u002db\":\"^1.1.0\"}}" {
		t.Errorf("%s %v", out, err)
	}
}

func TestNpmRangeFloor(t *testing.T) {
	for in, want := range map[string]string{"^1.2.3": "1.2.3", "~0.1.0": "0.1.0", "1.0.0": "1.0.0", "=1.0.0": "1.0.0", "^1.2": "", ">=1.0.0": "", "*": "", "^1.0.0-rc.1": "", "v1.0.0": ""} {
		if got, _ := NpmRangeFloor(in); got != want {
			t.Errorf("NpmRangeFloor(%q) = %q, want %q", in, got, want)
		}
	}
}

func replaceAll(s, a, b string) string { return strings.ReplaceAll(s, a, b) }
func contains(s, sub string) bool      { return strings.Contains(s, sub) }
