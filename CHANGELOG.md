# Changelog

All notable changes are listed here. The format follows Keep a Changelog; versions follow SemVer.

## [Unreleased]

### Added
- DynamoDB residuals: open-job copies are backfilled from the pending index on every start and reconciled on read (a stale or orphaned copy no longer shows on a dashboard); `Dynamo.RecountDependents` recounts a package's adaptive counter from the index, raises its shard count and moves dependents to their hashed shard, and the first change to a package with no counter item does the same; `RIPPLE_DYNAMODB_COUNTER_STRIPES` (default 1) spreads the counter over several items (`WithCounterStripes`); `dynamofake` gains BatchGetItem, Scan, `UPDATED_OLD`, a reserved-word check, a held-index mode and throttling hooks, and CI diffs the fake's results against DynamoDB Local's (`scripts/check-dynamo-parity.sh`). Additive, no migration. Verified on the fake only; the DynamoDB Local runs are CI-only and nothing was run against real AWS (throttling, index lag and counter striping under load are unmeasured). See [docs/store-dynamodb.md](docs/store-dynamodb.md).
- Documented that `tidy: minimal` stays opt-in, with the reasoning (README). No default changed. Workspace graphs whose sibling is at a version no proxy serves still open a draft: reproduced against the fake proxy and recorded in ROADMAP, not fixable offline.
- `directives: extended` also rewrites the import paths on a `mockgen` command line (`mockgen ...` or `go run <module>/mockgen ...`): the package of package and archive mode and the `bar/baz` of each `foo=bar/baz` element of `-imports`, from the flags its README documents (vendored in `docs/vendor/mockgen`). Any other flag stops the reading and the line is reported as before.
- `RIPPLE_GO_TOOLCHAIN_DIR`: verification runs the Go release a go.mod names (toolchain line, else the newest installed patch of the go line's minor) from a directory of releases the operator installed, instead of only the server's Go, so a `go` directive bump above the server's Go is checked rather than drafted. Nothing is downloaded; the PR body says which release ran. See [docs/go-toolchain.md](docs/go-toolchain.md).
- A `go-toolchain` bump PR also moves the Go version in `.mise.toml` (`go` under `[tools]`), a devcontainer `image` (`devcontainers/go:` or `golang:`), `go.work` (`go` and `toolchain` lines) and a `GO_VERSION` / `GOLANG_VERSION` assignment in a Makefile, with the same exact-line, same-minor, older-than-target rules as the other files. Verified end to end against the fake GitHub on every store.
- GitHub Enterprise Server: the release is read once at start from `GET /api/v3/meta` and logged; a release below ripple's own floor of 3.9 earns a warning. A failed read never blocks start. Each other host's docs page now says whether it reports an archive event (none verified to). `Event.Ref` stays populated during its deprecation window. Verified against the fake GHES only.
- The repository id is stored on the repo record (`model.Repo.RepoID`; Postgres migration `006_repo_id.sql`, DynamoDB attribute `repo_id`, additive, old rows read 0 and adopt it from the next signed push). A push under a new name is tied by id to the record held under the old one and renames it (jobs and index move) instead of registering a second repo; a push with another id than the stored one is ignored with a warning. A removal now leaves a 24-hour tombstone: a late signed push for a removed repo no longer registers it again, unless it carries a different repository id (a new repository that took the name); an explicit re-add clears it. Verified end to end against the fake GitHub on the memory store and the in-process DynamoDB; Postgres only in CI.
- GitHub Actions manager (`ecosystem: github-actions`, opt-in): bumps `uses: owner/repo@tag` and SHA pins with their `# vX.Y.Z` comment (annotated tags dereferenced) in workflows and composite actions, on the same tag shape, linking the release notes; `platform.TagSource` is an optional additive capability implemented for GitHub. Without the App's `workflows` permission no PR is opened and the job is deferred with a reason. Verified against the fake GitHub only. See [docs/github-actions.md](docs/github-actions.md).
- Replicas, residuals closed: the job row is fenced on the repo lease (`Store.UpdateJobFenced`: a transaction on DynamoDB, one statement on Postgres, so a replica that stalled past its lease cannot overwrite the new holder's job state); pause, resume, remove, rename and the dashboard flush take the repo lease like a scan does; a claimed event waits while another replica's older event of the same repo is still queued (`Store.HasOlderEvent`); `RIPPLE_REPLICA` and `RIPPLE_LEASE_TTL` name the replica and set the lease length, with a `replica` start-up log and a `lease taken over` log. Additive (no migration); `dynamofake` gains `TransactWriteItems`. One replica behaves as before. Verified by the shared store suite (fenced update, older events, an 8-way contention race) and a two-replica e2e on the memory store and the in-process DynamoDB; Postgres and DynamoDB Local only in CI; not against real contention or clock skew. See [docs/replicas.md](docs/replicas.md).
- OSV sweep after a gap: when the advisory poll hits its line cap, ripple re-checks every repo's dependencies against OSV in bounded batches (`Store` stays compatible: the optional `RepoLister` is implemented by memory and Postgres; DynamoDB repos are not swept). Verified against the fake GitHub and fake OSV; Postgres only in CI. See [docs/osv.md](docs/osv.md).
- Corrected advisories: when OSV amends a record so it no longer affects the version in use (or withdraws it), ripple skips the job that waited for it, closes the untouched PR it opened for it with a comment, and drops the dashboard entry; a PR a person touched is left open. Only for jobs made from this release on (they are indexed per advisory). Verified against the fake GitHub and fake OSV only. See [docs/osv.md](docs/osv.md).
- Advisory conflicts and pseudo-versions: a security `toolchain` job and a `go` directive job that edit the toolchain line of the same go.mod no longer open two conflicting PRs; the security PR goes first (higher fixed version first) and the other job waits, naming it, while it is open. A PR already open is never closed for this. New opt-in `security-from-pseudo-versions: true` (gomod) moves a dependency required at a pseudo-version to the release that fixes its advisory, never automerged; without it the advisory is listed on the dashboard. Verified against the fake GitHub and fake OSV only. See [docs/osv.md](docs/osv.md).
- Superseded pull requests, on more hosts and more reliably: GitLab, Gitea/Forgejo, Bitbucket Cloud, Bitbucket Data Center, Gerrit (abandon) and CodeCommit close ripple's untouched stale PR with a comment (`platform.PRCloser`, each with its own "touched by a person" read); Azure DevOps and SCM-Manager have no safe way to and keep both open (documented). A stale PR no longer holds the open-PR limit's slot its replacement needs; a close that failed is retried on the next run of the repo, also for a PR already open; ripple only closes a PR its own job record opened; GitHub's check now reads description edits and force-pushes. The close now comes before the comment. `close-superseded-pull-requests: false` applies to every host. Verified only against fakes; no real host.
- Run more than one replica: events and per-repo job runs are claimed with expiring leases and a fencing token, so replicas on one store share the queue, a crashed replica's work is retaken when its lease lapses (default 30s), and a job that pushed its branch before dying opens exactly one PR on takeover. Additive: `Store` gains `Leases` and `GetEvent` (Postgres migration `005_leases.sql`, DynamoDB `LEASE#` items); one replica behaves as before. Event order is kept per replica only. Verified by a two-replica e2e on the memory store and the in-process DynamoDB; Postgres only in CI; not against real contention. See [docs/replicas.md](docs/replicas.md).
- A scan no longer checks out the repo: it clones with `--filter=blob:none --no-checkout`, lists paths from the tree and fetches only the files managers match in one batched request (about 98% fewer bytes and disk on the repos measured; time is not lower), falling back to the usual full checkout when the host cannot (logged `partial scan unavailable`). Updates are unchanged. See [docs/bounds.md](docs/bounds.md).
- A `go-toolchain` bump PR also moves the Go version in `.go-version`, the `golang` line of `.tool-versions`, `go-version:` in root workflows and `FROM golang:` in Dockerfiles, on exact line shapes only (matrix expressions, aliases, digests and lists are left alone); the PR body lists the files. Only repos that opted in with `go-toolchain:`. See [docs/go-toolchain.md](docs/go-toolchain.md).
- DynamoDB: adaptive dependent sharding (`RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS`, off by default): a package's dependents move to more index partitions as they grow (doubling every N per shard), so only a hot package pays for fan-out; the table records that it was ever on, so turning it off strands nothing. Shard queries now run concurrently up to `RIPPLE_DYNAMODB_QUERY_CONCURRENCY` (default 4; 1 is sequential) with the same merged order. `internal/store/dynamofake` is an in-process DynamoDB over the real wire protocol, so the shared store suite runs in every local `go test`; CI still runs DynamoDB Local. See [docs/store-dynamodb.md](docs/store-dynamodb.md).
- Repository state from signed pushes and a pausing archive: a push's `default_branch` that differs from the stored one triggers one re-scan with the branch taken from the host, never from the payload (a contradicted claim is not examined again for an hour); a push for an unknown repo that carries the host's repository id registers it (the push beat the install or rename event) and a push under an old name follows the rename alias. GitHub `archived` pauses the repo (index and jobs kept, nothing opened while read-only) and `unarchived` resumes the jobs with no scan; `deleted` still forgets it. Pending jobs now move with a rename immediately, so a new repo that takes the freed name no longer inherits or loses them. `model.Event` gains `NewRepo`, `DefaultBranch` and `RepoID` (Postgres migration `004_event_typed_fields.sql`, DynamoDB attributes; old rows read as empty) and `platform.Event` gains `RepoID`, `RepoArchived`, `RepoUnarchived`; `Ref` stays readable as the deprecated overload (see [MIGRATION.md](MIGRATION.md)). Verified against the fake GitHub only, not real deliveries.
- Workspaces: a module that requires a `go.work` sibling without a `replace` is tidied with temporary local replaces and verified in workspace mode instead of opening a draft, and a workspace-level `vendor/` is refreshed with `go work vendor`. A workspace-only module graph skips `go work sync`, and the PR says so.
- New opt-in `tidy: minimal` on a `gomod` entry keeps the original go.mod requirements and go.sum lines and adds only what the update made necessary, instead of everything `go mod tidy` rewrites. The default is unchanged.
- Major bumps follow `tool` directives (Go 1.24) in `go.mod` to the new module path, so a repo that declares a tool from the bumped module no longer opens a draft. New opt-in `directives: extended` on a `gomod` entry also rewrites `//go:generate go run pkg@version` (the version becomes the one bumped to), `go tool pkg`, and the directives of a file whose body does not parse. The default rewrites exactly what it did.
- OSV residuals: advisories whose only fix is a pseudo-version can be proposed with `security-pseudo-versions: true` (never automerged); advisories with no fix ripple can propose are listed on the dependency dashboard; `stdlib` and `toolchain` advisories move the `toolchain` line for repos that opted in with `go-toolchain:`; and a scan checks the new dependencies of a repo against OSV with `POST /v1/querybatch`, so a repo registered after an advisory is not missed. The poller's list cap is now 20000 lines. A release fix is now preferred over a pseudo-version fix in the same record. See [docs/osv.md](docs/osv.md). Verified against the fake OSV; four read-only live requests confirmed the querybatch version format and the stdlib and toolchain names ([UPSTREAM.md](UPSTREAM.md)).
- Dead letters: an event that fails 3 times is kept dead (last error, attempts, times) instead of deleted, purged after `RIPPLE_EVENT_DEAD_RETENTION` (default 30d), and listed or requeued with `ripple events list|requeue|purge`. Dead events do not count against the backlog caps. Additive store changes: Postgres migration `003_dead_events.sql`, DynamoDB `EVENTS#dead` partition; old rows read as live. `Store` gains `ListDeadEvents`, `RequeueDeadEvent`, `PurgeDeadEvents` and `ListDueJobsExcluding`. See [README.md](README.md) and [docs/store-dynamodb.md](docs/store-dynamodb.md).
- Fairer due-job listing: the job runner no longer lists repos whose run is in flight, so a repo with more than 1000 due jobs cannot hold back every other repo's until it finishes.
- GitHub Enterprise Server support: `RIPPLE_GITHUB_ENTERPRISE_URL` (or `RIPPLE_GITHUB_API_URL` / `RIPPLE_GITHUB_GRAPHQL_URL`) derives REST `/api/v3` and GraphQL `/api/graphql`; `X-GitHub-Api-Version` falls back to `2022-11-28` on a 410 (or pin it with `RIPPLE_GITHUB_API_VERSION`); a GHES without GraphQL or the auto-merge mutation leaves the PR for review instead of warning. github.com defaults are unchanged. Verified against a fake GHES only. See [docs/platform-github-enterprise.md](docs/platform-github-enterprise.md).
- Webhook receivers share one hardening helper (`internal/webhook` `Guard` and `Dedupe`): a read deadline per body (30s, `ReadTimeout`; a slow sender gets 408 and frees its slot), HMAC verification while streaming so a forged body is never held in memory (bodies over 64 KiB spool to disk), and a delivery table keyed on each host's own id (GitLab `webhook-id`, Bitbucket `X-Request-UUID`/`X-Request-Id`, CodeCommit `MessageId`). Every host receiver adopts it; Azure DevOps now takes a read slot too. CodeCommit's SNS signature covers parsed fields and cannot be checked before the whole body, so it is bounded (1 MiB, slots, deadline). Valid traffic is unchanged; `DedupeBySignature` is opt-in.
- Dependency dashboard: opt in with `dependency-dashboard: true` in `.ripple.yaml` and ripple keeps one issue per repo (label `ripple`, marker comment on the first line) listing deferred, pending and failed jobs. Written at the end of a job or scan pass and after a feed poll, never per event; the body is deterministic with no timestamps of its own and an unchanged body costs no API call; capped well under GitHub's 65536-character limit with a counted notice; edits to it are ignored and a closed dashboard is not reopened. Issues disabled or the App without the Issues permission logs once and never fails a job. New optional `platform.IssueTracker` capability (GitHub only; `ErrNoIssues` elsewhere), optional `store.OpenJobLister` (memory, Postgres migration 003, DynamoDB per-repo item copies), a dashboard conformance test. Verified against the fake GitHub; the Issues permission and the 403/410 shapes are from the docs, not a live App. See the README.
- Backfill for new dependencies: when a scan finds a dependency the repo did not have, ripple compares it with the newest release of that module already stored and opens a normal job when it is behind (same cooldown, schedule, ignore rules and PR limit; at most 10 jobs per scan; a rescan changes nothing). Turn it off with `backfill-new-dependencies: false` in `.ripple.yaml`. New `Store.ListReleases`.
- Verification with cgo and private modules, opt-in and additive: `cgo: true` and `private-modules:` on a `gomod` entry, `RIPPLE_GO_CC`, and `RIPPLE_GOPROXY_USER`/`RIPPLE_GOPROXY_TOKEN` for a proxy that needs credentials (https only, given only to the steps that run no repository code, never in go's environment, argv, logs or a PR). A module with cgo files built with cgo off, or `cgo: true` with no compiler, is said in the PR rather than passing silently. See docs/sandbox.md.
- Go toolchain and `go` directive updates as their own update type, opt-in with `go-toolchain: toolchain|directive` on a `gomod` entry, from the go.dev release list (`RIPPLE_GO_RELEASES_URL`). The directive moves only on end of support, to the oldest supported minor, never lowering. See [docs/go-toolchain.md](docs/go-toolchain.md).
- DynamoDB: the pending-job queue is spread over `RIPPLE_DYNAMODB_JOB_SHARDS` index partitions (default 8) and a package's dependents over `RIPPLE_DYNAMODB_DEPENDENT_SHARDS` (default 1, so unchanged). Existing tables work as they are: the old keys are shard 0 and items move on their next write; the table records the highest shard count so lowering the setting strands nothing. See [docs/store-dynamodb.md](docs/store-dynamodb.md).
- OSV security advisories as an event source (`RIPPLE_OSV=true`, off by default; `RIPPLE_OSV_URL`, `RIPPLE_OSV_DUMP_URL`, `RIPPLE_OSV_INTERVAL`, `RIPPLE_OSV_MAX_FETCH`). A Go advisory affecting a version in a repo's go.mod becomes a PR for the lowest fixed version that skips the cooldown (ignore rules, schedule, groups and the open-PR limit still apply), titled `[security]`, labelled `security`, and linking the advisory. Advisories with no fixed version or only a pseudo-version fix are not proposed. Polls the public `modified_id.csv` with a persisted cursor and a bound on requests; an OSV outage never delays normal updates. Verified against a fake serving recorded payloads only. See [docs/osv.md](docs/osv.md).
- GitHub `repository` events: a rename or transfer moves the stored repo, its dependencies and its pending jobs to the new name with no re-scan; a default-branch change re-scans once and future PRs target the new branch (open PRs untouched); archive and delete stop ripple acting on the repo, unarchive resumes it. Optional `RepoRenamed`/`DefaultBranchChanged` platform events and a `ReportsRenames` conformance marker; other hosts are unchanged. GitHub's payload schemas are vendored in `docs/vendor/github/`. Verified against the fake GitHub only, not real deliveries.
- [MIGRATION.md](MIGRATION.md): the deprecation policy (additive first, deprecate then remove across a minor release) and every live deprecation; `scripts/check-migration.sh` (CI, `make check-migration`) fails when a `ripple:deprecated <id>` marker in code has no entry.
- Container runner for the build: `RIPPLE_SANDBOX=container` (Apple `container`), `docker` or `podman` runs `go`
  and local `git` in a fresh container of `RIPPLE_CONTAINER_IMAGE` (read-only root, no capabilities, CPU, memory
  and process limits, only the workspace mounted, killed and removed on timeout), configured with
  `RIPPLE_CONTAINER_CPUS`, `_MEMORY`, `_PULL` and `_NETWORK`. The default (`none`) is unchanged. Apple's runtime is
  tested for real on a Mac; Docker and Podman only against a fake CLI. See docs/sandbox.md.
- Superseded pull requests are closed: when a newer release of a module gets its own ripple PR, ripple's older
  untouched PR for that module is closed with a comment pointing at the new one. Never a PR a person touched
  (commits, reviews, comments, a rename), never on a failed check, branch kept. Opt out with
  `close-superseded-pull-requests: false` in `.ripple.yaml` (additive key). New optional `platform.PRCloser`
  capability, implemented by GitHub only; other hosts keep both PRs open. See the README.
- Release workflow (`.github/workflows/release.yml`): a pushed `vX.Y.Z` tag builds a multi-arch (amd64, arm64)
  image for GHCR with the version tag and digest, attaches static binaries and `SHA256SUMS` to the GitHub
  release, then installs the published artifacts from scratch and runs `ripple version`. The Dockerfile takes a
  `VERSION` build arg so the image reports the tag. Pull requests touching the workflow or Dockerfile run it
  as a dry run that publishes nothing.
- `make test-services` starts Postgres and DynamoDB Local itself and runs the suite with
  `RIPPLE_REQUIRE_SERVICES=1`, which turns a skipped service test into a failure; CI sets it too.
- CI smoke test of the built image over HTTP against a fake GitHub (`internal/smoke`, `make test-smoke`).
- Gerrit host (`RIPPLE_GERRIT_*`): changes proposed by pushing to `refs/for/<branch>` with a stable Change-Id, open-change lookup by topic, `ref-updated` webhooks, no auto-merge. `gitws` gains an optional `Proposal` push shape (GitHub and the others are unchanged) and the conformance suite a `PushCreatesProposals` marker. Not yet run against a real Gerrit. See [docs/platform-gerrit.md](docs/platform-gerrit.md).
- Bitbucket Cloud host (`RIPPLE_BITBUCKET_*`): workspace discovery, `repo:push` webhooks (changed files from the diffstat), pull requests, and auto-merge through merge queues only (otherwise reported unsupported). The platform conformance suite gains an optional `FilelessPushes` marker for hosts whose push payload lists no files. See [docs/platform-bitbucket.md](docs/platform-bitbucket.md).
- SCM-Manager host (`RIPPLE_SCMMANAGER_*`, Review plugin 3.11.0 or later): repository discovery by listing, webhook-plugin pushes authenticated by a shared-secret header (the plugin cannot sign), changed files from the modifications API, pull requests including drafts. No auto-merge: SCM-Manager has none. Verified only against a fake. See [docs/platform-scmmanager.md](docs/platform-scmmanager.md).

### Fixed
- Container backends: a command that timed out left its container behind, stopped, about half the time under Apple's CLI (`--rm` is the killed client's job); the reaper now always removes it. The live test checks only its own container and reports debris from other runs.

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
