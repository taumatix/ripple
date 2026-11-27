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
- the tag has no `VersionOverride=` attribute and is not inside an XML comment.

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

Known gaps, each on the ROADMAP: the Include that a later Update overrides is still read and proposed (an edit with no
effect, 260); when an Include has both a `Version` attribute and a child, dotnet uses the child but ripple still
reads and edits the attribute (260); an Update in another file than its Include, `PackageVersion Update=`, and
`VersionOverride=` stay unread (261 to 263).

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
