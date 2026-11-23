package engine

import "testing"

func TestStaleVersionReadsOnlyThisModulesBranches(t *testing.T) {
	cases := []struct {
		head, pkg, want string
	}{
		{"ripple/go/example.com-lib-v1.1.0", "example.com/lib", "v1.1.0"},
		{"ripple/go/example.com-lib-v2.0.0-rc.1", "example.com/lib", "v2.0.0-rc.1"},
		{"ripple/go/example.com-lib-extra-v1.0.0", "example.com/lib", ""},
		{"ripple/go/example.com-lib-v2-v2.0.0", "example.com/lib", ""},
		{"ripple/go/example.com-lib-v1.1", "example.com/lib", ""},
		{"ripple/group/aws", "example.com/lib", ""},
		{"feature/example.com-lib-v1.1.0", "example.com/lib", ""},
	}
	for _, c := range cases {
		got, ok := staleVersion(c.head, c.pkg)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("staleVersion(%q, %q) = %q, %v; want %q", c.head, c.pkg, got, ok, c.want)
		}
	}
}
