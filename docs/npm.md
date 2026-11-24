# npm manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: npm
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`package.json` files. Files under `node_modules` are never read.

## What is read and moved

`dependencies`, `devDependencies` and `optionalDependencies` of every `package.json` under `directory`.
`peerDependencies` are not touched: a peer range is a promise to users, not a pin.

| Range | Handling |
|---|---|
| `1.2.3`, `=1.2.3`, `^1.2.3`, `~1.2.3` | moved to a newer release; the operator is kept (`^1.2.3` -> `^1.5.0`) |
| `1`, `1.2`, `^1.2`, `~1.2`, `1.x`, `1.2.x`, `~3.4.*` | moved at the precision written, tail kept (`^1.2` and 1.7.4 give `^1.7`, `2.x` and 3.0.1 give `3.x`); not moved when it already names the new version at that precision (`1.x` stays for 1.9.0) |
| `>=1.2.3`, `>= 1.2` | the floor is raised (`>=1.2.3` -> `>=1.5.0`), spacing and precision kept |
| `>=1.2.3 <2`, `>=1 <=1.5` | a `>=` floor with `<`/`<=` upper bounds: the floor is raised and the bounds kept, but only to a version inside them (`2.0.0` is no target for `<2`) |
| `^1.4.0 \|\| ^2.0.0` | a union of the ranges above: only the alternative with the highest floor follows (`^1.4.0 \|\| ^2.3.0`), every other byte, older alternatives and spacing included, is kept; ripple never drops or adds an alternative |
| `*`, `latest`, `>1.2.3`, `<=1.2.3`, `^1.2 <1.5`, `1 - 2`, `^1.0.0-beta.1`, a union with any alternative not listed above | ignored: not a range ripple reads with certainty |
| `github:`, `git+`, `file:`, `http(s):`, `npm:` aliases, `workspace:` | ignored |

A bumped range is raised to the new version even when it already allowed it (`^1.2.3` with 1.2.9 published becomes
`^1.2.9`): without a lockfile refresh that is the only change a PR can carry. Only the range's string is edited; layout,
key order and indentation are kept byte for byte. One package moves per PR, on branch `ripple/npm/<name>-<version>`.

## Which version

Read anonymously from `registry.npmjs.org` as abbreviated metadata. The target is the newest version above the range's
version that is a release (no prerelease), is not deprecated, and is not above the `latest` dist-tag. For each dependency ripple considers the newest release of each update type and takes the newest one the repo's rules allow, so a repo that ignores majors still gets the minor behind a newer major. Update types
(patch, minor, major) follow the version, so `ignore`, cooldown and automerge rules apply as for Go modules. The
abbreviated document has no publication dates, so the cooldown runs from when ripple first saw the version.

Only packages some repo uses are read, once per poll, never the registry as a whole.

## Lockfiles

ripple never runs npm, yarn, pnpm or node. It rewrites a `package-lock.json` or `npm-shrinkwrap.json` itself only when the
change cannot differ from what npm would write, and says so in the PR body ("Lockfiles rewritten without npm"):

- `lockfileVersion` 2 or 3, no `workspaces`;
- the package sits at the top of `node_modules`, is a plain leaf (its entry holds only version, resolved, integrity,
  dev/optional flags, license, funding, engines and bin), and no other lock entry requires it;
- the root entry's range for it is the range in `package.json`, and it is not also a peer dependency;
- the registry's metadata for the locked and the new version agree on dependencies, optional and peer dependencies, bin,
  engines, os, cpu and install scripts, the locked integrity is the registry's for the locked version, the new version is
  newer, and both tarballs are on the same host.

Then the entry's version, tarball URL and integrity (from the registry's `dist`) and the root range change, and every
other byte of the file stays. In every other case, and for `yarn.lock`, `pnpm-lock.yaml` and bun lockfiles, the PR body
lists the file under "Lockfiles not regenerated" with the reason, and the owner refreshes it before merging (a
frozen-lockfile CI fails until then). A rewritten file has not been checked with `npm ci`.

## Private packages and other registries

The client never sends a credential. A package that answers 401, 403 or 404 is logged and left alone.

Registry routing is read from `.npmrc`, `.yarnrc.yml` (`npmRegistryServer`, `npmScopes`) and `.yarnrc` (`registry`,
`@scope:registry`) in the `package.json`'s directory and every parent up to the repo root; for each key the nearest file
that sets it wins. A package whose registry (its scope's, else the default) is not the public one is skipped with that
reason, because a public package of the same name would be a different package. "Public" means an https URL whose host is
exactly `registry.npmjs.org` (or yarn's mirror `registry.yarnpkg.com`): a URL that only mentions it in its path, or holds
an unexpanded `${VAR}`, is not. Credential keys in these files are never kept. ripple does not read from a registry a
repository names, so private registries stay unsupported (ROADMAP 200).

See [UPSTREAM.md](../UPSTREAM.md) for what was not verified against the real registry.
