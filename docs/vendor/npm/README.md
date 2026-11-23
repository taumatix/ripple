# npm registry and package.json excerpts

Fetched 2026-11-23 (via a summarising fetch, so the wording is condensed, not copied byte for byte) and read before
`internal/npmreg` and `internal/manager/npm.go` were written.

Source: <https://github.com/npm/registry/blob/main/docs/responses/package-metadata.md> and
<https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md>.

## Package metadata (`GET /{package}`)

- The endpoint "responds with the package metadata document, sometimes informally called a packument".
- Abbreviated metadata is requested with `Accept: application/vnd.npm.install-v1+json`; a fuller form is
  `application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8, */*`.
- Abbreviated top-level fields: `name`, `modified` (ISO timestamp), `dist-tags` (tag -> version), `versions`
  (version -> version object).
- Each abbreviated version object has `name`, `version`, `dist` (`tarball`, `shasum`, `integrity`), the four
  dependency maps, `engines`, `bin`, `cpu`, `os`, `_hasShrinkwrap`, and `deprecated` (the deprecation message, "if
  applicable").
- The full document additionally has `time` (publication timestamp per version), `maintainers`, `readme` and so on.
  The abbreviated one has **no** publication dates, so ripple's cooldown runs from when it first saw a version.

## Assumed, unverified (not in the excerpts above)

- A scoped name is requested as `/@scope%2Fname`.
- A package that does not exist, or a private one asked for without credentials, answers 404; 401/403 may also occur.
  Ripple treats all three as "not readable anonymously" and leaves the dependency alone.
- `deprecated` is a non-empty string on a deprecated version and absent otherwise (npm also writes `""` to undo it).
- The `latest` dist-tag names a version that exists in `versions`, and is what `npm install pkg` resolves to.
- `registry.npmjs.org` is the default registry, and `.npmrc` can override it per scope (`@scope:registry=URL`) or
  wholesale (`registry=URL`).
- package.json: `dependencies`, `devDependencies`, `optionalDependencies` map names to a semver range, and the
  operators `^` and `~` and a bare version have their usual meaning (node-semver). Not retrieved: node-semver's own
  grammar. Ripple only rewrites a range it can read with certainty (see docs/npm.md) and leaves the rest alone.
