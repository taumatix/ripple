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

// otherVersionFiles are the places a Go version hides besides go.mod, each with one
// line a bump must move and, in the same file, lines it must not touch.
func otherVersionFiles(old string) map[string]string {
	return map[string]string{
		".go-version":    old + "\n",
		".tool-versions": "nodejs 20.1.0\ngolang " + old + " # keep\n",
		".github/workflows/ci.yml": "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: " + old +
			"\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n          go-version-file: go.mod\n",
		"Dockerfile":                      "FROM golang:" + old + "-alpine AS build\nFROM alpine:3.20\nRUN echo FROM golang:" + old + "\n",
		"README.md":                       "Needs go " + old + "\n",
		".mise.toml":                      "[tools]\ngo = \"" + old + "\"\nnode = \"20\"\n",
		".devcontainer/devcontainer.json": "{\n  \"image\": \"mcr.microsoft.com/devcontainers/go:" + old + "-bookworm\"\n}\n",
		"go.work":                         "go 1.22\n\ntoolchain go" + old + "\n\nuse .\n",
		"Makefile":                        "GO_VERSION ?= " + old + "\nbuild:\n\tgo build ./...\n",
	}
}

func TestToolchainBumpMovesTheGoVersionInOtherFiles(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		old := fmt.Sprintf("1.%d.0", n)
		files := withMode(tcRepo("1.22", "go"+old), "toolchain")
		for p, c := range otherVersionFiles(old) {
			files[p] = c
		}
		// The same files in a repo that did not opt in must stay as they are.
		quiet := withMode(tcRepo("1.22", "go"+old), "")
		for p, c := range otherVersionFiles(old) {
			quiet[p] = c
		}
		e.gh.AddRepo("acme/app", files)
		e.gh.AddRepo("acme/quiet", quiet)
		start(t, e, "acme/app", "acme/quiet")
		e.clock.Advance(time.Minute)
		e.tick()

		head, body := onePR(t, e, "acme/app", false)
		nw := fmt.Sprintf("1.%d.9", n)
		for p, want := range map[string]string{
			".go-version":    nw + "\n",
			".tool-versions": "nodejs 20.1.0\ngolang " + nw + " # keep\n",
			".github/workflows/ci.yml": "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: " + nw +
				"\n      - uses: actions/setup-go@v5\n        with:\n          go-version: ${{ matrix.go }}\n          go-version-file: go.mod\n",
			"Dockerfile":                      "FROM golang:" + nw + "-alpine AS build\nFROM alpine:3.20\nRUN echo FROM golang:" + old + "\n",
			"README.md":                       "Needs go " + old + "\n",
			".mise.toml":                      "[tools]\ngo = \"" + nw + "\"\nnode = \"20\"\n",
			".devcontainer/devcontainer.json": "{\n  \"image\": \"mcr.microsoft.com/devcontainers/go:" + nw + "-bookworm\"\n}\n",
			"go.work":                         "go 1.22\n\ntoolchain go" + nw + "\n\nuse .\n",
			"Makefile":                        "GO_VERSION ?= " + nw + "\nbuild:\n\tgo build ./...\n",
		} {
			if got, _ := e.gh.FileAt("acme/app", head, p); got != want {
				t.Errorf("%s:\n got %q\nwant %q", p, got, want)
			}
		}
		for _, want := range []string{"Go version in other files", "`.go-version`", "`Dockerfile`", "`.tool-versions`", "`.github/workflows/ci.yml`", "`.mise.toml`", "`.devcontainer/devcontainer.json`", "`go.work`", "`Makefile`"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "`README.md`") {
			t.Errorf("README is not a version file:\n%s", body)
		}
		if prs := e.gh.PRs("acme/quiet"); len(prs) != 0 {
			t.Errorf("no go-toolchain key, no PR: %+v", prs)
		}
	})
}

func TestDirectiveBumpMovesTheGoVersionInOtherFiles(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		n := localMinor(t)
		dl := newDL(t)
		e := newEnvOpts(t, st, defaultLimits(), opts{goDL: dl})
		dl.Set(goVer(n, 9), goVer(n-1, 9))
		old := fmt.Sprintf("1.%d.3", n-2)
		files := withMode(tcRepo(fmt.Sprintf("1.%d", n-2), "go"+old), "directive")
		files[".go-version"] = old + "\n"
		files["Dockerfile"] = fmt.Sprintf("FROM golang:1.%d\n", n-2) // floats on the minor: moves with the minor
		e.gh.AddRepo("acme/old", files)
		start(t, e, "acme/old")
		e.clock.Advance(time.Minute)
		e.tick()

		head, _ := onePR(t, e, "acme/old", false)
		if got, _ := e.gh.FileAt("acme/old", head, ".go-version"); got != fmt.Sprintf("1.%d.9\n", n-1) {
			t.Errorf(".go-version %q", got)
		}
		if got, _ := e.gh.FileAt("acme/old", head, "Dockerfile"); got != fmt.Sprintf("FROM golang:1.%d\n", n-1) {
			t.Errorf("Dockerfile %q", got)
		}
	})
}
