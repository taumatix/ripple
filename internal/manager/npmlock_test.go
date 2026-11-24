package manager

import (
	"errors"
	"strings"
	"testing"
)

const lockV3 = `{
  "name": "app",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "app",
      "dependencies": {
        "left-pad": "^1.2.3"
      },
      "devDependencies": {
        "@s/tool": "~2.0.0"
      }
    },
    "node_modules/@s/tool": {
      "version": "2.0.1",
      "resolved": "https://registry.npmjs.org/@s/tool/-/tool-2.0.1.tgz",
      "integrity": "sha512-OLD2",
      "dev": true,
      "license": "MIT"
    },
    "node_modules/left-pad": {
      "version": "1.2.3",
      "resolved": "https://registry.npmjs.org/left-pad/-/left-pad-1.2.3.tgz",
      "integrity": "sha512-OLD1",
      "license": "MIT"
    }
  }
}
`

func TestPlanNpmLockV3KeepsEveryOtherByte(t *testing.T) {
	plan, err := PlanNpmLock([]byte(lockV3), "left-pad", "^1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "1.2.3" || !strings.HasSuffix(plan.Resolved, "left-pad-1.2.3.tgz") {
		t.Errorf("plan = %+v", plan)
	}
	out, err := plan.Apply("left-pad", "^1.5.0", "1.5.0", "https://registry.npmjs.org/left-pad/-/left-pad-1.5.0.tgz", "sha512-NEW1")
	if err != nil {
		t.Fatal(err)
	}
	want := lockV3
	for from, to := range map[string]string{`"left-pad": "^1.2.3"`: `"left-pad": "^1.5.0"`, `"version": "1.2.3"`: `"version": "1.5.0"`,
		`left-pad-1.2.3.tgz`: `left-pad-1.5.0.tgz`, `sha512-OLD1`: `sha512-NEW1`} {
		want = strings.Replace(want, from, to, 1)
	}
	if string(out) != want {
		t.Errorf("only the five strings move:\n%s", out)
	}
	// A scoped package in devDependencies.
	plan, err = PlanNpmLock([]byte(lockV3), "@s/tool", "~2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out, _ = plan.Apply("@s/tool", "~2.0.3", "2.0.3", "https://registry.npmjs.org/@s/tool/-/tool-2.0.3.tgz", "sha512-NEW2")
	if !strings.Contains(string(out), `"@s/tool": "~2.0.3"`) || !strings.Contains(string(out), `"version": "2.0.3"`) || !strings.Contains(string(out), `"dev": true`) {
		t.Errorf("%s", out)
	}
}

func TestPlanNpmLockV2MovesTheTreeToo(t *testing.T) {
	lock := `{"name":"app","lockfileVersion":2,"packages":{"":{"dependencies":{"a":">=1.0.0"}},` +
		`"node_modules/a":{"version":"1.0.0","resolved":"https://r/a-1.0.0.tgz","integrity":"sha512-O"}},` +
		`"dependencies":{"a":{"version":"1.0.0","resolved":"https://r/a-1.0.0.tgz","integrity":"sha512-O"}}}`
	plan, err := PlanNpmLock([]byte(lock), "a", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out, err := plan.Apply("a", ">=1.2.0", "1.2.0", "https://r/a-1.2.0.tgz", "sha512-N")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"app","lockfileVersion":2,"packages":{"":{"dependencies":{"a":">=1.2.0"}},` +
		`"node_modules/a":{"version":"1.2.0","resolved":"https://r/a-1.2.0.tgz","integrity":"sha512-N"}},` +
		`"dependencies":{"a":{"version":"1.2.0","resolved":"https://r/a-1.2.0.tgz","integrity":"sha512-N"}}}`
	if string(out) != want {
		t.Errorf("got %s", out)
	}
}

func TestPlanNpmLockDeclines(t *testing.T) {
	leaf := `"node_modules/a":{"version":"1.0.0","resolved":"https://r/a.tgz","integrity":"sha512-O"}`
	for name, tc := range map[string]struct{ lock, spec, why string }{
		"v1":              {`{"lockfileVersion":1,"dependencies":{}}`, "^1.0.0", "lockfileVersion 1"},
		"bad json":        {`{`, "^1.0.0", "not valid JSON"},
		"range differs":   {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.1"}},` + leaf + `}}`, "^1.0.0", "range for a"},
		"not in root":     {`{"lockfileVersion":3,"packages":{"":{},` + leaf + `}}`, "^1.0.0", "not in the lockfile's root"},
		"also a peer":     {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0"},"peerDependencies":{"a":"*"}},` + leaf + `}}`, "^1.0.0", "peer dependency"},
		"workspaces":      {`{"lockfileVersion":3,"packages":{"":{"workspaces":["w"],"dependencies":{"a":"^1.0.0"}},` + leaf + `}}`, "^1.0.0", "workspaces"},
		"not hoisted":     {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0"}}}}`, "^1.0.0", "not hoisted"},
		"has deps":        {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0"}},"node_modules/a":{"version":"1.0.0","resolved":"u","integrity":"i","dependencies":{"b":"1"}}}}`, "^1.0.0", `"dependencies"`},
		"install script":  {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0"}},"node_modules/a":{"version":"1.0.0","resolved":"u","integrity":"i","hasInstallScript":true}}}`, "^1.0.0", "hasInstallScript"},
		"no integrity":    {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0"}},"node_modules/a":{"version":"1.0.0","resolved":"u"}}}`, "^1.0.0", "integrity"},
		"required by dep": {`{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":"^1.0.0","b":"1"}},` + leaf + `,"node_modules/b":{"version":"1.0.0","dependencies":{"a":"^1.0.0"}}}}`, "^1.0.0", "required by node_modules/b"},
		"v2 no tree":      {`{"lockfileVersion":2,"packages":{"":{"dependencies":{"a":"^1.0.0"}},` + leaf + `}}`, "^1.0.0", "dependencies tree"},
		"v2 requires":     {`{"lockfileVersion":2,"packages":{"":{"dependencies":{"a":"^1.0.0"}},` + leaf + `},"dependencies":{"a":{"version":"1.0.0","requires":{"b":"1"}}}}`, "^1.0.0", "requires"},
	} {
		_, err := PlanNpmLock([]byte(tc.lock), "a", tc.spec)
		if !errors.Is(err, ErrLockDeclined) || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: err = %v, want a decline mentioning %q", name, err, tc.why)
		}
	}
}

func TestNpmLockApplyDoesNotEscapeComparators(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{"":{"dependencies":{"a":">=1.0.0"}},"node_modules/a":{"version":"1.0.0","resolved":"u","integrity":"i"}}}`
	plan, err := PlanNpmLock([]byte(lock), "a", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := plan.Apply("a", ">=1.1.0", "1.1.0", "u2", "i2")
	if !strings.Contains(string(out), `"a":">=1.1.0"`) {
		t.Errorf("%s", out)
	}
}
