package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const npmConfig = "updates:\n  - ecosystem: npm\n"

func npmEnv(t *testing.T, st store.Store) (*env, *testutil.NpmRegistry) {
	reg := testutil.NewNpmRegistry(t)
	return newEnvOpts(t, st, defaultLimits(), opts{npm: reg}), reg
}

const npmPackageJSON = `{
  "name": "app",
  "dependencies": {
    "left-pad": "^1.2.3",
    "@types/node": "~20.1.0",
    "wild": "*"
  },
  "devDependencies": {
    "jest": "29.0.0"
  },
  "peerDependencies": {
    "react": "^18.0.0"
  }
}
`

func TestNpmRangeBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("@types/node", "20.1.0")
		reg.Publish("jest", "29.0.0")
		reg.Publish("react", "18.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": npmPackageJSON, "package-lock.json": "{}\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"package.json": npmPackageJSON})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 3 {
			t.Fatalf("left-pad, @types/node and jest are tracked (not wild, not a peer), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("left-pad", "1.5.0")
		reg.Publish("left-pad", "1.6.0-beta.1") // a prerelease: ignored
		reg.Publish("left-pad", "1.7.0")
		reg.Deprecate("left-pad", "1.7.0", "do not use")
		reg.Tag("left-pad", "latest", "1.5.0")
		reg.Publish("@types/node", "20.2.1")
		reg.Publish("react", "19.0.0") // a peer range is never moved
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per package (left-pad, @types/node), got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "package.json")
			var from, to, body = "", "", pr.Body
			switch {
			case strings.Contains(pr.Head, "left-pad"):
				from, to = `"^1.2.3"`, `"^1.5.0"`
			case strings.Contains(pr.Head, "types-node"):
				from, to = `"~20.1.0"`, `"~20.2.1"`
			default:
				t.Fatalf("unexpected branch %q (jest, wild and the peer stay)", pr.Head)
			}
			if want := strings.Replace(npmPackageJSON, from, to, 1); got != want {
				t.Errorf("%s: only that range moves, operator kept:\n%s", pr.Head, got)
			}
			for _, w := range []string{"Lockfiles not regenerated", "`package-lock.json`", "`" + strings.Trim(from, `"`) + "`"} {
				if !strings.Contains(body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, body)
				}
			}
			if strings.Contains(body, "module index") || !strings.Contains(body, "when a new version was published to the npm registry") {
				t.Errorf("%s: the footer must say where an npm version is seen:\n%s", pr.Head, body)
			}
			if !strings.HasPrefix(pr.Head, "ripple/npm/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range reg.Requests() {
			if r.Authorization != "" || !strings.HasPrefix(r.Accept, "application/vnd.npm.install-v1+json") {
				t.Errorf("request %+v: anonymous, abbreviated metadata only", r)
			}
		}
		if n := len(e.gh.PRs("acme/optout")); n != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestNpmScopedRegistryInNpmrcIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("@corp/internal", "1.0.0")
		reg.Publish("left-pad", "1.2.3")
		pj := `{"dependencies": {"@corp/internal": "^1.0.0", "left-pad": "^1.2.3"}}` + "\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": pj,
			".npmrc": "@corp:registry=https://npm.corp.example/\n//npm.corp.example/:_authToken=${TOKEN}\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.Publish("@corp/internal", "1.1.0") // what a squatter on the public registry could publish
		reg.Publish("left-pad", "1.3.0")
		e.tick()
		e.tick()

		head, _ := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "package.json")
		if want := `{"dependencies": {"@corp/internal": "^1.0.0", "left-pad": "^1.3.0"}}` + "\n"; got != want {
			t.Errorf("the corp package must stay:\n%s", got)
		}
		j, ok, err := e.st.GetJob(context.Background(), model.JobKey("acme/app", model.DatasourceNpm, "@corp/internal", "1.1.0"))
		if err != nil || !ok || j.State != model.JobSkipped || !strings.Contains(j.Reason, ".npmrc") {
			t.Errorf("job = %+v, %v, %v", j, ok, err)
		}
	})
}

func TestNpmPrivatePackageIsSkippedAndOthersStillMove(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("secret", "1.0.0")
		reg.Deny("secret")
		pj := `{"dependencies": {"left-pad": "1.2.3", "secret": "1.0.0", "ghost": "^2.0.0"}}` + "\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "web/package.json": pj})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		reg.Publish("left-pad", "1.2.4")
		reg.Publish("secret", "1.0.1")
		e.tick()
		e.tick()

		head, body := onePR(t, e, "acme/app", false)
		got, _ := e.gh.FileAt("acme/app", head, "web/package.json")
		if want := `{"dependencies": {"left-pad": "1.2.4", "secret": "1.0.0", "ghost": "^2.0.0"}}` + "\n"; got != want {
			t.Errorf("%s", got)
		}
		if strings.Contains(body, "Lockfiles not regenerated") {
			t.Errorf("no lockfile exists, none should be reported:\n%s", body)
		}
	})
}

func TestNpmUpToDateOpensNothing(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		reg.Publish("left-pad", "1.2.3")
		reg.Publish("left-pad", "2.0.0-rc.1")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": npmConfig, "package.json": `{"dependencies":{"left-pad":"^1.2.3"}}`})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("an up-to-date repo got %d PRs", n)
		}
	})
}

// Partial ranges and x-ranges move at the precision written, and a repo that ignores
// majors still gets the newest minor or patch while a newer major exists.
func TestNpmPartialRangesAndTargetPerUpdateType(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := npmEnv(t, st)
		for pkg, v := range map[string]string{"part": "1.2.0", "xr": "2.0.0", "whole": "1.0.0", "held": "1.2.3"} {
			reg.Publish(pkg, v)
		}
		cfg := npmConfig + "    ignore:\n      - dependency: \"*\"\n        update-types: [major]\n"
		pj := "{\n  \"dependencies\": {\n    \"part\": \"^1.2\",\n    \"xr\": \"2.x\",\n    \"whole\": \"1\",\n    \"held\": \"^1.2.3\"\n  }\n}\n"
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": cfg, "package.json": pj})
		start(t, e, "acme/app")
		if n := len(e.deps("acme/app")); n != 4 {
			t.Fatalf("partial ranges are tracked, got %+v", e.deps("acme/app"))
		}
		e.clock.Advance(time.Minute)
		reg.Publish("part", "1.7.4")  // ^1.2 -> ^1.7
		reg.Publish("xr", "2.0.9")    // 2.x already allows it: nothing to change
		reg.Publish("whole", "1.9.0") // "1" already allows it
		reg.Publish("held", "1.2.9")  // the minor/patch the repo may take...
		reg.Publish("held", "2.0.0")  // ...while a major it ignores is newer
		e.tick()
		e.tick()
		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("want PRs for part and held only, got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "package.json")
			from, to := `"part": "^1.2"`, `"part": "^1.7"`
			if strings.Contains(pr.Head, "held") {
				from, to = `"held": "^1.2.3"`, `"held": "^1.2.9"`
			} else if !strings.Contains(pr.Head, "part") {
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(pj, from, to, 1); got != want {
				t.Errorf("%s:\n%s", pr.Head, got)
			}
		}
	})
}
