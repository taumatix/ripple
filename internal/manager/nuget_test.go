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
	// "Child" is a <Version> child element, read since real dotnet restore resolved it; the
	// "Update" of "Other" has no earlier Include to update, so dotnet ignores it and so do we.
	want := "Newtonsoft.Json@13.0.1 Serilog@3.1.0 Multi.Line@2.0.0 Child@1.0.0 Over@1.0.0 Central@4.5.6"
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

// The item forms are what real dotnet restore resolved (TestRealtoolNugetItemForms): a
// <Version> child counts, an Update after its Include replaces the version, an Update before
// its Include or with none does nothing, and a child beats an attribute on the same item.
const csprojForms = `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Update="Early" Version="9.0.0" />
    <PackageReference Include="Early" Version="1.0.0" />
    <PackageReference Include="Late" Version="1.0.0" />
    <PackageReference Update="Late" Version="2.0.0" />
    <PackageReference Include="Bare" />
    <PackageReference Update="Bare" Version="3.0.0" />
    <PackageReference Include="ChildFirst" Version="1.0.0" />
    <PackageReference Update="ChildFirst">
      <Version>4.0.0</Version>
    </PackageReference>
    <PackageReference Update="Orphan" Version="5.0.0" />
    <PackageReference Include="Kid">
      <PrivateAssets>all</PrivateAssets>
      <Version>6.0.0</Version>
    </PackageReference>
    <PackageReference Include="Twice"><Version>7.0.0</Version><Version>8.0.0</Version></PackageReference>
    <PackageReference Include="Both" Version="1.1.0"><Version>1.2.0</Version></PackageReference>
    <PackageReference Include="CondChild"><Version Condition="'$(X)'==''">1.0.0</Version></PackageReference>
    <PackageReference Include="Prop"><Version>$(V)</Version></PackageReference>
    <PackageReference Include="Open"><Version>2.2.2</Version>
    <PackageReference Include="After" Version="1.0.1" />
    <PackageVersion Update="Central" Version="4.0.0" />
    <PackageReference Include="Ov" VersionOverride="9.0.0" />
    <PackageReference Include="OvBoth" Version="1.0.0" VersionOverride="2.0.0" />
    <PackageReference Update="Ov" VersionOverride="9.5.0" />
    <PackageVersion Include="OvCentral" VersionOverride="3.0.0" />
    <PackageReference Include="OverrideToo" VersionOverride="1.0.0"><Version>1.0.0</Version></PackageReference>
  </ItemGroup>
</Project>
`

func TestNugetExtractItemForms(t *testing.T) {
	deps, err := Nuget{}.Extract("app.csproj", []byte(csprojForms))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range deps {
		got = append(got, d.Package+"@"+d.Version)
	}
	// Early's Update comes before its Include: not read, the Include is. Late and ChildFirst have
	// an Include that a later Update overrides: only the Update is read. Both reads the child,
	// which dotnet uses over the attribute. "Open" is never closed before the next reference and
	// "Twice" and "CondChild" have children that cannot be told.
	// A VersionOverride is the version the project restores under central package management, so
	// "Ov" is read; beside a Version (OvBoth, NU1008), an Update, on a PackageVersion or with a
	// `<Version>` child (OverrideToo) it is not.
	want := "Early@1.0.0 Late@2.0.0 Bare@3.0.0 ChildFirst@4.0.0 Kid@6.0.0 Both@1.2.0 After@1.0.1 Ov@9.0.0"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestNugetApplyItemForms(t *testing.T) {
	for _, tc := range []struct{ pkg, spec, to, from, into string }{
		{"Late", "2.0.0", "2.5.0", `Update="Late" Version="2.0.0"`, `Update="Late" Version="2.5.0"`},
		{"Bare", "3.0.0", "3.1.0", `Update="Bare" Version="3.0.0"`, `Update="Bare" Version="3.1.0"`},
		{"ChildFirst", "4.0.0", "4.2.0", "<Version>4.0.0</Version>", "<Version>4.2.0</Version>"},
		{"Kid", "6.0.0", "6.0.9", "<Version>6.0.0</Version>", "<Version>6.0.9</Version>"},
		{"Ov", "9.0.0", "9.1.0", `VersionOverride="9.0.0"`, `VersionOverride="9.1.0"`},
		{"Both", "1.2.0", "1.2.5", "<Version>1.2.0</Version>", "<Version>1.2.5</Version>"},
	} {
		dep := model.Dependency{File: "app.csproj", Package: tc.pkg, Version: tc.spec}
		out, err := Nuget{}.Apply([]byte(csprojForms), dep, "", tc.to)
		if err != nil {
			t.Fatalf("%s: %v", tc.pkg, err)
		}
		if want := strings.Replace(csprojForms, tc.from, tc.into, 1); string(out) != want {
			t.Errorf("%s: only that version moves, got:\n%s", tc.pkg, out)
		}
	}
	for _, tc := range []struct{ pkg, spec string }{{"Orphan", "5.0.0"}, {"Early", "9.0.0"}, {"Twice", "7.0.0"}, {"Open", "2.2.2"},
		{"Late", "1.0.0"}, {"ChildFirst", "1.0.0"}, {"Both", "1.1.0"}} {
		dep := model.Dependency{File: "app.csproj", Package: tc.pkg, Version: tc.spec}
		if _, err := (Nuget{}).Apply([]byte(csprojForms), dep, "", "9.9.9"); !errors.Is(err, ErrStale) {
			t.Errorf("%s %s is not a declaration dotnet uses: want ErrStale, got %v", tc.pkg, tc.spec, err)
		}
	}
}
