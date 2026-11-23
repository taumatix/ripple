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
	residualBroken     = "//go:build ignore\n\npackage main\n\n//go:generate go run example.com/lib/cmd/gen@v1.0.0\nfunc ( {\n"
	residualBrokenWant = "//go:build ignore\n\npackage main\n\n//go:generate go run example.com/lib/v2/cmd/gen@v2.0.0\nfunc ( {\n"
)

func residualRepo(config string) map[string]string {
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.24\n\nrequire " + rLib + " v1.0.0\n\ntool " + rLib + "/cmd/gen\n",
		"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
		"gen.go":  residualGen,
		"bad.go":  residualBroken,
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
	})
}

func TestMajorBumpExtendedDirectives(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, head := residualRun(t, st, "updates:\n  - ecosystem: gomod\n    directives: extended\n")
		fileIs(t, e, head, "gen.go", residualGenWant)
		fileIs(t, e, head, "bad.go", residualBrokenWant)
	})
}
