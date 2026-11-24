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
inline table `name = { version = "req", ... }` (one line or several), a dotted key (`name.version = "req"`), or a
`[dependencies.name]` table with a `version` key. A `package = "real-name"` key names the crate that is looked up.
The file is read with a TOML scanner that keeps byte offsets; a file that is not valid TOML (an unterminated string, a
duplicate key) is skipped with a warning.

| Requirement | Handling |
|---|---|
| `1.2.3`, `=1.2.3`, `^1.2.3`, `~1.2.3` | moved to a newer release; the operator is kept (`~1.2.3` -> `~1.2.9`) |
| `1`, `1.2`, `^1.2`, `~1.2`, `=0.2` | moved at the precision written (`1.2` and 1.7.4 give `1.7`); not moved when it already names the new version at that precision (`1` stays `1` for 1.9.0, `1.2` for 1.2.9) |
| `>=1.2`, `>=1.2.3` | the lower bound moves like a caret requirement, at the precision written (`>=1.2` and 1.7.4 give `>=1.7`) |
| `>=1.2, <2`, `^1.2, <1.5` | the lower bound moves, but only to a version the upper bounds allow; an upper bound (`<`, `<=`) is a decision and is never edited, so 2.0.0 is not a target for `<2` |
| `1.*`, `1.2.*` | moves only to a version the wildcard does not already match, keeping the wildcard (`1.*` -> `2.*`, `1.2.*` -> `1.3.*`); `1.*` with 1.9.0 published has nothing to do |
| `*`, `>1`, `<2` alone, several lower bounds, prereleases | ignored: not a requirement ripple reads with certainty |
| a `path`, `git` or `registry` key, `workspace = true` | ignored (a local or other-registry crate is not on crates.io; an inherited one has no version of its own) |
| `name = { workspace = true }` in a member | has no version of its own; the root's `[workspace.dependencies]` entry is the one that moves, for every member |

A bumped requirement is raised to the new version even when it already allowed it (`1.0.100` with 1.0.200 published
becomes `1.0.200`): without a lockfile refresh that is the only change a PR can carry. Only the requirement's string is
edited, whatever shape the dependency was written in; layout, comments and key order are kept byte for byte. One crate moves per PR, on branch
`ripple/cargo/<crate>-<version>`.

An update's type follows cargo's compatibility rule, which puts the break at the leftmost nonzero number the
requirement writes: `1.2.3` breaks on the major, `0.2.3` on the minor (0.2.3 -> 0.3.0 is "major"), `0.0.3` on the patch
(0.0.3 -> 0.0.4 is "major"). The operator does not change that. So `ignore`, cooldown and automerge rules see a 0.x
minor bump as the breaking change it is. For each dependency ripple considers the newest release of each type and takes
the newest one the repo's rules allow: with majors ignored, `0.2.3` still gets 0.2.9 while 0.3.0 exists.

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
