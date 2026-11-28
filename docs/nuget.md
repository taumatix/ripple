# NuGet manager (nuget.org)

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: nuget
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
project files.

## What is read

`*.csproj`, `*.fsproj`, `*.vbproj` and `Directory.Packages.props` files. Each `<PackageReference Include="Id"
Version="1.2.3" />` and (central package management) `<PackageVersion Include="Id" Version="1.2.3" />` is tracked when:

- the version attribute is three numeric segments, `1.2.3`. A NuGet version attribute is a minimum version, so this plain
  form is what "pin to this release" looks like;
- the id is a valid package id (letters, digits, `.`, `_`, `-`);
- the tag is not inside an XML comment.

Also read, both settled by running real `dotnet restore` (`TestRealtoolNugetItemForms`, CI `realtool / nuget`,
2026-11-27, .NET 8 SDK on `Newtonsoft.Json`) and only inside this opt-in ecosystem:

- a `PackageReference`'s `<Version>1.2.3</Version>` child element (with no `Version` attribute). MSBuild item metadata may
  be a child element (the vendored [items page](vendor/msbuild/msbuild-items.md)); dotnet restored 13.0.3 from
  `<Version>13.0.3</Version>`. Only the digits inside the element are edited. Exactly one plain `<Version>` child is read:
  two of them, one with a `Condition`, and an element not closed before the next reference are left alone;
- `<PackageReference Update="Id" Version="1.2.3" />` (or the child form) **when an `Include="Id"` PackageReference comes
  before it in the same file**. MSBuild processes items in order of appearance and `Update` changes the metadata of
  items already evaluated (vendored pages); dotnet restored the Update's 13.0.3 over the Include's 12.0.3, restored the
  Include's own 12.0.3 when the Update came first, gave an Include with no version the Update's version, and restored
  nothing for an Update with no Include. So an Update before its Include, or with none, is not read. The Update's own
  digits are what is edited.

Two consequences, both from what dotnet restored (`TestRealtoolNugetItemForms`) and both round-tripped through real
`dotnet restore` after the edit (`TestRealtoolNugetItemFormsEdit`):

- **An item that a later Update overrides is dead and is skipped.** For `Include="X" Version="1.0.0"` followed by
  `Update="X" Version="2.0.0"` only the Update (2.0.0) is read and edited; proposing the Include would change nothing.
  The engine has no per-skip report channel, so the skipped Include is silent: no PR opens for it.
- **An Include with both a `Version` attribute and a `<Version>` child is edited at the child**, the one dotnet uses; the
  attribute is left alone.

An item whose version cannot be told (several `<Version>` children, or one with a `Condition`) is skipped, and so is an
Include that such an Update overrides, because the live version is unknown. Not read yet, each on the ROADMAP: an Update
in another file than its Include (261). That one is a fact, not a feature: with an Include in `Directory.Packages.props` and
a `PackageVersion Update` of it in the project, the project's Update is what `dotnet restore` holds, so ripple's edit of the
props Include changes nothing restore holds (`TestRealtoolNugetCrossFileUpdate`, run in CI only).

`<PackageVersion Update="Id" Version="1.2.3" />` after a `<PackageVersion Include="Id">` of the same file is read like a
PackageReference Update: the Update is edited and the Include before it is dead (`TestRealtoolNugetPackageVersionUpdate`,
CI). A PackageVersion Update does not touch a PackageReference of the same id. An Update naming a list (`Update="A;B"`)
or a wildcard (`Update="A*"`) is not read, and every earlier item of the same kind that it matches is left alone too,
because MSBuild may have overridden it and the live version is unknown. That the list and wildcard forms really
override is MSBuild's documented item semantics, assumed, not run here; the case-insensitivity of item identity is
also unverified (ROADMAP 326).

`<PackageReference Include="Id" VersionOverride="1.2.3" />` (central package management) is read, and its digits are
edited: `TestRealtoolNugetVersionOverride` (CI, `nuget-cpm` fixture) asserts that real `dotnet restore` holds the
override over the `PackageVersion` of `Directory.Packages.props`, and holds the new version after ripple's edit. Not
read: a `VersionOverride` beside a `Version` attribute or an Update, with a `<Version>` child, or on a `PackageVersion`.
The central `PackageVersion` of the same package is still a separate entry and moves on its own; a project that
overrides it is not affected by that move, so a central bump can leave the overriding project behind, which is
intended. Not verified: `CentralPackageVersionOverrideEnabled=false` (the override is then an error, and ripple would
still edit it).

Skipped: ranges (`[1.0,2.0)`, `(,1.0]`), floating versions (`1.*`), properties (`$(Ver)`), prereleases (`1.0.0-beta`),
two- and four-segment versions, `packages.config`, and `.props` files other than `Directory.Packages.props`.

## What is proposed

The versions of each tracked id are read from the flat container of nuget.org: ripple fetches the service index
(`https://api.nuget.org/v3/index.json`), takes the `PackageBaseAddress/3.0.0` resource, then reads
`<base>/<lowercase id>/index.json`, once per id per poll, anonymously. Only releases of three numeric segments are
proposed; prereleases and four-segment versions are ignored. Listed and unlisted versions are both in that document, so
an unlisted version can be proposed. The `cooldown` runs from the first poll that saw the version.

Ripple edits only the version digits; quotes, attribute order and layout stay. If the file changed since the poll the
job is stale and nothing is written. A change is applied to every declaration of that id at that version in the file.

## Private feeds (read this)

ripple reads only nuget.org and does not read `NuGet.Config`. A package id that also exists on nuget.org but is
served from a private feed in your `NuGet.Config` would be looked up on nuget.org, and a newer version published there
under that id would be proposed (dependency confusion). Use package source mapping and do not enable this ecosystem for
repos whose private ids could be claimed on nuget.org.

## Lock files

A `packages.lock.json` beside the edited file is not rewritten. The PR body names it as stale; run `dotnet restore
--force-evaluate` and push to the branch.

The branch prefix is `ripple/nuget/`. An id written in different cases in different files is tracked as separate packages.

See [UPSTREAM.md](../UPSTREAM.md): nothing here is verified against the real nuget.org.
