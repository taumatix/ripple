package manager

import (
	"errors"
	"strings"
	"testing"

	"github.com/taumatix/ripple/internal/model"
)

const csproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.1" />
    <PackageReference Include='Serilog' Version='3.1.0'>
      <PrivateAssets>all</PrivateAssets>
    </PackageReference>
    <PackageReference
        Include="Multi.Line"
        Version="2.0.0" />
    <PackageReference Include="Range" Version="[1.0,2.0)" />
    <PackageReference Include="Float" Version="1.*" />
    <PackageReference Include="Prop" Version="$(PropVer)" />
    <PackageReference Include="Pre" Version="1.0.0-beta.1" />
    <PackageReference Include="Four" Version="1.2.3.4" />
    <PackageReference Include="Child"><Version>1.0.0</Version></PackageReference>
    <PackageReference Update="Other" Version="1.0.0" />
    <PackageReference Include="Over" VersionOverride="1.0.0" />
    <PackageReference Include="NoVersion" />
    <!-- <PackageReference Include="Commented" Version="9.9.9" /> -->
    <PackageVersion Include="Central" Version="4.5.6" />
  </ItemGroup>
</Project>
`

func TestNugetExtract(t *testing.T) {
	deps, err := Nuget{}.Extract("app.csproj", []byte(csproj))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		if d.Datasource != model.DatasourceNuget || d.Manager != "nuget" {
			t.Errorf("%+v", d)
		}
		got = append(got, d.Package+"@"+d.Version)
	}
	want := "Newtonsoft.Json@13.0.1 Serilog@3.1.0 Multi.Line@2.0.0 Central@4.5.6"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestNugetApply(t *testing.T) {
	dep := model.Dependency{File: "app.csproj", Package: "Serilog", Version: "3.1.0"}
	out, err := Nuget{}.Apply([]byte(csproj), dep, "", "3.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(csproj, "Version='3.1.0'", "Version='3.1.1'", 1); string(out) != want {
		t.Errorf("only the digits move:\n%s", out)
	}
	dep = model.Dependency{File: "app.csproj", Package: "Multi.Line", Version: "2.0.0"}
	if out, err = (Nuget{}).Apply([]byte(csproj), dep, "", "v2.1.0"); err != nil || !strings.Contains(string(out), `Version="2.1.0" />`) {
		t.Errorf("multi-line tag: %v\n%s", err, out)
	}
	if _, err := (Nuget{}).Apply([]byte(csproj), model.Dependency{File: "x", Package: "Newtonsoft.Json", Version: "13.0.0"}, "", "14.0.0"); !errors.Is(err, ErrStale) {
		t.Errorf("a changed version is stale, got %v", err)
	}
	if _, err := (Nuget{}).Apply([]byte(csproj), model.Dependency{File: "x", Package: "Commented", Version: "9.9.9"}, "", "9.9.10"); !errors.Is(err, ErrStale) {
		t.Errorf("a commented reference is not there, got %v", err)
	}
	if _, err := (Nuget{}).Apply([]byte(csproj), model.Dependency{Package: "Serilog", Version: "3.1.0"}, "other", "3.2.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("rename: %v", err)
	}
	if _, err := (Nuget{}).Apply([]byte(csproj), model.Dependency{Package: "Serilog", Version: "3.1.0"}, "", "4.0.0-rc1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("prerelease target: %v", err)
	}
}

func TestNugetMatches(t *testing.T) {
	n := Nuget{Dir: "/src"}
	for p, want := range map[string]bool{"src/a.csproj": true, "src/x/B.FSPROJ": true, "src/c.vbproj": true, "src/Directory.Packages.props": true, "a.csproj": false, "src/a.sln": false, "src/packages.config": false} {
		if n.Matches(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
