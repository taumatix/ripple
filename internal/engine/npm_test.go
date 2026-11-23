package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

func TestApplicableNpm(t *testing.T) {
	dep := func(v string) model.Dependency {
		return model.Dependency{Datasource: model.DatasourceNpm, Package: "left-pad", Version: v}
	}
	for _, c := range []struct {
		cur, next string
		want      rules.UpdateType
		ok        bool
	}{
		{"^1.2.3", "1.2.4", rules.Patch, true},
		{"~1.2.3", "1.3.0", rules.Minor, true},
		{"1.2.3", "2.0.0", rules.Major, true},
		{"^1.2.3", "1.2.3", "", false},
		{"^1.2.3", "1.0.0", "", false},
		{"^1.2.3", "1.3.0-rc.1", "", false},
		{"^1.2", "1.3.0", rules.Minor, true},
		{"^1.2", "1.2.9", "", false}, // names 1.2.9 at its precision already
		{"1", "1.9.0", "", false},
		{"1", "2.0.0", rules.Major, true},
		{"1.x", "2.0.0", rules.Major, true},
		{"*", "1.3.0", "", false},
		{">=1.0.0", "1.3.0", "", false},
	} {
		got, ok := applicableNpm(dep(c.cur), "left-pad", c.next)
		if got != c.want || ok != c.ok {
			t.Errorf("%s -> %s = %v, %v; want %v, %v", c.cur, c.next, got, ok, c.want, c.ok)
		}
	}
	if _, ok := applicableNpm(dep("^1.0.0"), "other", "1.1.0"); ok {
		t.Error("another package is not an update")
	}
}
