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
29. **More datasources and feeds, as many ecosystems as Renovate has.** npm replicate, PyPI, crates.io
   sparse index, Maven, NuGet, RubyGems, Packagist, Docker/OCI tags, GitHub releases/tags, Helm, Terraform
   registry. Where a registry has a changes feed, use it; where it has none (OCI registries), fall back to polling
   only the packages the reverse index says someone imports, never every repo. Each datasource says which of the two it is.
30. **More managers, toward Renovate parity.** ~~GitHub Actions~~ shipped ([docs/github-actions.md](docs/github-actions.md)).
   Remaining, by value, each its own entry: 30b Dockerfile (`FROM`, tags and digests, needs an OCI datasource,
   see 29), 30c npm, 30d Cargo, 30e pip, 30f Helm, and the long tail.
96. **Actions registry cursor.** The packages the actions watcher polls live in one cursor
    (`github-tags:packages`), written read-modify-write: replicas can lose each other's additions until the
    next scan, and a package no repo uses any more is never pruned. Keep it per package in the store.
97. **Actions: supersede and non-matching shapes.** A newer tag opens a second PR beside an open older one,
    and a dependency whose shape has no newer tag (`v4` with only `v5.0.0` upstream) is silently left alone;
    supersede like gomod and surface the skip on the dashboard.
98. **Actions on GHES.** Tags are read from the repo's own host, so an action on github.com is not found from
    a GHES install; allow a configured upstream host. Also verify the workflows-permission refusal text
    against a real GitHub push.
99. **PR footer says "module index".** Pull requests from the actions watcher use the Go module footer.
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
43. **Verify a Go bump with the Go it names.** Verification runs with the server's installed Go
    (`GOTOOLCHAIN=local`), so a directive above it opens a draft and a toolchain bump is checked
    against a different Go. Offer a toolchain image per supported minor; CI stays the real check until then.
51. **OSV: should a security PR bypass the open-PR limit and the schedule?** Today only the cooldown is skipped.
    Needs a decision from real use. Trade-offs: bypassing the limit makes a security fix arrive even when a repo
    is at its cap of open PRs (the case where it matters most) but lets one advisory affecting many modules open
    many PRs at once; bypassing the schedule removes the "not on a Friday" protection for the one PR type
    that is most often wanted at once, but merges ripple would not otherwise open out of hours. Options:
    bypass only the limit; bypass both for severity HIGH or CRITICAL; or per-repo opt-in. Not decided here.
52. **OSV: finish the live run.** Four read-only requests on 2026-11-23 settled the querybatch version
    format and the stdlib and toolchain names (UPSTREAM.md `verified-live`). Two more
    requests (2026-11-23): stdlib and toolchain 1.21.0 gave disjoint id sets and no next_page_token. Still open: a
    ripple run pointed at real OSV end to end, a querybatch result large enough to paginate, rate limits, CSV
    ordering, and the 404 delay.
53. **Backfill residuals.** A new dependency beyond `DefaultMaxBackfill` (10 per scan) is never backfilled, and
    only a release of the exact module path is found: a newer major path on the same line is not. Make the
    bound a server setting (`RIPPLE_MAX_BACKFILL`), carry the unserved remainder to the next scan instead of
    dropping it, look across the line for a newer major, and cover releases the feed never recorded (a module
    nobody else imports has none stored) with a bounded index lookup. Also ListDependents is still an
    eventually consistent GSI read on DynamoDB, so a release fanned out in that window can still miss a repo
    that registered a moment before.
58. **Prove the credentialed container path live.** `RIPPLE_GOPROXY_TOKEN` under a container backend (GOPROXY
    userinfo over https) is covered by argv and refusal tests only, not run against a real container and an
    https proxy. Also offer an image with a C toolchain and headers, and a preflight that checks it for `cgo: true`.
59. **Private modules without a proxy.** Modules reachable only by direct VCS fetch are not supported, because
    `GOVCS=*:off` forbids it. Consider `GOAUTH`, or a ripple-run caching proxy fed from the repo's own credentials,
    and make `build`/`vet` stop asking the proxy for `.info` so a proxy that rejects it cannot fail verification.
60. **Dead-letter residuals.** There is no metric for dead events (the service has none yet; the log line
    `event failed; dead-lettered` is all there is). A rescan lists the whole live event set every 30s: on
    DynamoDB use a stream or a narrower index, and on Postgres LISTEN/NOTIFY. DynamoDB's due-job exclusion
    is a filter applied after the read, so it still reads the excluded repo's items. A failed *job* is not
    requeueable from `ripple events`. If persisting the dead state fails the event stays live and is retried
    after a restart rather than looping.
61. **Run the GHES support against a real GHES.** It is written from the docs and a fake. Check on a real instance (a trial
    licence or a sponsored one): the first release that accepts `2026-03-10`, whether `enablePullRequestAutoMerge` exists
    and what an absent one answers, a disabled GraphQL/REST status, and the webhook payloads; then record the facts in
    `UPSTREAM.md` and `docs/vendor/ghes`.
63. **Several GitHub hosts.** One GitHub host (github.com or one GHES) per server. Serving github.com and a GHES together
    needs a second host name in the registry with its own webhook path, App and secret.
64. **Webhook delivery ids in the Store.** The delivery table is in memory, per process, so a restart or a second
    replica forgets a handled delivery (queued events still key on it). Persist ids with a TTL, and wire
    `DedupeBySignature` (opt-in, off by default because a genuine identical redelivery is dropped) into the app
    and the other signed hosts once a real redelivery has shown identical bodies are rare.
65. **Webhook retry-id stability, verified.** Whether Bitbucket Cloud's `X-Request-UUID` and Azure DevOps' event
    `id` stay the same across a retry is unverified; confirm against a real delivery before relying on dedupe.
66. **Webhook bodies off the disk, and a server read timeout.** A forged body is no longer held in memory but a
    signed body over 64 KiB is spooled to a temp file (`MaxInflight` x `MaxBody` of disk). Also set
    `http.Server.ReadHeaderTimeout`/`ReadTimeout` in `cmd`, since the per-body deadline needs a ResponseWriter
    that supports deadlines.
67. **Dependency dashboard: a "re-run now" control, and more hosts.** The dashboard (opt-in
    `dependency-dashboard`) is a read-only report. A checkbox that releases a deferred job early needs the
    `issues` webhook, a check of the editor's repository permission (never the issue's text) and an audit
    line; do that or leave it out for good. Only GitHub has an issue tracker implementation: GitLab, Gitea and
    Forgejo issues, and Azure Boards, are the same `platform.IssueTracker` interface. Also list ripple's open PRs
    and the jobs skipped for an invalid config, so the issue answers "what is ripple waiting on" completely.
68. **Dashboard bookkeeping gaps.** The set of repos to refresh after a feed poll is in memory, so a restart
    drops it until the repo's next run or scan. A person's edit to the dashboard body is not noticed until the
    jobs change (the stored hash is of what ripple last wrote). Turning the key off leaves the issue as it was
    instead of closing it. A repo with issues unavailable is retried hourly, in memory only.
76. **Remove the `Ref` overload on `model.Event`.** `Ref` is still populated beside the typed fields and marked
    deprecated. Remove the fallback in the minor release after the one that introduced it, once no queue holds old
    rows (`ripple:deprecated event-ref-overload` in MIGRATION.md).
77. **Generators that take an import path.** `directives: extended` rewrites `go run pkg[@version]` and
    `go tool pkg`; `mockgen`, `stringer -type` and the like name the module in their own flag grammar
    (`-source`, `-imports`, `reflect` mode packages) and stay reported in the PR body. Each needs its own
    grammar, so add them one tool at a time, behind `directives: extended`, from the tool's documented flags.
78. **Workspace-only graphs: what is still a draft.** A module that requires a go.work sibling without a
    `replace` is now tidied with temporary local replaces and built in workspace mode, and `go work vendor`
    refreshes a workspace `vendor/`. Not handled: `go work sync` is skipped for such a workspace (it fails on
    them), a member's own `vendor/` is not refreshed (ripple says so in the PR body), and a requirement on a
    sibling at a version no proxy serves cannot be built by Go itself, so those repos stay drafts. Verify
    against a real multi-module repo, and consider a `replace`-free `go work vendor` for members.
79. **`tidy: minimal`: make it the default, or say why not.** The option keeps every original go.mod
    requirement and go.sum line and adds what `go mod tidy` added or raised; it prunes only go.sum lines
    below the version go.mod selects. It is verified by a build and vet, but a kept requirement whose go.sum
    lines are missing opens a draft. Look at real PR diffs, then decide whether to flip the default
    (a behaviour change, so a minor release with a note) or document it as permanently opt-in.
89. **Partial scan on other hosts.** The scan without a checkout is verified against GitHub and the fake
    only. GitLab, Bitbucket, Gitea, Azure and the rest may not serve `filter=blob:none` or by-id blob
    fetches; they fall back to a full checkout, unmeasured. Check each and record which saves anything.
90. **Partial scan relies on non-cone sparse checkout.** Git marks it deprecated. Move the batched blob
    fetch to `git fetch --stdin` of object ids or `git backfill` once they work on a `--depth 1` clone.
91. **Go version in more files.** `.mise.toml`, `.devcontainer/devcontainer.json`, `go.work`, `Makefile`
    variables and workflows in a module subdirectory are not followed by a toolchain bump.
92. **Supersede against real hosts.** GitHub, GitLab, Gitea/Forgejo, Bitbucket Cloud and Data Center, Gerrit and
    CodeCommit close or abandon a stale PR, written from vendored docs and checked only against fakes. Several
    reads rest on source or assumptions (GitLab and Gitea system-note and timeline wording, CodeCommit commenting
    on a closed PR, Gerrit abandon permission). Run each once on a real instance and record it.
93. **Azure DevOps and SCM-Manager cannot close a superseded PR.** Azure exposes no edit history of a title or
    description; SCM-Manager's reject, comment and history endpoints are documented only in plugin source. Add
    the closer when either API gives a reliable "a person touched it" read.
94. **Edits no host can report.** Not detected: a description edit on Forgejo, Bitbucket Cloud (beyond its
    activity log), CodeCommit and Gerrit; a changed label, assignee or reviewer on GitLab and Gerrit; a
    force-push under ripple's own commit identity on Gerrit, Bitbucket Cloud and CodeCommit. A person who does
    one of these loses nothing only if ripple stored what it opened: record the head SHA and body hash on the
    job and compare, which would cover every host the same way.
95. **Supersede re-reads touched PRs every run.** A stale PR a person touched is checked again (about six
    reads) each time ripple works on the repo, because the retry cannot tell it from a failed close. Remember
    "touched" on the job, cleared when the PR changes.
100. **Replicas: a store-assigned event sequence.** Event order across replicas now holds only as far as
    each replica's clock does: `Seq` is the receiving replica's `UnixNano`, so skewed clocks can order two events
    of a repo wrongly, and the claim-time `HasOlderEvent` check trusts that order. On DynamoDB the check reads the
    eventually consistent `gsi1`, so an older event written a moment ago can be missed; Postgres and the memory
    store have no such window. Assign `Seq` in the store (a Postgres sequence, a DynamoDB counter item) and make
    the DynamoDB check consistent.
101. **Replicas: soak against real databases.** The shared suite races goroutines on one store handle and the
    e2e runs two replicas; neither is a real multi-node Postgres or DynamoDB under load, or with a skewed clock.
    Run three replicas against both with a clock offset and check one PR per job and ordered events.
102. **Replicas: the remaining unfenced writes.** The job rows the runner writes are fenced on the repo lease;
    the advisory pass's `UpdateJob` (`advisory.go`), event `UpdateEvent`/`DeleteEvent` by a replica whose event
    lease lapsed, and the cursor writes of pause, remove and rename run under a lease but are not fenced by the
    store. Extend fencing to them, or show each is idempotent.
103. **Replicas: metrics and one feed poller.** Expose a replica-count and lease-takeover metric (today only the
    `replica` start-up and `lease taken over` log lines exist) and elect one replica for the release-feed poll
    (two polling is safe, since job enqueue is insert-if-absent, but wasteful).
104. **OSV: sweep DynamoDB repos and large repos.** The post-gap sweep needs `store.RepoLister`, which memory and
    Postgres have; DynamoDB has no cheap listing (needs a repo index item) so it is not swept. The batch (10) is
    not configurable, and a huge repo is still bound by `maxHistoryQueries` and `maxHistoryFetch`. Postgres
    `ListRepos` is verified only in CI.
105. **OSV: reconcile older and busy cases.** Corrections only reach jobs indexed under `osv-jobs` (made after this
    release); a repo busy for over 2s keeps its stale job or PR until the next record change; a PR body is not
    rewritten when a record is corrected; a record shared by twin GHSA and GO notes is reconciled per note.
106. **OSV: conflict and pseudo-version residuals.** An already-open non-security toolchain PR is not closed when a
    security one arrives (whoever merges second resolves it). A plain, non-advisory release move off a pseudo-
    version is not implemented; rule to use: the first release above the pseudo-version and released after its
    timestamp, opt-in. Decision recorded: `security-from-pseudo-versions` is opt-in and never automerged until real
    PR diffs show otherwise.
107. **Archive events on other hosts.** GitLab (`project_update`), Gitea/Forgejo, Bitbucket and Azure DevOps report no
    archive event in the docs checked (see each `docs/platform-*.md`). Poll the archived flag per host on a refresh,
    or map an event once a host documents one; Forgejo and Bitbucket Data Center were not checked.
108. **Sweep removal tombstones.** `removed|<repo>` cursor rows are ignored after 24 hours but never deleted.
109. **Repo ids from install events.** `repos_added` events carry no repository id, so a repo added that way adopts it
    only on its first signed push; a push with a mismatched id is ignored, not resolved by a lookup on the host.
110. **DynamoDB: failed jobs from before the open-job copies.** The backfill reads the sparse pending index, so a job
    that failed before the copies existed is in no index and stays off its repo's dashboard until its next write.
    Finding it takes a Scan of the table (`dynamofake` now has a base-table Scan). Run one as an explicit one-off,
    not on every start.
111. **DynamoDB: recount all packages, and a trigger for it.** `RecountDependents` repairs one package and runs lazily
    only when a counter item is missing. Nothing recounts a counter that drifted (crash between the item writes and
    the ADD, or while the option was off), and nothing calls it. Add a recount over every package (a Scan for
    `PKGCOUNT#` items), a CLI command or maintenance hook, and a drift check that compares the counter to a
    `Select=COUNT` Query and logs the gap.
112. **DynamoDB: the fake's reserved-word list is a subset.** `dynamofake` rejects only the reserved words the store
    could plausibly use as an unaliased name. Take the full list from the documentation, and add the same check for
    key-condition and projection expressions if the real service applies it there. The parity check will say
    where it matters.
113. **DynamoDB: throttling and index lag against a real table.** `ThrottleNext` and `HoldIndex` model the errors and
    the stale reads, not their timing or frequency, and DynamoDB Local throttles and lags neither. Run the sharded
    and striped paths against a real on-demand table once (a throwaway account, a bounded run) and record the
    retry counts, the index lag a recount or a `ListDependents` sees, and whether the SDK's three attempts are
    enough for a burst.
114. **DynamoDB: tune and measure counter striping.** `RIPPLE_DYNAMODB_COUNTER_STRIPES` defaults to 1 and the
    estimate that avoids summing the stripes on every write is a heuristic. Measure write throttling on one hot
    package with and without stripes, choose a default or document how to size `n` from the dependent count, and
    consider batching the counter per scan (already one ADD per package per scan) before striping.
115. **DynamoDB: adaptive counters while the option is off.** With `RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS` unset, writes
    do not touch the counters, so turning it on later starts from a stale count until the lazy recount or an
    explicit one. Either keep counting with the option off (cheap, one ADD per package per scan) or document that
    `RecountDependents` is the step to run after turning it on.
