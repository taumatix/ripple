package manager

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

func eq(t *testing.T, want, got any, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%v: want %#v, got %#v", msg, want, got)
	}
}

func tcDeps(t *testing.T, mode, content string) map[string]string {
	t.Helper()
	deps, err := GoMod{Toolchain: mode}.Extract("go.mod", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range deps {
		if d.Datasource == model.DatasourceGoToolchain {
			got[d.Package] = d.Version
		}
	}
	return got
}

func TestGoModExtractsToolchainOnlyWhenAsked(t *testing.T) {
	const mod = "module m\n\ngo 1.24\n\ntoolchain go1.24.3\n"
	eq(t, map[string]string{}, tcDeps(t, "", mod), "off by default")
	eq(t, map[string]string{"toolchain": "v1.24.3"}, tcDeps(t, GoToolchainLine, mod))
	eq(t, map[string]string{"go": "v1.24", "toolchain": "v1.24.3"}, tcDeps(t, GoToolchainDirective, mod))
}

func TestGoModToolchainIgnoresWhatIsNotAVersion(t *testing.T) {
	eq(t, map[string]string{}, tcDeps(t, GoToolchainDirective, "module m\n\ngo 1.25rc1\n\ntoolchain default\n"))
	eq(t, map[string]string{"go": "v1.22.0"}, tcDeps(t, GoToolchainDirective, "module m\n\ngo 1.22.0\n"), "no toolchain line, none invented")
}

func applyTC(t *testing.T, content, pkg, from, to string) (string, error) {
	t.Helper()
	d := model.Dependency{File: "go.mod", Manager: "gomod", Datasource: model.DatasourceGoToolchain, Package: pkg, Version: from}
	out, err := GoMod{}.Apply([]byte(content), d, pkg, to)
	return string(out), err
}

func TestApplyToolchainLine(t *testing.T) {
	out, err := applyTC(t, "module m\n\ngo 1.24\n\ntoolchain go1.24.3\n", "toolchain", "v1.24.3", "v1.24.9")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "module m\n\ngo 1.24\n\ntoolchain go1.24.9\n", out, "the go directive is untouched")
}

func TestApplyDirectiveKeepsItsSpelling(t *testing.T) {
	out, err := applyTC(t, "module m\n\ngo 1.24\n", "go", "v1.24", "v1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "module m\n\ngo 1.26\n", out, "a minor stays a minor, never a patch")
	out, err = applyTC(t, "module m\n\ngo 1.24.2\n", "go", "v1.24.2", "v1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "module m\n\ngo 1.26.0\n", out, "the lowest requirement that names the minor")
}

func TestApplyDirectiveRaisesAToolchainLineItWouldContradict(t *testing.T) {
	out, err := applyTC(t, "module m\n\ngo 1.24\n\ntoolchain go1.24.3\n", "go", "v1.24", "v1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "module m\n\ngo 1.26\n\ntoolchain go1.26.11\n", out)
	out, err = applyTC(t, "module m\n\ngo 1.24\n\ntoolchain go1.27.0\n", "go", "v1.24", "v1.26.11")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "toolchain go1.27.0") {
		t.Errorf("a toolchain line already above must stay:\n%s", out)
	}
}

func TestApplyToolchainNeverLowersAndDetectsStale(t *testing.T) {
	_, err := applyTC(t, "module m\n\ngo 1.24\n\ntoolchain go1.24.3\n", "toolchain", "v1.24.3", "v1.24.1")
	if err == nil {
		t.Error("a lower version is refused")
	}
	_, err = applyTC(t, "module m\n\ngo 1.24\n\ntoolchain go1.24.5\n", "toolchain", "v1.24.3", "v1.24.9")
	if !errors.Is(err, ErrStale) {
		t.Errorf("want ErrStale, got %v", err)
	}
	_, err = applyTC(t, "module m\n\ngo 1.25\n", "go", "v1.24", "v1.26.11")
	if !errors.Is(err, ErrStale) {
		t.Errorf("want ErrStale, got %v", err)
	}
	_, err = applyTC(t, "module m\n\ngo 1.24\n", "toolchain", "v1.24.3", "v1.24.9")
	if !errors.Is(err, ErrStale) {
		t.Errorf("want ErrStale, got %v", err)
	}
}

func TestGoDirectiveSpelling(t *testing.T) {
	eq(t, "1.26", GoDirective("v1.24", "v1.26.11"))
	eq(t, "1.26.0", GoDirective("v1.24.0", "v1.26.11"))

}
