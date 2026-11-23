package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
)

// installFakeGo puts a go.dev-shaped release directory under dir whose bin/go logs its
// arguments and succeeds. It stands for a Go the server does not itself run, so a
// module whose go line is above the server's Go can still be checked.
func installFakeGo(t *testing.T, dir, release string) (log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "calls.log")
	bin := filepath.Join(dir, "go"+release, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\nexit 0\n", release, log)
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func calls(t *testing.T, log string) string {
	t.Helper()
	b, _ := os.ReadFile(log)
	return string(b)
}

// directiveAboveServer builds a repo whose directive will move past the Go the tests run
// on, with the release feed arranged so that it does.
func directiveAboveServer(t *testing.T, st store.Store, toolchainDir string) (*env, int) {
	t.Helper()
	n := localMinor(t)
	dl := newDL(t)
	var tweak func(*engine.Engine)
	if toolchainDir != "" {
		tweak = func(en *engine.Engine) { en.GoToolchainDir = toolchainDir }
	}
	e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl, engine: tweak})
	dl.Set(goVer(n+2, 1), goVer(n+1, 1))
	e.gh.AddRepo("acme/app", withMode(tcRepo(fmt.Sprintf("1.%d", n-1), ""), "directive"))
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.tick()
	return e, n
}

// Without a toolchain directory the verification Go is the server's, so a directive above
// it fails verification: the behaviour the new setting is measured against.
func TestDirectiveAboveServerGoIsADraftWithoutToolchainDir(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, _ := directiveAboveServer(t, st, "")
		onePR(t, e, "acme/app", true)
	})
}

// With the release installed, the same bump is checked by the Go it names.
func TestVerifyUsesTheGoTheBumpNames(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		dir := t.TempDir()
		n := localMinor(t)
		log := installFakeGo(t, dir, fmt.Sprintf("1.%d.4", n+1))
		e, _ := directiveAboveServer(t, st, dir)
		_, body := onePR(t, e, "acme/app", false)
		got := calls(t, log)
		for _, want := range []string{"mod tidy", "build ./...", "vet ./..."} {
			if !strings.Contains(got, want) {
				t.Errorf("the installed Go never ran %q:\n%s", want, got)
			}
		}
		if rel := fmt.Sprintf("go1.%d.4", n+1); !strings.Contains(body, rel) || !strings.Contains(body, "toolchain directory") {
			t.Errorf("body should say which Go checked it:\n%s", body)
		}
		if strings.Contains(body, "need not be the version named above") {
			t.Errorf("body still says the check ran with a different Go:\n%s", body)
		}
	})
}

// A directory that lacks the named release changes nothing: the server's Go is used and
// the PR says CI is the real check, as before.
func TestMissingReleaseFallsBackToTheServersGo(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		dir := t.TempDir()
		n := localMinor(t)
		log := installFakeGo(t, dir, fmt.Sprintf("1.%d.4", n-5))
		e, _ := directiveAboveServer(t, st, dir)
		onePR(t, e, "acme/app", true)
		if got := calls(t, log); got != "" {
			t.Errorf("an unrelated release must not run:\n%s", got)
		}
	})
}
