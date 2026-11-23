package registry

import "testing"

func TestParseName(t *testing.T) {
	cases := []struct {
		in   string
		want string // Package(), or "" when not a name
	}{
		{"nginx", "docker.io/library/nginx"},
		{"library/nginx", "docker.io/library/nginx"},
		{"grafana/grafana", "docker.io/grafana/grafana"},
		{"docker.io/grafana/grafana", "docker.io/grafana/grafana"},
		{"index.docker.io/library/nginx", "docker.io/library/nginx"},
		{"ghcr.io/Org/img", ""}, // repository names are lower case
		{"ghcr.io/org/img", "ghcr.io/org/img"},
		{"localhost:5000/x", "localhost:5000/x"},
		{"quay.io/a/b/c", "quay.io/a/b/c"},
		{"${BASE}", ""},
		{"my$img", ""},
		{"", ""},
		{"scratch/", ""},
	}
	for _, c := range cases {
		r, ok := ParseName(c.in)
		got := ""
		if ok {
			got = r.Package()
		}
		if got != c.want {
			t.Errorf("ParseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePackageRoundTrips(t *testing.T) {
	r, ok := ParsePackage("ghcr.io/org/img")
	if !ok || r.Registry != "ghcr.io" || r.Repo != "org/img" {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := ParsePackage("nohost"); ok {
		t.Error("a package always carries its registry")
	}
}
