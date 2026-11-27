package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

func TestApplicableRubygemsTwoSegment(t *testing.T) {
	dep := func(v string) model.Dependency {
		return model.Dependency{Manager: "bundler", Datasource: model.DatasourceRubygems, Package: "g", Version: v}
	}
	for _, tc := range []struct {
		name, spec, release string
		want                rules.UpdateType
		ok                  bool
	}{
		// docs/vendor/rubygems/gemfile.md, "Gems > Version": "~> 2.1 is identical to >= 2.1 and < 3.0".
		{"later minor is already allowed", "~> 1.2", "1.3.0", "", false},
		{"later patch is already allowed", "~> 1.2", "1.2.9", "", false},
		{"the floor itself", "~> 1.2", "1.2.0", "", false},
		{"older", "~> 1.2", "1.1.9", "", false},
		{"next major leaves the constraint", "~> 1.2", "2.0.0", rules.Major, true},
		{"a major further on", "~> 1.2", "4.7.1", rules.Major, true},
		// "~> 1" resolved by the real `bundle lock` inside 1.x (TestRealtoolBundlerOneSegment, ROADMAP 253).
		{"one segment: later minor is already allowed", "~> 1", "1.6.13", "", false},
		{"one segment: the floor itself", "~> 1", "1.0.0", "", false},
		{"one segment: older", "~> 2", "1.9.0", "", false},
		{"one segment: next major leaves it", "~> 1", "2.2.26", rules.Major, true},
		{"one segment: zero", "~> 0", "1.0.0", rules.Major, true},
		{"three segments keep their old behaviour: minor", "~> 1.2.3", "1.3.0", rules.Minor, true},
		{"three segments: patch", "~> 1.2.3", "1.2.4", rules.Patch, true},
		{"exact pin: patch", "1.2.3", "1.2.4", rules.Patch, true},
	} {
		got, ok := applicableRubygems(dep(tc.spec), "g", tc.release)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: %s -> %s = (%q, %v), want (%q, %v)", tc.name, tc.spec, tc.release, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRubygemsTargetsTwoSegment(t *testing.T) {
	d := model.Dependency{Manager: "bundler", Datasource: model.DatasourceRubygems, Package: "g", Version: "~> 1.2"}
	got := rubygemsTargets([]string{"1.2.0", "1.9.0", "2.0.0", "2.5.1", "3.0.0"}, d, "g")
	if len(got) != 1 || got[0].version != "3.0.0" || got[0].typ != rules.Major {
		t.Errorf("only the newest major is a target, got %+v", got)
	}
}
