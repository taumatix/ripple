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
	want := "serde@1.0.100 tokio@~1.2.3 short@1.2 real-name@=4.5.6 pretty_assertions@^1.4.0 cc@1.0.79 libc@0.2.150 anyhow@1.0.70"
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
	for _, spec := range []string{"*", "1.*", ">=1.2", ">=1, <2", "1.2.3-rc.1", "1.2.3.4", ""} {
		if _, ok := ParseCargoReq(spec); ok {
			t.Errorf("%q must not parse", spec)
		}
	}
}
