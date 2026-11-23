package e2e

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/engine"
	"github.com/taumatix/ripple/internal/store"
)

const cgoOnly = "package main\n\n// static int one(void) { return 1; }\nimport \"C\"\n\nimport \"" + rLib + "\"\n\nfunc main() { println(C.one(), lib.Get()) }\n"

// cgoFallback builds with or without cgo: the cgo file is the only one that needs a compiler.
var cgoFallback = map[string]string{
	"main.go":    "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get(), impl()) }\n",
	"with.go":    "//go:build cgo\n\npackage main\n\n// static int one(void) { return 1; }\nimport \"C\"\n\nfunc impl() int { return int(C.one()) }\n",
	"without.go": "//go:build !cgo\n\npackage main\n\nfunc impl() int { return 0 }\n",
}

func cgoRepo(t *testing.T, e *env, files map[string]string, cfg string) {
	t.Helper()
	e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
	e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
	repo := map[string]string{"go.mod": gomod("example.com/app", rLib+" v1.0.0")}
	for k, v := range files {
		repo[k] = v
	}
	if cfg != "" {
		repo[".ripple.yaml"] = "updates:\n  - ecosystem: gomod\n" + cfg
	}
	e.gh.AddRepo("acme/app", repo)
	start(t, e, "acme/app")
	e.clock.Advance(time.Minute)
	e.publish(rLib, "v1.1.0")
	e.tick()
}

func needCC(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler on this machine")
	}
}

// A module with cgo files and no opt-in builds with cgo off. When a fallback exists the
// build is green, so the PR says the C code was not compiled: never a silent green.
func TestCgoFilesBuiltWithCgoOffSayso(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		cgoRepo(t, e, cgoFallback, "")
		_, body := onePR(t, e, "acme/app", false)
		for _, want := range []string{"cgo off", "with.go", "cgo: true"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
	})
}

// A cgo-only package cannot build with cgo off: the default stays a draft with the real error.
func TestCgoOnlyPackageDefaultsToDraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		cgoRepo(t, e, map[string]string{"main.go": cgoOnly}, "")
		_, body := onePR(t, e, "acme/app", true)
		if !strings.Contains(body, "Verification failed") {
			t.Errorf("body:\n%s", body)
		}
	})
}

// cgo: true builds the same package with a real C compiler, and the PR carries no caveat.
func TestCgoOptInBuildsWithCompiler(t *testing.T) {
	needCC(t)
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		cgoRepo(t, e, map[string]string{"main.go": cgoOnly}, "    cgo: true\n")
		_, body := onePR(t, e, "acme/app", false)
		if strings.Contains(body, "cgo off") || strings.Contains(body, "could not verify") {
			t.Errorf("body should be clean:\n%s", body)
		}
	})
}

// A real compile error in the C code is a failure, not an infrastructure gap.
func TestCgoOptInCompileErrorOpensDraft(t *testing.T) {
	needCC(t)
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		bad := strings.Replace(cgoOnly, "return 1;", "return undeclared_symbol;", 1)
		cgoRepo(t, e, map[string]string{"main.go": bad}, "    cgo: true\n")
		_, body := onePR(t, e, "acme/app", true)
		if !strings.Contains(body, "Verification failed") {
			t.Errorf("body:\n%s", body)
		}
	})
}

// cgo: true with no compiler on the server says it could not verify, and does not pass.
func TestCgoOptInWithoutCompilerSaysSo(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{engine: func(en *engine.Engine) { en.GoCC = "ripple-no-such-compiler" }})
		cgoRepo(t, e, map[string]string{"main.go": cgoOnly}, "    cgo: true\n")
		_, body := onePR(t, e, "acme/app", false)
		if !strings.Contains(body, "could not verify") || !strings.Contains(body, "no C compiler") {
			t.Errorf("body:\n%s", body)
		}
	})
}

const proxyToken = "tok-3f9a-very-secret"

// With a proxy credential configured the go commands that fetch modules authenticate,
// build and vet never see it, and it appears nowhere a person or a log can read it.
func TestPrivateProxyCredentialReachesOnlyFetchSteps(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnvOpts(t, st, defaultLimits(), opts{engine: func(en *engine.Engine) {
			en.GoProxyUser, en.GoProxyToken = "ripple", proxyToken
		}})
		e.proxy.RequireAuth("ripple", proxyToken)
		cgoRepo(t, e, map[string]string{"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n"},
			"    private-modules:\n      - example.com/*\n")
		head, body := onePR(t, e, "acme/app", false)
		reqs := e.proxy.Requests()
		if len(reqs) == 0 {
			t.Fatal("the proxy was never asked")
		}
		for _, r := range reqs {
			if r.HasAuth && !r.Authorized {
				t.Errorf("a wrong credential reached the proxy: %s", r.Path)
			}
			// build and vet carry no credential; go asks them for a module's .info (its
			// timestamp, best effort) and the 401 is harmless. Everything that
			// downloads code is the tidy step and must be authenticated.
			if !r.Authorized && !strings.HasSuffix(r.Path, ".info") {
				t.Errorf("unauthenticated request reached the proxy: %s (all: %+v)", r.Path, reqs)
			}
		}
		sumHas(t, e, "acme/app", head, "go.sum", []string{rLib + " v1.1.0 h1:"}, nil)
		for name, text := range map[string]string{"PR body": body, "logs": e.logs.String()} {
			if strings.Contains(text, proxyToken) {
				t.Errorf("the credential is in the %s", name)
			}
		}
	})
}

// Without the credential the same private proxy refuses, and that is a visible failure.
func TestPrivateProxyWithoutCredentialIsADraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.RequireAuth("ripple", proxyToken)
		cgoRepo(t, e, map[string]string{"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n"}, "")
		if prs := e.gh.PRs("acme/app"); len(prs) == 1 && !prs[0].Draft {
			t.Errorf("an unauthenticated update must not look verified:\n%s", prs[0].Body)
		}
	})
}
