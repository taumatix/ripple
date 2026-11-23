package e2e

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/rules"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

// localMinor is the minor of the Go running the tests. The server verifies with that
// Go (GOTOOLCHAIN=local), so the fake release list is built around it and no real Go
// release number appears in these tests.
func localMinor(t *testing.T) int {
	t.Helper()
	m := regexp.MustCompile(`^go1\.([0-9]+)`).FindStringSubmatch(runtime.Version())
	if m == nil {
		t.Skipf("cannot tell the minor of %q", runtime.Version())
	}
	n, _ := strconv.Atoi(m[1])
	if n < 24 {
		t.Skipf("the test needs a Go new enough to have two older minors to bump from, have %s", runtime.Version())
	}
	return n
}

func tcRepo(goLine, toolchainLine string) map[string]string {
	mod := "module example.com/app\n\ngo " + goLine + "\n"
	if toolchainLine != "" {
		mod += "\ntoolchain " + toolchainLine + "\n"
	}
	return map[string]string{
		"go.mod":       mod,
		"main.go":      "package main\n\nfunc main() {}\n",
		".ripple.yaml": "updates:\n  - ecosystem: gomod\n    go-toolchain: TOOLCHAIN\n",
	}
}

func withMode(files map[string]string, mode string) map[string]string {
	if mode == "" {
		delete(files, ".ripple.yaml")
		return files
	}
	files[".ripple.yaml"] = strings.Replace(files[".ripple.yaml"], "TOOLCHAIN", mode, 1)
	return files
}

func goVer(minor, patch int) string { return fmt.Sprintf("go1.%d.%d", minor, patch) }

func TestToolchainLineFollowsPatchReleases(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		e.gh.AddRepo("acme/app", withMode(tcRepo("1.22", goVer(n, 0)), "toolchain"))
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick() // a replay of the same list must not open a second PR

		head, body := onePR(t, e, "acme/app", false)
		mod, _ := e.gh.FileAt("acme/app", head, "go.mod")
		if want := "go 1.22\n\ntoolchain " + goVer(n, 9) + "\n"; !strings.Contains(mod, want) {
			t.Errorf("want the toolchain line moved and the go directive left alone:\n%s", mod)
		}
		for _, want := range []string{"toolchain line", "dependants do not read it", "CI on this pull request"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "least Go version") {
			t.Errorf("a toolchain-line PR must not talk about raising the directive:\n%s", body)
		}
		if got := e.gh.PRs("acme/app")[0].Title; got != "Update the Go toolchain to "+goVer(n, 9) {
			t.Errorf("title %q", got)
		}
	})
}

func TestToolchainUpdatesAreOffByDefault(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		e.gh.AddRepo("acme/app", withMode(tcRepo(fmt.Sprintf("1.%d", n-2), goVer(n-2, 0)), ""))
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if prs := e.gh.PRs("acme/app"); len(prs) != 0 {
			t.Errorf("no go-toolchain key, no PR: %+v", prs)
		}
		if dl.Requests() != 0 {
			t.Errorf("no repo opted in, so the release list must not be fetched; %d requests", dl.Requests())
		}
	})
}

func TestDirectiveMovesOnlyPastEndOfSupportToTheOldestSupportedMinor(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		e.gh.AddRepo("acme/old", withMode(tcRepo(fmt.Sprintf("1.%d", n-2), goVer(n-2, 3)), "directive"))
		e.gh.AddRepo("acme/supported", withMode(tcRepo(fmt.Sprintf("1.%d", n-1), ""), "directive"))
		e.gh.AddRepo("acme/toolchain-mode", withMode(tcRepo(fmt.Sprintf("1.%d", n-2), ""), "toolchain"))
		start(t, e, "acme/old", "acme/supported", "acme/toolchain-mode")
		e.clock.Advance(time.Minute)
		e.tick()

		head, body := onePR(t, e, "acme/old", false)
		mod, _ := e.gh.FileAt("acme/old", head, "go.mod")
		want := fmt.Sprintf("go 1.%d\n\ntoolchain %s\n", n-1, goVer(n-1, 9))
		if !strings.Contains(mod, want) {
			t.Errorf("want the directive on the oldest supported minor (not the newest, not a patch) and the toolchain line lifted above it:\n%s", mod)
		}
		for _, want := range []string{"least Go version this module needs", "every module that depends on this one", "no longer supported"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
		if got := e.gh.PRs("acme/old")[0].Title; got != fmt.Sprintf("Update the go directive to 1.%d", n-1) {
			t.Errorf("title %q", got)
		}
		for _, r := range []string{"acme/supported", "acme/toolchain-mode"} {
			if prs := e.gh.PRs(r); len(prs) != 0 {
				t.Errorf("%s: nothing is due: %+v", r, prs)
			}
		}
	})
}

func TestDirectiveBumpTheServerCannotBuildOpensADraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		// The oldest supported minor is newer than the Go on the server, which never
		// downloads a toolchain (GOTOOLCHAIN=local): the PR must say it is unverified.
		dl.Set(goVer(n+2, 1), goVer(n+1, 1))
		e.gh.AddRepo("acme/app", withMode(tcRepo(fmt.Sprintf("1.%d", n-2), ""), "directive"))
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()

		_, body := onePR(t, e, "acme/app", true)
		for _, want := range []string{"Verification failed", "GOTOOLCHAIN=local"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
	})
}

func TestToolchainJobWaitsOutTheCooldownFromFirstSight(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, rules.Limits{MinimumReleaseAge: 48 * time.Hour, OpenPRLimit: 5}, opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		e.gh.AddRepo("acme/app", withMode(tcRepo("1.22", goVer(n, 0)), "toolchain"))
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if prs := e.gh.PRs("acme/app"); len(prs) != 0 {
			t.Fatalf("the cooldown must hold the PR back: %+v", prs)
		}
		key := model.JobKey("acme/app", model.DatasourceGoToolchain, model.PackageGoToolchain, "v1."+strconv.Itoa(n)+".9")
		j, ok, err := e.st.GetJob(context.Background(), key)
		if err != nil || !ok {
			t.Fatalf("job %s: %v %v", key, ok, err)
		}
		if j.ReleasedAt.IsZero() || !j.NotBefore.Equal(j.ReleasedAt.Add(48*time.Hour)) {
			t.Errorf("released %v not before %v: the cooldown must run from first sight", j.ReleasedAt, j.NotBefore)
		}
		e.clock.Advance(24 * time.Hour)
		e.tick() // a replay a day later must not push the date out
		e.clock.Advance(25 * time.Hour)
		e.tick()
		onePR(t, e, "acme/app", false)
	})
}

func newDL(t *testing.T) *testutil.GoDL { return testutil.NewGoDL(t) }
