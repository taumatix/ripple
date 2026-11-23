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
	if _, ok := applicableCargo(d, "serde", "1.3.0"); ok {
		t.Error("a partial requirement is not moved")
	}
}
