//go:build live

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

// The //go:generate lines ripple rewrites on a major bump, run with the real tools (ROADMAP
// 170): a fake GitHub repo whose generate lines use go.uber.org/mock's mockgen (with the flags
// v0.6.0 added to the README's list), github.com/golang/mock's mockgen (-self_package) and
// stringer is bumped to lib/v2 by a real ripple run, then the PR's files are written to disk
// and `go generate` runs the rewritten lines. Only the tools come from the real proxy
// (proxy.golang.org, anonymous, read only); lib and the app are the fakes. Run with
// `go test -tags live`; it needs network and a Go toolchain on PATH.
func TestLiveRewrittenGenerateLinesRunWithTheRealTools(t *testing.T) {
	e := newEnv(t, store.NewMemory(), defaultLimits())
	iface := func(v string) map[string]string {
		return map[string]string{"lib.go": "package lib\n\n// Getter reads " + v + ".\ntype Getter interface{ Get() string }\n"}
	}
	e.proxy.Publish(rLib, "v1.0.0", iface("1"))
	e.proxy.Publish(rLib2, "v2.0.0", iface("2"))
	gen := "package main\n\n" +
		"//go:generate go run go.uber.org/mock/mockgen -write_command_comment=false -self_package=example.com/lib -destination=mock_uber.go -package=main example.com/lib Getter\n" +
		"//go:generate go run github.com/golang/mock/mockgen -self_package=example.com/lib -destination=mock_golang.go -package=main example.com/lib Getter\n" +
		"//go:generate go run golang.org/x/tools/cmd/stringer -type=Pill\n"
	e.gh.AddRepo("acme/app", map[string]string{
		"go.mod":       "module example.com/app\n\ngo 1.24\n\nrequire " + rLib + " v1.0.0\n",
		"main.go":      "package main\n\nimport \"" + rLib + "\"\n\nvar _ lib.Getter\n\nfunc main() {}\n",
		"gen.go":       gen,
		"pill.go":      "package main\n\ntype Pill int\n\nconst (\n\tPlacebo Pill = iota\n\tAspirin\n)\n",
		".ripple.yaml": "updates:\n  - ecosystem: gomod\n    directives: extended\n",
	})
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.publish(rLib2, "v2.0.0")
	e.tick()
	head, _ := onePR(t, e, "acme/app", false)

	dir := t.TempDir()
	for _, f := range []string{"go.mod", "main.go", "gen.go", "pill.go"} {
		body, ok := e.gh.FileAt("acme/app", head, f)
		if !ok {
			t.Fatalf("%s missing on the PR head", f)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	genText, _ := e.gh.FileAt("acme/app", head, "gen.go")
	if got := strings.Count(genText, rLib2); got != 4 { // two package arguments and two -self_package values
		t.Fatalf("want the new path on four mockgen words, got %d:\n%s", got, genText)
	}

	// stringer first: -self_package makes the generated mocks refer to Getter unqualified, which
	// does not compile in package main, and stringer type-checks the package it generates for.
	for _, run := range []string{"stringer", "mockgen"} {
		cmd := exec.Command("go", "generate", "-run", run, "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOWORK=off",
			"GOPROXY="+e.proxy.URL+",https://proxy.golang.org", "GONOSUMDB=example.com", "GOSUMDB=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go generate -run %s over the rewritten lines: %v\n%s\ngen.go:\n%s", run, err, out, genText)
		}
	}
	for file, want := range map[string]string{
		"mock_uber.go":   "MockGetter",
		"mock_golang.go": "MockGetter",
		"pill_string.go": "Aspirin",
	} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s: %v, want it to contain %q", file, err, want)
		}
	}
}
