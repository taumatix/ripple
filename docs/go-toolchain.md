# Go toolchain and `go` directive updates

Off by default. A `gomod` update entry opts in with `go-toolchain`:

```yaml
updates:
  - ecosystem: gomod
    go-toolchain: toolchain   # or: directive
```

| Value | What ripple proposes |
|---|---|
| unset | nothing (the default) |
| `toolchain` | the `toolchain` line follows the newest patch of its own minor; once that minor is out of support it moves to the oldest supported minor |
| `directive` | everything `toolchain` does, plus the `go` line moves to the oldest supported minor when its own minor is out of support |

## Security advisories

With OSV on ([osv.md](osv.md)), an advisory for `stdlib` or `toolchain` moves the `toolchain` line to the lowest
fixed Go release, skipping the cooldown, only in a repo that opted in with `go-toolchain:`. A repo with only a `go`
directive is not touched. If a directive PR would lift the same file's toolchain line, the two can conflict (ROADMAP).

## The same version elsewhere

A toolchain or directive PR also moves the Go version named next to go.mod, so CI and
local tooling do not stay on the old release. Only these exact line shapes are rewritten,
in the repo root, the module's directory, `.github/workflows/` and `.devcontainer/` at the root:

- `.go-version`: a file holding just a version.
- `.tool-versions`: a `golang <version>` line (a trailing comment is kept).
- workflows: `go-version: 1.24.1`, quoted or not, alone or as a list item.
- Dockerfiles (`Dockerfile`, `Dockerfile.*`, `*.Dockerfile`): `FROM golang:<version>[-suffix] [AS name]`.
- `.mise.toml` / `mise.toml`: `go = "1.24.1"` under `[tools]` only.
- `.devcontainer/devcontainer.json` (also one directory deeper) and `.devcontainer.json`: the `image` line when it is
  `mcr.microsoft.com/devcontainers/go:<version>[-suffix]` or `golang:<version>[-suffix]`. A `1-1.24-bookworm` tag
  floats on the minor and is left alone, as is a Go feature's `version`.
- `go.work`: the `go 1.24.1` and `toolchain go1.24.1` lines, in the repo root or the module's directory.
- `Makefile`, `GNUmakefile` and `*.mk`: a `GO_VERSION` or `GOLANG_VERSION` assignment (`=`, `:=`, `::=`, `?=`,
  optionally `export`ed) whose value is a literal version.

A file moves only if it names the same minor go.mod had and an older release than the
target. A version written without a patch (`1.24`) keeps that spelling and moves only when
the minor does. Matrix expressions, `stable`, `go-version-file`, image digests, version
lists, `ARG`-driven tags and comments are left alone. The PR body lists the files moved.
A workflow under a module's own `.github/workflows/` is not followed: GitHub only runs the root's. Other tools reading other files are not touched.

## Choices, and why

- **The two lines mean different things.** `go` is the least Go version the module needs: a
  dependant cannot build with anything older, so raising it raises their requirement. `toolchain`
  only picks the Go that builds this module; dependants do not read it. So a toolchain PR is safe
  to merge and a directive PR says so in its body.
- **The directive never takes a patch.** It moves only because the Go project stopped supporting
  its minor, and then to the oldest minor that is still supported (the smallest step off the
  unsupported releases), not the newest. A directive written `1.25` becomes `1.26`; one written
  `1.25.3` becomes `1.26.0`. Neither is ever lowered, and a line newer than every supported
  release (a pre-release Go) is left alone.
- **A directive PR also lifts a `toolchain` line that would sit below it**, to the same release,
  because Go rejects that combination. In that case no separate toolchain PR is opened for the file.
- **Support policy.** Go supports the two newest minors. ripple reads that from
  `https://go.dev/dl/?mode=json` (override with `RIPPLE_GO_RELEASES_URL`), which without
  `include=all` lists exactly the supported stable releases, newest patch each. Release
  candidates are ignored.
- **No release dates.** That feed has none, so the cooldown (`minimum-release-age`) runs from the
  first poll that saw the release, and a replay never moves it.
- **Verification uses the server's Go, or the release the go.mod names.** The sandbox runs `go` with
  `GOTOOLCHAIN=local` and never downloads a toolchain ([sandbox.md](sandbox.md)). By default `go mod tidy`,
  `build` and `vet` therefore check the code against the Go installed on the server, which may differ from the
  version in the PR. A directive above the server's Go fails verification and the PR opens as a draft; a toolchain
  line above it does not fail (Go ignores it under `GOTOOLCHAIN=local`). Each PR body says CI is the real check.
- **A toolchain directory removes that gap.** Set `RIPPLE_GO_TOOLCHAIN_DIR` to a directory holding releases the
  operator installed, one per directory in the layout a go.dev download unpacks to (`go1.25.3/bin/go`,
  `go1.26.1/bin/go`; a directory named only for a minor, `go1.26`, counts as `.0`). Verification then runs the
  release the module's go.mod names: the `toolchain` line exactly, else the newest installed patch of the `go`
  line's minor that is not older than the line. For a bump PR that is the go.mod as the bump left it, so a
  directive moved above the server's Go is checked by that Go and no longer opens a draft. A module with no
  matching release, or no directory set, uses the server's Go as before. Still `GOTOOLCHAIN=local`: ripple
  downloads nothing and the PR body says which release ran. Not available with the container sandboxes, whose
  image carries its own Go; the server refuses to start with both.

The release list is fetched only while some repo has opted in.
