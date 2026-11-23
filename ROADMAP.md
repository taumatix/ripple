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
29. **More datasources and feeds, as many ecosystems as Renovate has.** ~~npm registry~~ (shipped with 30c, polled per package; the replicate feed is not used), PyPI, ~~crates.io
   sparse index~~ (shipped with 30d, polled per crate), Maven, NuGet, RubyGems, Packagist, ~~Docker/OCI tags~~ (shipped with 30b, polled per image), GitHub releases/tags, Helm, Terraform
   registry. Where a registry has a changes feed, use it; where it has none (OCI registries), fall back to polling
   only the packages the reverse index says someone imports, never every repo. Each datasource says which of the two it is.
30. **More managers, toward Renovate parity.** ~~GitHub Actions~~ shipped ([docs/github-actions.md](docs/github-actions.md)).
   ~~30b Dockerfile~~ shipped ([docs/docker.md](docs/docker.md)). ~~30c npm~~ shipped ([docs/npm.md](docs/npm.md)). ~~30d Cargo~~ shipped ([docs/cargo.md](docs/cargo.md)). Remaining, by value, each its own entry: 30e pip, 30f Helm, and the long tail.
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
67. **Dependency dashboard: a "re-run now" control, and more hosts.** The dashboard (opt-in
    `dependency-dashboard`) is a read-only report. A checkbox that releases a deferred job early needs the
    `issues` webhook, a check of the editor's repository permission (never the issue's text) and an audit
    line; do that or leave it out for good. Only GitHub has an issue tracker implementation: GitLab, Gitea and
    Forgejo issues, and Azure Boards, are the same `platform.IssueTracker` interface (each needs its issues API
    read from the vendored docs and verified against a real instance, see 61). Also list ripple's open PRs, so
    the issue answers "what is ripple waiting on" completely: that needs a platform listing on every sync and a
    refresh when a PR closes. (Shipped: an invalid config is now said on an existing dashboard.)
76. **Remove the `Ref` overload on `model.Event`.** `Ref` is still populated beside the typed fields and marked
    deprecated. Remove the fallback in the minor release after the one that introduced it, once no queue holds old
    rows (`ripple:deprecated event-ref-overload` in MIGRATION.md).
89. **Partial scan on real hosts.** The fallback is verified against a fake server in both half-way shapes
    (filter without blob by id, blob by id without filter; docs/bounds.md), not against a real GitLab, Bitbucket,
    Gitea/Forgejo, Azure DevOps, Gerrit or CodeCommit. Clone from one of each once with `RIPPLE_LIVE_SCAN_REPO` and
    record which row of that table each falls in and what it saves.
92. **Supersede against real hosts.** GitHub, GitLab, Gitea/Forgejo, Bitbucket Cloud and Data Center, Gerrit and
    CodeCommit close or abandon a stale PR, written from vendored docs and checked only against fakes. Several
    reads rest on source or assumptions (GitLab and Gitea system-note and timeline wording, CodeCommit commenting
    on a closed PR, Gerrit abandon permission). Run each once on a real instance and record it.
93. **Azure DevOps and SCM-Manager cannot close a superseded PR.** Azure exposes no edit history of a title or
    description; SCM-Manager's reject, comment and history endpoints are documented only in plugin source. Add
    the closer when either API gives a reliable "a person touched it" read. Rechecked 2026-11-23 against the
    vendored Azure REST pull-request update reference (docs/vendor/azuredevops) and the SCM-Manager source
    notes: neither adds an edit history; nothing buildable offline.
94. **Edits no host can report.** Not detected: a description edit on Forgejo, Bitbucket Cloud (beyond its
    activity log), CodeCommit and Gerrit; a changed label, assignee or reviewer on GitLab and Gerrit; a
    force-push under ripple's own commit identity on Gerrit, Bitbucket Cloud and CodeCommit. A person who does
    one of these loses nothing only if ripple stored what it opened: record the head SHA and body hash on the
    job and compare, which would cover every host the same way. It needs the head SHA on `platform.PR` and a
    new job column (a Postgres migration and a DynamoDB attribute), so it waits for the store work in flight.
124. **Supersede: a touched PR whose head moves.** A PR a person touched is now remembered by URL
    (cursor `supersede-touched:<repo>`) until it leaves the open list. Touched is treated as permanent; a person
    who undoes the touch (reverts a rename, deletes a comment) is not noticed until the PR closes. Fine for the
    reads the hosts report today; revisit only if a host reports a revert.
101. **Replicas: soak against real databases.** The shared suite races goroutines on one store handle and the
    e2e runs two replicas; neither is a real multi-node Postgres or DynamoDB under load, or with a skewed clock.
    Run three replicas against both with a clock offset and check one PR per job and ordered events.
107. **Archive events on other hosts.** GitLab (`project_update`), Gitea/Forgejo, Bitbucket and Azure DevOps report no
    archive event in the docs checked (see each `docs/platform-*.md`). Poll the archived flag per host on a refresh,
    or map an event once a host documents one; Forgejo and Bitbucket Data Center were not checked.
109. **Repo ids from install events.** `repos_added` events carry no repository id, so a repo added that way adopts it
    only on its first signed push; a push with a mismatched id is ignored, not resolved by a lookup on the host.
113. **DynamoDB: throttling and index lag against a real table.** `ThrottleNext` and `HoldIndex` model the errors and
    the stale reads, not their timing or frequency, and DynamoDB Local throttles and lags neither. Run the sharded
    and striped paths against a real on-demand table once (a throwaway account, a bounded run) and record the
    retry counts, the index lag a recount or a `ListDependents` sees, and whether the SDK's three attempts are
    enough for a burst.
114. **DynamoDB: tune and measure counter striping.** `RIPPLE_DYNAMODB_COUNTER_STRIPES` defaults to 1 and the
    estimate that avoids summing the stripes on every write is a heuristic. Measure write throttling on one hot
    package with and without stripes, choose a default or document how to size `n` from the dependent count, and
    consider batching the counter per scan (already one ADD per package per scan) before striping.
116. **Container image per Go minor, and downloads.** `RIPPLE_GO_TOOLCHAIN_DIR` runs an operator-installed release
    on the host sandbox only; a container sandbox ignores it, and `GOTOOLCHAIN` downloads through the module proxy
    stay off. Offer an image per minor, or a proxy-fed download with a checksum check, so a bump above the
    installed Go does not stay a draft there. Verified only with a fake `go` script.
117. **Generators: stringer, `-self_package`, golang/mock.** `directives: extended` rewrites mockgen's documented
    flags only. `stringer -type` takes no import path, `mockgen -self_package` and the older
    `github.com/golang/mock` grammar are not read, and stay reported in the PR body. Add one at a time from the
    tool's documented flags.
118. **Workspace-only graphs against a real repo.** A requirement on a sibling at a version no proxy serves
    still opens a draft: with the workspace on, Go reads the sibling's `.mod` at the required version and gets a 404
    (reproduced against the fake proxy). `go work sync` is skipped for such workspaces and a member's own `vendor/`
    is not refreshed (its `modules.txt` would record the temporary replace). Verify against a real multi-module
    repo and consider a `replace`-free `go work vendor` per member.
119. **`tidy: minimal` evidence.** It stays opt-in: no real PR diffs exist to show the default would lose less
    than it risks (a kept requirement with missing go.sum lines opens a draft; the default `go mod tidy` result is
    what a maintainer reproduces by hand). Collect the diffs of repos that opted in and revisit.
120. **Backfill reads the GSI consistently.** `ListDependents` is still an eventually consistent GSI read on
    DynamoDB, so a release fanned out in that window can miss a repo that registered a moment before. The carried
    backfill closes it only for dependencies new to the repo, not for the window itself.
121. **Backfill lookup residuals.** The proxy lookup asks `@latest` only, so a prerelease-only or retracted-latest
    module is missed; it is not cached across repos (every repo with the same new module asks again, up to 20 per
    scan); a module that skipped more than three majors is found only by the feed; and it was verified against a
    fake proxy, not the real one.
122. **Webhook bodies: stream the parse.** A signed body is still held whole, on disk (default) or in memory
    (`RIPPLE_WEBHOOK_SPOOL=memory`), because an HMAC cannot be checked before its last byte and each receiver
    unmarshals the whole payload. Extracting only the fields ripple acts on while the stream is hashed, and
    discarding the result of a forgery, would hold neither; it means a token-level parse per host's payload.
123. **Dashboard: share the sets.** The refresh set
    (`dashboard-dirty`) and the repo list that drives the hourly read-back (`dashboard-watch`) are
    read-modify-write cursors, so two replicas marking at once can lose an entry; the loss heals at the repo's
    next run or write, but a per-item store write would remove it. Read-back finds the issue by its marker, so an
    edit that removes the marker is repaired by number and one that deletes the issue is recreated, both
    covered only against the fake.
125. **OSV: sweep residuals.** The DynamoDB repo index (`REPOS` items) is built by one Scan the first time a listing is asked
    for; that Scan and the listing are verified on the in-process fake, and DynamoDB Local runs them only in CI. A scan
    (not the sweep) still runs one history round, so a repo with over 500 pairs or 100 records is completed only by a sweep
    or a go.mod change; consider a few rounds per scan once real repo sizes are known. Postgres `ListRepos` is CI-only.
126. **OSV: reconcile older cases.** Jobs made before the `osv-jobs` index are still not found: the sweep's `RepoLister` plus
    `ListOpenJobs` could find them per repo. A PR body is not rewritten when a record is corrected (the fix version or
    summary in it can be stale), and a record shared by twin GHSA and GO notes is reconciled per note. The busy-repo retry
    list holds at most 100 ids.
127. **OSV: conflict and pseudo-version residuals.** The beaten-PR close covers a non-security PR when a security
    toolchain PR opens; a lower security toolchain PR open beside a higher one is not closed, and a toolchain PR opened by an
    older run than the security job is only held, never closed, unless the security PR opens in a later run with it open.
    A plain, non-advisory release move off a pseudo-version is still not implemented; rule to use: the first release above
    the pseudo-version and released after its timestamp, opt-in.
128. **Docker: private registries.** Only anonymous pulls work; a private image answers 401/403 and is skipped.
    Add per-registry credentials (a token or `config.json`-style auth) kept out of logs and PR bodies.
129. **Docker: compose files, Kubernetes manifests and `ARG` tags.** `image:` keys in compose files, `ARG
    VERSION=...` used in `FROM image:${VERSION}`, and `COPY --from=image:tag` are not read.
130. **Docker: verify against real registries.** The client and the fake were written from the spec excerpts
    (`docs/vendor/registry`); run it against Docker Hub and GHCR once (host, `library/` prefix, index digest
    `Accept` types, 429 handling) and record the result in UPSTREAM.md.
131. **Docker: package cursor and release dates.** `docker:packages` is one read-modify-write cursor (see 151) and the
    cooldown counts from first sight because tags carry no date; read the image config `created` time instead.
132. **Webhook retry ids, verified on real hosts.** Delivery ids are now persisted (store delivery table), but whether
    Bitbucket Cloud's `X-Request-UUID` and Azure DevOps' payload `id` stay the same across a retry is still
    unverified: the vendored docs state neither (see docs/platform-bitbucket.md, docs/platform-azuredevops.md). A
    retry under a new id is handled again, which is safe. Needs a real workspace and organisation to observe.
133. **Wire `DedupeBySignature`.** Still off and not wired into the app or the other signed hosts: a genuine identical
    redelivery would be dropped. Wait for real redelivery data showing identical bodies are rare.
134. **Delivery dedupe: hosts without a delivery id, and the DynamoDB cursor Scan.** The persistent claim covers events
    whose host sets `Delivery`; others rely on the in-memory table. `Dynamo.PurgeCursors` is a filtered Scan of the
    table (run at most daily) and is unmeasured on a large table; move tombstones to an indexed prefix if it hurts.
140. **Credentialed private proxy under a confined container network.** `RIPPLE_CONTAINER_CONFINE` is refused with
    `RIPPLE_GOPROXY_TOKEN`: go sends GOPROXY userinfo only over https and the sidecar speaks http. Give the sidecar
    TLS (a per-start certificate the image trusts) or have it add the Authorization header for the steps that may carry
    it, without sending the credential to build and vet. Entry 58 needs this to be proven live.
141. **Publish the runner image.** `docker/runner/Dockerfile` is built in the `container-runtimes` CI job and documented, but
    no image is published: build and push it to GHCR on the release train (public, since it holds only upstream
    images; check the package visibility, there is no API for it) and name the tag in docs/sandbox.md.
142. **Confinement leftovers and sharing.** The confined network has fixed names, so two servers on one container runtime
    would remove each other's, and a killed server's job containers keep the network alive until they exit (the next
    start then fails to recreate it). Name it per `RIPPLE_REPLICA` and remove the labelled job containers of that
    replica at start, then test the kill-and-restart case live.
144. **Replicas: elect the other pollers.** Only the Go module feed poll is elected; the toolchain, Actions and Docker
    pollers still run on every replica (safe, wasteful). Give each its own `feed:<name>` lease the way `PollElected`
    does.
145. **DynamoDB: the hygiene commands at scale, and on a schedule.** `ripple dynamodb check-counts`, `recount` and
    `backfill-failed-jobs` find their work by a Scan of the whole table, which has only run against the in-process fake
    (the DynamoDB Local run is CI-only, real AWS never). Measure the Scan and the per-package recount on a large table,
    page them with a rate limit so they cannot starve live traffic, and decide whether the server should run the drift
    check itself (log the gap) instead of leaving it to an operator.
146. **e2e: run the suite with `-count=2` in CI.** Entry 139 made `waitRepo` wait for the first scan; repeating the suite
    would show any test that still depends on a run order or leftover state. Do it once the container-runtimes job is not
    the long pole. The podman fake-runtime container failure that CI missed on PR #53 was not reproduced.
147. **npm: ranges and sections beyond `x.y.z`.** Only a full version with no operator, `=`, `^` or `~` is moved. Partial
    versions (`^1.2`), comparators (`>=1.2.3`), unions, hyphen ranges, a current prerelease, `peerDependencies`,
    `overrides`/`resolutions`, `npm:` aliases and workspace specs are left alone. The target is the newest release only, so a
    repo that disables major updates gets no minor update while a newer major exists; pick the newest target per update type.
148. **npm: regenerate lockfiles offline.** ripple edits package.json and reports the lockfiles it could not refresh
    (`package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, bun). The abbreviated metadata carries
    `dist.integrity` and `dist.tarball` per version, so a package-lock v2/v3 entry for a leaf package whose own dependencies did
    not change could be rewritten without running npm; start there, and say what is still left to the owner.
149. **npm: private registries, and verify against the real one.** A package whose `.npmrc` names another registry is skipped
    (guard against dependency confusion); supporting it needs credentials and a registry setting per scope. Nothing here has run
    against registry.npmjs.org: the client and fake come from the vendored excerpts (scoped-name encoding, 404 for private
    packages, the `deprecated` shape, a `latest` tag that exists are assumed; see UPSTREAM.md). Also: `npm:packages` is one
    read-modify-write cursor (see 151), versions carry no publication date (the full document has `time` but is much larger).
151. **Package cursors: one key per package.** `github-tags:packages` (and `docker:packages`, `npm:packages`) is one
    read-modify-write cursor: replicas can still lose each other's additions until the next scan of a repo that uses the
    package. Actions now prune a package no repo uses (entry 96); the rest needs a per-package key and a store method
    listing cursors by prefix (`internal/store`, all three backends), then `notePackages` moves to it.
153. **Actions: verify against real GitHub and a real GHES.** The workflows-permission refusal text, the upstream fallback
    (`RIPPLE_ACTIONS_UPSTREAM_URL`, 404 on the host then the configured upstream) and superseding of stale actions PRs were
    written against the fake; none ran against github.com or a GHES (see 61). The supersede sweep and closing of beaten PRs
    for actions rely on the branch name `ripple/actions/<slug>-<tag>`.
154. **Cargo: requirements beyond `x.y.z`, and 0.x semantics.** Only a full version with no operator, `=`, `^` or `~`
    is moved. Partial requirements (`1.2`), comparators, wildcards, multi-part requirements, multi-line inline tables,
    dotted keys and workspace-inherited dependencies (`workspace = true`, resolved through `[workspace.dependencies]`) are
    left alone; the manager is a line scanner, not a TOML parser, so a real parser is the way to widen it safely. Also
    `^0.2.3` -> `0.3.0` is breaking for cargo but classified "minor" by the shared rules; classify 0.x the way cargo does.
155. **Cargo: refresh Cargo.lock.** ripple edits Cargo.toml and reports the lock files it left stale (docs/cargo.md);
    a lock entry needs the checksum and dependency edges of the new version, which only cargo's resolver knows. The sparse
    index line carries `cksum` and `deps`, so a lock edit for a leaf crate whose dependencies did not change may be
    possible offline; start there and say what remains.
156. **Cargo: other registries, MSRV, and verify against the real index.** A `registry =` dependency and a repo with
    `replace-with` are skipped, not read from their own index (needs `.cargo/config.toml` parsing and per-registry
    credentials). `rust_version` in the index is ignored, so a bump may need a newer compiler than the repo's
    `rust-version`. Nothing here has run against index.crates.io: the client and fake come from the vendored excerpts
    (mixed-case names, 404 for a missing crate, large files, conditional requests are assumed; see UPSTREAM.md). Also:
    `cargo:packages` is one read-modify-write cursor (see 96), versions carry no publication date, and the PR footer
    still says "module index" (see 99).
157. **Event numbering: measure the hot counter, and the marker backfill.** The event sequence counter is one item written once
    per event, and every event now also writes and later deletes a marker (`REPOEVENTS#<repo>`); at webhook rates that is
    fine, but neither was measured under contention against DynamoDB Local or real DynamoDB. The marker backfill reads the
    eventually consistent index once, so an event an older replica wrote just before the first check can lack a marker until
    that replica's version is gone; Postgres also numbers a duplicate delivery (a gap, harmless).
