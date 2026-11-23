# Cargo manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: cargo
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`Cargo.toml` files. Files under a `target` directory are never read.

## What is read and moved

The dependency tables of every `Cargo.toml` under `directory`: `[dependencies]`, `[dev-dependencies]`,
`[build-dependencies]`, their `[target.<cfg>.*]` forms and `[workspace.dependencies]`, written as `name = "req"`, as a
one-line inline table `name = { version = "req", ... }`, or as a `[dependencies.name]` table with a `version` key. A
`package = "real-name"` key names the crate that is looked up.

| Requirement | Handling |
|---|---|
| `1.2.3`, `=1.2.3`, `^1.2.3`, `~1.2.3` | moved to a newer release; the operator is kept (`~1.2.3` -> `~1.2.9`) |
| `*`, `1.*`, `1.2`, `>=1.2`, `>=1, <2`, prereleases | ignored: not a requirement ripple reads with certainty |
| a `path`, `git` or `registry` key, `workspace = true` | ignored (a local or other-registry crate is not on crates.io; an inherited one has no version of its own) |
| multi-line inline tables, dotted keys (`foo.version = "1"`) | ignored |

A bumped requirement is raised to the new version even when it already allowed it (`1.0.100` with 1.0.200 published
becomes `1.0.200`): without a lockfile refresh that is the only change a PR can carry. Only the requirement's string is
edited; layout, comments and key order are kept byte for byte. One crate moves per PR, on branch
`ripple/cargo/<crate>-<version>`. For a `0.x` crate cargo treats a minor bump as breaking, but ripple classifies by the
version numbers (0.2 -> 0.3 is "minor"), so `ignore` and automerge rules see it as one.

## Which version

Read anonymously from crates.io's sparse index (`index.crates.io`, one file per crate). The target is the newest version
above the requirement's version that is a release (no prerelease) and is not yanked. Update types (patch, minor, major)
follow the version, so `ignore`, cooldown and automerge rules apply as for Go modules. The index has no publication
dates, so the cooldown runs from when ripple first saw the version.

Only crates some repo uses are read, once per poll, never the index as a whole.

## Cargo.lock

ripple never runs cargo, so it does not regenerate `Cargo.lock`, and it does not edit it by hand: a lock entry carries
a checksum and the resolved dependency edges of the new version, neither of which ripple can know without running
cargo's resolver. **Cargo.lock is left exactly as it was.** When one sits beside an edited `Cargo.toml`, or in a parent
directory (a workspace keeps its lock at the root), the PR body lists it under "Cargo.lock not regenerated": the owner
runs `cargo update -p <crate>` before merging (a `--locked` CI fails until then). Since a requirement `"1.0.100"`
already allows 1.0.200, a repo without `--locked` builds either way; the PR is how the floor moves.

## Other registries and mirrors

The client never sends a credential. A crate that answers 401, 403 or 404 is logged and left alone. A dependency with
a `registry` key is skipped, and so is every dependency of a repo whose `.cargo/config.toml` (or `.cargo/config`), in
the manifest's directory or a parent, contains `replace-with`: the crates.io source is then replaced, and a crates.io
crate of the same name could be a different crate (a dependency-confusion step). The job is recorded as skipped with
that reason.

See [UPSTREAM.md](../UPSTREAM.md) for what was not verified against the real index.
