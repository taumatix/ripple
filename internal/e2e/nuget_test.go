package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/store"
	"github.com/taumatix/ripple/internal/testutil"
)

const nugetConfig = "updates:\n  - ecosystem: nuget\n"

func nugetEnv(t *testing.T, st store.Store) (*env, *testutil.Nuget) {
	n := testutil.NewNuget(t)
	return newEnvOpts(t, st, defaultLimits(), opts{nuget: n}), n
}

const csprojE2E = `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageReference Include="Serilog" Version="3.1.0" />
    <PackageReference Include="Ranged" Version="[1.0,2.0)" />
  </ItemGroup>
</Project>
`

func TestNugetPackageBump(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := nugetEnv(t, st)
		reg.Publish("Newtonsoft.Json", "13.0.1")
		reg.Publish("Serilog", "3.1.0")
		reg.Publish("Ranged", "1.0.0")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": nugetConfig, "src/App/App.csproj": csprojE2E, "src/App/packages.lock.json": "{}\n"})
		e.gh.AddRepo("acme/optout", map[string]string{"src/App/App.csproj": csprojE2E})
		start(t, e, "acme/app", "acme/optout")
		if n := len(e.deps("acme/app")); n != 2 {
			t.Fatalf("Newtonsoft.Json and Serilog are tracked (not the range), got %+v", e.deps("acme/app"))
		}

		e.clock.Advance(time.Minute)
		reg.Publish("Newtonsoft.Json", "13.0.3")
		reg.Publish("Newtonsoft.Json", "14.0.0-beta1") // a prerelease: not proposed
		reg.Publish("Serilog", "3.1.1.5")              // four segments: not proposed
		reg.Publish("Serilog", "3.2.0")
		reg.Publish("Ranged", "1.5.0") // never read: a range
		e.tick()
		e.tick() // a replay must not open a second PR

		prs := e.gh.PRs("acme/app")
		if len(prs) != 2 {
			t.Fatalf("one PR per package, got %d", len(prs))
		}
		for _, pr := range prs {
			got, _ := e.gh.FileAt("acme/app", pr.Head, "src/App/App.csproj")
			from, to := "", ""
			switch {
			case strings.Contains(strings.ToLower(pr.Head), "newtonsoft"):
				from, to = `Version="13.0.1"`, `Version="13.0.3"`
			case strings.Contains(strings.ToLower(pr.Head), "serilog"):
				from, to = `Version="3.1.0"`, `Version="3.2.0"`
			default:
				t.Fatalf("unexpected branch %q", pr.Head)
			}
			if want := strings.Replace(csprojE2E, from, to, 1); got != want {
				t.Errorf("%s: only that version moves, layout kept:\n%s", pr.Head, got)
			}
			if lock, _ := e.gh.FileAt("acme/app", pr.Head, "src/App/packages.lock.json"); lock != "{}\n" {
				t.Errorf("%s: packages.lock.json must be left alone, got %q", pr.Head, lock)
			}
			for _, w := range []string{"packages.lock.json not regenerated", "`src/App/packages.lock.json`", "dotnet restore"} {
				if !strings.Contains(pr.Body, w) {
					t.Errorf("%s: PR body lacks %q:\n%s", pr.Head, w, pr.Body)
				}
			}
			if !strings.HasPrefix(pr.Head, "ripple/nuget/") {
				t.Errorf("branch %q", pr.Head)
			}
		}
		for _, r := range reg.Requests() {
			if r.Authorization != "" || !(r.Path == "/v3/index.json" || strings.HasPrefix(r.Path, "/v3-flatcontainer/")) {
				t.Errorf("request %+v: anonymous, and only the index and a package's versions", r)
			}
			if strings.Contains(r.Path, "ranged") {
				t.Errorf("a reference ripple does not track was asked for: %s", r.Path)
			}
			if strings.ToLower(r.Path) != r.Path {
				t.Errorf("package ids are looked up lowercased, got %s", r.Path)
			}
		}
		if len(e.gh.PRs("acme/optout")) != 0 || len(e.deps("acme/optout")) != 0 {
			t.Error("a repo that did not opt in was touched")
		}
	})
}

func TestNugetDeniedPackageIsLeftAlone(t *testing.T) {
	forEach(t, func(t *testing.T, st store.Store) {
		e, reg := nugetEnv(t, st)
		reg.Publish("Secret.Pkg", "1.0.0")
		reg.Deny("Secret.Pkg")
		e.gh.AddRepo("acme/app", map[string]string{".ripple.yaml": nugetConfig, "a.csproj": `<Project><ItemGroup><PackageReference Include="Secret.Pkg" Version="1.0.0" /></ItemGroup></Project>` + "\n"})
		start(t, e, "acme/app")
		e.clock.Advance(time.Minute)
		e.tick()
		if n := len(e.gh.PRs("acme/app")); n != 0 {
			t.Errorf("a package that refuses anonymous reads opened %d PRs", n)
		}
	})
}
