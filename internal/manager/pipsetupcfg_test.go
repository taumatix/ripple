package manager

import (
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/pypi"
)

func TestPipSetupCfgExtract(t *testing.T) {
	src := "[options]\npython_requires = >=3.8\ninstall_requires =\n    # c\n    requests==2.31.0   # pinned\n    click>=8.0 ; python_version >= \"3.8\"\nsetup_requires = wheel>=0.40\ntests_require =\n    wild==1.*\n\n[options.extras_require]\ndev =\n    pytest>=7.0\n:python_version<\"3.8\" =\n    importlib-metadata>=4.0\n\n[options.entry_points]\nconsole_scripts =\n    app = app:main\n"
	deps, _ := (Pip{Dir: "/"}).Extract("setup.cfg", []byte(src))
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+d.Version)
	}
	if want := "requests==2.31.0 click>=8.0 wheel>=0.40 wild==1.* pytest>=7.0 importlib-metadata>=4.0"; strings.Join(got, " ") != want {
		t.Errorf("got %v", got)
	}
}

func TestPipWildcardSpec(t *testing.T) {
	floor, op := PipSpecFloor("==1.2.*")
	if floor != "1.2" || op != "==*" {
		t.Errorf("floor %q op %q", floor, op)
	}
	for _, spec := range []string{"==1.*,<2", "==*", "!=1.*", ">=1.*"} {
		if f, _ := PipSpecFloor(spec); f != "" {
			t.Errorf("%q must stay unread, floor %q", spec, f)
		}
	}
	allow := PipSpecAllows("==1.2.*")
	for ver, want := range map[string]bool{"1.2.9": false, "1.3.0": true, "2.0": true, "1.2": false} {
		v, _ := pypi.Parse(ver)
		if allow(v) != want {
			t.Errorf("==1.2.* allows %s = %v", ver, !want)
		}
	}
	if got := pipWildPrefix("==1.2.*", "1.3.4"); got != "1.3" {
		t.Errorf("prefix %q", got)
	}
	if got := pipWildPrefix("==1.2.*", "2"); got != "2.0" {
		t.Errorf("padded prefix %q", got)
	}
	if got := pipWildPrefix("==1.2.3", "2.0.1"); got != "2.0.1" {
		t.Errorf("exact pin %q", got)
	}
}
