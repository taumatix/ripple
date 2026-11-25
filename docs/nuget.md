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
- the tag has no `Update=` or `VersionOverride=` attribute and is not inside an XML comment.

Skipped: ranges (`[1.0,2.0)`, `(,1.0]`), floating versions (`1.*`), properties (`$(Ver)`), prereleases (`1.0.0-beta`),
two- and four-segment versions, a version given as a `<Version>` child element, `packages.config`, and `.props`
files other than `Directory.Packages.props`.

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
