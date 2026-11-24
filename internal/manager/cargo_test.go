package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const cargoToml = `[package]
name = "app"
version = "0.1.0"

[dependencies]
serde = "1.0.100"   # caret by default
tokio = { version = "~1.2.3", features = ["full"] }
rand = ">=0.8"
local = { path = "../local", version = "0.1.0" }
fork = { git = "https://example.com/fork", version = "2.0.0" }
alt = { version = "3.0.0", registry = "corp" }
wild = "*"
short = "1.2"
renamed = { package = "real-name", version = "=4.5.6" }
inherited = { workspace = true }

[dev-dependencies]
serde = "1.0.100"
pretty_assertions = "^1.4.0"

[build-dependencies.cc]
version = "1.0.79"
features = ["parallel"]

[target.'cfg(unix)'.dependencies]
libc = "0.2.150"

[workspace.dependencies]
anyhow = "1.0.70"

[features]
serde = "1.9.9"
`

func TestCargoMatches(t *testing.T) {
	c := Cargo{Dir: "/"}
	for p, want := range map[string]bool{"Cargo.toml": true, "a/b/Cargo.toml": true, "cargo.toml": false, "target/x/Cargo.toml": false, "Cargo.lock": false} {
		if got := c.Matches(p); got != want {
			t.Errorf("Matches(%q) = %v", p, got)
		}
	}
	if (Cargo{Dir: "/app"}).Matches("other/Cargo.toml") {
		t.Error("outside the directory")
	}
}

func TestCargoExtract(t *testing.T) {
	deps, err := Cargo{Dir: "/"}.Extract("Cargo.toml", []byte(cargoToml))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceCargo || d.Manager != "cargo" || d.File != "Cargo.toml" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "serde@1.0.100 tokio@~1.2.3 rand@>=0.8 short@1.2 real-name@=4.5.6 pretty_assertions@^1.4.0 cc@1.0.79 libc@0.2.150 anyhow@1.0.70"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %v\nwant %s", got, want)
	}
}

func TestCargoApplyKeepsEverythingElse(t *testing.T) {
	c := Cargo{Dir: "/"}
	cases := []struct{ pkg, ver, to, from, into string }{
		{"serde", "1.0.100", "1.0.200", `serde = "1.0.100"   # caret`, `serde = "1.0.200"   # caret`},
		{"tokio", "~1.2.3", "1.2.9", `version = "~1.2.3"`, `version = "~1.2.9"`},
		{"real-name", "=4.5.6", "4.6.0", `version = "=4.5.6"`, `version = "=4.6.0"`},
		{"cc", "1.0.79", "1.0.99", `version = "1.0.79"`, `version = "1.0.99"`},
		{"short", "1.2", "1.7.4", `short = "1.2"`, `short = "1.7"`},
		{"libc", "0.2.150", "0.2.160", `libc = "0.2.150"`, `libc = "0.2.160"`},
		{"anyhow", "1.0.70", "1.0.80", `anyhow = "1.0.70"`, `anyhow = "1.0.80"`},
	}
	for _, tc := range cases {
		out, err := c.Apply([]byte(cargoToml), model.Dependency{File: "Cargo.toml", Package: tc.pkg, Version: tc.ver}, "", tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.pkg, err)
		}
		n := 1
		if tc.pkg == "serde" {
			n = 2 // [dependencies] and [dev-dependencies] carry the same range; one dependency
		}
		want := strings.Replace(cargoToml, tc.from, tc.into, 1)
		if tc.pkg == "serde" {
			want = strings.ReplaceAll(cargoToml, `serde = "1.0.100"`, `serde = "1.0.200"`)
		}
		if string(out) != want {
			t.Errorf("%s (%d places): only the range string moves:\n%s", tc.pkg, n, out)
		}
	}
	// [features] serde = "1.9.9" is not a dependency and stays.
	out, _ := c.Apply([]byte(cargoToml), model.Dependency{File: "Cargo.toml", Package: "serde", Version: "1.0.100"}, "", "1.0.200")
	if !strings.Contains(string(out), `serde = "1.9.9"`) {
		t.Error("[features] touched")
	}
}

func TestCargoApplyStaleAndRefusals(t *testing.T) {
	c := Cargo{Dir: "/"}
	_, err := c.Apply([]byte(cargoToml), model.Dependency{File: "Cargo.toml", Package: "serde", Version: "1.0.1"}, "", "1.0.2")
	if !errors.Is(err, ErrStale) {
		t.Errorf("err = %v", err)
	}
	if _, err := c.Apply([]byte(cargoToml), model.Dependency{File: "Cargo.toml", Package: "serde", Version: "1.0.100"}, "", "1.1.0-rc.1"); err == nil {
		t.Error("a prerelease is not a target")
	}
	if _, err := c.Apply([]byte(cargoToml), model.Dependency{File: "Cargo.toml", Package: "serde", Version: "1.0.100"}, "other", "1.0.200"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename err = %v", err)
	}
}

func TestCargoPartialRequirementsKeepTheirPrecision(t *testing.T) {
	c := Cargo{Dir: "/"}
	for _, tc := range []struct{ spec, to, want string }{
		{"1", "2.3.4", "2"},
		{"^1.2", "1.9.1", "^1.9"},
		{"~1.2", "1.3.0", "~1.3"},
		{"=0.2", "0.3.5", "=0.3"},
		{"0.2.3", "0.3.0", "0.3.0"},
	} {
		in := "[dependencies]\nfoo = \"" + tc.spec + "\"\n"
		out, err := c.Apply([]byte(in), model.Dependency{File: "Cargo.toml", Package: "foo", Version: tc.spec}, "", tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.spec, err)
		}
		if want := "[dependencies]\nfoo = \"" + tc.want + "\"\n"; string(out) != want {
			t.Errorf("%s -> %s: got %q, want %q", tc.spec, tc.to, out, want)
		}
	}
}

func TestCargoReqBreaking(t *testing.T) {
	for spec, want := range map[string]int{
		"1.2.3": 0, "^1": 0, "1.2": 0, "0": 0, "~1": 0, "~1.2.3": 0, "=1.2.3": 0,
		"0.2.3": 1, "^0.2": 1, "~0.2.3": 1, "=0.2": 1, "0.0": 1,
		"0.0.3": 2, "~0.0.3": 2, "0.0.0": 2,
	} {
		r, ok := ParseCargoReq(spec)
		if !ok || r.Breaking() != want {
			t.Errorf("%s: breaking position %d (parsed %v), want %d", spec, r.Breaking(), ok, want)
		}
	}
	for _, spec := range []string{"*", ">1.2", "<2", "<=1.2", ">=1, >=2", "1.*, <2", "1.2.3-rc.1", "1.2.3.4", "", ">=1,", "1.x"} {
		if _, ok := ParseCargoReq(spec); ok {
			t.Errorf("%q must not parse", spec)
		}
	}
}

func TestCargoReqComparatorsAndWildcards(t *testing.T) {
	// spec, a version it moves to, the text it becomes ("" when it must not move).
	for _, tc := range []struct{ spec, to, want string }{
		{">=0.8", "0.9.1", ">=0.9"},
		{">=1.2.3", "1.4.0", ">=1.4.0"},
		{">= 1.2", "1.7.4", ">= 1.7"},
		{">=1, <2", "1.9.0", ""}, // ">=1" already names 1.x at its precision
		{">=1.2, <2", "1.9.0", ">=1.9, <2"},
		{">=1.2,<2", "1.9.0", ">=1.9,<2"},
		{">=1.2, <2", "2.0.0", ""}, // the upper bound is a decision, never moved
		{">=1.2.0, <=1.5.0", "1.5.0", ">=1.5.0, <=1.5.0"},
		{">=1.2.0, <=1.5.0", "1.5.1", ""},
		{">=1.2.0, <=1.5", "1.5.9", ">=1.5.9, <=1.5"}, // <=1.5 takes all of 1.5.x
		{">=1.2.0, <1.5", "1.5.0", ""},
		{"^1.2.3, <1.5", "1.4.2", "^1.4.2, <1.5"},
		{"1.*", "2.0.0", "2.*"},
		{"1.*", "1.9.0", ""}, // already matched
		{"1.2.*", "1.3.0", "1.3.*"},
		{"1.2.*", "1.2.9", ""},
		{"1.2.*", "2.0.1", "2.0.*"},
		{"0.*", "1.0.0", "1.*"},
		{"1.*", "0.9.0", ""}, // older
	} {
		r, ok := ParseCargoReq(tc.spec)
		if !ok {
			t.Errorf("%q must parse", tc.spec)
			continue
		}
		got := ""
		if r.Applicable(tc.to) {
			got = r.Text(tc.to)
		}
		if got != tc.want {
			t.Errorf("%q -> %s: got %q, want %q", tc.spec, tc.to, got, tc.want)
		}
	}
	if r, _ := ParseCargoReq("1.2.*"); !r.Wild || r.Floor() != "1.2.0" || r.Breaking() != 0 {
		t.Errorf("wildcard parse: %+v", r)
	}
}

// Every way TOML spells a dependency is read, and an edit changes the requirement's
// string and nothing else.
func TestCargoReadsEveryTOMLSpelling(t *testing.T) {
	src := "# header\r\n[dependencies]\r\n" +
		"multi = {\n  version = \"1.2.3\", # why\n  features = [\"a\", \"b\"],\n}\n" +
		"dotted.version = \"2.0.0\"\n" +
		"dotted.features = [\"x\"]\n" +
		"'quoted' = 'literal-str-3.0.0'\n" +
		"lit = { version = '4.0.0' }\n" +
		"[dependencies.tbl]\n# c\nfeatures = [\"f\"]\nversion = \"5.0.0\"  # trailing\n" +
		"[target.'cfg(target_os = \"linux\")'.dev-dependencies]\nlin = \"6.0.0\"\n" +
		"[target.x86_64-pc-windows-msvc.build-dependencies]\nwin = \"7.0.0\"\n" +
		"[[bin]]\nname = \"x\"\n" +
		"[dependencies.skipped]\nversion = \"9.0.0\"\npath = \"../s\"\n" +
		"[package.metadata]\nnotdep = \"8.0.0\"\n"
	deps, err := Cargo{Dir: "/"}.Extract("Cargo.toml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	// "quoted" holds a non-requirement string: tracked only if it parses.
	want := "multi@1.2.3 dotted@2.0.0 lit@4.0.0 tbl@5.0.0 lin@6.0.0 win@7.0.0"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %v\nwant %s", got, want)
	}
	for _, tc := range []struct{ pkg, ver, to, from, into string }{
		{"multi", "1.2.3", "1.2.4", `version = "1.2.3", # why`, `version = "1.2.4", # why`},
		{"dotted", "2.0.0", "2.1.0", `dotted.version = "2.0.0"`, `dotted.version = "2.1.0"`},
		{"lit", "4.0.0", "4.0.1", `version = '4.0.0'`, `version = '4.0.1'`},
		{"tbl", "5.0.0", "5.0.2", `version = "5.0.0"  # trailing`, `version = "5.0.2"  # trailing`},
		{"lin", "6.0.0", "6.1.0", `lin = "6.0.0"`, `lin = "6.1.0"`},
		{"win", "7.0.0", "7.0.3", `win = "7.0.0"`, `win = "7.0.3"`},
	} {
		out, err := Cargo{Dir: "/"}.Apply([]byte(src), model.Dependency{File: "Cargo.toml", Package: tc.pkg, Version: tc.ver}, "", tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.pkg, err)
		}
		if want := strings.Replace(src, tc.from, tc.into, 1); string(out) != want {
			t.Errorf("%s: only the requirement moves:\n%s", tc.pkg, out)
		}
	}
}

func TestCargoApplyComparatorsAndWildcardsInPlace(t *testing.T) {
	in := "[dependencies]\na = \">=1.2, <2\"  # keep\nb = { version = \"1.*\", optional = true }\nc = \"1.2.*\"\n"
	for _, tc := range []struct{ pkg, ver, to, from, into string }{
		{"a", ">=1.2, <2", "1.9.0", `">=1.2, <2"  # keep`, `">=1.9, <2"  # keep`},
		{"b", "1.*", "2.3.4", `"1.*", optional`, `"2.*", optional`},
		{"c", "1.2.*", "1.4.0", `"1.2.*"`, `"1.4.*"`},
	} {
		out, err := Cargo{Dir: "/"}.Apply([]byte(in), model.Dependency{File: "Cargo.toml", Package: tc.pkg, Version: tc.ver}, "", tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.pkg, err)
		}
		if want := strings.Replace(in, tc.from, tc.into, 1); string(out) != want {
			t.Errorf("%s:\n%s", tc.pkg, out)
		}
	}
}

func TestCargoInvalidTOMLIsAnError(t *testing.T) {
	for _, src := range []string{
		"[dependencies]\nserde = \"1.0\nx = 1\n",
		"[dependencies\nserde = \"1\"\n",
		"[dependencies]\nserde = \"1\"\nserde = \"2\"\n",
		"[dependencies]\nserde \"1\"\n",
		"[dependencies]\nserde = { version = \"1\" \n",
		"[dependencies]\nserde = \"1\" junk\n",
	} {
		if _, err := (Cargo{Dir: "/"}).Extract("Cargo.toml", []byte(src)); err == nil {
			t.Errorf("%q must not parse", src)
		}
	}
}

func TestCargoRustVersion(t *testing.T) {
	for _, tc := range []struct {
		src, ver string
		inherits bool
	}{
		{"[package]\nname = \"a\"\nrust-version = \"1.70\"\n", "1.70", false},
		{"[package]\nrust-version.workspace = true\n", "", true},
		{"[workspace.package]\nrust-version = \"1.65.0\"\n", "1.65.0", false},
		{"[package]\nname = \"a\"\n", "", false},
		{"[dependencies]\nrust-version = \"9\"\n", "", false},
	} {
		ver, inh, err := CargoRustVersion([]byte(tc.src))
		if err != nil || ver != tc.ver || inh != tc.inherits {
			t.Errorf("%q: got %q, %v, %v", tc.src, ver, inh, err)
		}
	}
	if _, _, err := CargoRustVersion([]byte("[package\n")); err == nil {
		t.Error("invalid TOML must error")
	}
}

func TestRustVersionNewer(t *testing.T) {
	for _, tc := range []struct {
		need, have string
		want       bool
	}{
		{"1.80", "1.70", true}, {"1.70", "1.70.0", false}, {"1.70.1", "1.70", true},
		{"1.9", "1.10", false}, {"1.70", "1.80", false}, {"nightly", "1.70", false}, {"1.70", "x", false},
	} {
		if got := RustVersionNewer(tc.need, tc.have); got != tc.want {
			t.Errorf("RustVersionNewer(%q, %q) = %v", tc.need, tc.have, got)
		}
	}
}
