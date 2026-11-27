package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

func TestApplicablePackagistTwoSegment(t *testing.T) {
	dep := func(v string) model.Dependency {
		return model.Dependency{Manager: "composer", Datasource: model.DatasourcePackagist, Package: "a/b", Version: v}
	}
	for _, tc := range []struct {
		spec, release string
		want          rules.UpdateType
		ok            bool
	}{
		// docs/vendor/packagist/composer-versions.md: ~1.2 is ">=1.2 <2.0.0"; ^0.3 is ">=0.3.0 <0.4.0".
		{"^1.2", "1.9.0", "", false},
		{"~1.2", "1.2.5", "", false},
		{"^1.2", "1.1.0", "", false},
		{"^1.2", "2.0.0", rules.Major, true},
		{"~1.2", "2.3.1", rules.Major, true},
		{"^0.3", "0.3.7", "", false},
		{"^0.3", "0.4.0", rules.Minor, true},
		{"^0.3", "1.0.0", rules.Major, true},
		// composer 2 (TestRealtoolComposerConstraintMeaning): ~0.3 locks 0.9.11; ^1 and ~1 lock 1.27.1; ^0 locks 0.9.11.
		{"~0.3", "0.9.0", "", false},
		{"~0.3", "1.0.0", rules.Major, true},
		{"^1", "1.9.0", "", false},
		{"~1", "1.9.0", "", false},
		{"^1", "2.0.0", rules.Major, true},
		{"~1", "3.1.0", rules.Major, true},
		{"^0", "0.9.0", "", false},
		{"^0", "1.0.0", rules.Major, true},
		{"^1.2.3", "1.3.0", rules.Minor, true}, // three segments: as before
		{"1.2.3", "1.2.4", rules.Patch, true},
	} {
		got, ok := applicablePackagist(dep(tc.spec), "a/b", tc.release)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s -> %s = (%q, %v), want (%q, %v)", tc.spec, tc.release, got, ok, tc.want, tc.ok)
		}
	}
}
