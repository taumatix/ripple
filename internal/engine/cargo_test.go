package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

func TestApplicableCargo(t *testing.T) {
	d := model.Dependency{Datasource: model.DatasourceCargo, Package: "serde", Version: "^1.0.100"}
	if typ, ok := applicableCargo(d, "serde", "1.0.200"); !ok || typ != "patch" {
		t.Errorf("= %v %v", typ, ok)
	}
	if _, ok := applicableCargo(d, "serde", "1.0.100"); ok {
		t.Error("the same version is not an update")
	}
	if _, ok := applicableCargo(d, "other", "1.0.200"); ok {
		t.Error("another crate")
	}
	d.Version = "1.2"
	if typ, ok := applicableCargo(d, "serde", "1.3.0"); !ok || typ != "minor" {
		t.Errorf("1.2 -> 1.3.0 = %v %v", typ, ok)
	}
	if _, ok := applicableCargo(d, "serde", "1.2.9"); ok {
		t.Error("1.2 already names 1.2.9 at its precision: nothing to change")
	}
}

// Cargo's compatibility rule decides the type: the leftmost nonzero number of a 0.x
// requirement is its major.
func TestApplicableCargoZeroVersionSemantics(t *testing.T) {
	for _, tc := range []struct {
		req, to string
		want    string
	}{
		{"0.2.3", "0.2.9", "patch"},
		{"0.2.3", "0.3.0", "major"},
		{"^0.2", "0.3.1", "major"},
		{"0.2.3", "1.0.0", "major"},
		{"0.0.3", "0.0.4", "major"},
		{"0.0.3", "0.1.0", "major"},
		{"0", "0.5.0", ""}, // "0" already names 0.5.0 at its precision
		{"0", "1.0.0", "major"},
		{"~1.2.3", "1.3.0", "minor"},
		{"1.2.3", "1.3.0", "minor"},
		{"=1.2.3", "1.2.4", "patch"},
		{"~0.2.3", "0.3.0", "major"},
	} {
		d := model.Dependency{Datasource: model.DatasourceCargo, Package: "x", Version: tc.req}
		typ, ok := applicableCargo(d, "x", tc.to)
		if string(typ) != tc.want || ok != (tc.want != "") {
			t.Errorf("%s -> %s = %q %v, want %q", tc.req, tc.to, typ, ok, tc.want)
		}
	}
}
