package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

const (
	rLib  = "example.com/lib"
	rLib2 = "example.com/lib/v2"
)

func rlib(v, fn string) map[string]string {
	return map[string]string{"lib.go": "package lib\n\nconst V = \"" + v + "\"\n\nfunc " + fn + "() string { return V }\n"}
}

func sumHas(t *testing.T, e *env, repo, head, file string, wants, avoids []string) {
	t.Helper()
	sum, ok := e.gh.FileAt(repo, head, file)
	if !ok {
		t.Errorf("%s missing on %s", file, head)
		return
	}
	for _, w := range wants {
		if !strings.Contains(sum, w) {
			t.Errorf("%s lacks %q:\n%s", file, w, sum)
		}
	}
	for _, a := range avoids {
		if strings.Contains(sum, a) {
			t.Errorf("%s still holds %q:\n%s", file, a, sum)
		}
	}
}

func onePR(t *testing.T, e *env, repo string, draft bool) (head, body string) {
	t.Helper()
	prs := e.gh.PRs(repo)
	if len(prs) != 1 {
		t.Fatalf("%s: want exactly 1 PR, got %+v", repo, prs)
	}
	if prs[0].Draft != draft {
		t.Fatalf("%s: draft=%v, want %v\n%s", repo, prs[0].Draft, draft, prs[0].Body)
	}
	if draft && prs[0].AutoMerge {
		t.Errorf("a draft must never be auto-merged")
	}
	return prs[0].Head, prs[0].Body
}

func TestMinorBumpRefreshesGoSum(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":  gomod("example.com/app", rLib+" v1.0.0"),
			"main.go": "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
		})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.publish(rLib, "v1.1.0")
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		mod, _ := e.gh.FileAt("acme/app", head, "go.mod")
		if !strings.Contains(mod, rLib+" v1.1.0") {
			t.Errorf("go.mod:\n%s", mod)
		}
		sumHas(t, e, "acme/app", head, "go.sum", []string{rLib + " v1.1.0 h1:", rLib + " v1.1.0/go.mod h1:"}, []string{"v1.0.0"})
	})
}

func TestMinorBumpFailingBuildOpensDraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Fetch")) // Get was removed
		e.gh.AddRepo("acme/app", map[string]string{
			"go.mod":       gomod("example.com/app", rLib+" v1.0.0"),
			"main.go":      "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
			".ripple.yaml": "updates:\n  - ecosystem: gomod\n    automerge: minor\n",
		})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.publish(rLib, "v1.1.0")
		e.tick()

		_, body := onePR(t, e, "acme/app", true)
		for _, want := range []string{"Verification failed", "go build ./...", "undefined: lib.Get"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
	})
}

func monoFiles(bUses string) map[string]string {
	return map[string]string{
		"go.work":  "go 1.22\n\nuse (\n\t./a\n\t./b\n\t./c\n)\n",
		"a/go.mod": gomod("example.com/mono/a", rLib+" v1.0.0"),
		"a/a.go":   "package a\n\nimport \"" + rLib + "\"\n\nfunc Version() string { return lib.Get() }\n",
		"b/go.mod": gomod("example.com/mono/b", rLib+" v1.0.0"),
		"b/b.go":   "package b\n\nimport \"" + rLib + "\"\n\nfunc Version() string { return lib." + bUses + "() }\n",
		// c reaches the library only through a: its requirement is indirect.
		"c/go.mod": "module example.com/mono/c\n\ngo 1.22\n\nrequire (\n\texample.com/mono/a v0.0.0\n\t" + rLib + " v1.0.0 // indirect\n)\n\nreplace example.com/mono/a => ../a\n",
		"c/c.go":   "package c\n\nimport \"example.com/mono/a\"\n\nfunc Version() string { return a.Version() }\n",
	}
}

func monoEnv(t *testing.T, st store.Store, v2Func, bUses string) *env {
	e := newEnv(t, st, defaultLimits())
	e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
	e.proxy.Publish(rLib2, "v2.0.0", rlib("2.0.0", v2Func))
	e.gh.AddRepo("acme/mono", monoFiles(bUses))
	start(t, e, "acme/mono")
	e.clock.Advance(time.Minute)
	e.publish(rLib2, "v2.0.0")
	e.tick()
	return e
}

func TestMajorBumpMultiModuleWorkspace(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := monoEnv(t, st, "Get", "Get")
		head, _ := onePR(t, e, "acme/mono", false)
		for _, m := range []string{"a", "b"} {
			mod, _ := e.gh.FileAt("acme/mono", head, m+"/go.mod")
			if !strings.Contains(mod, rLib2+" v2.0.0") || strings.Contains(mod, rLib+" v1") {
				t.Errorf("%s/go.mod:\n%s", m, mod)
			}
			sumHas(t, e, "acme/mono", head, m+"/go.sum", []string{rLib2 + " v2.0.0 h1:"}, []string{rLib + " v1"})
			src, _ := e.gh.FileAt("acme/mono", head, m+"/"+m+".go")
			if !strings.Contains(src, `"`+rLib2+`"`) {
				t.Errorf("%s/%s.go not rewritten:\n%s", m, m, src)
			}
		}
		// c only required the library indirectly: it is refreshed, not left stale.
		cmod, _ := e.gh.FileAt("acme/mono", head, "c/go.mod")
		if !strings.Contains(cmod, rLib2+" v2.0.0 // indirect") || strings.Contains(cmod, rLib+" v1") {
			t.Errorf("c/go.mod:\n%s", cmod)
		}
		sumHas(t, e, "acme/mono", head, "c/go.sum", []string{rLib2 + " v2.0.0 h1:"}, []string{rLib + " v1"})
	})
}

func TestMajorBumpWorkspaceBreakageOpensDraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		// v2 drops Get for Fetch; only module b still calls it in a way that cannot compile.
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib2, "v2.0.0", rlib("2.0.0", "Fetch"))
		files := monoFiles("Get")
		files["a/a.go"] = "package a\n\nimport \"" + rLib + "\"\n\nfunc Version() string { return lib.V }\n"
		e.gh.AddRepo("acme/mono", files)
		start(t, e, "acme/mono")
		e.clock.Advance(time.Minute)
		e.publish(rLib2, "v2.0.0")
		e.tick()

		_, body := onePR(t, e, "acme/mono", true)
		for _, want := range []string{"Verification failed", "(in b)", "undefined: lib.Get"} {
			if !strings.Contains(body, want) {
				t.Errorf("body lacks %q:\n%s", want, body)
			}
		}
	})
}

func TestMajorBumpRefreshesVendorDir(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib2, "v2.0.0", rlib("2.0.0", "Get"))
		e.gh.AddRepo("acme/vend", map[string]string{
			"go.mod":                     gomod("example.com/vend", rLib+" v1.0.0"),
			"main.go":                    "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
			"vendor/modules.txt":         "# " + rLib + " v1.0.0\n## explicit; go 1.21\n" + rLib + "\n",
			"vendor/" + rLib + "/lib.go": rlib("1.0.0", "Get")["lib.go"],
		})
		start(t, e, "acme/vend")
		e.clock.Advance(time.Minute)
		e.publish(rLib2, "v2.0.0")
		e.tick()

		head, _ := onePR(t, e, "acme/vend", false)
		mt, _ := e.gh.FileAt("acme/vend", head, "vendor/modules.txt")
		if !strings.Contains(mt, "# "+rLib2+" v2.0.0") || strings.Contains(mt, rLib+" v1") {
			t.Errorf("vendor/modules.txt:\n%s", mt)
		}
		if _, ok := e.gh.FileAt("acme/vend", head, "vendor/"+rLib2+"/lib.go"); !ok {
			t.Errorf("v2 not vendored")
		}
		if _, ok := e.gh.FileAt("acme/vend", head, "vendor/"+rLib+"/lib.go"); ok {
			t.Errorf("v1 still vendored")
		}
	})
}

func TestMinorBumpRefreshesVendorDir(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := newEnv(t, st, defaultLimits())
		e.proxy.Publish(rLib, "v1.0.0", rlib("1.0.0", "Get"))
		e.proxy.Publish(rLib, "v1.1.0", rlib("1.1.0", "Get"))
		e.gh.AddRepo("acme/vend", map[string]string{
			"go.mod":                     gomod("example.com/vend", rLib+" v1.0.0"),
			"main.go":                    "package main\n\nimport \"" + rLib + "\"\n\nfunc main() { println(lib.Get()) }\n",
			"vendor/modules.txt":         "# " + rLib + " v1.0.0\n## explicit; go 1.21\n" + rLib + "\n",
			"vendor/" + rLib + "/lib.go": rlib("1.0.0", "Get")["lib.go"],
		})
		start(t, e, "acme/vend")
		e.clock.Advance(time.Minute)
		e.publish(rLib, "v1.1.0")
		e.tick()
		head, _ := onePR(t, e, "acme/vend", false)
		v, _ := e.gh.FileAt("acme/vend", head, "vendor/"+rLib+"/lib.go")
		if !strings.Contains(v, `"1.1.0"`) {
			t.Errorf("vendored lib not refreshed:\n%s", v)
		}
	})
}
