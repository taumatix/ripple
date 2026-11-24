package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/gotool"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// publishFakeToolchain publishes golang.org/toolchain at the version the Go project uses
// for a release, with a bin/go that logs its arguments.
func publishFakeToolchain(e *env, release, log string) {
	script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\nexit 0\n", release, log)
	e.proxy.Publish("golang.org/toolchain", fmt.Sprintf("v0.0.1-go%s.%s-%s", release, runtime.GOOS, runtime.GOARCH),
		map[string]string{"bin/go": script, "pkg/tool/x": "x"})
}

// lazyInstaller builds the installer on first use, once the env (and so the proxy URL) exists.
type lazyInstaller func() *gotool.Installer

func (l lazyInstaller) Ensure(ctx context.Context, goLine, toolchainLine string) (string, string, error) {
	return l().Ensure(ctx, goLine, toolchainLine)
}

// downloadingServer is directiveAboveServer with an installer that reads the fake proxy.
func downloadingServer(t *testing.T, st store.Store, dir string, publish func(e *env, n int)) (*env, int) {
	t.Helper()
	n := localMinor(t)
	dl := newDL(t)
	var e *env
	var sdb *testutil.SumDB
	tweak := func(en *engine.Engine) {
		en.GoToolchainDir = dir
		en.GoInstaller = lazyInstaller(func() *gotool.Installer {
			return &gotool.Installer{Dir: dir, Proxy: e.proxy.URL, SumDB: sdb.GoSumDB()}
		})
	}
	e = newEnvOpts(t, st, defaultLimits(), opts{goDL: dl, engine: tweak})
	sdb = testutil.NewSumDB(t, e.proxy)
	publish(e, n)
	dl.Set(goVer(n+2, 1), goVer(n+1, 1))
	e.gh.AddRepo("acme/app", withMode(tcRepo(fmt.Sprintf("1.%d", n-1), ""), "directive"))
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.tick()
	return e, n
}

// A directive bump to a release the toolchain directory lacks installs it from the module
// proxy and verifies with it.
func TestVerifyDownloadsTheGoTheBumpNames(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		dir := t.TempDir()
		log := filepath.Join(t.TempDir(), "calls.log")
		e, n := downloadingServer(t, st, dir, func(e *env, n int) {
			publishFakeToolchain(e, fmt.Sprintf("1.%d.4", n+1), log)
			publishFakeToolchain(e, fmt.Sprintf("1.%d.1", n+1), log)
		})
		_, body := onePR(t, e, "acme/app", false)
		got := calls(t, log)
		for _, want := range []string{"mod tidy", "build", "vet"} {
			if !strings.Contains(got, want) {
				t.Errorf("the downloaded Go never ran %q:\n%s", want, got)
			}
		}
		rel := fmt.Sprintf("go1.%d.4", n+1)
		if !strings.Contains(body, rel) {
			t.Errorf("body should say which Go checked it:\n%s", body)
		}
		if _, err := os.Stat(filepath.Join(dir, rel, "bin", "go")); err != nil {
			t.Errorf("release not installed: %v", err)
		}
		if fi, err := os.Stat(filepath.Join(dir, rel, "bin", "go")); err == nil && fi.Mode()&0o111 == 0 {
			t.Errorf("installed go is not executable: %v", fi.Mode())
		}
	})
}

// A proxy without the release changes nothing: the server's Go checks the bump, as it
// does with no installer.
func TestVerifyFallsBackWhenTheDownloadFails(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		dir := t.TempDir()
		e, _ := downloadingServer(t, st, dir, func(*env, int) {})
		onePR(t, e, "acme/app", true)
		if !strings.Contains(e.logs.String(), "go toolchain download failed") {
			t.Errorf("the failure should be logged:\n%s", e.logs.String())
		}
	})
}
