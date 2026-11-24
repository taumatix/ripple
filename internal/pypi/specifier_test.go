package pypi_test

import (
	"testing"

	"github.com/taumatix/ripple/internal/pypi"
)

func TestSatisfies(t *testing.T) {
	for _, tc := range []struct {
		spec, v string
		want    bool
	}{
		{">=7.0,<8", "7.4.4", true},
		{">=7.0,<8", "8.0.0", false},
		{">=7.0,<8", "6.9", false},
		{"<8", "8.0.post1", false},
		{">=1.20,<2,!=1.5.*", "1.5.3", false},
		{">=1.20,<2,!=1.5.*", "1.26.4", true},
		{"==1.*", "1.9", true},
		{"==1.*", "2.0", false},
		{"==1.0", "1.0.0", true},
		{"~=1.4.2", "1.4.9", true},
		{"~=1.4.2", "1.5.0", false},
		{"~=1.4", "1.9", true},
		{"~=1.4", "2.0", false},
		{">1.0", "1.0.post1", false},
		{">1.0", "1.0.1", true},
		{">1.0.post1", "1.0.post2", true},
		{"<=2", "2.0.0", true},
	} {
		v, _ := pypi.Parse(tc.v)
		got, ok := pypi.Satisfies(tc.spec, v)
		if !ok || got != tc.want {
			t.Errorf("Satisfies(%q, %q) = %v, %v; want %v", tc.spec, tc.v, got, ok, tc.want)
		}
	}
	v, _ := pypi.Parse("1.0")
	for _, spec := range []string{"===1.0", ">=x", "~=1", "<1.*", ">=1,"} {
		if _, ok := pypi.Satisfies(spec, v); ok {
			t.Errorf("%q should be unknown", spec)
		}
	}
}
