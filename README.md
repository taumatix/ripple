# ripple

An event-driven dependency updater for GitHub. ripple watches the Go module feed once, looks up
which repositories import the package that just released, and opens a pull request in those
repositories only.

**Status: v0.2.0.** Go modules only (plus a generic regex manager). See [ROADMAP.md](ROADMAP.md).

## Why it is different

Renovate and Dependabot walk every repository on a schedule and ask the registry about every
dependency. ripple inverts that:

- The feed (index.golang.org) is read **once**, however many repositories you have.
- A **reverse index** maps package to importing repos. A release for a package nobody imports costs
  nothing; a release for one importer costs one clone, not a scan of the org.
- Optionally (`RIPPLE_OSV=true`) security advisories from [OSV](https://osv.dev) become PRs for the fixed
  version that skip the cooldown: [docs/osv.md](docs/osv.md).
- Result: far fewer API calls and rate-limit pressure, and a PR as soon as the cooldown allows,
  not at the next scheduled run.

**Simplicity is a design goal.** Renovate's power comes from layered presets, `packageRules` and
precedence. ripple deliberately has none of that in v0.1: one small file (`.ripple.yaml`) per repo, no inherited
defaults, no rule ordering. A file means what it says and nothing else. Presets and org defaults are
on the roadmap as opt-in extras that can never change a plain file's meaning.

## Install

```sh
go install github.com/taumatix/ripple/cmd/ripple@v0.2.0   # needs git on PATH
ripple version                                              # prints: ripple v0.2.0
```

Or build the container from a checkout: `git clone --branch v0.2.0 https://github.com/taumatix/ripple && docker build -t ripple ripple`.
Each release also publishes static binaries (linux, macOS and Windows) on its GitHub release page and a
multi-arch image, `ghcr.io/taumatix/ripple:<version>`. `go install` is the open install path: the image's GHCR
package stays private until a maintainer makes it public once in the GitHub UI (there is no API for that), so
`docker pull` needs a login until then. The binary also needs the `go` toolchain on `PATH` at runtime to verify
major-version updates; the Dockerfile includes it.

## Run

1. Create a GitHub App with permissions Contents: read and write, Pull requests: read and write,
   Metadata: read; subscribe to `installation`, `installation_repositories` and `push`; set a
   webhook secret and point it at `https://<host>/webhook`.
2. Configure the service through the environment and start it:

```sh
export RIPPLE_WEBHOOK_SECRET=... RIPPLE_GITHUB_APP_ID=... RIPPLE_GITHUB_KEY_FILE=/run/secrets/app.pem
export RIPPLE_STORE=postgres RIPPLE_POSTGRES_DSN=postgres://...
ripple serve
```

`ripple config validate --server` lists every variable. Service-wide defaults (feed URL, open-PR
limit, minimum release age, whether automerge is allowed) live there, not in repo files.

## Configuration

A repo needs no file: without one ripple updates its Go modules with the server defaults. To change
that, add `.ripple.yaml` at the repository root. This is the whole schema:

```yaml
version: 1
open-pull-requests-limit: 3        # at most this many ripple PRs open at once
close-superseded-pull-requests: true  # close ripple's own untouched PR when a newer version gets one (default: true)
backfill-new-dependencies: true    # bring a dependency the repo starts using to the newest release already known (default: true)
updates:
  - ecosystem: gomod               # gomod, or regex (see README)
    directory: /                   # this directory and everything below it
    go-toolchain: toolchain        # optional, off by default: toolchain | directive (docs/go-toolchain.md)
    cooldown: 7d                   # wait this long after a release (never less than the server minimum)
    schedule:
      days: [mon, tue]
      from: "22:00"
      to: "05:00"
      timezone: Europe/Paris
    ignore:
      - dependency: github.com/legacy/*
      - dependency: github.com/foo/bar
        update-types: [major]
    groups:
      aws:
        patterns: ["github.com/aws/*"]
    automerge: patch               # patch, minor or major (default: never); needs auto-merge on the repo
```

- `automerge: major` is the only way a major update is ever merged automatically; `patch` and
  `minor` never include one.
- `close-superseded-pull-requests` (default `true`): when a newer release of the same module gets its own
  ripple PR, ripple closes its older PR for that module with a comment pointing at the new one. It never
  closes a PR for another module, for an equal or newer version, a group PR, or one whose replacement is a
  draft. It never closes a PR a person touched: a commit by anyone but ripple, a review, a review comment, a
  comment, or a rename. Each of those is checked on every close, and a check that fails closes nothing. It
  does not delete the branch (turn on "automatically delete head branches" in the repository settings), and
  it does not notice an edited description. Only the GitHub host can close pull requests so far; on the
  others both PRs stay open. Set it to `false` to keep every PR.
- `backfill-new-dependencies` (default `true`): a repo that starts importing a module only hears about
  releases published afterwards, so when a scan finds a dependency the repo did not have, ripple compares it
  with the newest release of that exact module path already stored from the feed and opens a normal job when
  the repo is behind. The job obeys the same cooldown, schedule, ignore rules, PR limit and verification as any
  other. It never asks a feed: a release ripple has not stored is not found. At most 10 jobs are created per
  scan (a repo registering 5000 dependencies gets 10, the rest wait for their next release), and a rescan
  with no new dependency creates none. Set it to `false` to turn it off.
- `cooldown` can only **raise** the server's minimum release age; `open-pull-requests-limit` can
  only **lower** the server's limit; `automerge` works only if the server allows it.
- Unknown keys are errors that name the key and line. `ripple config validate [FILE]` checks a file
  (by default `.ripple.yaml` in the current directory) and prints a corrected example. An invalid file means no updates for that repo until it is fixed.
- **File location.** `.ripple.yaml` at the repository root is the file, and it is the same on every
  host. v0.1.0 read `.github/ripple.yml`; that path still works as a **deprecated** alias with the
  same meaning and ripple logs a deprecation warning on each scan of such a repo. If both files
  exist, `.ripple.yaml` wins, the alias is ignored (not merged), and ripple warns that both exist;
  `ripple config validate` prints the same warning. The alias is removed no earlier than the minor
  release after next, so not before v0.3.0. To migrate: `git mv .github/ripple.yml .ripple.yaml`.
- A `regex` update takes `files` (globs) and `patterns` (RE2 with `(?P<name>...)` and
  `(?P<version>...)` groups, or a fixed `package`).

## Major version bumps

Go puts the major version in the module path (`/v2` and up; `gopkg.in` paths always carry `.vN`; the
Go modules reference documents both, <https://go.dev/ref/mod#major-version-suffixes>). ripple's reverse
index therefore keys on the *line*: the path without its major suffix, so `example.com/m/v2` and
`example.com/m/v3` are one entry and a release of v3 finds the repos still on v2. The same holds for
`example.com/m` to `example.com/m/v2` and `gopkg.in/yaml.v2` to `gopkg.in/yaml.v3`.

For a major update the gomod manager rewrites `go.mod` (require; an unversioned `replace` follows the
new path; a `replace` or `exclude` pinned to an old-major version is dropped) and every import path
in the module's Go files. Imports are located with `go/parser`, so aliased, `.` and `_` imports,
test files, build-tagged files and cgo files are covered, while comments and string constants are not
touched. Then ripple runs `go mod tidy`, `go build ./...` and `go vet ./...` in the branch, with the
environment stripped, cgo and toolchain downloads off and modules fetched only from `RIPPLE_GOPROXY`.
The PR is titled `[major] ...`, labelled `major` and `breaking`, and links the release notes. If the
build fails, the PR is still opened, as a **draft** carrying the compiler output. Majors are never
auto-merged unless the repo file says `automerge: major`, and a draft never is. The server needs the
`go` command installed (the Docker image has it); without it every major PR is a draft saying so.
Set `RIPPLE_SANDBOX=auto` to confine those commands at the OS level, or `container`, `docker` or `podman` (with
`RIPPLE_CONTAINER_IMAGE`) to run them in a container; what that covers and what it does not is in
[docs/sandbox.md](docs/sandbox.md).

Every Go update, minor and patch included, runs `go mod tidy`, `go build ./...` and `go vet ./...` in each
module it edits, so the PR carries a correct `go.sum`; a failure opens a draft with the output. In repos with
several modules, any other module that requires the dependency (even indirectly) or an edited module is
refreshed and verified too. A module with a `vendor/modules.txt` gets `go mod vendor`. A `go.work` has its
`replace` directives follow a major bump, and the workspace is checked with `go work sync` and a build and
vet of each used module. Without the `go` command, a minor or patch PR is opened as before and says it
was not verified.

A major bump also rewrites the package of a plain `//go:generate go run <package>` and canonical
`package p // import "..."` comments (both found with go/parser, never by pattern). Directives that
mention the old path in any other shape (`@version`, quoting, a variable, another command, a program
argument) are left alone and listed in the PR body.

**cgo and private modules.** Verification builds with cgo off by default, so a module with cgo files that has
a pure-Go fallback builds green without compiling its C; the PR then says the C code was not compiled. Set
`cgo: true` on a `gomod` entry to build with a C compiler (`RIPPLE_GO_CC`, default `cc`); with no compiler the
PR says it could not verify (a draft for a major bump), never a silent pass. Under a container backend the image
brings the compiler. `private-modules:` lists module patterns (`example.com/team/*`, `*.corp.example.com`)
kept out of the checksum database. A proxy that needs credentials takes `RIPPLE_GOPROXY_USER` and
`RIPPLE_GOPROXY_TOKEN` (https only, with `RIPPLE_GOPROXY` set to that proxy); only `go mod tidy`, `vendor` and `work sync` get them, never `build` or
`vet`, and they stay out of go's environment, argv, logs and PRs. Details in [docs/sandbox.md](docs/sandbox.md).

Not covered yet: `go work vendor` for a workspace-level `vendor/` and the remaining directive shapes
(see [ROADMAP.md](ROADMAP.md)).

## Architecture

```
GitHub App webhook ──► app ──► engine.Scan ──► managers ──► Store (repo -> deps)
index.golang.org   ──► Watcher ──► reverse index lookup ──► jobs (one per repo|pkg|version)
                                                     └──► workers ──► Platform: clone, edit, push, open PR
```

Interfaces from day one: `Platform` (GitHub, [GitLab](docs/platform-gitlab.md), [Azure DevOps](docs/platform-azuredevops.md); Gitea and Forgejo, [docs/platform-gitea.md](docs/platform-gitea.md); Bitbucket Cloud, [docs/platform-bitbucket.md](docs/platform-bitbucket.md); [Gerrit](docs/platform-gerrit.md); Bitbucket Server / Data Center, [docs/platform-bitbucketserver.md](docs/platform-bitbucketserver.md); AWS CodeCommit, [docs/platform-codecommit.md](docs/platform-codecommit.md); SCM-Manager, [docs/platform-scmmanager.md](docs/platform-scmmanager.md); adding a host: [docs/platform-authoring.md](docs/platform-authoring.md)), `Feed` (Go modules), `Manager` (gomod, regex),
`Store` (Postgres, DynamoDB, in-memory). The DynamoDB access patterns are in
[docs/store-dynamodb.md](docs/store-dynamodb.md).

## Known limits

Releases are stored only for packages that have dependents.

## Known limitations and security model

ripple runs `git` and `go` on repositories it does not trust. **There is no OS-level sandbox.**
What exists is: a scrubbed environment (no inherited secrets, `GOENV=off`, `GOTOOLCHAIN=local`,
`GOWORK=off`, `GOVCS=*:off`, an allowlist of five `go` commands (`go work sync` only for a workspace inside the checkout)), the installation token fetched fresh for each network git command and kept out of
the build, and, before any `go` process starts, a refusal to run in a checkout where a `go.mod`,
`go.sum`, `go.work` or `.go` file is a symbolic link, or where a `replace` directive points at an
absolute path or outside the repository. Such a job fails permanently with a clear reason and opens no
pull request. These checks stop the known write and read escapes; they do not contain a build that runs hostile code (a malicious dependency or a toolchain bug is
outside what they can promise).

Operators should therefore run ripple as an unprivileged user in a container where only the
workspace directory is writable, the root filesystem is read-only, and egress is restricted to
GitHub, the module index and the Go proxy. Do not mount credentials other than the App key's
secret store, and do not run it on a host with access to a private network.

Webhook events are stored before the receiver answers 202 and deleted once handled, so a restart resumes
the queue. Installations take turns for `RIPPLE_EVENT_WORKERS` (default 4) workers, each using at most
`RIPPLE_EVENT_WORKERS_PER_INSTALLATION` (default 2), and one repo's events run in order, one at a time. The
stored backlog is capped at `RIPPLE_EVENT_BACKLOG` (default 10000) events overall and
`RIPPLE_EVENT_INSTALLATION_BACKLOG` (default 1000) per installation, one event per repo named in a delivery;
past a cap the receiver answers 503 and stores nothing, and GitHub does not redeliver by itself, so that
delivery is lost unless redelivered from the App's Recent Deliveries page. A failing event is retried
3 times with backoff, then logged (`event failed; dead-lettered`) and kept dead, with its last error, attempt
count and times, for `RIPPLE_EVENT_DEAD_RETENTION` (default 30d), after which it is purged. Dead events are
not work, so they do not count against the backlog caps. `ripple events list` shows them, `ripple events
requeue ID` (or `--all`) makes one live again with a fresh 3 attempts, and `ripple events purge [AGE]`
deletes them early; the commands read the store named by the same `RIPPLE_STORE` variables, so there is no
HTTP endpoint, and a running server picks a requeued event up within 30 seconds. The webhook receiver refuses unsigned requests before reading the body,
buffers at most 4 bodies at once, and drops a repeated `X-GitHub-Delivery` for an hour (in memory, per
process; the id is an unsigned header, so this stops redelivery and naive replays, not a rewritten header). See
[ROADMAP.md](ROADMAP.md) for the planned fixes.

## Upstream contracts

How config and API changes are deprecated and removed, and what is deprecated now: [MIGRATION.md](MIGRATION.md).

The feed format and GitHub API version ripple was built against, with check dates: [UPSTREAM.md](UPSTREAM.md).

## Development

`make test` runs unit tests; the Postgres and DynamoDB Local tests skip without a database.
`make test-services` starts both itself (Docker, or Apple `container` where Docker has no daemon), runs
the whole suite with `RIPPLE_REQUIRE_SERVICES=1` so a skipped service test fails, and removes them. CI always
runs them that way. `make test-smoke` builds the image and drives it over HTTP against a fake GitHub (a signed
push event and a new release must end in exactly one pull request); CI runs it on every pull request.

## License

Apache-2.0.
