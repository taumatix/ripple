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
- **Verification uses the server's Go.** The sandbox runs `go` with `GOTOOLCHAIN=local` and never
  downloads a toolchain ([sandbox.md](sandbox.md)). `go mod tidy`, `build` and `vet` therefore check
  the code against the Go installed on the server, which may differ from the version in the PR.
  A directive above the server's Go fails verification and the PR opens as a draft; a toolchain
  line above it does not fail (Go ignores it under `GOTOOLCHAIN=local`). Each PR body says CI is
  the real check.

The release list is fetched only while some repo has opted in.
