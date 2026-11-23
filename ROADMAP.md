# Roadmap

Ordered by how much each entry limits real deployments. The top is next.

1. **Linux sandbox that can reach the proxy.** `RIPPLE_SANDBOX=bwrap` cannot confine the network to the
   module proxy, so `go` refuses to run under it, and bwrap has never run on a Linux host. Give it a
   network namespace with a forwarded socket to the gate (or run `go` with a prefilled module cache),
   and add a Linux CI job that runs the sandbox end-to-end tests. See [docs/sandbox.md](docs/sandbox.md).
3. **Exercise the release workflow for real, and finish the smoke test.** `release.yml` has never run on a tag:
   the tag trigger, the GHCR push, the release upload and the `verify` job (which installs the published
   binary and image and runs `ripple version`) are **unverified** until the first release train tags, and
   the first run may need fixes; check it right after that tag. The dry run (pull request or
   `workflow_dispatch`) only builds. The GHCR package is private and a human must flip it public once in the UI
   (no API). The image smoke test (`internal/smoke`) cannot assert the PR's `go.mod` content because the
   server has no `RIPPLE_GOSUMDB`: add that setting (also what a private-proxy deployment needs), set it
   off in the smoke test, import the fake module there, and assert the bumped `go.mod` and `go.sum`.
   Apple `container` has no `--network host`, so the smoke test runs against the image only in CI.

6. **Scan without a full checkout.** The clone filter saves nothing while the scan checks out HEAD
   ([docs/bounds.md](docs/bounds.md)): a huge-blob repo still downloads every blob at HEAD. Clone `--no-checkout`
   (or sparse) and read only the paths the managers match, so a repo's size stops mattering to a scan.
7. **Run more than one replica.** The event queue and the job runner assume one process per store: two
   replicas would both load the same queued events and both pick the same due jobs. Claim work with a lease
   (Postgres `FOR UPDATE SKIP LOCKED` with an owner and expiry; DynamoDB a conditional owner/lease write), so
   replicas share the queue and a crashed one's work is retaken when its lease lapses.
8. **Dead letters and fairer due-job listing.** An event that fails 3 times is logged and deleted; keep it in
   a failed state an operator can list and requeue instead. Due jobs are listed 1000 at a time, oldest first,
   and a repo whose run is in flight is skipped, so a repo with more than 1000 due jobs can hold back every
   other repo's until it finishes: list per repo, or exclude repos in flight from the query.
9. **Close the residual webhook gaps.** A request with a well-formed but forged signature still buffers its body (up to `MaxBody`, at most `MaxInflight` at once) before the HMAC fails, and a slow sender can hold those slots until the server read timeout, so genuine deliveries get 503 meanwhile: spool to a temp file or hash while reading with a per-read deadline. The delivery id is an unsigned header and the table is in memory: persist it in the Store and key on the body signature too (queued events already key on it, so a redelivery of a still-queued delivery is not queued twice; once handled, only the in-memory table remembers it).

Platforms (code hosts). The goal is the whole list Renovate supports, each behind the one `Platform`
interface, each with the same end-to-end test (a push or release event leads to exactly one PR in
exactly the importing repos, and zero API calls for the rest). Every platform entry starts by
fetching and vendoring that platform's own documentation (webhook events and signatures, PR API,
auth model, rate limits); the notes below are what I expect, **unverified** until then.

11. **GitLab, verified against a real instance.** The host is written from the vendored docs and has never
   run against gitlab.com or a self-managed instance ([docs/platform-gitlab.md](docs/platform-gitlab.md) lists
   what is unverified). Run a pass against a real project with a project token and with a group token, record
   one real push webhook, one 429 and one auto-merge answer, and replace the fake's guesses.
   Residuals: discover repos by system-hook `project_create`/`project_destroy` instead of the periodic
   membership listing (needs admin and is not in the vendored docs); rate-limit headers other than
   `Retry-After`/`RateLimit-Reset`; `merge_request` events to close ripple MRs; GitLab labels with commas.
12. **Gitea/Forgejo against real instances.** The host shipped from vendored docs and a fake only
    (docs/platform-gitea.md "Unverified"). Run the conformance flow against a real Gitea and a real Forgejo
    (no containers on the agent host: use codeberg.org or a CI service container), recording the real rate-limit
    answer, the scheduled-merge status code, whether Forgejo sends `X-Hub-Signature-256`, labels by name on create,
    and the git basic-auth user. Also: Forgejo before 7 cannot be detected (set `RIPPLE_GITEA_FLAVOUR`), and
    `repository` created/deleted webhooks are parsed but unproven.
13. **Bitbucket Cloud against a real workspace.** The host shipped from vendored docs and a fake only
    (docs/platform-bitbucket.md "Unverified"). Record the 429 headers, the 202 of a queued merge, `x-token-auth`
    with an API token, and merge-commit diffstats. Also: auto-merge exists only through merge queues, so a
    merge-check poll is the open design question if queues stay a paid feature; webhooks are created by hand,
    so per-workspace webhook registration is a follow-up.
14. **Azure DevOps follow-ups** (the host itself shipped, see [docs/platform-azuredevops.md](docs/platform-azuredevops.md)):
    verify it against a real organisation (every wire shape is from the docs and a fake: repo name accepted as
    `repositoryId`, `autoCompleteSetBy` taken from `createdBy.id`, the exact service-hook headers); Entra ID
    bearer tokens, which need a `gitws` scheme for sending `Authorization: Bearer` to git as `http.extraHeader`;
    names with spaces (projects and repos often have them: escape path segments in REST and clone URLs); register
    a repo on `git.repo.created` and forget it on `git.repo.deleted` / `git.repo.renamed` instead of waiting for
    the next listing; the commit-changes lookup (merge commits, paging past
    one page of changes) is unverified against a real organisation.
15. **Bitbucket Server / Data Center against a real instance.** The host shipped from vendored docs and a
    fake only (docs/platform-bitbucketserver.md "Unverified"). Run the conformance flow against a real
    Data Center (Atlassian's evaluation licence, no containers on the agent host), recording whether a 429
    carries `Retry-After`, the `autoMerge` response and who performs the merge, `draft` support by release,
    `path.toString` in `compare/changes`, and Basic versus Bearer for project tokens. Also: `repo:modified`
    renames are left to periodic discovery, and a repository with no commits may list but not clone.
16. **AWS CodeCommit: verify against real AWS.** Built from the vendored docs and a fake
    ([docs/platform-codecommit.md](docs/platform-codecommit.md)); no AWS account has run it. Needs a
    sandbox account: confirm SigV4 API calls and the git password are accepted, the title limit
    (100 or 150), the EventBridge input shape through SNS, the cn and eusc endpoints. Residuals: one
    AWS account per ripple, labels and auto-merge do not exist on the service, the SNS certificate
    chain is not validated beyond the signature.
17. **Gerrit: verify against a real instance.** Built from the docs and a fake ([docs/platform-gerrit.md](docs/platform-gerrit.md)),
    never run for real. Check the webhooks payload and URL token, 429 headers, clone and change URLs, WIP and hashtag
    permissions, "no new changes" on identical re-push. Residuals: auto-merge through submit-on-label if the docs
    ever support it, `stream-events` as an alternative to webhooks, learning default branches at discovery.
18. **SCM-Manager against a real instance.** The host shipped from vendored docs, the plugins' source and a
    fake only (see [docs/platform-scmmanager.md](docs/platform-scmmanager.md)). Verify on a real SCM-Manager 3.x
    with the Review and Webhook plugins: `Authorization: Bearer <api key>` on the REST API, git basic auth with
    an API key, the webhook plugin's custom-header table and `branches` in its changesets, the 201 `Location`,
    modifications of a merge commit, `shouldDeleteSourceBranch`, and what a rate limit looks like if any.
    Then: create the webhook through the plugin's REST API instead of by hand (not yet read), and confirm one
    global webhook with `${repository.namespace}` in the URL works.

Everything else:

- **Periodic re-scan for hosts without push events.** A host discovered by listing (`platform.Lister`)
  and without push webhooks is scanned when it first appears and never again; re-scan when its default
  branch head moves, compared at each listing.
- **Listing state outside a single cursor.** `App.Discover` keeps a host's previous listing as one JSON
  store cursor, which DynamoDB caps at 400 KB (roughly 10k repo names). Store it per repo before a host
  with larger installations ships.
- **Host-level receiver hardening shared, not copied.** `internal/webhook` (signature before body,
  inflight cap, delivery dedupe) is GitHub-shaped; lift the host-neutral parts into a helper the other
  receivers wrap, so each new host does not re-implement them.
19. **`go work vendor` and workspace-only module graphs.** A workspace-level `vendor/` is not refreshed,
   and modules that reach each other only through `go.work` (no `replace`) fail the per-module
   `go mod tidy`, which ignores the workspace, so those PRs open as drafts.
20. **Tidy only what the update needs.** `go mod tidy` also drops requirements the repo never imports and
   rewrites unrelated lines; consider limiting the PR to the dependency's own `go.sum` entries.
21. **Go toolchain and `go` directive bumps.** Propose `toolchain` and `go` line updates as their own
   update type, from the Go release feed.
22. **Directive residuals on a major bump.** `go:generate` lines are rewritten only for a plain
   `go run <package>`; the rest is reported in the PR body, not changed. Handle `go run pkg@vX` (pick a
   version of the new major), other generators that take import paths (`mockgen`, `stringer -type`), and
   `go tool`; examine directives in files whose body does not parse.
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
36. **Remove the `.github/ripple.yml` alias.** Deprecated in the release after v0.1.0 in favour of the
    root `.ripple.yaml`; remove it no earlier than v0.3.0 (the minor release after next), with a
    `MIGRATION.md` and a last warning release, per the deprecate-then-remove rule.
37. **Whole job in a container, with a per-job token and Kubernetes Jobs.** The container runner
    ([docs/sandbox.md](docs/sandbox.md)) confines only `go` and local `git`; clone, edit and push still run in the
    server. Move the whole job into the image behind a `Runner` interface (workspace, env allowlist, network policy,
    limits, streamed logs, exit status) with the installation token passed as a short-lived per-job secret, add a
    Kubernetes Jobs backend, and publish an updater image per ecosystem so a repo's build never runs next to the
    App key. Later, behind the plugin mechanism (entry 33): a dependency collector as an isolated container with the
    same limits.
38. **Verify the Docker and Podman runners for real, in CI.** Their argv is unverified against a daemon (fake CLI
    only). Add a CI job on a hosted Linux runner (Docker is installed) that runs the container end-to-end test with
    `RIPPLE_SANDBOX=docker`, a Go image and a proxy reachable from the container (the fake proxy bound to the docker
    bridge address), and a Podman variant if a runner offers it; fix whatever the real CLI rejects.
39. **Confine the container's network to the module proxy.** `go` runs on the runtime's default network, so a
    repository's build can reach anything that network can. Ship a recipe, then a built-in option, that creates an
    internal network holding only a proxy sidecar; also run `git` and `go` together in one image with a
    documented, pinned Go version so `GOTOOLCHAIN=local` always satisfies the repo's `go` line.
