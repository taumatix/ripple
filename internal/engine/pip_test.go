package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

func TestApplicablePip(t *testing.T) {
	d := model.Dependency{Datasource: model.DatasourcePip, Package: "requests", Version: "==2.31.0"}
	for ver, want := range map[string]string{"2.31.1": "patch", "2.32.0": "minor", "3.0": "major", "2.31.0.1": "patch"} {
		if typ, ok := applicablePip(d, "requests", ver); !ok || string(typ) != want {
			t.Errorf("%s = %v %v, want %s", ver, typ, ok, want)
		}
	}
	for _, ver := range []string{"2.31.0", "2.30.0", "3.0rc1", "2.32.dev1"} {
		if _, ok := applicablePip(d, "requests", ver); ok {
			t.Errorf("%s is not an update", ver)
		}
	}
	if _, ok := applicablePip(d, "other", "2.32.0"); ok {
		t.Error("another project")
	}
	d.Version = "==2.*"
	if typ, ok := applicablePip(d, "requests", "3.0.0"); !ok || typ != "major" {
		t.Errorf("a wildcard follows a release outside its prefix: %v %v", typ, ok)
	}
	if _, ok := applicablePip(d, "requests", "2.9.0"); ok {
		t.Error("a release the wildcard still matches is not a move")
	}
}
