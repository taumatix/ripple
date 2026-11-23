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
| `*`, `latest`, `1.x`, `^1.2`, `>=1.2.3`, `1.2 \|\| 2`, `1 - 2`, `^1.0.0-beta.1` | ignored: not a range ripple reads with certainty |
| `github:`, `git+`, `file:`, `http(s):`, `npm:` aliases, `workspace:` | ignored |

A bumped range is raised to the new version even when it already allowed it (`^1.2.3` with 1.2.9 published becomes
`^1.2.9`): without a lockfile refresh that is the only change a PR can carry. Only the range's string is edited; layout,
key order and indentation are kept byte for byte. One package moves per PR, on branch `ripple/npm/<name>-<version>`.

## Which version

Read anonymously from `registry.npmjs.org` as abbreviated metadata. The target is the newest version above the range's
version that is a release (no prerelease), is not deprecated, and is not above the `latest` dist-tag. Update types
(patch, minor, major) follow the version, so `ignore`, cooldown and automerge rules apply as for Go modules. The
abbreviated document has no publication dates, so the cooldown runs from when ripple first saw the version.

Only packages some repo uses are read, once per poll, never the registry as a whole.

## Lockfiles

ripple never runs npm, yarn, pnpm or node, so it cannot regenerate a lockfile. When `package-lock.json`,
`npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml` or a bun lockfile sits beside an edited `package.json`, the PR body
lists it under "Lockfiles not regenerated": the owner refreshes it before merging (a frozen-lockfile CI fails until then).

## Private packages and other registries

The client never sends a credential. A package that answers 401, 403 or 404 is logged and left alone. A package whose
`.npmrc` (beside the `package.json` or at the repo root) sets `registry=` to another host, or `@scope:registry=` for the
package's scope, is skipped with that reason, because a public package of the same name would be a different package.

See [UPSTREAM.md](../UPSTREAM.md) for what was not verified against the real registry.
