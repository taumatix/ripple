# Roadmap

Ordered by how much each entry limits real deployments. The top is next.

1. **OS-level sandbox for `go` and `git`.** A hostile repo's build runs as the server's uid with the whole filesystem readable. Run each job under bwrap/nsjail or in a per-job container with a separate uid, a read-only root, only the workspace writable and no network except the proxy. This is the minimum for a single host; entry 2 generalises it to container runners.
2. **Run jobs in a container runner, with updaters as OCI images.** Entry 1 contains the build; this entry
   moves the whole job (clone, edit, verify, push) out of the server process. Define a `Runner` interface
   (start a job from an image with a workspace, an env allowlist, a network policy and CPU, memory and time limits;
   stream logs; report the exit status) and ship backends for Docker, Apple `container`, Podman and Kubernetes
   Jobs, plus the current in-process runner as the default so nothing changes for existing installs. Each
   ecosystem's updater is a published image (the Go one carries the toolchain), so a repo's build never runs
   next to the App key. The token reaches the job as a short-lived, per-job secret. Cross-backend end-to-end
   test: the same job over a fake platform on every backend that is available, skipped (and said so) where its
   runtime is absent. CLI flags and socket paths for each runtime are **unverified** until the entry fetches
   that runtime's documentation. Later, behind the plugin mechanism (entry 33): a dependency collector that
   runs as an isolated container for discoveries too complex or too risky to run in-process, with the same
   `Runner` and the same limits.
3. **Publish a prebuilt image and binaries on each release.** Today the only container route is
   `docker build` from a checkout, and a tag produces no artifacts. Add a release workflow, triggered by the
   tag, that builds a multi-arch image (it carries the `go` toolchain) and pushes it to GHCR with the version tag
   and a digest, plus static binaries attached to the GitHub release. The package must be made public once by
   hand in the GitHub UI (there is no API for it); until then the README names `go install` as the open path and
   the image as the shut one. Each release then installs the published image from scratch and runs `ripple version`.
4. **Hermetic integration tests, and a smoke test of the built image.** The Postgres and DynamoDB store
   tests skip when no database is reachable, so a local `go test ./...` passes without running them, and
   the Docker image is built and run only in CI. Make `make test-services` start both services itself (Docker, or Apple `container`
   on the agent host), fail rather than skip under an opt-in `RIPPLE_REQUIRE_SERVICES=1`, and add a CI step
   that runs the built image against a fake GitHub with a signed push event and expects exactly one PR.
5. **Per-installation workers and a durable event queue.** Webhook events are handled one at a time behind a queue of 1024, then the receiver answers 503; GitHub does not redeliver, so those events are lost (one noisy installation starves the rest). Also, the poll loop's `RunDue` waits on slow major verifies and stalls the global feed poll: run jobs off the poll path.
6. **Bound the scan and the fan-out.** A repo with a million files or dependencies costs unbounded memory and time. Cap files and dependencies per repo, clone with `--filter=blob:limit=1m`, and memoise the per-repo config decision in `FanOut` (`internal/engine/fanout.go`) instead of re-parsing it per dependency.
8. **Fetch the installation token per network git command.** The token is minted once per job and can expire mid-job on a slow clone or push. Use a credential helper that asks `auth.go` each time (`git.go` clone, fetch and push).
9. **Close the residual webhook gaps.** A request with a well-formed but forged signature still buffers its body (up to `MaxBody`, at most `MaxInflight` at once) before the HMAC fails, and a slow sender can hold those slots until the server read timeout, so genuine deliveries get 503 meanwhile: spool to a temp file or hash while reading with a per-read deadline. The delivery id is an unsigned header and the table is in memory: persist it in the Store and key on the body signature too.

Platforms (code hosts). The goal is the whole list Renovate supports, each behind the one `Platform`
interface, each with the same end-to-end test (a push or release event leads to exactly one PR in
exactly the importing repos, and zero API calls for the rest). Every platform entry starts by
fetching and vendoring that platform's own documentation (webhook events and signatures, PR API,
auth model, rate limits); the notes below are what I expect, **unverified** until then.

10. **Platform seam and conformance suite.** Before a second platform: take the GitHub-isms out of the
   interface (App installation tokens, GraphQL node ids and the auto-merge mutation in `platform.PR`,
   installation events as the way repos get registered) and replace them with what every host can
   provide: a credential source per host, repo discovery (webhook, group/project hook, or a periodic
   listing where a host has no install event), a "request auto-merge or merge when green" capability that
   may be absent, and a per-platform rate-limit signal. Turn the GitHub e2e tests into one shared
   suite that each platform's fake server must pass. The per-repo file location is decided: `.ripple.yaml` at the
   repository root on every host, with `.github/ripple.yml` a deprecated GitHub-only alias (see the
   removal entry at the end). Keep the file's meaning identical everywhere.
11. **GitLab** (gitlab.com and self-managed). Merge requests, project/group webhooks, `Private-Token` or
   OAuth, merge-when-pipeline-succeeds. Subgroups and per-project tokens are the awkward part.
12. **Gitea and Forgejo.** One client with two flavours: Forgejo forked Gitea and their APIs diverge
   slowly. Webhook signature headers and the auto-merge call differ; detect the flavour from the version endpoint.
13. **Bitbucket Cloud.** Pull requests, repository webhooks, app passwords or OAuth consumers; no native
    auto-merge, so automerge is a merge-check poll or unsupported (say which).
14. **Azure DevOps.** Service hooks (code pushed, PR events), PAT or Entra token, auto-complete on PRs,
    organisation/project/repo hierarchy instead of owner/repo.
15. **Bitbucket Server / Data Center.** A different REST API from Bitbucket Cloud; webhooks per
    repo or project; HTTP access tokens.
16. **AWS CodeCommit.** No webhooks: repository events arrive through EventBridge or SNS, so this needs an
    inbound adapter (SQS or an HTTPS subscription) in front of the same event path. PRs have no
    auto-merge. IAM credentials per account and region.
17. **Gerrit.** Changes, not pull requests: push to `refs/for/<branch>`, one change per update with a
    stable Change-Id so a re-push amends it, labels and submit rules instead of auto-merge. Events from the webhooks
    plugin or `stream-events`; the project listing and ACLs are per instance.
18. **SCM-Manager.** Pull requests and webhooks via its REST API and the webhook plugin
    (<https://docs.renovatebot.com/modules/platform/scm-manager/> is Renovate's reference for what it needs from the host).

Everything else:

19. **`go.sum` and `go mod tidy` refresh for every update.** Major bumps already tidy, build and vet
   in the branch; minor and patch PRs still edit `go.mod` only, so many fail CI until someone runs
   `go mod tidy`. Reuse the same verification and draft-on-failure path.
20. **Major bumps in repos with several modules, `go.work`, or `vendor/`.** Today only the module
   whose `go.mod` lists the dependency is rewritten and verified; a workspace, other modules that
   require it, and a vendor directory (`go mod vendor`) are not refreshed.
21. **Go toolchain and `go` directive bumps.** Propose `toolchain` and `go` line updates as their own
   update type, from the Go release feed.
22. **Rewrite `go:generate` lines and `// import` comments on a major bump.** Only import specs are
   rewritten now; `go run example.com/m/v2/cmd/x` in a directive and canonical import comments are not.
23. **Verify with cgo and private modules.** The sandbox builds with `CGO_ENABLED=0` (cgo files are
   rewritten but not compiled) and only from `RIPPLE_GOPROXY`; support a toolchain image with C
   headers and credentials for private proxies, without exposing the App token to the build.
24. **Supersede and close stale PRs.** A newer release leaves the older ripple PR open next to it.
25. **Backfill for newly registered dependencies.** A repo that starts importing a package only hears
   about releases published afterwards; compare against the latest stored release on registration.
   This also closes the DynamoDB GSI eventual-consistency window.
26. **Default-branch rename handling.** Handle the `repository` rename and default-branch change
   events; today a stale `DefaultBranch` is corrected only on the next push.
27. **Dependency dashboard issue.** One issue per repo listing pending, deferred and failed jobs.
28. **Dynamo hot partition.** Shard `JOBS#pending` and very popular `PKG#` partitions; revisit the
   eventual-consistency notes in `docs/store-dynamodb.md` under load.
29. **More datasources and feeds, as many ecosystems as Renovate has.** npm replicate, PyPI, crates.io
   sparse index, Maven, NuGet, RubyGems, Packagist, Docker/OCI tags, GitHub releases/tags, Helm, Terraform
   registry. Where a registry has a changes feed, use it; where it has none (OCI registries), fall back to polling
   only the packages the reverse index says someone imports, never every repo. Each datasource says which of the two it is.
30. **More managers, toward Renovate parity.** Dockerfile, GitHub Actions, npm, Cargo, pip, Helm,
   and the long tail; each manager and datasource pair is its own entry once the first few exist.
31. **OSV advisories as an event source.** A security advisory becomes a job that bypasses the cooldown.
32. **GHES GraphQL** differences and per-host API base handling.
33. **Plugin mechanism** for out-of-tree managers, datasources and platforms.
34. **Presets, org-level defaults and packageRules-like matching.** Later and strictly opt-in: a repo
    file must opt into a preset by name, and none of these may ever change the meaning of a plain
    per-repo `.ripple.yaml`. No implicit inheritance, no rule precedence.
35. **MIGRATION notes policy.** Rules for config-schema and API changes: additive first, deprecate
    across a minor release, `MIGRATION.md` for any break.
36. **Remove the `.github/ripple.yml` alias.** Deprecated in the release after v0.1.0 in favour of the
    root `.ripple.yaml`; remove it no earlier than v0.3.0 (the minor release after next), with a
    `MIGRATION.md` and a last warning release, per the deprecate-then-remove rule.
