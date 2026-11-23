# Changelog

All notable changes are listed here. The format follows Keep a Changelog; versions follow SemVer.

## [Unreleased]

### Added
- Release workflow (`.github/workflows/release.yml`): a pushed `vX.Y.Z` tag builds a multi-arch (amd64, arm64)
  image for GHCR with the version tag and digest, attaches static binaries and `SHA256SUMS` to the GitHub
  release, then installs the published artifacts from scratch and runs `ripple version`. The Dockerfile takes a
  `VERSION` build arg so the image reports the tag. Pull requests touching the workflow or Dockerfile run it
  as a dry run that publishes nothing.
- `make test-services` starts Postgres and DynamoDB Local itself and runs the suite with
  `RIPPLE_REQUIRE_SERVICES=1`, which turns a skipped service test into a failure; CI sets it too.
- CI smoke test of the built image over HTTP against a fake GitHub (`internal/smoke`, `make test-smoke`).
- Bitbucket Cloud host (`RIPPLE_BITBUCKET_*`): workspace discovery, `repo:push` webhooks (changed files from the diffstat), pull requests, and auto-merge through merge queues only (otherwise reported unsupported). The platform conformance suite gains an optional `FilelessPushes` marker for hosts whose push payload lists no files. See [docs/platform-bitbucket.md](docs/platform-bitbucket.md).
- SCM-Manager host (`RIPPLE_SCMMANAGER_*`, Review plugin 3.11.0 or later): repository discovery by listing, webhook-plugin pushes authenticated by a shared-secret header (the plugin cannot sign), changed files from the modifications API, pull requests including drafts. No auto-merge: SCM-Manager has none. Verified only against a fake. See [docs/platform-scmmanager.md](docs/platform-scmmanager.md).

### Security
- OS-level sandbox for `go` and local `git` (`RIPPLE_SANDBOX`: `none` (default), `auto`, `seatbelt`, `bwrap`).
  Under `seatbelt` (macOS) the commands run with every path unreadable and unwritable except the
  checkout, the build cache and the toolchain, and the only network they can open is a loopback gate
  to `RIPPLE_GOPROXY` behind a random path prefix. `bwrap` (Linux) is implemented but unverified and cannot
  confine the network to a proxy, so `go` refuses to run under it. Clone, fetch and push are not
  confined. See [docs/sandbox.md](docs/sandbox.md).
- Webhook receiver: requests without a well-formed `X-Hub-Signature-256` are refused before the body is
  read, at most 4 bodies are buffered at once (others get 503 with `Retry-After`), and a declared length
  over the limit gets 413. A repeated `X-GitHub-Delivery` within an hour is acknowledged and dropped, so a
  replayed signed event is handled once; a delivery that failed with 503 can still be redelivered.
  `webhook.Handler` gains optional `MaxBody`, `MaxInflight`, `DedupeTTL` and `Now` fields; zero values keep safe defaults.

### Changed
- After GitHub answers a rate limit, the other repos of that installation are deferred until the time it gave
  instead of each spending a call to be refused.
- A repo that lists a Go dependency it never imports now has that requirement dropped by the `go mod tidy`
  that every update runs.
- The installation token is fetched from the token source for each network git command (clone and push), not once per job, so a job that outlives one token still pushes. The token reaches git only through that process's environment, never argv or the remote URL.
- Clones pass `--filter=blob:limit=1m`; with `--depth 1` and a checkout this saves little (measured in
  docs/bounds.md).
- Fan-out loads and parses a repo's config once per release instead of once per dependency row.

### Added
- AWS CodeCommit host (`RIPPLE_CODECOMMIT_REGIONS`, `RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS`): SigV4-signed API and
  git password, events through EventBridge and an SNS HTTPS subscription at `/webhook/codecommit`, repositories
  discovered by listing per region. Built from the docs and a fake only; unverified against real AWS. See
  [docs/platform-codecommit.md](docs/platform-codecommit.md).
- Bitbucket Server / Data Center host (`RIPPLE_BITBUCKET_SERVER_*`): REST under `/rest/api/1.0`, repo discovery by listing plus `repo:refs_changed` webhooks (HMAC `X-Hub-Signature`), default branch and changed files fetched per push, auto-merge. Written from vendored docs and a fake, not a real instance; see [docs/platform-bitbucketserver.md](docs/platform-bitbucketserver.md).
- Gitea and Forgejo host (`RIPPLE_GITEA_*`): one client, flavour detected from `/api/v1/version`, repo discovery by listing plus push webhooks, "merge when checks succeed". Written from vendored docs and a fake, not a real instance; see [docs/platform-gitea.md](docs/platform-gitea.md).
- GitLab (gitlab.com and self-managed) as a host, enabled by `RIPPLE_GITLAB_TOKEN` and/or `RIPPLE_GITLAB_TOKENS`
  (per-group and per-project access tokens, longest scope wins, each with its own rate limit). Merge requests
  (drafts as `Draft:`, labels), merge when the pipeline succeeds, push webhooks authenticated by secret token or
  signing token at `/webhook/gitlab`, projects found by membership listing, subgroups of any depth. Written from
  the vendored docs and **not yet run against a real GitLab**: see [docs/platform-gitlab.md](docs/platform-gitlab.md).
- Azure DevOps Services host (`RIPPLE_AZUREDEVOPS_*`): repos as `org/project/repo`, found by listing, pushes
  from `git.push` service hooks (HTTP Basic, no HMAC exists), PATs, auto-complete. Written from the vendored
  docs and a fake, not yet run against a real organisation. See [docs/platform-azuredevops.md](docs/platform-azuredevops.md).
- A major bump now also rewrites `//go:generate go run <package>` lines and canonical `// import "..."`
  comments, and lists directives that still mention the old path in the PR body. `manager.RewriteGo` returns
  that report; `RewriteGoImports` keeps its signature.
- Platform seam for hosts other than GitHub: per-host credentials, repo discovery by webhook or by periodic
  listing, optional auto-merge and a per-host rate-limit signal, with one conformance suite
  (`internal/platform/platformtest`) every host must pass and a guide in
  [docs/platform-authoring.md](docs/platform-authoring.md). GitHub behaves as before; its webhook stays at
  `/webhook` and is also served at `/webhook/github`. `.github/ripple.yml` is read on GitHub only.
- Every Go update, not only majors, runs `go mod tidy`, build and vet in the modules it edits, so minor and
  patch PRs carry a correct `go.sum`; a failure opens a draft with the output.
- Multi-module repos: other modules that require the updated dependency or an edited module are tidied and
  verified; a `vendor/` directory is refreshed with `go mod vendor`; a `go.work` has its replaces follow a
  major bump and is checked with `go work sync`, build and vet. `go.work` entries that point outside the
  repository are refused like an escaping `replace`.
- Webhook events are persisted in the store (all three backends; Postgres migration `002_events.sql`) before
  the receiver answers 202, and a restart resumes them. Installations share `RIPPLE_EVENT_WORKERS` (default 4)
  fairly, each capped at `RIPPLE_EVENT_WORKERS_PER_INSTALLATION` (default 2), and one repo's events run in
  order; the backlog is capped by `RIPPLE_EVENT_BACKLOG` (10000) and `RIPPLE_EVENT_INSTALLATION_BACKLOG` (1000),
  past which the receiver answers 503. Due jobs run on their own loop, so a slow major-bump verify no longer
  delays the feed poll.
- `RIPPLE_MAX_FILES` (default 100000) and `RIPPLE_MAX_DEPENDENCIES` (default 5000) cap one repo scan; a repo
  over a cap is skipped with a logged warning and no updates until it is under it
  ([docs/bounds.md](docs/bounds.md)).

## [0.2.0] - 2026-11-22

### Added
- `.ripple.yaml` (repository root, the same on every host) is the per-repo configuration file;
  `ripple config validate` checks it by default.

### Deprecated
- `.github/ripple.yml`, the path v0.1.0 read, is a deprecated alias for `.ripple.yaml` with identical
  meaning. When both exist `.ripple.yaml` wins, the alias is ignored, and a warning is logged and shown
  by `ripple config validate`. Removal no earlier than v0.3.0.

### Documentation
- ROADMAP entries for every Renovate code host, container job runners with updaters as OCI images,
  a prebuilt image and binaries, and hermetic integration tests.

## [0.1.0] - 2026-11-22

### Added
- `ripple version` prints the module version the binary was built from.
- Event-driven updater: polls the Go module index once, finds importing repositories through a
  reverse index, and opens pull requests only there.
- `.github/ripple.yml` per-repo configuration with strict validation; `ripple config validate`.
- Managers: `gomod` and a generic `regex` manager.
- Stores: Postgres and DynamoDB (single table, documented GSI), plus an in-memory store.
- minimumReleaseAge, grouping, per-rule automerge, schedule windows and an open-PR limit.
- GitHub App webhook receiver and `ripple serve`; Dockerfile.
- Go major-version updates: the reverse index keys on the module line, so `/vN` to `/vN+1`, an
  unsuffixed path to `/v2` and `gopkg.in/x.v2` to `.v3` are found; `go.mod` and all import paths are
  rewritten with `go/parser`, verified with `go mod tidy`, `go build` and `go vet`, opened as a
  labelled `[major]` PR (a draft with the compiler output if verification fails), never auto-merged
  unless the repo file says `automerge: major`. New server setting `RIPPLE_GOPROXY`.

### Security
- `go` is no longer run in a checkout containing a symlinked `go.mod`, `go.sum`, `go.work` or `.go`
  file, or a `replace` to an absolute or out-of-repository path; the job fails permanently, no PR.

### Fixed
- The regex manager replaced only some matches when several patterns matched out of offset order;
  matches are now sorted, and overlapping ones are rejected.
- A major bump no longer fails because one `.go` file does not parse (it is listed in the PR body),
  and no longer rewrites `testdata/` or directories starting with `_` or `.`.
- Corrected the `webhook.Sink` comment: GitHub does not redeliver a failed webhook by itself.

### Documentation
- README section "Known limitations and security model"; ROADMAP entries for an OS sandbox,
  durable per-installation queues, scan bounds, webhook body bounds, per-command tokens and delivery dedupe.

[Unreleased]: https://github.com/taumatix/ripple/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/taumatix/ripple/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/taumatix/ripple/releases/tag/v0.1.0
