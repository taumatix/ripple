# OS sandbox for `go` and `git`

A repository ripple scans is untrusted, and `go mod tidy`, `go build` and `go vet` run against it as the
server's uid. `RIPPLE_SANDBOX` confines those commands. The default is `none`, so existing installs do not change.

| Value | Backend | Platform |
|---|---|---|
| `none` | no confinement | any |
| `auto` | seatbelt on macOS, bwrap on Linux; fails if the binary is missing, never falls back to `none` | macOS, Linux |
| `seatbelt` | `sandbox-exec` | macOS |
| `bwrap` | bubblewrap | Linux |
| `container` | Apple `container` (an OCI image in a lightweight VM) | macOS 26, Apple silicon |
| `docker`, `podman` | the Docker or Podman CLI | Linux, macOS |

## Container runners (`container`, `docker`, `podman`)

Each confined command runs in a fresh container of `RIPPLE_CONTAINER_IMAGE`, which must carry `go` and
`git` (for example `golang:1.27`; the `-alpine` variants have no `git`, so local git commands would fail).
`argv[0]` is run by its base name from the image's `PATH`, and the image's Go version must satisfy the
repositories' `go` lines because `GOTOOLCHAIN=local`. The `go` and `git` binaries on the host are not used.

| Setting | Default | Meaning |
|---|---|---|
| `RIPPLE_CONTAINER_IMAGE` | none, required | image to run |
| `RIPPLE_CONTAINER_CPUS` | `2` | CPUs per container |
| `RIPPLE_CONTAINER_MEMORY` | `2G` | memory per container |
| `RIPPLE_CONTAINER_PULL` | `missing` | `missing`, `always` or `never` (docker, podman); Apple's CLI has no pull option, so only `missing` |
| `RIPPLE_CONTAINER_NETWORK` | runtime default | network joined by commands that need the module proxy |

What each container gets: a read-only root with a tmpfs `/tmp`; every capability dropped; the server's uid and
gid; only the job workspace (and a shared build cache outside it) mounted read-write, at the same paths so
`HOME`, `GOPATH` and `GOCACHE` hold; the policy's environment passed by key only (`-e KEY`, so values never appear
on a command line) without the host's `PATH`; a unique name and `--rm`. Docker and Podman also get
`--pids-limit 512`, `--security-opt no-new-privileges` and `--pull`. Nothing else of the host is visible: not the
home directory, the App key or other jobs.

**Network.** Commands that need none (local git) get `--network none`. `go` needs the module proxy and a
container cannot reach the host's loopback gate that seatbelt uses, so it gets the runtime's default network (or
`RIPPLE_CONTAINER_NETWORK`) and `GOPROXY` is the configured `RIPPLE_GOPROXY` itself, which must therefore be
reachable from inside the container (not `127.0.0.1`). **That network is not confined to the proxy**: a repository's
build can reach whatever the container network can. What is gained is that no secret is in reach (the App key and
installation token are never mounted or passed). To confine it, point `RIPPLE_CONTAINER_NETWORK` at an internal
runtime network (Docker `network create --internal`) that holds only a proxy container, and set `RIPPLE_GOPROXY` to it.

**Timeouts.** Killing the `container run` client does not stop the container (verified with Apple's CLI: it kept
running). When a command's context ends, ripple runs `<cli> kill <name>` and, for Docker and Podman, `<cli> rm -f
<name>`; Apple's `--rm` removes it after the kill. A server killed outright can leave containers behind, labelled
`ripple.job=1`: list them with `container ls -a` or `docker ps -a --filter label=ripple.job=1`.

**Verification status.** The Apple `container` argv was written from `container run --help` (version 1.4.1) and
run for real: `internal/sandbox/container_live_darwin_test.go` runs `go build`, `go mod tidy`, a read-only-root write, a
networkless fetch, a host file outside the mounts, and a timeout reap. It skips when the CLI is absent and never runs
in CI (GitHub's hosted runners have no Apple container runtime). **Docker and Podman are unverified against a real
runtime**: their options come from the documented `docker run` reference and are tested only against a fake CLI
(`internal/testutil/fakecontainer.go`) that records its argv and then runs the command on the host; the
cross-flavour engine test (`internal/e2e/container_test.go`) uses it for all three flavours. The `go` command with a real
proxy through a real container, and `git` inside an image, have not been run under Apple's CLI either (the live image
has `go` only). Only `go` and local `git` run in a container; clone, fetch, push and the server stay on the host.

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
  deployments should use `docker` or `podman` (above) instead, or wait for entry 1 of the roadmap.
- `sandbox-exec` is deprecated by Apple. If it is removed, `auto` fails at start-up.

## What is verified

Seatbelt is exercised for real: `internal/sandbox` runs commands under it and checks that a file outside
the workspace cannot be read or written and that only the named loopback port connects, and
`internal/e2e` drives a webhook-to-PR dependency update with `go` and `git` confined. Both live in `_darwin_test.go` files, so they do not run on Linux CI.
Bwrap is covered only by argv unit tests; it has never run.

macOS notes: `/usr/bin/git` is an `xcrun` shim that writes a cache outside the workspace, so ripple runs the
Command Line Tools' own `git` under the sandbox. Temporary files go to the workspace (`TMPDIR`).

## cgo and private modules

Both are opt-in; with neither set nothing above changes.

- **cgo.** `cgo: true` on a `gomod` entry sets `CGO_ENABLED=1` for `go build` and `go vet` (the compiler is
  `RIPPLE_GO_CC`, else `cc`). The host's compiler is looked up first; when it is missing the step returns
  `ErrNoCompiler`, which the engine treats like a missing toolchain: a draft for a major bump, a "could not
  verify" line in the PR body otherwise. Under a container backend the image must carry the compiler (the host
  cannot know), so a missing one is an ordinary build failure there. A compiler runs code from the checkout, so
  these steps are confined by the same backend as everything else, and carry no credential. Without `cgo: true`,
  a module containing `import "C"` gets a line in the PR saying its C was not compiled.
- **Private modules.** `private-modules:` becomes `GONOSUMDB` (the checksum database is neither asked about
  nor told these names). `GONOPROXY` is deliberately not set: `GOPRIVATE` would imply a direct VCS fetch, which
  `GOVCS=*:off` forbids, so private modules must be served by `RIPPLE_GOPROXY` (an Athens, Artifactory or similar).
- **Credentials.** `RIPPLE_GOPROXY_USER` and `RIPPLE_GOPROXY_TOKEN` go only to `mod tidy`, `mod vendor` and
  `work sync`, which execute no repository code. With no sandbox or a seatbelt/bwrap one, the gate holds them and
  adds basic auth to each upstream request, so go's environment has no secret and the gate refuses plain http to
  a non-loopback host. With a container backend, go needs them itself: they go in `GOPROXY` userinfo through the
  client's environment (`-e GOPROXY`, never argv), and ripple refuses a non-https proxy because go would. Output
  is scrubbed of the token and its base64 forms. `build` and `vet` make unauthenticated requests, so a proxy that
  answers 401 to `.info` is tolerated by go but one that makes it fatal would fail verification.
