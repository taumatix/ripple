# OS sandbox for `go` and `git`

A repository ripple scans is untrusted, and `go mod tidy`, `go build` and `go vet` run against it as the
server's uid. `RIPPLE_SANDBOX` confines those commands. The default is `none`, so existing installs do not change.

| Value | Backend | Platform |
|---|---|---|
| `none` | no confinement | any |
| `auto` | seatbelt on macOS, bwrap on Linux; fails if the binary is missing, never falls back to `none` | macOS, Linux |
| `seatbelt` | `sandbox-exec` | macOS |
| `bwrap` | bubblewrap | Linux |

## What is confined

- `go mod tidy`, `go build ./...`, `go vet ./...` and every local `git` command (checkout, clean, commit, ...).
- Writable: the job workspace and, if configured, the shared Go build cache. Everything else is read-only or
  invisible. Readable: system trees, the Go toolchain and the workspace. Not readable: the home directory, the
  App key file and other jobs' workspaces unless they sit under a readable system tree.
- Network: none, except one loopback port, a gate that forwards to `RIPPLE_GOPROXY` only. The gate's URL carries
  a random prefix and does not follow redirects.

## What is not

- `git clone`, `fetch` and `push` run unconfined: they hold the installation token and execute no repository
  code (hooks are off, `GIT_ALLOW_PROTOCOL` is pinned).
- The server process itself, and every other part of the job.
- `bwrap` cannot confine the network to a proxy port, so `go` refuses to run under it rather than share the
  host network. Until a Linux backend can reach the proxy (a network namespace with a forwarded socket), Linux
  deployments have no working option here; entry 2 of the roadmap (container runners) is the fix.
- `sandbox-exec` is deprecated by Apple. If it is removed, `auto` fails at start-up.

## What is verified

Seatbelt is exercised for real: `internal/sandbox` runs commands under it and checks that a file outside
the workspace cannot be read or written and that only the named loopback port connects, and
`internal/e2e` drives a webhook-to-PR dependency update with `go` and `git` confined. Both live in `_darwin_test.go` files, so they do not run on Linux CI.
Bwrap is covered only by argv unit tests; it has never run.

macOS notes: `/usr/bin/git` is an `xcrun` shim that writes a cache outside the workspace, so ripple runs the
Command Line Tools' own `git` under the sandbox. Temporary files go to the workspace (`TMPDIR`).
