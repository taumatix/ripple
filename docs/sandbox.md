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
`git`. Build the image ripple's own checks use from `docker/runner/Dockerfile` (`golang:<GO_VERSION>-alpine` plus
`git`, `GOTOOLCHAIN=local`; `GO_VERSION` is a build argument, default the Go ripple is built with):
`docker build -t ripple-runner -f docker/runner/Dockerfile docker/runner`. A bare `golang:1.27-alpine` has no `git`,
so local git commands would fail there; the Debian `golang:1.27` has both. Because the image's Go decides what
`GOTOOLCHAIN=local` can satisfy, build the runner with a Go at least as new as the highest `go` line of the repositories you
update. Publishing a prebuilt image is not done yet (ROADMAP).

**One image per Go minor.** With `RIPPLE_CONTAINER_GO_IMAGES=1.26=ripple-runner:1.26,1.27=ripple-runner:1.27` (`<minor>=<image>`
pairs), a go.mod that names a configured minor, by its `toolchain` line or else its `go` line (the newest across the modules
of a workspace), has its `go mod`, `build` and `vet` steps run in that image, so a directive bump above the default
image's Go is checked by the Go it asks for instead of opening a draft. Local git, and a go.mod naming a minor with no image,
use `RIPPLE_CONTAINER_IMAGE`, which is still required. The PR body names the image. `make runner-images MINORS="1.26 1.27"`
builds `ripple-runner:<minor>` from `docker/runner/Dockerfile` (`RUNTIME=podman` or `container` for the others).
Only an exact minor matches: a module on 1.25 does not borrow the 1.26 image. Only with a container, docker or podman
sandbox; the host sandboxes use `RIPPLE_GO_TOOLCHAIN_DIR`. A `GOTOOLCHAIN` download is still off.
`argv[0]` is run by its base name from the image's `PATH`, and the image's Go version must satisfy the
repositories' `go` lines because `GOTOOLCHAIN=local`. The `go` and `git` binaries on the host are not used.

| Setting | Default | Meaning |
|---|---|---|
| `RIPPLE_CONTAINER_IMAGE` | none, required | image to run |
| `RIPPLE_CONTAINER_CPUS` | `2` | CPUs per container |
| `RIPPLE_CONTAINER_MEMORY` | `2G` | memory per container |
| `RIPPLE_CONTAINER_PULL` | `missing` | `missing`, `always` or `never` (docker, podman); Apple's CLI has no pull option, so only `missing` |
| `RIPPLE_CONTAINER_NETWORK` | runtime default | network joined by commands that need the module proxy |
| `RIPPLE_CONTAINER_CONFINE` | `false` | `true` makes the server create the confined network below at start (docker, podman); excludes `RIPPLE_CONTAINER_NETWORK` |

What each container gets: a read-only root with a tmpfs `/tmp`; every capability dropped; the server's uid and
gid; only the job workspace (and a shared build cache outside it) mounted read-write, at the same paths so
`HOME`, `GOPATH` and `GOCACHE` hold; the policy's environment passed by key only (`-e KEY`, so values never appear
on a command line) without the host's `PATH`; a unique name and `--rm`. `HOME` and `TMPDIR` are the exception to key-only: they are the job directory, not secret,
and go as `-e HOME=<dir>` so that the container *client* can keep its own `HOME` (it is started with the server's
variables that locate the runtime and its state: `HOME`, `XDG_*`, `DOCKER_*`, `CONTAINER*`, `DBUS_SESSION_BUS_ADDRESS`,
not the rest of the server's environment). Otherwise rootless Podman would keep its image storage under the job directory,
where the repository's code can read it; the live CI job checks the client wrote nothing there. Docker and Podman also get
`--pids-limit 512`, `--security-opt no-new-privileges` and `--pull`. Nothing else of the host is visible: not the
home directory, the App key or other jobs.

**Network.** Commands that need none (local git) get `--network none`. `go` needs the module proxy and a
container cannot reach the host's loopback gate that seatbelt uses, so it gets the runtime's default network (or
`RIPPLE_CONTAINER_NETWORK`) and `GOPROXY` is the configured `RIPPLE_GOPROXY` itself, which must therefore be
reachable from inside the container (not `127.0.0.1`). **By default that network is not confined to the proxy**: a repository's
build can reach whatever the container network can (see Confining the network below). What is gained is that no secret is in reach (the App key and
installation token are never mounted or passed). To confine it, point `RIPPLE_CONTAINER_NETWORK` at an internal
runtime network that holds only a proxy container, and set `RIPPLE_GOPROXY` to it.

**Timeouts.** Killing the `container run` client does not stop the container (verified with Apple's CLI: it kept
running). When a command's context ends, ripple runs `<cli> kill <name>` and, for Docker and Podman, `<cli> rm -f
<name>`; Apple's `--rm` removes it after the kill. A server killed outright can leave containers behind, labelled
`ripple.job=1`: list them with `container ls -a` or `docker ps -a --filter label=ripple.job=1`.

**Confining the network.** `sandbox.ConfineNetwork(ctx, flavour, bin, ConfineOptions{Upstream: "https://proxy.golang.org"})`
(docker and podman; Apple's `container` has no internal networks) runs `network create --internal`, starts an
nginx sidecar (`nginx:1.27-alpine`) that forwards everything to the upstream, joins it to the internal network and waits
for it to answer. Pass the result's `Network` as `ContainerOptions.Network` and its `ProxyURL` (`http://<name>-proxy:3128`)
as the command's `GOPROXY` and `Policy.Proxy`; `Close` removes both. A command on that network reaches the sidecar and
nothing else: no other address, no DNS for other names.

`RIPPLE_CONTAINER_CONFINE=true` (with `RIPPLE_SANDBOX=docker` or `podman`) makes the server do this: at start it removes a
confinement an earlier server left behind (the network `ripple-confine-<replica>` and the container `ripple-confine-<replica>-proxy`; the
name follows `RIPPLE_REPLICA`, so servers with different replica names share one container runtime, and two with the same
name still replace each other; an unset replica uses `ripple-confine`), creates a fresh one forwarding to `RIPPLE_GOPROXY`, and every command
that needs the proxy joins that network with `GOPROXY` pointed at the sidecar, whatever `RIPPLE_GOPROXY` is for the
server itself, which keeps using it directly (the feed, version checks). It is removed on a normal stop (SIGINT, SIGTERM). A start
fails if the runtime cannot make the network. `RIPPLE_GOPROXY` must be `scheme://host[:port]` with no path or
credentials, and `RIPPLE_GOPROXY_TOKEN` is refused with it (go sends a credential only over https and the sidecar speaks
http; a credentialed private proxy is a ROADMAP entry). Job containers left by a server killed outright are removed by the next start (every container attached to that
network) before the network is recreated. `RIPPLE_CONTAINER_NETWORK` can still name a
network you created yourself.

**Verification status.** The `container-runtimes` CI job runs `internal/sandbox/container_live_shared_test.go` (build,
`go mod tidy`, a read-only-root write, writable mounts, a networkless fetch, a host file outside the mounts, a timeout
reap) against real Docker and real Podman on a hosted Linux runner, and the confined network (`go mod download` through the
sidecar to the real module proxy; `1.1.1.1`, `proxy.golang.org` and `example.com` unreachable from the container). The
Apple runner runs the same checks on a developer's Mac (`container_live_darwin_test.go`), which CI cannot. The same job builds `docker/runner/Dockerfile`, runs the local git steps (init, add, commit, checkout, clean) and `go version` in
it, and starts the server's wiring (`newSandbox` with `RIPPLE_CONTAINER_CONFINE`) to fetch a module through the sidecar,
replace a leftover confinement and clean up, and the kill-and-restart case with a job container left on the network plus two replicas sharing the runtime (`TestServerConfineSurvivesAKilledServerAndSharesTheRuntimeLive`, CI-only). Not verified anywhere: the server process itself running a whole job in a
container end to end with a real runtime (the engine's end-to-end test uses the fake CLI), Podman as a non-rootless install,
SELinux hosts, Docker Desktop, and a very long replica name (the confinement name is truncated and hashed to fit a 63-character DNS label; unit-tested only). The unit tests still use the fake CLI (`internal/testutil/fakecontainer.go`) and the
cross-flavour engine test (`internal/e2e/container_test.go`). Only `go` and local `git` run in a container; clone,
fetch, push and the server stay on the host.

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
- `bwrap` needs the `ripple` executable as its relay (below). If the executable's path cannot be resolved, or the
  policy cannot be met, `go` refuses to run rather than share the host network.
- `sandbox-exec` is deprecated by Apple. If it is removed, `auto` fails at start-up.

## bwrap: how `go` reaches the proxy

bwrap always starts the command in a fresh network namespace (`--unshare-all`): the only interface is `lo`. For a step
that needs the proxy, the server serves the gate (the same reverse proxy seatbelt uses, with its random path prefix)
additionally on a unix socket in a private `0700` temp directory. The command is started through ripple itself as
`ripple sandbox-relay <port> <socket> -- go ...`: the ripple executable and that one socket are the only host paths
bound in beyond the workspace and system trees. The relay listens on the namespace's `127.0.0.1:<port>` (the gate's
port, so `GOPROXY` is the same URL as under seatbelt), forwards each connection to the socket, runs `go`, and returns its
exit status. Nothing else is reachable: no other host loopback port, no external address (`Network is unreachable`).
`sandbox-relay` is an internal subcommand of the `ripple` binary, not a user command.

It fails closed: a policy that asks for the proxy gets `ErrNetUnsupported` (so `go` does not run) unless the backend
has an absolute relay path, there is a gate socket, and exactly one port was asked for.

## What is verified

Seatbelt is exercised for real: `internal/sandbox` runs commands under it and checks that a file outside
the workspace cannot be read or written and that only the named loopback port connects, and
`internal/e2e` drives a webhook-to-PR dependency update with `go` and `git` confined. Both live in `_darwin_test.go` files, so they do not run on Linux CI.

Bwrap runs for real in the `sandbox-bwrap` CI job (`.github/workflows/bwrap.yml`, on `ubuntu-24.04` and
`ubuntu-latest`), never on a developer's Mac: `internal/sandbox/bwrap_live_linux_test.go` (file confinement; no host loopback
port without a proxy; through the relay only the gate answers, another host port and `1.1.1.1` do not; the exit status
survives) and `internal/e2e/sandbox_bwrap_linux_test.go` (a webhook-to-PR update with `go mod tidy`, `build`, `vet` and
local `git` confined, `go.sum` refreshed through the gate). `RIPPLE_REQUIRE_BWRAP=1`, set in that job, makes a missing
bwrap a failure and the job fails on any skipped test. **Ubuntu 24.04 and AppArmor:** the job probes
`bwrap --unshare-all` before the tests and only lifts `kernel.apparmor_restrict_unprivileged_userns` if the probe fails;
see the job log for what each runner needed (on 2026-11-26 `ubuntu-latest` reported bubblewrap 0.11.1 with that sysctl at 1 and
needed no change). On your own Ubuntu 24.04+ host, check that `bwrap --unshare-all --ro-bind / / true` works for the
server's user before choosing `bwrap`. Not verified: other distributions, a bwrap older than 0.11, a server running with
a restricted `/tmp` (the gate socket directory comes from `os.MkdirTemp`), and cgo builds under bwrap.

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
