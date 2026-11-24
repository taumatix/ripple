# Roadmap

Ordered by how much each entry limits real deployments. The top is next.

1. **Linux sandbox that can reach the proxy.** `RIPPLE_SANDBOX=bwrap` cannot confine the network to the
   module proxy, so `go` refuses to run under it, and bwrap has never run on a Linux host. Give it a
   network namespace with a forwarded socket to the gate (or run `go` with a prefilled module cache),
   and add a Linux CI job that runs the sandbox end-to-end tests. See [docs/sandbox.md](docs/sandbox.md).
3. **Finish the image smoke test.** The release workflow ran for real on v0.3.0 (tag trigger, five binaries, a
   multi-arch GHCR image, the release, and the `verify` job all green; the GHCR package answered an anonymous
   pull). What is left is `internal/smoke`: it cannot assert the PR's `go.mod` content because the
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
    ([docs/platform-codecommit.md](docs/platform-codecommit.md)). The AWS Emulator now runs the API half
    (list, create, list-open, changed files, close) and checks the git credential is accepted
    ([docs/aws-emulator.md](docs/aws-emulator.md)); it cannot say whether real AWS agrees. Needs a
    sandbox account: confirm SigV4 API calls and the git password are accepted, the title limit
    (100 or 150), the EventBridge input shape through SNS, the cn and eusc endpoints, and a shallow
    checkout and push. Residuals: one AWS account per ripple, labels and auto-merge do not exist on the
    service, the SNS certificate chain is not validated beyond the signature.
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
29. **More datasources and feeds, as many ecosystems as Renovate has.** ~~npm registry~~ (shipped with 30c, polled per package; the replicate feed is not used), ~~PyPI~~ (shipped with 30e, polled per project), ~~crates.io
   sparse index~~ (shipped with 30d, polled per crate), Maven, NuGet, RubyGems, Packagist, ~~Docker/OCI tags~~ (shipped with 30b, polled per image), GitHub releases/tags, Helm, Terraform
   registry. Where a registry has a changes feed, use it; where it has none (OCI registries), fall back to polling
   only the packages the reverse index says someone imports, never every repo. Each datasource says which of the two it is.
30. **More managers, toward Renovate parity.** ~~GitHub Actions~~ shipped ([docs/github-actions.md](docs/github-actions.md)).
   ~~30b Dockerfile~~ shipped ([docs/docker.md](docs/docker.md)). ~~30c npm~~ shipped ([docs/npm.md](docs/npm.md)). ~~30d Cargo~~ shipped ([docs/cargo.md](docs/cargo.md)). ~~30e pip~~ shipped ([docs/pip.md](docs/pip.md)). Remaining, by value, each its own entry: 30f Helm, and the long tail.
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
52. **OSV: what a live run still cannot show.** On 2026-11-24 a ripple run pointed at real OSV, the record parser, querybatch
    against ripple's own evaluator, the CSV order and the 404 were all run live (`live.yml`, UPSTREAM.md `verified-live`).
    Still open: a querybatch result large enough to paginate (no Go query returned `next_page_token`), rate limits and
    abuse blocking, the delay between an id in the CSV and its record, and withdrawn records.
58. **Prove the credentialed container path live.** `RIPPLE_GOPROXY_TOKEN` under a container backend (GOPROXY
    userinfo over https) is covered by argv and refusal tests only, not run against a real container and an
    https proxy. Also offer an image with a C toolchain and headers, and a preflight that checks it for `cgo: true`.
59. **Private modules without a proxy.** Modules reachable only by direct VCS fetch are not supported, because
    `GOVCS=*:off` forbids it. Consider `GOAUTH`, or a ripple-run caching proxy fed from the repo's own credentials,
    and make `build`/`vet` stop asking the proxy for `.info` so a proxy that rejects it cannot fail verification.
60. **Dead-letter residuals.** The only metrics are the two `GET /replica` fields (`events_dead_lettered`, `events_dead`); there is
    no Prometheus-format endpoint or latency histogram. A rescan lists the whole live event set every 30s: on
    DynamoDB use a stream or a narrower index, and on Postgres LISTEN/NOTIFY. DynamoDB's due-job exclusion
    is a filter applied after the read, so it still reads the excluded repo's items. If persisting the dead state
    fails the event stays live and is retried after a restart rather than looping.
61. **Run the GHES support against a real GHES.** It is written from the docs and a fake. Check on a real instance (a trial
    licence or a sponsored one): the first release that accepts `2026-03-10`, whether `enablePullRequestAutoMerge` exists
    and what an absent one answers, a disabled GraphQL/REST status, and the webhook payloads; then record the facts in
    `UPSTREAM.md` and `docs/vendor/ghes`.
63. **Several GitHub hosts: more than one extra, and github.com as the second.** One extra GitHub is served as
    the host `ghes` (`RIPPLE_GHES_*`, see docs/platform-github-enterprise.md), with the API, GraphQL and git bases
    derived from a GHES root. Still open: a third and later GitHub (the registry holds one builder per name, so it
    needs a name per host, e.g. `RIPPLE_GITHUB_HOSTS=a,b` with `RIPPLE_GITHUB_<NAME>_*`), a second host that is
    github.com itself (`api.github.com`, not `/api/v3`), and the installation id, which the queue's per-installation
    limit counts across hosts although two GitHubs can reuse a number.
67. **Dependency dashboard: a "re-run now" control, and more hosts.** The dashboard (opt-in
    `dependency-dashboard`) is a read-only report. A checkbox that releases a deferred job early needs the
    `issues` webhook, a check of the editor's repository permission (never the issue's text) and an audit
    line; do that or leave it out for good. Only GitHub has an issue tracker implementation: GitLab, Gitea and
    Forgejo issues, and Azure Boards, are the same `platform.IssueTracker` interface (each needs its issues API
    read from the vendored docs and verified against a real instance, see 61). Also list ripple's open PRs, so
    the issue answers "what is ripple waiting on" completely: that needs a platform listing on every sync and a
    refresh when a PR closes. (Shipped: an invalid config is now said on an existing dashboard.)
76. **Remove the `Ref` overload on `model.Event`.** `Ref` is still populated beside the typed fields and marked
    deprecated; the typed fields have not been in a release yet (they ship in the next minor, v0.3.0), so nothing is
    removable until the minor after, v0.4.0. By then, remove the `Ref` use for `repo_renamed` / `default_branch_changed`
    in `internal/app` (`typedOrLegacy`, the enqueue that fills it) and the marker, if no deployment's log shows the WARN
    `queued event has no typed field; using the deprecated Ref` (`ripple:deprecated event-ref-overload` in
    MIGRATION.md). The `ref` column stays: it is a push's ref.
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
101. **Replicas: soak against real databases.** The shared suite races goroutines on one store handle and the
    e2e runs two replicas; neither is a real multi-node Postgres or DynamoDB under load, or with a skewed clock.
    Run three replicas against both with a clock offset and check one PR per job and ordered events.
113. **DynamoDB: throttling and index lag against a real table.** `ThrottleNext` and `HoldIndex` model the errors and
    the stale reads, not their timing or frequency. DynamoDB Local and the AWS Emulator (verified 2026-11-24, see
    [docs/aws-emulator.md](docs/aws-emulator.md)) both pass the whole store suite but neither throttles nor lags
    its index, so the sharded and striped paths are now checked for correctness against two backends and still for
    behaviour under load against none. Run them against a real on-demand table once (a throwaway account, a
    bounded run) and record the retry counts, the index lag a recount or a `ListDependents` sees, and whether the
    SDK's three attempts are enough for a burst.
114. **DynamoDB: tune and measure counter striping.** `RIPPLE_DYNAMODB_COUNTER_STRIPES` defaults to 1 and the
    estimate that avoids summing the stripes on every write is a heuristic. The emulator proves stripes stay
    correct (TestDynamoCounterStripes* pass against it) but has no throttling model, so it cannot say whether
    stripes relieve a hot partition; that needs a real table. Measure write throttling on one hot package with and
    without stripes, choose a default or document how to size `n` from the dependent count, and consider batching
    the counter per scan (already one ADD per package per scan) before striping.
118. **Workspace-only graphs against a real repo.** A requirement on a sibling at a version no proxy serves
    still opens a draft: with the workspace on, Go reads the sibling's `.mod` at the required version and gets a 404
    (reproduced against the fake proxy). `go work sync` is skipped for such workspaces and a member's own `vendor/`
    is not refreshed (its `modules.txt` would record the temporary replace). Verify against a real multi-module
    repo and consider a `replace`-free `go work vendor` per member.
119. **`tidy: minimal` evidence.** It stays opt-in: no real PR diffs exist to show the default would lose less
    than it risks (a kept requirement with missing go.sum lines opens a draft; the default `go mod tidy` result is
    what a maintainer reproduces by hand). Collect the diffs of repos that opted in and revisit.
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
126. **OSV: reconcile older cases.** A pending job made before the `osv-jobs` index is now indexed by a one-time backfill; a
    done job (and so its open PR) from before the index is still not found, because the store lists only pending and failed
    jobs per repo. A PR body is not rewritten when a record is corrected (the fix version or
    summary in it can be stale), and a record shared by twin GHSA and GO notes is reconciled per note. The busy-repo retry
    list holds at most 100 ids.
127. **OSV: conflict and pseudo-version residuals.** The beaten-PR close covers a non-security PR when a security
    toolchain PR opens (a lower security toolchain PR beside a higher one is closed by the supersede flow, pinned by an e2e
    test), and a toolchain PR opened by an older run than the security job is only held, never closed, unless the security PR opens in a later run with it open.
    A plain, non-advisory release move off a pseudo-version is still not implemented; rule to use: the first release above
    the pseudo-version and released after its timestamp, opt-in.
132. **Webhook retry ids, verified on real hosts.** Delivery ids are now persisted (store delivery table), but whether
    Bitbucket Cloud's `X-Request-UUID` and Azure DevOps' payload `id` stay the same across a retry is still
    unverified: the vendored docs state neither (see docs/platform-bitbucket.md, docs/platform-azuredevops.md). A
    retry under a new id is handled again, which is safe. Needs a real workspace and organisation to observe.
133. **Signature dedupe: the default.** `RIPPLE_WEBHOOK_DEDUPE_SIGNATURE=true` now spans replicas (the signed-body key is
    claimed in `store.DeliveryTable` beside the delivery id), but it is still off by default. Turn it on by default
    only once real redelivery data shows identical signed bodies are rare; that needs a real deployment, and flipping
    it needs a `MIGRATION.md` note.
140. **Credentialed private proxy under a confined container network.** `RIPPLE_CONTAINER_CONFINE` is refused with
    `RIPPLE_GOPROXY_TOKEN`: go sends GOPROXY userinfo only over https and the sidecar speaks http. Give the sidecar
    TLS (a per-start certificate the image trusts) or have it add the Authorization header for the steps that may carry
    it, without sending the credential to build and vet. Entry 58 needs this to be proven live.
141. **Publish the runner image.** `docker/runner/Dockerfile` is built in the `container-runtimes` CI job and documented, but
    no image is published: build and push it to GHCR on the release train (public, since it holds only upstream
    images; check the package visibility, there is no API for it) and name the tag in docs/sandbox.md.
148. **npm: regenerate lockfiles offline.** ripple edits package.json and reports the lockfiles it could not refresh
    (`package-lock.json`, `npm-shrinkwrap.json`, `yarn.lock`, `pnpm-lock.yaml`, bun). The abbreviated metadata carries
    `dist.integrity` and `dist.tarball` per version, so a package-lock v2/v3 entry for a leaf package whose own dependencies did
    not change could be rewritten without running npm; start there, and say what is still left to the owner. `npmreg` parses
    neither `dist` nor per-version `dependencies` yet (nor does the fake publish them): add both first.
149. **npm: private registries.** A package whose `.npmrc` names another registry is skipped (guard against dependency
    confusion); supporting it needs credentials and a registry setting per scope. The public registry is verified live
    (2026-11-24, UPSTREAM.md); not verified: private registries and their 401/403 answers, rate limits, and a very large
    package. Also: `npm:packages` is one package set kept per package (see 159), versions carry no publication date (the
    full document has `time` but is much larger).
153. **Actions: verify against a real GHES and the write paths.** The tag, commit and release reads from github.com were run live
    (2026-11-24, UPSTREAM.md). Still written against the fake only: the workflows-permission refusal text, the upstream fallback against
    a real GHES (`RIPPLE_ACTIONS_UPSTREAM_URL`, 404 on the host then the configured upstream) and superseding of stale actions
    PRs on any real host (see 61). The supersede sweep and closing of beaten PRs for actions rely on the branch name
    `ripple/actions/<slug>-<tag>`. Anonymous reads are 60 an hour per address, so a shared runner can be refused; the client
    reports it as a rate limit.
155. **Cargo: refresh Cargo.lock.** ripple edits Cargo.toml and reports the lock files it left stale (docs/cargo.md);
    a lock entry needs the checksum and dependency edges of the new version, which only cargo's resolver knows. The sparse
    index line carries `cksum` and `deps`, so a lock edit for a leaf crate whose dependencies did not change may be
    possible offline; start there and say what remains.
156. **Cargo: other registries and MSRV.** A `registry =` dependency and a repo with `replace-with` are skipped, not read
    from their own index (needs `.cargo/config.toml` parsing and per-registry credentials). `rust_version` in the index is
    ignored, so a bump may need a newer compiler than the repo's `rust-version`. index.crates.io is verified live
    (2026-11-24); not verified: another registry, the largest crates, conditional requests, rate limits. Also:
    `cargo:packages` is one read-modify-write cursor (see 96), versions carry no publication date, and the PR footer
    still says "module index" (see 99).
157. **Event numbering: measure the hot counter, and the marker backfill.** The event sequence counter is one item written once
    per event, and every event now also writes and later deletes a marker (`REPOEVENTS#<repo>`); at webhook rates that is
    fine, but neither was measured under contention against DynamoDB Local or real DynamoDB. The marker backfill reads the
    eventually consistent index once, so an event an older replica wrote just before the first check can lack a marker until
    that replica's version is gone; Postgres also numbers a duplicate delivery (a gap, harmless).
158. **Replicas: elect the advisory poll, and say which sources a replica polls in the fleet count.** The OSV advisory poll
    (`pollAdvisories`, its own loop) still runs on every replica. Each source is elected on its own lease, so two replicas
    starting together may split the sources; `feed_pollers` counts replicas polling any source and cannot say which one is
    unpolled. Heartbeat the set of sources (needs a `store.ReplicaInfo` field and a column or attribute per store) so the
    fleet view can alert on a source nobody polls. Also: `App.Tick` (cron-driven) now goes through the elections too.
160. **pip: Poetry and other manifests, wider specifiers.** Poetry's `[tool.poetry.dependencies]` (caret and tilde
    ranges), `Pipfile`, `setup.cfg`, `setup.py`, `[build-system] requires`, `.in` files and constraints files are not read;
    nor are `>=1,<2` style ranges, `==1.*` and `!=`. The manager is a scanner for PEP 508 requirements and a line scanner
    for TOML, not a parser, so a real TOML parser is the way to widen it safely.
161. **pip: refresh lock files, Requires-Python, and other indexes.** `poetry.lock`, `uv.lock`, `pdm.lock` and a
    pip-compile output are reported, not regenerated (docs/pip.md). `Requires-Python` is ignored, so a bump may need a
    newer interpreter. A dependency on a private index is skipped, not read from its own index (needs per-index
    credentials and a PEP 503/691 client); the guard also skips a file that names `https://pypi.org/simple` explicitly.
162. **pip: compare PEP 440 with the `packaging` library.** pypi.org is verified live (2026-11-24: yanked, fileless and
    pre-release handling, non-normalized names, 404, and that every version of four large projects parses). Still open:
    ordering was only checked for antisymmetry, not against `packaging` on a corpus; `releases` is documented as deprecated
    in favour of the PEP 691 index and could be dropped; rate limits and private indexes.
163. **Cargo: wildcards, comparators, and a real TOML parser.** Partial requirements (`1.2`) and 0.x semantics are done
    (docs/cargo.md). Still left alone: wildcards (`1.*`), comparators (`>=1.2`), multi-part requirements (`>=1, <2`),
    multi-line inline tables and dotted keys (`foo.version = "1"`); the manager is a line scanner, not a TOML parser, so a
    real parser is the way to widen it safely. The same newest-only target still applies to npm (see 147).
164. **npm: comparators, unions, and other sections.** Partial ranges, x-ranges and the newest target per update type are
    done (docs/npm.md). Still left alone: comparators (`>=1.2.3`), unions (`1.2 || 2`), hyphen ranges, a current
    prerelease, `peerDependencies`, `overrides`/`resolutions`, `npm:` aliases and workspace specs. Also npm classifies by
    number position, so `0.2.3` -> `0.3.0` is "minor" (Cargo has used the leftmost-nonzero rule since 154); do the same
    here once a repo reports it.
165. **Docker: Helm values, Kustomize and per-stage ARGs.** Compose files, Kubernetes manifests, `COPY --from` and global
    `ARG` tags are read (docs/docker.md). Not read: Helm `values.yaml` (`repository:`/`tag:` pairs), Kustomize `images:`,
    `ARG` declared after a `FROM`, an ARG that is only part of a tag, and `RUN --mount=from=`.
166. **Docker: credential helpers, identity tokens, per-repo logins.** `RIPPLE_REGISTRY_AUTH_FILE` reads inline `auths` only
    (docs/docker.md). Left: `credsStore`/`credHelpers` (run `docker-credential-*`; needs the sandbox's care, since the helper
    is a program), `identitytoken` (the OAuth2 refresh-token grant against the token endpoint, for Azure and some Docker Hub
    setups), a login limited to the repos that name it in `.ripple.yaml` (today one login serves every repo the server reads, so
    a repo can make ripple present it to a registry only by naming that registry's images), reloading the file without a restart,
    and a real private registry run (see 130).
167. **Delivery dedupe: SCM-Manager without changeset ids, and retiring the legacy tombstone name.** GitLab now keys
    its delivery on `webhook-id` (else `Idempotency-Key`), so a signed redelivery is dropped by any replica; an
    SCM-Manager payload with no changeset ids still lacks a safe key. Removal tombstones moved to `removed#<name>` and
    the Dynamo sweep is a Query; the legacy `removed|` read path and the `tombstones-legacy` marker can be deleted a
    release after every replica runs a version that no longer writes it (v0.3.0 wrote it; the release after it must
    ship first). The Query of `removed#` on a very large table is unmeasured.
170. **Generators: run the rewritten lines.** The mockgen (`-self_package`, golang/mock flags) and stringer grammar
    comes from the vendored READMEs and flag declarations, and is checked by rewriting text; no rewritten
    `go:generate` line has been run with the real tool. Run `go generate` in a sandbox job over a repo using
    each, and add `-write_command_comment` and the other flags newer mockgen releases add as they are documented.
171. **Go toolchain downloads, and published runner images.** The per-minor images (`RIPPLE_CONTAINER_GO_IMAGES`) are
    built by the operator, and the host sandboxes still only run releases an operator installed in
    `RIPPLE_GO_TOOLCHAIN_DIR`. A proxy-fed download (`golang.org/toolchain@v0.0.1-go<version>.<os>-<arch>` through
    `RIPPLE_GOPROXY`, checked against the checksum database by `go mod download`, unpacked as `go<version>/bin/go`) would
    remove the manual install; publish `ripple-runner:<minor>` images from CI too. The image selection was verified
    against the fake runtime only; CI builds each minor and runs `go version` in it but no real runtime has run a
    ripple verification in a per-minor image.
172. **OSV: security-bypass by severity.** `security-bypass` (entry 51) is per update entry and all or nothing. A
    `severity: [high, critical]` qualifier (bypass only for those) and a per-advisory cap on PRs opened past the limit were left
    out until real use shows whether a repo wants them.
173. **Resolve an id mismatch by asking the host.** A repo added by `installation_repositories` or `installation`
    now stores its id (GitHub carries it in every added-repository object), but a signed push whose id differs from
    the stored one is still ignored, not resolved with a lookup of the name on the host. A GitHub lookup
    (`GET /repositories/{id}`) would say which name the id holds now and settle a missed rename or a reused name.
174. **Archive on the hosts that still do not report it.** GitLab, Gitea/Forgejo and Bitbucket Data Center now read an
    archive from their listing (entry 107); the Lister hosts SCM-Manager and Gerrit still drop an archived repo from
    the listing, which forgets it with its jobs. SCM-Manager's repository object has an archived flag in its source
    notes only, and Gerrit's read-only project state (`GET /projects/?state=READ_ONLY`) is not vendored: read both
    from vendored docs, then implement `platform.ArchivedLister`. Bitbucket Cloud and Azure DevOps (whose
    `isDisabled` is a different state) document no archive at all in `docs/vendor`; nothing to build for them.
175. **Archive from the listing against real hosts.** The listing reads (`archived=true` on GitLab, the `archived` flag
    of Gitea/Forgejo, `archived=ARCHIVED` on Data Center 8+) ran only against fakes; also check that GitLab's
    `simple=true` listing under `archived=true` answers what the doc says, and that a Data Center before 8.0 lists no
    archived repo. Folds into entry 92's real-host run.
176. **DynamoDB hygiene: measure it, and stop scanning for the package list.** Paging and the server's drift check (entry 145)
    exist but were only run against the in-process fake, DynamoDB Local and the AWS Emulator (in-memory, so its timings say nothing about AWS); nobody has timed a Scan or a per-package recount
    on a table with millions of items, or on real AWS with provisioned capacity, so the suggested page and pause are
    arithmetic, not measurement. The check still finds the packages by Scanning every item for `DEP#` keys: keep the set of
    packages with dependents in a small index item (or a sparse GSI) so the check reads that instead. The server only
    reports drift; whether it should recount a package it finds drifted on its own (rate-limited, one replica at a time
    under the lease) is undecided.
179. **Supersede on a later run.** Within one run, PRs opened for the same module are now closed by the next one (fixed
    with the live OSV run). A sweep on a later run did not close lower PRs in one observed case after a rerun and a tick;
    reproduce it with a fake and either fix it or explain it in docs/osv.md.
184. **Retry-After in the other clients.** `internal/httpretry` now waits out a 429 (and a 503 with Retry-After) for the
    Go index, the Go proxy lookup, OSV and the registry client; `npmreg`, `pypi`, `cratesindex` and the GitHub tag reads
    still treat both as a plain error and retry on the next poll. Give each an `httpretry.Policy` field and a fake test.
185. **Live: a retracted `@latest` and a prerelease-only module.** The fake and unit tests cover a module whose `@latest`
    is retracted (it proposes the newest version that is not), all versions retracted (the module is still answered) and
    prerelease-only; `go.mongodb.org/mongo-driver` is the one real retraction fixture in `live.yml`. Find a real module whose
    highest stable version retracts itself and a real prerelease-only module and pin them read-only, so the proxy's own
    `@latest` behaviour for them is observed, not assumed.
186. **Registry tag lists: stop early.** A 429 or a slow page is now waited out or ended by the context, but a large image
    (ghcr.io, ~20s over many pages) is still read to the last page. Stop at the first tag newer than the current one of the
    same shape, or page with `last=` from the current tag, and measure it against the registry fake with a page counter.
187. **SCM-Manager: head commit on the open-PR listing.** `platform.PR.HeadSHA` is the one host field still empty on
    every host that lists open PRs: the vendored SCM-Manager docs show no source-revision field on an in-progress
    pull request (the Review plugin's history tracks "source branch revision" but its list shape is not documented
    here). Read a real list answer (or the plugin source), fill it if a field exists, else keep a touched PR
    touched until it closes and say so in docs/platform-scmmanager.md.
188. **Emulator: read what the first run says about the whole clone.** `gitws.Checkout` now falls back to a whole
    clone when shallow is refused, and the emulator CodeCommit test accepts either outcome (it asserts the
    checkout's files if the emulator serves a whole clone, logs the refusal if not). Read the `codecommit` job's
    log: if the clone worked, extend the test to commit and push a ripple/ branch and open the PR through the host
    (closing the last CodeCommit gap in ROADMAP 16); if it did not, record the emulator's answer in
    docs/aws-emulator.md and stop.
189. **DynamoDB: the registration window against real index lag.** `ListDependents` adds the rows registered in the last five
    minutes (docs/store-dynamodb.md), a constant chosen far above the usual index lag and proved only against the fake's
    `HoldIndex`, which freezes the index outright. Nobody has measured real GSI propagation delay (or the cost of the extra
    Query and BatchGetItem on a hot package during a mass install) on AWS; make the window a setting if it proves wrong.
    A dependency a lagging index still lists after the repo dropped it is the mirror case and is not covered.
190. **Package cursors: bound the listing.** Every poll lists the whole `<cursor>#<package>` set of each datasource (one
    paginated index Query on DynamoDB) and takes a debug line with the size and elapsed time ("polled packages listed"); the
    legacy list is folded and deleted on first read and every datasource prunes packages nobody uses. Not done: reading the
    set in slices across polls so a very large install does not list every package each time, and any measurement on a real
    install. A fold racing a replica still running the old version can leave a package only in the legacy cursor until the
    next read folds it again. Cursors named `<group>#<key>` written before per-package keys carry no index attributes on
    DynamoDB and are not listed by group; none existed.
191. **Webhooks: what the streamed parse still costs.** Every receiver now parses a signed body as it streams
    (`webhook.Extractor`), so none holds one whole. Left: the extractor keeps up to 16 MiB of values while the signature is
    unchecked, so a forged body with one huge field it reads (a `ref`) costs that much per inflight request, up from
    64 KiB; measure on a busy receiver and lower `maxExtract` or cap per-field length if it matters. GitLab with a plain
    secret token (no signing key) still reads its header-authenticated body whole before parsing it; Azure DevOps,
    Gerrit, SCM-Manager and CodeCommit authenticate from headers and read whole, bounded by their `maxBody`.
192. **Backfill lookup: real proxy spot-checks.** The far-major probe (`RIPPLE_BACKFILL_MAJORS`) and the lookup are
    pinned against the fake proxy only. A prerelease-only real module and a real retraction have not been tried
    against proxy.golang.org, and a raised probe has no measured cost on a repo with hundreds of new dependencies
    (each far major is one proxy request of the 20 a scan allows).
