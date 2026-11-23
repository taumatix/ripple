package engine

import (
	"testing"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
)

func TestActionTarget(t *testing.T) {
	tags := []string{"v1", "v3", "v4", "v5", "v4.1", "v4.2", "v4.1.2", "v4.2.0", "v5.0.1", "v5.0.0", "v6.0.0-rc.1", "main", "nightly", "6", "7.1.0", "release/v9"}
	for _, c := range []struct{ cur, want string }{
		{"v4", "v5"},         // floating major keeps its precision
		{"v4.1", "v4.2"},     // minor
		{"v4.1.2", "v5.0.1"}, // newest full version
		{"v5", ""},           // already newest
		{"v5.0.1", ""},       // already newest
		{"v4.2", ""},         // newest 2-component tag
		{"v2", "v5"},         // a gap in the tags is fine
		{"6", ""},            // v-less, one component: no newer v-less tag of its precision
		{"7.0.0", "7.1.0"},   // v-less keeps its spelling
		{"v6.0.0-rc.1", ""},  // not a tag ripple tracks
	} {
		if got := actionTarget(c.cur, tags); got != c.want {
			t.Errorf("actionTarget(%q) = %q, want %q", c.cur, got, c.want)
		}
	}
}

func TestApplicableGitHubTags(t *testing.T) {
	d := model.Dependency{Datasource: model.DatasourceGitHubTags, Package: "actions/checkout", Version: "v4"}
	for _, c := range []struct {
		pkg, ver string
		typ      rules.UpdateType
		ok       bool
	}{
		{"actions/checkout", "v5", rules.Major, true},
		{"actions/checkout", "v4", "", false},
		{"actions/checkout", "v3", "", false},
		{"actions/checkout", "v4.1.0", "", false}, // another precision is not this dependency's update
		{"actions/other", "v5", "", false},
	} {
		typ, ok := applicable(d, model.DatasourceGitHubTags, c.pkg, c.ver)
		if ok != c.ok || typ != c.typ {
			t.Errorf("applicable(v4 -> %s %s) = %q, %v; want %q, %v", c.pkg, c.ver, typ, ok, c.typ, c.ok)
		}
	}
	p := model.Dependency{Datasource: model.DatasourceGitHubTags, Package: "a/b", Version: "v1.2.3"}
	if typ, ok := applicable(p, model.DatasourceGitHubTags, "a/b", "v1.3.0"); !ok || typ != rules.Minor {
		t.Errorf("minor: %q %v", typ, ok)
	}
	if typ, ok := applicable(p, model.DatasourceGitHubTags, "a/b", "v1.2.4"); !ok || typ != rules.Patch {
		t.Errorf("patch: %q %v", typ, ok)
	}
}
