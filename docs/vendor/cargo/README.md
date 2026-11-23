# Cargo sparse index and Cargo.toml excerpts

Fetched 2026-11-23 (via a summarising fetch, so the wording is condensed, not copied byte for byte) and read before
`internal/cratesindex` and `internal/manager/cargo.go` were written.

Sources: <https://doc.rust-lang.org/cargo/reference/registry-index.html> and
<https://doc.rust-lang.org/cargo/reference/specifying-dependencies.html>.

## Sparse index

- A registry URL with the `sparse+` prefix (`sparse+https://index.crates.io/`) uses "an individual HTTP request" per
  index file.
- Path of a crate's file: 1 character names in `1/`, 2 characters in `2/`, 3 characters in `3/{first-character}/`, 4 or
  more in `{first-two}/{second-two}/`; example: `cargo` is at `ca/rg/cargo`. "Although the index filenames are in
  lowercase, the fields that contain package names in `Cargo.toml` and the index JSON data are case-sensitive."
- A file holds one JSON object per line, one per published version, with `name`, `vers` (SemVer 2.0.0), `deps`,
  `cksum`, `features`, `yanked` (the only field that can change after publication) and optionally `rust_version`.
- Each entry of `deps` has `name` (the aliased name if renamed), `req`, `features`, `optional`, `default_features`,
  `target`, `kind` (`dev`, `build`, `normal`), `registry` and `package` (the original name if renamed). ripple reads
  none of `deps`.

## Cargo.toml

- `1.2.3` is `^1.2.3` ("Leaving off the caret is a simplified equivalent syntax"); `~1.2.3 := >=1.2.3, <1.3.0`;
  `=1.2.3` is exact; `*`, `1.*` and comparison operators exist; several comma-separated requirements must all hold.
- Tables: `[dependencies]`, `[dev-dependencies]`, `[build-dependencies]`, `[target.'cfg(unix)'.dependencies]`, and
  `[workspace.dependencies]` (inherited with `workspace = true`).
- A dependency is a version string, an inline table (`{ version = "...", features = [...] }`) or a table of its own
  (`[dependencies.awesome]`, with `version`, `features`, ...).
- `registry = "name"` selects another registry; `git`/`path` select a source; a `version` next to a `path` or `git` is
  what is used when the package is published. `package = "foo"` renames: the key is the local name, `package` the crate.

## Assumed, unverified (not in the excerpts above)

- `index.crates.io` is the sparse index of crates.io, served without credentials; a crate that does not exist answers
  404.
- Index responses may carry `ETag`/`Last-Modified` for conditional requests (ripple does not send them).
- `Cargo.lock` lives beside a package's Cargo.toml or at the workspace root; `.cargo/config.toml` (or `.cargo/config`)
  in the directory or a parent can replace the crates.io source with `[source.crates-io] replace-with = "..."`.
- The exact TOML grammar: ripple's reader is a line scanner, not a TOML parser (see docs/cargo.md for what it reads).
