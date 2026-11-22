package e2e

import (
	"context"
	"github.com/taumatix/ripple/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
)

const (
	ghV60 = "github.com/google/go-github/v60"
	ghV61 = "github.com/google/go-github/v61"
)

// ghLib is a stand-in for go-github at major n: a client package plus the two
// packages the fixture imports with "." and "_".
func ghLib(n string, newClient string) map[string]string {
	return map[string]string{
		"github/github.go":    "package github\n\ntype Client struct{ Version string }\n\nfunc " + newClient + "() *Client { return &Client{Version: \"v" + n + "\"} }\n",
		"github/dot/dot.go":   "package dot\n\nfunc Hello() string { return \"hello\" }\n",
		"github/side/side.go": "package side\n\nfunc init() {}\n",
	}
}

const (
	fxMain = `package main

import (
	"fmt"

	ghapi "github.com/google/go-github/v60/github"
	. "github.com/google/go-github/v60/github/dot"
	_ "github.com/google/go-github/v60/github/side"
)

// The client lives at github.com/google/go-github/v60/github.
const doc = "github.com/google/go-github/v60/github"

func main() {
	c := ghapi.NewClient()
	fmt.Println(doc, c.Version, Hello())
}
`
	fxMainWant = `package main

import (
	"fmt"

	ghapi "github.com/google/go-github/v61/github"
	. "github.com/google/go-github/v61/github/dot"
	_ "github.com/google/go-github/v61/github/side"
)

// The client lives at github.com/google/go-github/v60/github.
const doc = "github.com/google/go-github/v60/github"

func main() {
	c := ghapi.NewClient()
	fmt.Println(doc, c.Version, Hello())
}
`
	fxSub = `package sub

import "github.com/google/go-github/v60/github"

func Version() string { return github.NewClient().Version }
`
	fxSubWant = `package sub

import "github.com/google/go-github/v61/github"

func Version() string { return github.NewClient().Version }
`
	fxTest = `package main

import (
	"testing"

	"github.com/google/go-github/v60/github"
)

func TestClient(t *testing.T) {
	if github.NewClient() == nil {
		t.Fatal("nil client")
	}
}
`
	fxTestWant = `package main

import (
	"testing"

	"github.com/google/go-github/v61/github"
)

func TestClient(t *testing.T) {
	if github.NewClient() == nil {
		t.Fatal("nil client")
	}
}
`
	// Never built here (the tag is not set) but still part of the module.
	fxTagged = `//go:build integration

package main

import "github.com/google/go-github/v60/github"

var _ = github.NewClient
`
	fxTaggedWant = `//go:build integration

package main

import "github.com/google/go-github/v61/github"

var _ = github.NewClient
`
	// Skipped by the build (cgo is off in ripple's sandbox) but rewritten all the same.
	fxCgo = `package main

/*
#include <stdlib.h>
*/
import "C"

import "github.com/google/go-github/v60/github"

var _ = github.NewClient
`
	fxCgoWant = `package main

/*
#include <stdlib.h>
*/
import "C"

import "github.com/google/go-github/v61/github"

var _ = github.NewClient
`
	// A nested module has its own go.mod: it is not part of this update.
	fxNested = `package vendored

import "github.com/google/go-github/v60/github"

var _ = github.NewClient
`
	fxGoMod = `module example.com/ghclient

go 1.22

require github.com/google/go-github/v60 v60.0.0

replace github.com/google/go-github/v60 v60.0.0 => github.com/google/go-github/v60 v60.0.1

exclude github.com/google/go-github/v60 v60.0.5
`
	fxGoModWant = `module example.com/ghclient

go 1.22

require github.com/google/go-github/v61 v61.0.0
`
)

func ghClientFiles() map[string]string {
	return map[string]string{
		"go.mod":                      fxGoMod,
		"main.go":                     fxMain,
		"main_test.go":                fxTest,
		"sub/sub.go":                  fxSub,
		"tagged.go":                   fxTagged,
		"cgo.go":                      fxCgo,
		"third_party/vendored/go.mod": "module example.com/vendored\n\ngo 1.22\n",
		"third_party/vendored/v.go":   fxNested,
	}
}

func majorEnv(t *testing.T, st store.Store, extra map[string]string, newClient string) *env {
	t.Helper()
	e := newEnv(t, st, defaultLimits())
	e.proxy.Publish(ghV60, "v60.0.0", ghLib("60", "NewClient"))
	e.proxy.Publish(ghV61, "v61.0.0", ghLib("61", newClient))
	files := ghClientFiles()
	for k, v := range extra {
		files[k] = v
	}
	e.gh.AddRepo("acme/ghclient", files)
	e.gh.AddRepo("acme/unrelated", map[string]string{"go.mod": gomod("example.com/unrelated", "example.com/other v1.0.0")})
	return e
}

func TestMajorBumpRewritesModAndImports(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := majorEnv(t, st, nil, "NewClient")
		start(t, e, "acme/ghclient", "acme/unrelated")
		ctx := context.Background()

		// Both majors of the module are one line in the reverse index.
		for _, p := range []string{ghV60, ghV61} {
			d, err := st.ListDependents(ctx, "go", p)
			if err != nil || len(d) != 1 || d[0].Repo != "acme/ghclient" || d[0].Package != ghV60 || d[0].Version != "v60.0.0" {
				t.Fatalf("dependents of %s = %+v, %v", p, d, err)
			}
		}

		platBefore := e.plat.Calls("acme/unrelated")
		ghBefore := e.gh.Calls("acme/unrelated")
		e.clock.Advance(time.Minute)
		e.publish(ghV61, "v61.0.0")
		e.tick()

		prs := e.gh.PRs("acme/ghclient")
		if len(prs) != 1 {
			t.Fatalf("want exactly 1 PR, got %+v", prs)
		}
		pr := prs[0]
		if pr.Title != "[major] Update "+ghV61+" to v61.0.0" {
			t.Errorf("title = %q", pr.Title)
		}
		if got := strings.Join(pr.Labels, ","); got != "major,breaking" {
			t.Errorf("labels = %q", got)
		}
		if pr.Draft || pr.AutoMerge {
			t.Errorf("draft=%v automerge=%v, want neither for a green major", pr.Draft, pr.AutoMerge)
		}
		for _, want := range []string{"Breaking: major version update", "https://github.com/google/go-github/releases/tag/v61.0.0"} {
			if !strings.Contains(pr.Body, want) {
				t.Errorf("body lacks %q:\n%s", want, pr.Body)
			}
		}

		want := map[string]string{
			"go.mod": fxGoModWant, "main.go": fxMainWant, "main_test.go": fxTestWant,
			"sub/sub.go": fxSubWant, "tagged.go": fxTaggedWant, "cgo.go": fxCgoWant,
			"third_party/vendored/v.go":   fxNested,
			"third_party/vendored/go.mod": "module example.com/vendored\n\ngo 1.22\n",
		}
		for f, w := range want {
			got, ok := e.gh.FileAt("acme/ghclient", pr.Head, f)
			if !ok || got != w {
				t.Errorf("%s on %s:\n--- got (found=%v)\n%s\n--- want\n%s", f, pr.Head, ok, got, w)
			}
		}
		sum, _ := e.gh.FileAt("acme/ghclient", pr.Head, "go.sum")
		if !strings.Contains(sum, ghV61+" v61.0.0 h1:") || strings.Contains(sum, "go-github/v60") {
			t.Errorf("go.sum not tidied to v61:\n%s", sum)
		}

		if n := e.plat.Calls("acme/unrelated") - platBefore; n != 0 {
			t.Errorf("platform calls for non-importing repo: %d, want 0", n)
		}
		if n := e.gh.Calls("acme/unrelated") - ghBefore; n != 0 {
			t.Errorf("GitHub calls for non-importing repo: %d, want 0", n)
		}

		// Merge: the push webhook re-indexes the new path; the old-major lookup
		// now resolves to it.
		merged := map[string]string{}
		for f := range want {
			merged[f], _ = e.gh.FileAt("acme/ghclient", pr.Head, f)
		}
		merged["go.sum"] = sum
		e.push("acme/ghclient", merged)
		e.eventually("new major path indexed", func() bool {
			d := e.deps("acme/ghclient")
			return len(d) == 1 && d[0].Package == ghV61 && d[0].Version == "v61.0.0"
		})
		d, err := st.ListDependents(ctx, "go", ghV60)
		if err != nil || len(d) != 1 || d[0].Package != ghV61 {
			t.Errorf("after merge, dependents via old path = %+v, %v", d, err)
		}
		// The rewritten repo is not offered the same update again.
		e.tick()
		if n := len(e.gh.PRs("acme/ghclient")); n != 1 {
			t.Errorf("PRs after re-poll = %d, want 1", n)
		}
	})
}

func TestMajorBuildFailureOpensDraft(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		// v61 renamed NewClient: the rewritten code cannot compile.
		e := majorEnv(t, st, map[string]string{
			".ripple.yaml": "updates:\n  - ecosystem: gomod\n    automerge: major\n",
		}, "New")
		start(t, e, "acme/ghclient", "acme/unrelated")
		e.clock.Advance(time.Minute)
		e.publish(ghV61, "v61.0.0")
		e.tick()

		prs := e.gh.PRs("acme/ghclient")
		if len(prs) != 1 {
			t.Fatalf("want 1 PR, got %+v", prs)
		}
		pr := prs[0]
		if !pr.Draft {
			t.Errorf("PR is not a draft")
		}
		if pr.AutoMerge {
			t.Errorf("a draft must never be auto-merged")
		}
		for _, want := range []string{"Verification failed", "go build ./...", "undefined: ghapi.NewClient"} {
			if !strings.Contains(pr.Body, want) {
				t.Errorf("body lacks %q:\n%s", want, pr.Body)
			}
		}
		got, _ := e.gh.FileAt("acme/ghclient", pr.Head, "main.go")
		if got != fxMainWant {
			t.Errorf("draft branch must carry the rewrite for a human to finish:\n%s", got)
		}
	})
}

func TestMajorAutomergeNeedsMajorOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		want       bool
	}{{"minor", "minor", false}, {"patch", "patch", false}, {"major", "major", true}} {
		t.Run(tc.name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := majorEnv(t, st, map[string]string{
					".ripple.yaml": "updates:\n  - ecosystem: gomod\n    automerge: " + tc.mode + "\n",
				}, "NewClient")
				start(t, e, "acme/ghclient")
				e.clock.Advance(time.Minute)
				e.publish(ghV61, "v61.0.0")
				e.tick()
				prs := e.gh.PRs("acme/ghclient")
				if len(prs) != 1 || prs[0].AutoMerge != tc.want || prs[0].Draft {
					t.Fatalf("automerge: %s: want automerge=%v on one non-draft PR, got %+v", tc.mode, tc.want, prs)
				}
			})
		})
	}
}

func TestMajorIgnoredByRepoFile(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := majorEnv(t, st, map[string]string{
			".ripple.yaml": "updates:\n  - ecosystem: gomod\n    ignore:\n      - dependency: github.com/google/*\n        update-types: [major]\n",
		}, "NewClient")
		start(t, e, "acme/ghclient")
		e.clock.Advance(time.Minute)
		e.publish(ghV61, "v61.0.0")
		e.tick()
		if prs := e.gh.PRs("acme/ghclient"); len(prs) != 0 {
			t.Fatalf("ignored major opened %+v", prs)
		}
	})
}

func TestMajorSuffixShapes(t *testing.T) {
	for _, tc := range []struct{ name, oldP, oldV, newP, newV, pkg string }{
		{"unsuffixed to v2", "example.com/lib", "v1.4.0", "example.com/lib/v2", "v2.0.0", "lib"},
		{"gopkg.in", "gopkg.in/yaml.v2", "v2.4.0", "gopkg.in/yaml.v3", "v3.0.1", "yaml"},
		{"v9 to v10", "example.com/big/v9", "v9.1.0", "example.com/big/v10", "v10.0.0", "big"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				e := newEnv(t, st, defaultLimits())
				lib := func(v string) map[string]string {
					return map[string]string{tc.pkg + ".go": "package " + tc.pkg + "\n\nconst V = \"" + v + "\"\n"}
				}
				e.proxy.Publish(tc.oldP, tc.oldV, lib(tc.oldV))
				e.proxy.Publish(tc.newP, tc.newV, lib(tc.newV))
				e.gh.AddRepo("acme/app", map[string]string{
					"go.mod":   "module example.com/app\n\ngo 1.22\n\nrequire " + tc.oldP + " " + tc.oldV + "\n",
					"main.go":  "package main\n\nimport \"" + tc.oldP + "\"\n\nfunc main() { println(" + tc.pkg + ".V) }\n",
					"sub/s.go": "package sub\n\nimport x \"" + tc.oldP + "\"\n\nvar _ = x.V\n",
				})
				start(t, e, "acme/app")
				e.clock.Advance(time.Minute)
				e.publish(tc.newP, tc.newV)
				e.tick()
				prs := e.gh.PRs("acme/app")
				if len(prs) != 1 || prs[0].Draft {
					t.Fatalf("want one non-draft PR, got %+v", prs)
				}
				if want := "[major] Update " + tc.newP + " to " + tc.newV; prs[0].Title != want {
					t.Errorf("title %q, want %q", prs[0].Title, want)
				}
				mod, _ := e.gh.FileAt("acme/app", prs[0].Head, "go.mod")
				if want := "module example.com/app\n\ngo 1.22\n\nrequire " + tc.newP + " " + tc.newV + "\n"; mod != want {
					t.Errorf("go.mod:\n%s\nwant:\n%s", mod, want)
				}
				main, _ := e.gh.FileAt("acme/app", prs[0].Head, "main.go")
				if want := "package main\n\nimport \"" + tc.newP + "\"\n\nfunc main() { println(" + tc.pkg + ".V) }\n"; main != want {
					t.Errorf("main.go:\n%s\nwant:\n%s", main, want)
				}
				sub, _ := e.gh.FileAt("acme/app", prs[0].Head, "sub/s.go")
				if want := "package sub\n\nimport x \"" + tc.newP + "\"\n\nvar _ = x.V\n"; sub != want {
					t.Errorf("sub/s.go:\n%s\nwant:\n%s", sub, want)
				}
			})
		})
	}
}

// A hostile repo must not make ripple's go invocation touch the server. Each case
// plants something go would follow out of the checkout; the outside file must be
// byte-identical afterwards, the job failed for good, and no pull request exists.
func TestMajorRefusesUnsafeCheckout(t *testing.T) {
	const secret = "TOP SECRET: not for a pull request\n"
	for _, tc := range []struct {
		name  string
		files func(outside string) map[string]string
	}{
		{"symlinked go.sum", func(outside string) map[string]string {
			return map[string]string{"go.sum": "symlink:" + outside}
		}},
		{"replace to absolute path", func(outside string) map[string]string {
			return map[string]string{"go.mod": fxGoMod + "\nreplace example.com/x => " + filepath.Dir(outside) + "\n"}
		}},
		{"replace outside the repository", func(string) map[string]string {
			return map[string]string{"go.mod": fxGoMod + "\nreplace example.com/x => ../../etc\n"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEach(t, func(t *testing.T, st store.Store) {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte(secret), 0o600); err != nil {
					t.Fatal(err)
				}
				e := majorEnv(t, st, tc.files(outside), "NewClient")
				start(t, e, "acme/ghclient", "acme/unrelated")
				e.clock.Advance(time.Minute)
				e.publish(ghV61, "v61.0.0")
				e.tick()

				if prs := e.gh.PRs("acme/ghclient"); len(prs) != 0 {
					t.Fatalf("want no PR, got %+v", prs)
				}
				got, err := os.ReadFile(outside)
				if err != nil || string(got) != secret {
					t.Fatalf("outside file changed: %q, %v", got, err)
				}
				j := e.job("acme/ghclient", ghV61, "v61.0.0")
				if j.State != model.JobFailed || !strings.Contains(j.Reason, "unsafe checkout") {
					t.Errorf("job state=%v reason=%q, want failed with an unsafe checkout reason", j.State, j.Reason)
				}
			})
		})
	}
}

func TestMajorSkipsUnparsableAndIgnoredDirs(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		const broken = "package main\n\nimport (\n\t\"github.com/google/go-github/v60/github\"\n"
		e := majorEnv(t, st, map[string]string{
			"testdata/x.go":  broken,
			"testdata/y.go":  fxSub,
			"_scratch/z.go":  fxSub,
			".hidden/h.go":   fxSub,
			"broken/real.go": broken,
		}, "NewClient")
		start(t, e, "acme/ghclient", "acme/unrelated")
		e.clock.Advance(time.Minute)
		e.publish(ghV61, "v61.0.0")
		e.tick()

		prs := e.gh.PRs("acme/ghclient")
		if len(prs) != 1 {
			t.Fatalf("one unparsable file must not fail the job; PRs = %+v", prs)
		}
		pr := prs[0]
		if !strings.Contains(pr.Body, "Files not rewritten") || !strings.Contains(pr.Body, "`broken/real.go`") {
			t.Errorf("body should list the unparsable file:\n%s", pr.Body)
		}
		if strings.Contains(pr.Body, "testdata/x.go") {
			t.Errorf("testdata is ignored by go and must not be reported:\n%s", pr.Body)
		}
		for f, w := range map[string]string{
			"testdata/x.go": broken, "testdata/y.go": fxSub, "_scratch/z.go": fxSub, ".hidden/h.go": fxSub,
			"broken/real.go": broken, "main.go": fxMainWant, "sub/sub.go": fxSubWant,
		} {
			if got, _ := e.gh.FileAt("acme/ghclient", pr.Head, f); got != w {
				t.Errorf("%s:\n%s\nwant:\n%s", f, got, w)
			}
		}
	})
}

const (
	genOld = `package main

//go:generate go run github.com/google/go-github/v60/github/dot -out gen.txt
//go:generate -command dot go run github.com/google/go-github/v60/github/dot
//go:generate go run github.com/google/go-github/v60/github/dot@latest
//go:generate mockgen github.com/google/go-github/v60/github Client
//go:generate go run ./local

// A directive inside a comment, a string or a raw string is not a directive:
// //go:generate go run github.com/google/go-github/v60/github/dot
var inString = "\n//go:generate go run github.com/google/go-github/v60/github/dot"

var inRaw = ` + "`" + `
//go:generate go run github.com/google/go-github/v60/github/dot
` + "`" + `
`
	genWant = `package main

//go:generate go run github.com/google/go-github/v61/github/dot -out gen.txt
//go:generate -command dot go run github.com/google/go-github/v61/github/dot
//go:generate go run github.com/google/go-github/v60/github/dot@latest
//go:generate mockgen github.com/google/go-github/v60/github Client
//go:generate go run ./local

// A directive inside a comment, a string or a raw string is not a directive:
// //go:generate go run github.com/google/go-github/v60/github/dot
var inString = "\n//go:generate go run github.com/google/go-github/v60/github/dot"

var inRaw = ` + "`" + `
//go:generate go run github.com/google/go-github/v60/github/dot
` + "`" + `
`
	genTagged     = "//go:build integration\n\npackage main\n\n//go:generate go run github.com/google/go-github/v60/github/dot\n"
	genTaggedWant = "//go:build integration\n\npackage main\n\n//go:generate go run github.com/google/go-github/v61/github/dot\n"
	mirrorOld     = "package mirror // import \"github.com/google/go-github/v60/github/mirror\"\n"
	mirrorWant    = "package mirror // import \"github.com/google/go-github/v61/github/mirror\"\n"
	// Other import comments are the repository's own and stay.
	ownComment = "package own // import \"example.com/ghclient/own\"\n"
	// A copy of the library under a nested module keeps its own paths.
	vendoredGen = "package vendored\n\n//go:generate go run github.com/google/go-github/v60/github/dot\n"
)

func TestMajorRewritesGenerateAndImportComments(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e := majorEnv(t, st, map[string]string{
			"gen.go":                      genOld,
			"gen_tagged.go":               genTagged,
			"mirror/mirror.go":            mirrorOld,
			"own/own.go":                  ownComment,
			"testdata/gen.go":             genOld,
			"third_party/vendored/gen.go": vendoredGen,
		}, "NewClient")
		start(t, e, "acme/ghclient")
		e.clock.Advance(time.Minute)
		e.publish(ghV61, "v61.0.0")
		e.tick()

		prs := e.gh.PRs("acme/ghclient")
		if len(prs) != 1 || prs[0].Draft {
			t.Fatalf("want one non-draft PR (it builds), got %+v", prs)
		}
		pr := prs[0]
		for f, w := range map[string]string{
			"gen.go": genWant, "gen_tagged.go": genTaggedWant, "mirror/mirror.go": mirrorWant,
			"own/own.go": ownComment, "testdata/gen.go": genOld, "third_party/vendored/gen.go": vendoredGen,
		} {
			if got, _ := e.gh.FileAt("acme/ghclient", pr.Head, f); got != w {
				t.Errorf("%s:\n--- got\n%s\n--- want\n%s", f, got, w)
			}
		}
		if !strings.Contains(pr.Body, "Old path left in place") || !strings.Contains(pr.Body, "`gen.go:5`") || !strings.Contains(pr.Body, "`gen.go:6`") {
			t.Errorf("body should list the ambiguous directives:\n%s", pr.Body)
		}
		for _, no := range []string{"`gen.go:3`", "`gen.go:4`", "gen_tagged.go", "testdata"} {
			if strings.Contains(pr.Body, no) {
				t.Errorf("body should not list %s:\n%s", no, pr.Body)
			}
		}
	})
}
