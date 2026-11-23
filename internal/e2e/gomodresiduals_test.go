package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

func rlibWithCmd(v, fn string) map[string]string {
	f := rlib(v, fn)
	f["cmd/gen/main.go"] = "package main\n\nfunc main() {}\n"
	return f
}

const (
	residualGen = "package main\n\n" +
		"//go:generate go run example.com/lib/cmd/gen@v1.0.0 -o out.go\n" +
		"//go:generate go run example.com/lib/cmd/gen@latest\n" +
		"//go:generate go tool example.com/lib/cmd/gen\n" +
		"//go:generate go tool gen\n"
	residualGenWant = "package main\n\n" +
		"//go:generate go run example.com/lib/v2/cmd/gen@v2.0.0 -o out.go\n" +
		"//go:generate go run example.com/lib/v2/cmd/gen@latest\n" +
		"//go:generate go tool example.com/lib/v2/cmd/gen\n" +
		"//go:generate go tool gen\n"
	residualMock = "package main\n\n" +
		"//go:generate mockgen -destination=mock_lib.go example.com/lib Getter\n" +
		"//go:generate go run go.uber.org/mock/mockgen -source=lib.go -imports=l=example.com/lib,o=other/pkg\n" +
		"//go:generate go run github.com/golang/mock/mockgen -prog_only -self_package=example.com/lib example.com/lib Getter\n"
	residualMockWant = "package main\n\n" +
		"//go:generate mockgen -destination=mock_lib.go example.com/lib/v2 Getter\n" +
		"//go:generate go run go.uber.org/mock/mockgen -source=lib.go -imports=l=example.com/lib/v2,o=other/pkg\n" +
		"//go:generate go run github.com/golang/mock/mockgen -prog_only -self_package=example.com/lib/v2 example.com/lib/v2 Getter\n"
	residualBroken     = "//go:build ignore\n\npackage main\n\n//go:generate go run example.com/lib/cmd/gen@v1.0.0\nfunc ( {\n"
	residualBrokenWant = "//go:build ignore\n\npackage main\n\n//go:generate go run example.com/lib/v2/cmd/gen@v2.0.0\nfunc ( {\n"
)

func residualRepo(config string) map[string]string {
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.24\n\nrequire " + rLib + " v1.0.0\n\ntool " + rLib + "/cmd/gen\n",
		"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
		"gen.go":  residualGen,
		"bad.go":  residualBroken,
		"mock.go": residualMock,
	}
	if config != "" {
		files[".ripple.yaml"] = config
	}
	return files
}

func residualRun(t *testing.T, st store.Store, config string) (*env, string) {
	e := newEnv(t, st, defaultLimits())
	e.proxy.Publish(rLib, "v1.0.0", rlibWithCmd("1.0.0", "Get"))
	e.proxy.Publish(rLib2, "v2.0.0", rlibWithCmd("2.0.0", "Get"))
	e.gh.AddRepo("acme/app", residualRepo(config))
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.publish(rLib2, "v2.0.0")
	e.tick()
	head, _ := onePR(t, e, "acme/app", false)
	return e, head
}

func fileIs(t *testing.T, e *env, head, file, want string) {
	t.Helper()
	if got, _ := e.gh.FileAt("acme/app", head, file); got != want {
		t.Errorf("%s:\n--- got\n%s\n--- want\n%s", file, got, want)
	}
}

// A tool directive follows its module to the new major; without that go mod tidy
// fails on a tool package the module no longer provides and the PR opens as a draft.
func TestMajorBumpMovesToolDirective(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := residualRun(t, st, "")
		mod, _ := e.gh.FileAt("acme/app", head, "go.mod")
		if !strings.Contains(mod, "tool "+rLib2+"/cmd/gen") || strings.Contains(mod, "tool "+rLib+"/cmd/gen") {
			t.Errorf("go.mod:\n%s", mod)
		}
		// Off by default: the directives are untouched, as before.
		fileIs(t, e, head, "gen.go", residualGen)
		fileIs(t, e, head, "bad.go", residualBroken)
		fileIs(t, e, head, "mock.go", residualMock)
	})
}

func TestMajorBumpExtendedDirectives(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := residualRun(t, st, "updates:\n  - ecosystem: gomod\n    directives: extended\n")
		fileIs(t, e, head, "gen.go", residualGenWant)
		fileIs(t, e, head, "bad.go", residualBrokenWant)
		fileIs(t, e, head, "mock.go", residualMockWant)
	})
}

// wsOnlyFiles is a workspace whose module b reaches module a only through go.work:
// b requires a at a published version that is behind the workspace's copy (it lacks
// a.Version) and has no replace for it, so b builds only with go.work on.
func wsOnlyFiles() map[string]string {
	return map[string]string{
		"go.work":  "go 1.22\n\nuse (\n\t./a\n\t./b\n)\n",
		"a/go.mod": gomod("example.com/ws/a", rLib+" v1.0.0"),
		"a/a.go":   "package a\n\nimport \"" + rLib + "\"\n\nfunc Version() string { return lib.Get() }\n",
		"b/go.mod": gomod("example.com/ws/b", rLib+" v1.0.0", "example.com/ws/a v0.1.0"),
		"b/b.go":   "package b\n\nimport (\n\t\"example.com/ws/a\"\n\t\"" + rLib + "\"\n)\n\nfunc Version() string { return a.Version() + lib.Get() }\n",
	}
}

func wsOnlyRun(t *testing.T, st store.Store, published map[string]string, files map[string]string) (*env, string) {
	e := newEnv(t, st, defaultLimits())
	e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
	e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
	e.proxy.Publish("example.com/ws/a", "v0.1.0", published)
	e.gh.AddRepo("acme/ws", files)
	start(t, e, "acme/ws")
	e.clock.Advance(time.Minute)
	e.publish(rLib, "v1.1.0")
	e.tick()
	head, _ := onePR(t, e, "acme/ws", false)
	return e, head
}

func TestWorkspaceOnlyModulesAreRefreshedWithoutReplace(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := wsOnlyRun(t, st, map[string]string{"a.go": "package a\n"}, wsOnlyFiles())
		for _, m := range []string{"a", "b"} {
			mod, _ := e.gh.FileAt("acme/ws", head, m+"/go.mod")
			if !strings.Contains(mod, rLib+" v1.1.0") || strings.Contains(mod, "replace") {
				t.Errorf("%s/go.mod:\n%s", m, mod)
			}
			sumHas(t, e, "acme/ws", head, m+"/go.sum", []string{rLib + " v1.1.0 h1:"}, []string{rLib + " v1.0.0"})
		}
		if mod, _ := e.gh.FileAt("acme/ws", head, "b/go.mod"); !strings.Contains(mod, "example.com/ws/a v0.1.0") {
			t.Errorf("b/go.mod lost its requirement on a:\n%s", mod)
		}
	})
}

// The published sibling has no package a at all, so go mod tidy of b fails on its own.
func TestWorkspaceOnlyModulesTidyAgainstAMissingPackage(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := wsOnlyRun(t, st, map[string]string{"README.md": "x\n"}, wsOnlyFiles())
		for _, m := range []string{"a", "b"} {
			mod, _ := e.gh.FileAt("acme/ws", head, m+"/go.mod")
			if !strings.Contains(mod, rLib+" v1.1.0") || strings.Contains(mod, "replace") {
				t.Errorf("%s/go.mod:\n%s", m, mod)
			}
		}
		if mod, _ := e.gh.FileAt("acme/ws", head, "b/go.mod"); !strings.Contains(mod, "example.com/ws/a v0.1.0") {
			t.Errorf("b/go.mod lost its requirement on a:\n%s", mod)
		}
	})
}

// A workspace's own vendor/ (made by go work vendor) is refreshed with the modules.
func TestWorkspaceVendorIsRefreshed(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
		e.gh.AddRepo("acme/wsv", map[string]string{
			"go.work":                    "go 1.22\n\nuse ./a\n",
			"a/go.mod":                   gomod("example.com/wsv/a", rLib+" v1.0.0"),
			"a/a.go":                     "package a\n\nimport \"" + rLib + "\"\n\nfunc Version() string { return lib.Get() }\n",
			"vendor/modules.txt":         "## workspace\n# " + rLib + " v1.0.0\n## explicit; go 1.22\n" + rLib + "\n",
			"vendor/" + rLib + "/lib.go": rlib("1.0.0", "Get")["lib.go"],
		})
		start(t, e, "acme/wsv")
		e.clock.Advance(time.Minute)
		e.publish(rLib, "v1.1.0")
		e.tick()

		head, _ := onePR(t, e, "acme/wsv", false)
		mt, _ := e.gh.FileAt("acme/wsv", head, "vendor/modules.txt")
		if !strings.Contains(mt, "# "+rLib+" v1.1.0") || strings.Contains(mt, rLib+" v1.0.0") {
			t.Errorf("vendor/modules.txt:\n%s", mt)
		}
		if v, _ := e.gh.FileAt("acme/wsv", head, "vendor/"+rLib+"/lib.go"); !strings.Contains(v, "1.1.0") {
			t.Errorf("vendored lib not refreshed:\n%s", v)
		}
	})
}

func tidyRun(t *testing.T, st store.Store, config string) (*env, string) {
	e := newEnv(t, st, defaultLimits())
	e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
	e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
	files := map[string]string{
		"go.mod": gomod("example.com/app", rLib+" v1.0.0", "example.com/unused v1.0.0") +
			"\nreplace example.com/unused => ./unused\n",
		"unused/go.mod": "module example.com/unused\n\ngo 1.22\n",
		"main.go":       "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
	}
	if config != "" {
		files[".ripple.yaml"] = config
	}
	e.gh.AddRepo("acme/app", files)
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.publish(rLib, "v1.1.0")
	e.tick()
	head, _ := onePR(t, e, "acme/app", false)
	return e, head
}

// go mod tidy drops a requirement nothing imports, and without tidy: minimal that is
// still what the PR carries.
func TestTidyDefaultStillDropsUnusedRequirements(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := tidyRun(t, st, "")
		mod, _ := e.gh.FileAt("acme/app", head, "go.mod")
		if strings.Contains(mod, "example.com/unused v1.0.0") || !strings.Contains(mod, rLib+" v1.1.0") {
			t.Errorf("default go.mod:\n%s", mod)
		}
	})
}

func TestTidyMinimalOption(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := tidyRun(t, st, "updates:\n  - ecosystem: gomod\n    tidy: minimal\n")
		mod, _ := e.gh.FileAt("acme/app", head, "go.mod")
		if !strings.Contains(mod, "example.com/unused v1.0.0") || !strings.Contains(mod, rLib+" v1.1.0") {
			t.Errorf("minimal go.mod:\n%s", mod)
		}
		sumHas(t, e, "acme/app", head, "go.sum", []string{rLib + " v1.1.0 h1:"}, []string{rLib + " v1.0.0"})
	})
}
