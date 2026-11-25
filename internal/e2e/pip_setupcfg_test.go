package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const pipSetupCfg = `[metadata]
name = app
version = 1.0

[options]
python_requires = >=3.8
install_requires =
    # the HTTP stack
    requests==2.31.0   # pinned
    click>=8.0 ; python_version >= "3.8"
    Flask_Login~=0.6.2
setup_requires = wheel>=0.40
tests_require =
    wild==1.*

[options.extras_require]
dev =
    pytest>=7.0
:python_version<"3.8" =
    importlib-metadata>=4.0

[options.entry_points]
console_scripts =
    app = app:main
`

// setup.cfg is read as text (never executed), and a `==1.*` wildcard follows a release
// outside its prefix, keeping the wildcard.
func TestPipSetupCfgAndWildcardBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		idx := testutil.NewPyPI(t)
		lim := defaultLimits()
		lim.OpenPRLimit = 10
		e := newEnvOpts(t, st, lim, opts{pypi: idx})
		for _, p := range [][2]string{{"requests", "2.31.0"}, {"click", "8.0"}, {"flask-login", "0.6.2"}, {"wheel", "0.40"},
			{"wild", "1.0"}, {"pytest", "7.0"}, {"importlib-metadata", "4.0"}} {
			idx.Publish(p[0], p[1])
		}
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": "open-pull-requests-limit: 10\n" + pipConfig, "setup.cfg": pipSetupCfg, "setup.py": "raise SystemExit('never run')\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		idx.Publish("requests", "2.32.3")
		idx.Publish("click", "8.1.7")
		idx.Publish("flask-login", "0.6.10")
		idx.Publish("wheel", "0.45.1")
		idx.Publish("wild", "1.5") // inside the prefix: not a move
		idx.Publish("wild", "2.3.1")
		idx.Publish("pytest", "8.3.4")
		idx.Publish("importlib-metadata", "8.5.0")
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 7 {
			t.Fatalf("one PR per project, got %d", len(prs))
		}
		want := map[string][2]string{
			"requests":           {"requests==2.31.0   # pinned", "requests==2.32.3   # pinned"},
			"click":              {"click>=8.0 ;", "click>=8.1.7 ;"},
			"flask-login":        {"Flask_Login~=0.6.2", "Flask_Login~=0.6.10"},
			"wheel":              {"wheel>=0.40", "wheel>=0.45.1"},
			"wild":               {"wild==1.*", "wild==2.*"},
			"pytest":             {"pytest>=7.0", "pytest>=8.3.4"},
			"importlib-metadata": {"importlib-metadata>=4.0", "importlib-metadata>=8.5.0"},
		}
		for _, pr := range prs {
			name := strings.TrimPrefix(pr.Head, "ripple/pip/")
			name = name[:strings.LastIndex(name, "-")]
			w, ok := want[name]
			if !ok {
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			got, _ := e.gh.FileAt("acme/app", pr.Head, "setup.cfg")
			if exp := strings.Replace(pipSetupCfg, w[0], w[1], 1); got != exp {
				t.Errorf("%s: only the version moves:\n%s", pr.Head, got)
			}
			if sp, _ := e.gh.FileAt("acme/app", pr.Head, "setup.py"); sp != "raise SystemExit('never run')\n" {
				t.Errorf("%s: setup.py must be untouched", pr.Head)
			}
		}
	})
}
