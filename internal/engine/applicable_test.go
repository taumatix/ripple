package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

func TestApplicable(t *testing.T) {
	dep := func(mgr, pkg, v string) model.Dependency {
		return model.Dependency{Manager: mgr, Datasource: "go", Package: pkg, Version: v}
	}
	for _, tc := range []struct {
		name string
		d    model.Dependency
		pkg  string
		ver  string
		want rules.UpdateType
		ok   bool
	}{
		{"same path patch", dep("gomod", "a.com/m", "v1.0.0"), "a.com/m", "v1.0.1", rules.Patch, true},
		{"same path older", dep("gomod", "a.com/m", "v1.1.0"), "a.com/m", "v1.0.1", "", false},
		{"next major", dep("gomod", "a.com/m/v2", "v2.1.0"), "a.com/m/v3", "v3.0.0", rules.Major, true},
		{"unsuffixed to v2", dep("gomod", "a.com/m", "v1.4.0"), "a.com/m/v2", "v2.0.0", rules.Major, true},
		{"gopkg.in", dep("gomod", "gopkg.in/yaml.v2", "v2.4.0"), "gopkg.in/yaml.v3", "v3.0.1", rules.Major, true},
		{"skipping majors", dep("gomod", "a.com/m/v2", "v2.1.0"), "a.com/m/v5", "v5.0.0", rules.Major, true},
		{"never backwards", dep("gomod", "a.com/m/v3", "v3.0.0"), "a.com/m/v2", "v2.9.0", "", false},
		{"other line", dep("gomod", "a.com/m/v2", "v2.0.0"), "a.com/n/v3", "v3.0.0", "", false},
		{"path major must match version", dep("gomod", "a.com/m/v2", "v2.0.0"), "a.com/m/v3", "v2.5.0", "", false},
		{"regex manager never renames", dep("regex", "a.com/m/v2", "v2.0.0"), "a.com/m/v3", "v3.0.0", "", false},
		{"prefix is not a line", dep("gomod", "a.com/m", "v1.0.0"), "a.com/mx/v2", "v2.0.0", "", false},
	} {
		got, ok := applicable(tc.d, "go", tc.pkg, tc.ver)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReleaseNotes(t *testing.T) {
	for pkg, want := range map[string]string{
		"github.com/google/go-github/v61": "https://github.com/google/go-github/releases/tag/v61.0.0",
		"gopkg.in/yaml.v3":                "https://pkg.go.dev/gopkg.in/yaml.v3@v61.0.0",
		"example.com/m/v2":                "https://pkg.go.dev/example.com/m/v2@v61.0.0",
	} {
		if got := releaseNotes(pkg, "v61.0.0"); got != want {
			t.Errorf("%s: %s, want %s", pkg, got, want)
		}
	}
}

func TestApplicablePseudoCurrent(t *testing.T) {
	const pseudo = "v1.2.4-0.20240101000000-abcdefabcdef"
	dep := func(v string) model.Dependency {
		return model.Dependency{Manager: "gomod", Datasource: "go", Package: "example.com/m", Version: v}
	}
	for _, tc := range []struct {
		name string
		d    model.Dependency
		pkg  string
		fix  string
		want rules.UpdateType
		ok   bool
	}{
		{"patch release above the commit", dep(pseudo), "example.com/m", "v1.2.4", rules.Patch, true},
		{"minor release above", dep(pseudo), "example.com/m", "v1.3.0", rules.Minor, true},
		{"release at or below the commit is no move", dep(pseudo), "example.com/m", "v1.2.3", "", false},
		{"another major is never proposed", dep(pseudo), "example.com/m", "v2.0.0", "", false},
		{"another module", dep(pseudo), "example.com/other", "v1.2.4", "", false},
		{"a pseudo-version target is the other opt-in", dep(pseudo), "example.com/m", "v1.2.5-0.20240201000000-123456abcdef", "", false},
		{"a prerelease target", dep(pseudo), "example.com/m", "v1.3.0-rc.1", "", false},
		{"a released dependency is not this case", dep("v1.2.0"), "example.com/m", "v1.2.4", "", false},
	} {
		typ, ok := applicablePseudoCurrent(tc.d, tc.pkg, tc.fix)
		if typ != tc.want || ok != tc.ok {
			t.Errorf("%s: got %q %v, want %q %v", tc.name, typ, ok, tc.want, tc.ok)
		}
	}
}
