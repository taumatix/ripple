# Roadmap

Ordered by how much each entry limits real deployments. The top is next.


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
12. ~~**Gitea/Forgejo against real instances.**~~ Shipped: `hosts-gitea.yml` runs the conformance suite and `TestReal*`
    against Gitea 1.27.3, Forgejo 17.0.2 and Forgejo 7.0.16 ([docs/platform-gitea.md](docs/platform-gitea.md) "Real
    instances", recorded answers in `docs/vendor/gitea/real-answers.md`). Left: Forgejo before 7 still cannot be
    detected (set `RIPPLE_GITEA_FLAVOUR`); the rest is ROADMAP 281 to 283.
13. **Bitbucket Cloud against a real workspace.** The host shipped from vendored docs and a fake only
    (docs/platform-bitbucket.md "Unverified"). Record the 429 headers, the 202 of a queued merge, `x-token-auth`
    with an API token, and merge-commit diffstats. Also: auto-merge exists only through merge queues, so a
    merge-check poll is the open design question if queues stay a paid feature; webhooks are created by hand,
    so per-workspace webhook registration is a follow-up.
14. **Azure DevOps follow-ups** (the host itself shipped, see [docs/platform-azuredevops.md](docs/platform-azuredevops.md)):
    verify it against a real organisation (every wire shape is from the docs and a fake: repo name accepted as
    `repositoryId`, `autoCompleteSetBy` taken from `createdBy.id`, the exact service-hook headers, the
    `git.repo.created`/`deleted`/`renamed` payloads, and spaces in names as `%20` in REST and clone URLs); the
    commit-changes lookup (merge commits, paging past one page of changes) is unverified against a real
    organisation.
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
    checkout (the emulator refuses shallow and serves the whole clone and the push, so only real AWS can say that shallow is served). Residuals: one AWS account per ripple, labels and auto-merge do not exist on the
    service, the SNS certificate chain is not validated beyond the signature.
17. **Gerrit: what the real run left open.** The conformance suite now runs against a real Gerrit 3.15.0 in CI
    ([docs/platform-gerrit.md](docs/platform-gerrit.md), Verified). Still unrecorded: a 429 and its `Retry-After`,
    WIP, hashtag and abandon permission for an ordinary (non-admin) account, "no new changes" on identical re-push,
    merge-commit file lists. Residuals: auto-merge through submit-on-label if the docs ever support it,
    `stream-events` as an alternative to webhooks, learning default branches at discovery.
290. **Gerrit: run the real job as an ordinary account.** The CI account is an administrator with push on
    `refs/heads/*`; give it only read and push to `refs/for/*` plus Forge Author and rerun, so WIP, hashtag and
    abandon permission and the minimum grant the docs state are checked rather than assumed.
291. **Gerrit: default author email.** The default `ripple@users.noreply.invalid` cannot be registered on a real Gerrit
    (`.invalid` is refused) and needs Forge Author; `RIPPLE_GERRIT_AUTHOR_EMAIL` is the way out. Decide whether the
    default should change (additive only: a breaking default needs a deprecation) or startup should warn.
18. **SCM-Manager against a real instance.** The host shipped from vendored docs, the plugins' source and a
    fake only (see [docs/platform-scmmanager.md](docs/platform-scmmanager.md)). Verify on a real SCM-Manager 3.x
    with the Review and Webhook plugins: `Authorization: Bearer <api key>` on the REST API, git basic auth with
    an API key, the webhook plugin's custom-header table and `branches` in its changesets, the 201 `Location`,
    modifications of a merge commit, `shouldDeleteSourceBranch`, and what a rate limit looks like if any.
    Then: create the webhook through the plugin's REST API instead of by hand (not yet read), and confirm one
    global webhook with `${repository.namespace}` in the URL works.

Everything else:

29. **More datasources and feeds, as many ecosystems as Renovate has.** ~~npm registry~~ (shipped with 30c, polled per package; the replicate feed is not used), ~~PyPI~~ (shipped with 30e, polled per project), ~~crates.io
   sparse index~~ (shipped with 30d, polled per crate), ~~Maven~~ (shipped as the maven manager, [docs/maven.md](docs/maven.md), polled per artifact), ~~NuGet~~ (shipped as the nuget manager, [docs/nuget.md](docs/nuget.md), polled per package id), ~~RubyGems~~ (shipped as the bundler manager, [docs/bundler.md](docs/bundler.md), polled per gem), ~~Packagist~~ (shipped as the composer manager, [docs/composer.md](docs/composer.md), polled per package), ~~Docker/OCI tags~~ (shipped with 30b, polled per image), GitHub releases/tags, ~~Helm~~ (shipped with 30f, polled per repository index), ~~Terraform registry~~ (shipped as the terraform manager, [docs/terraform.md](docs/terraform.md), polled per module or provider). Only GitHub releases/tags remains. Where a registry has a changes feed, use it; where it has none (OCI registries), fall back to polling
   only the packages the reverse index says someone imports, never every repo. Each datasource says which of the two it is.
30. **More managers, toward Renovate parity.** ~~GitHub Actions~~ shipped ([docs/github-actions.md](docs/github-actions.md)).
   ~~30b Dockerfile~~ shipped ([docs/docker.md](docs/docker.md)). ~~30c npm~~ shipped ([docs/npm.md](docs/npm.md)). ~~30d Cargo~~ shipped ([docs/cargo.md](docs/cargo.md)). ~~30e pip~~ shipped ([docs/pip.md](docs/pip.md)). ~~30f Helm~~ shipped ([docs/helm.md](docs/helm.md)). Remaining: the long tail, each its own entry.
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
60. **Dead-letter residuals.** `RIPPLE_METRICS` now serves a Prometheus endpoint (dead-letter counts, takeovers, request latency by route),
    but its histogram times only the HTTP acknowledgement: add event-processing and job latency, and queue-depth
    gauges. A rescan lists the whole live event set every 30s: on
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
229. **Gerrit reports no PR description, so only its head is compared.** `platform.PR.Body` is filled by every other
    host's PR read and the engine now treats a stale PR whose head or body hash differs from its job's as touched
    (entry 94, second half). Gerrit's change has no description (the body ripple posts is the first change
    message), so a message edit there is invisible to the comparison; a second read of the change's messages would
    close it. A label, assignee or reviewer change on GitLab and Gerrit stays outside it. The comparison guards
    the closer only: ripple never force-pushes an open PR's branch today, so there is no second site to guard.
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
123. **Dashboard: read-back edge cases against a real host.** The refresh and watch sets are now one cursor per repo, so
    two replicas marking at once cannot lose an entry. Read-back finds the issue by its marker, so an
    edit that removes the marker is repaired by number and one that deletes the issue is recreated, both
    covered only against the fake.
125. **OSV: sweep residuals.** The DynamoDB repo index (`REPOS` items) is built by one Scan the first time a listing is asked
    for; that Scan and the listing are verified on the in-process fake (including that the build runs once and reruns after a failed one), and DynamoDB Local runs them only in CI. A scan
    (not the sweep) still runs one history round, so a repo with over 500 pairs or 100 records is completed only by a sweep
    or a go.mod change; consider a few rounds per scan once real repo sizes are known. Postgres `ListRepos` is CI-only.
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
153. **Actions: verify against a real GHES and the write paths.** The tag, commit and release reads from github.com were run live
    (2026-11-24, UPSTREAM.md). Still written against the fake only: the workflows-permission refusal text, the upstream fallback against
    a real GHES (`RIPPLE_ACTIONS_UPSTREAM_URL`, 404 on the host then the configured upstream) and superseding of stale actions
    PRs on any real host (see 61). The supersede sweep and closing of beaten PRs for actions rely on the branch name
    `ripple/actions/<slug>-<tag>`. Anonymous reads are 60 an hour per address, so a shared runner can be refused; the client
    reports it as a rate limit.
155. **Cargo: refresh Cargo.lock.** ripple edits Cargo.toml and reports the lock files it left stale (docs/cargo.md);
    a lock entry needs the checksum and dependency edges of the new version, which only cargo's resolver knows. The sparse
    index line carries `cksum` and `deps`, so a lock edit for a leaf crate whose dependencies did not change may be
    possible offline; start there and say what remains. Looked at 2026-11-24 and left open: `cratesindex.Version` reads
    neither field yet; the Cargo.lock format is not vendored in docs/vendor/cargo (read it first); a transitive
    requirement on the crate (`=1.0.100` in another lock entry) cannot be checked without that crate's index line; the
    sandbox runs `go` and `git`, not cargo, and a `cargo update` needs the index and the crate files, so running it
    means a cargo image and a crates.io proxy in the confinement; and there is no cargo on the development host, so the
    result can only be checked by a live test that runs `cargo metadata --locked` on a runner.
156. **Cargo: other registries, and an MSRV fallback.** A `registry =` dependency and a repo with `replace-with` are skipped, not read
    from their own index (needs `.cargo/config.toml` parsing and per-registry credentials: a human decision on where those
    live, so it stays open). A release whose `rust_version` is newer than the repo's `rust-version` is skipped and the
    newest older release that does build is proposed instead (the retargeted job keeps the refused release's ReleasedAt, so
    the cooldown is measured against it: see 228). index.crates.io is verified live (2026-11-24); not verified:
    another registry, the largest crates, conditional requests, rate limits, `rust_version` on the real index. Also:
    `cargo:packages` is one read-modify-write cursor (see 96), versions carry no publication date, and the PR footer
    still says "module index" (see 99).
157. **Event numbering: measure the hot counter, and the marker backfill.** The event sequence counter is one item written once
    per event, and every event now also writes and later deletes a marker (`REPOEVENTS#<repo>`); at webhook rates that is
    fine, but neither was measured under contention against DynamoDB Local or real DynamoDB. The marker backfill reads the
    eventually consistent index once, so an event an older replica wrote just before the first check can lack a marker until
    that replica's version is gone; Postgres also numbers a duplicate delivery (a gap, harmless).
158. **Replicas: split sources deliberately.** Each source is elected on its own lease, so two replicas starting together
    may split the sources and one replica may hold all of them; there is no preference for spreading them. `App.Tick`
    (cron-driven) goes through the elections too. `sources_unpolled` names a source nobody polls but cannot say how long
    it has gone unpolled; it is verified on the memory store and the in-process DynamoDB, Postgres columns are CI-only.
161. **pip: refresh lock files, and other indexes (narrowed 2026-11-25, nothing built).** Each part waits on something
    a pass cannot supply. (a) Regenerating `poetry.lock`, `uv.lock`, `pdm.lock` or a pip-compile output needs the tool
    and an index at run time; ripple runs neither (docs/pip.md) and none is available offline here, so these stay
    reported under "Lock files not regenerated". Running them in the sandbox needs a human decision on which tools the
    sandbox image carries. (c) A private index needs per-index credentials and a PEP 503/691 client, and a real index to
    test against; the guard also skips a file that names `https://pypi.org/simple` explicitly. (`Requires-Python` is
    done: a release the repo's lowest Python cannot install is skipped, docs/pip.md.)
167. **Delivery dedupe: SCM-Manager without changeset ids, and retiring the legacy tombstone name.** GitLab now keys
    its delivery on `webhook-id` (else `Idempotency-Key`), so a signed redelivery is dropped by any replica; an
    SCM-Manager payload with no changeset ids still lacks a safe key. Removal tombstones moved to `removed#<name>` and
    the Dynamo sweep is a Query; the legacy `removed|` read path and the `tombstones-legacy` marker can be deleted a
    release after every replica runs a version that no longer writes it (v0.3.0 wrote it; the release after it must
    ship first). The Query of `removed#` on a very large table is unmeasured.
171. **Published runner images.** The per-minor images (`RIPPLE_CONTAINER_GO_IMAGES`) are built by the operator;
    publish `ripple-runner:<minor>` images from CI (a registry and its visibility are a human decision). The image
    selection was verified against the fake runtime only; CI builds each minor and runs `go version` in it but no real
    runtime has run a ripple verification in a per-minor image. Host toolchain downloads shipped
    (`RIPPLE_GO_TOOLCHAIN_DOWNLOAD`); left from them: a downloaded release is never pruned, and a pre-release
    (`go1.27rc1`) is not fetched.
175. **Archive from the listing against real hosts.** The listing reads (`archived=true` on GitLab, the `archived` flag
    of Gitea/Forgejo, `archived=ARCHIVED` on Data Center 8+) ran only against fakes; also check that GitLab's
    `simple=true` listing under `archived=true` answers what the doc says, and that a Data Center before 8.0 lists no
    archived repo. Folds into entry 92's real-host run.
185. **Live: a retracted `@latest` and a prerelease-only module.** The fake and unit tests cover a module whose `@latest`
    is retracted (it proposes the newest version that is not), all versions retracted (the module is still answered) and
    prerelease-only; `go.mongodb.org/mongo-driver` is the one real retraction fixture in `live.yml`. Find a real module whose
    highest stable version retracts itself and a real prerelease-only module and pin them read-only, so the proxy's own
    `@latest` behaviour for them is observed, not assumed.
187. **SCM-Manager: head commit on the open-PR listing.** `platform.PR.HeadSHA` is the one host field still empty on
    every host that lists open PRs: the vendored SCM-Manager docs show no source-revision field on an in-progress
    pull request (the Review plugin's history tracks "source branch revision" but its list shape is not documented
    here). Read a real list answer (or the plugin source), fill it if a field exists, else keep a touched PR
    touched until it closes and say so in docs/platform-scmmanager.md.
189. **DynamoDB: the registration window against real index lag.** `ListDependents` adds the rows registered in the last five
    minutes (docs/store-dynamodb.md), a constant chosen far above the usual index lag and proved only against the fake's
    `HoldIndex`, which freezes the index outright. Nobody has measured real GSI propagation delay (or the cost of the extra
    Query and BatchGetItem on a hot package during a mass install) on AWS; make the window a setting if it proves wrong.
    A dependency a lagging index still lists after the repo dropped it is the mirror case and is not covered.
192. **Backfill lookup: real proxy spot-checks.** The far-major probe (`RIPPLE_BACKFILL_MAJORS`) and the lookup are
    pinned against the fake proxy only. A prerelease-only real module and a real retraction have not been tried
    against proxy.golang.org, and a raised probe has no measured cost on a repo with hundreds of new dependencies
    (each far major is one proxy request of the 20 a scan allows).
193. **Generators: regenerate in the PR?** The rewritten `//go:generate` lines are now proven runnable with the real
    mockgen (go.uber.org/mock v0.6.0, github.com/golang/mock v1.6.0) and stringer (`-tags live`), but ripple does not
    run `go generate` after a bump, so a checked-in mock or `_string.go` stays as the old major produced it. Running
    it in a sandbox job executes repo-chosen tools with network, which is a decision to take (opt-in, and which
    sandbox) before building it. Other generators that take an import path (`protoc-gen-go` options, `wire`, `ent`)
    are still only listed.
200. **npm: reading a private registry.** Registry routing is read from `.npmrc`, `.yarnrc` and `.yarnrc.yml` up the tree and a
    package routed off registry.npmjs.org is skipped (docs/npm.md). Not done, because it is a decision and not code: reading
    from the registry a repository names means ripple's host fetches a URL chosen by repository content (an SSRF and
    private-network question) and, for most private registries, holding a credential per registry. Needs a decision first:
    an allowlist of registry hosts in server config (never from the repo), and where tokens live. `bunfig.toml` scopes and
    `publishConfig` are not read. Not verified: a real private registry's 401/403 answers, rate limits, a very large
    package; `npm:packages` is one package set kept per package (see 159) and versions carry no publication date.
201. **npm: lockfiles beyond a plain leaf.** `package-lock.json` / `npm-shrinkwrap.json` (lockfileVersion 2 and 3) are rewritten
    without npm for a hoisted leaf package that nothing else requires, whose new version has the same dependencies and
    install metadata (docs/npm.md). Not rewritten, and reported with the reason: lockfileVersion 1, workspaces, a package
    with dependencies or an install script, one another package requires (its range would have to be checked), a nested
    copy, `yarn.lock`, `pnpm-lock.yaml` and bun. Start with a package that has dependencies (needs the packument of each
    new transitive version and a resolver) or with `yarn.lock`/`pnpm-lock.yaml` leaf entries. Not verified: that `npm ci`
    accepts a rewritten file (npm cannot run in the sandbox, which runs only go and git), that the registry's abbreviated
    metadata carries `os`, `cpu` and `hasInstallScript` for every package (the shape check relies on them), and that
    dropping `license`/`funding` changes between versions is harmless. Running npm itself needs a sandbox profile with
    the registry and no repository secrets: a decision for the owner.
203. **Docker: credential helpers, per-repo logins, reload.** `RIPPLE_REGISTRY_AUTH_FILE` reads inline `auths` (user and
    password, `auth`, `identitytoken`; docs/docker.md). Left: `credsStore`/`credHelpers` (run `docker-credential-*`, a program
    that would need the sandbox's care and a human decision on running it), a login limited to the repos that name it in
    `.ripple.yaml` (today one login serves every repo the server reads, so a repo can make ripple present it to a registry
    only by naming that registry's images), reloading the file without a restart, and a real private registry run (see 130),
    which is also the only check of the refresh-token grant against Azure or Docker Hub.
204. **Repository ids: a missed rename by id, and the other hosts.** A signed push whose repository id differs from
    the stored one is settled by asking the host (`platform.RepoIdentifier`: GitHub `GET /repositories/{id}`, GitLab
    `GET /projects/{id}`, Gitea/Forgejo `GET /repositories/{id}`), but only when the id names this repo's own name
    (a recreated repo). Not done: the host answering with a different name for the *stored* id (a rename missed
    twice), and an implementation for Bitbucket, Azure DevOps, Gerrit, SCM-Manager and CodeCommit, which keep
    ignoring a mismatch as before. The GitLab and Gitea lookups (and Gitea's `repository.id` payload field and
    `/repositories/{id}` route, which the vendored excerpts do not cover) ran only against the fakes.
205. **Webhooks: the rest of the streamed-parse cost.** One string value or key is now capped at 16 KiB while the signature is unchecked, so a forged body with one huge `ref` costs 16 KiB, not up to 16 MiB; the whole budget (`maxExtract`, 16 MiB, reachable by many fields each under the cap) is unchanged and unmeasured on a busy receiver. Azure DevOps, Gerrit, SCM-Manager and CodeCommit authenticate from headers and read whole, bounded by their `maxBody`.
206. **DynamoDB hygiene: measure it, and decide on self-repair.** The hygiene commands and the server's drift check now list packages from the `PKGS` index (one Query; a table from before it is Scanned once, ROADMAP 176) instead of Scanning every item for `DEP#` keys. Still nobody has timed the check or a per-package recount on a table with millions of items or on real AWS with provisioned capacity, so the suggested page and pause are arithmetic, not measurement; every `PKGS` item shares one partition key, which a table with millions of packages would make a hot read partition (shard it by hash if it shows), and a package an older-version replica introduces after the marker is missed until the marker is deleted. The server only reports drift; whether it should recount a package it finds drifted on its own (rate-limited, one replica at a time under the lease) is undecided.
219. **Corpus checks: from a real sample, and what the first one found.** `internal/manager/testdata/corpus` holds manifests written
    the way ripgrep, tokio, express, bitnami and ingress-nginx write theirs, with reviewed `.want` files (`go test ./internal/manager
    -run TestCorpus -update-corpus` rewrites them); they were written from memory and the documented formats, not downloaded, so the
    miss rate over real crates.io, npm and Hub files is still unmeasured. The first two misses it found are fixed (an ingress-nginx
    style `registry:`/`image:`/`tag:` block, and a calendar tag with a zero-padded number, `ubuntu:22.04`). npm still classifies `0.2.3` -> `0.3.0` as minor (Cargo uses leftmost-nonzero);
    a Dockerfile `FROM` with a `v${V}` ARG prefix and Helm `global.imageRegistry` indirection stay unread. The Cargo `*`/lone `<`
    and npm hyphen, lone `>`, peer and `overrides` forms stay unread on purpose.
211. **pip: drop `releases` for the PEP 691 index; rate limits; private indexes.** PEP 440 ordering and specifier
    matching now agree with `packaging` on a recorded vector table (internal/pypi/testdata/pep440.json, regenerate with
    scripts/gen-pep440-vectors.py). Still open: `releases` is documented as deprecated in favour of the PEP 691 index and
    could be dropped, 429 handling for PyPI, and a private index (needs credentials, so a human decision).
213. **Archive on SCM-Manager and Gerrit against real instances.** Both hosts implement `platform.ArchivedLister` from vendored
    docs and the fakes: SCM-Manager from the `archived` field of `GET repositories`, Gerrit from `GET /projects/?state=READ_ONLY`.
    Check on real instances that an archived SCM-Manager repository still lists with `archived: true` (and `showArchived`
    does not change it), that a Gerrit read-only project is absent from the `ACTIVE` listing and present in `READ_ONLY`,
    and whether Gerrit's webhooks plugin or stream events can report the state change so the listing is not the only path.
    Bitbucket Cloud and Azure DevOps document no archive in `docs/vendor`; nothing to build for them.
215. **Docker Hub newest-first listing: check it against the real Hub.** `RIPPLE_REGISTRY_HUB_NEWEST` was written from the
    documented endpoint and a fake, and the docs do not say what `ordering` accepts, the default order or the largest
    `page_size`. A `live.yml` check should ask hub.docker.com for `library/nginx` with `ordering=-last_updated`, assert the
    results come back non-increasing and that `page_size=100` is honoured, and fail loudly if Hub changes, since ripple
    then silently falls back to the lexical list. Until it exists the option stays off by default. ghcr.io has no
    equivalent listing (OCI tags are lexical only), so `TagFloor` stays its only narrowing.
216. **`from-pseudo-versions`: real modules, and a "newest" mode.** The opt-in move of a pseudo-version dependency to the first
    release released after the commit (docs/osv.md) is verified on the fake proxy and index only. It does not check that the
    release really contains the commit (the proxy does not say; a release from a branch that never merged it passes), and it
    stops at the first release, so a dependency that wants the newest has to wait for the next ordinary update. Check it
    on a real pseudo-version dependency, then consider `from-pseudo-versions: newest`.
218. **pip: what is still left unread on purpose.** `setup.cfg` (`install_requires`, `setup_requires`, `tests_require`,
    `extras_require`) and `==1.*`/`==1.2.*` are read now (docs/pip.md). `setup.py` is never read or run, a lone `<`/`<=`,
    `!=`, a bare `*` and Poetry's array-of-constraints form (one entry per Python or platform) have no single floor to
    raise and stay unread; an array could move each entry whose `python` marker the repo's Python matches. A
    `setup.cfg`'s `python_requires` feeds the Requires-Python guard now. The setup.cfg reader is line-based and was checked against hand-written files, not a sample of
    real projects (see the corpus entry for pip once one is run).
222. **OSV: reconcile residuals.** The one-time `RIPPLE_OSV_BACKFILL_OPEN_PRS` pass (which finds a done job's open PR from before the `osv-jobs` index) has never run against a real host; a record shared by twin GHSA and GO notes is reconciled per note; the busy-repo retry list holds at most 100 ids. An amended record's line in a PR description is now rewritten (GitHub only, verified against the fake: that the real GraphQL `viewer { login }` of an installation token equals the editor login of the app's own description edit is assumed, and if it does not the PR is just treated as touched and left alone); the other hosts have no `PRBodyEditor` and no way yet to tell ripple's description edit from a person's.
223. **OSV: the beaten-PR sweep on real hosts.** A beaten directive or toolchain PR is now also closed on a later run of
    the repo (`sweepBeaten`), covered end to end against the fake GitHub only. A repo whose runs stop having plans never
    reaches the sweep, so a beaten PR beside a security PR waits for the next plan in that repo; the sweep is not run
    from the advisory reconcile, and it only pairs a security toolchain PR with a PR of the same repo, not across a
    fork or a second base branch.

224. **Azure DevOps: mint and refresh Entra tokens.** `platform.Credential{Scheme: platform.SchemeBearer}` is sent as
    `Authorization: Bearer` on REST and on git (tested against the fake), but nothing in `internal/app` builds one: the host
    still reads only `RIPPLE_AZUREDEVOPS_PAT`, and an Entra access token lives about an hour. Add a `Credentials` source that
    gets a token for a service principal (client secret, certificate or workload identity federation) or a managed
    identity and renews it before expiry, with its settings in `docs/platform-azuredevops.md`; the fake would need a token
    endpoint. See `docs/vendor/azuredevops/entra-oauth.md`.
225. **Supersede: a lower PR waits for any run with plans.** A lower PR whose close was refused is retried by the next run of
    the repo that has plans, and a rerun of the lower job is now judged again within its own run; a repo with no plans at
    all is not swept, so a lone refused close can wait for the next dependency change. Covered against the fake host only.
226. **OSV: the per-advisory bypass cap, measured.** `security-bypass-limit-per-advisory` counts the open PRs of one advisory in
    one repo through the `osv-jobs` index (at most 100 jobs per advisory), so an advisory with more indexed jobs than that
    counts only the first 100 and an open PR whose job is not indexed does not count. It caps open PRs, not only the ones a
    bypass opened, and covers the repo, not the installation. Covered against the fake host only; no real repo asked for it yet.
228. **pip: retarget residuals.** A release the repo's Python cannot run now retargets to the newest older runnable one when the repo is cloned (docs/pip.md). Left: the retargeted job takes the skipped release's `ReleasedAt`, so its cooldown counts from the newer release (no upload times are read); only the floor of the repo's Python is tested, so a repo supporting `>=3.9` is not retargeted away from a release needing `>=3.9.1`; Cargo's MSRV skip has no such fallback (ROADMAP 156); not run against the real PyPI index.
231. **Helm: the unread forms.** The datasource, manager and docs are written from the vendored helm-www pages
    ([docs/vendor/helm](docs/vendor/helm)). A `-tags live` test reads the real ingress-nginx `index.yaml`
    (`internal/helmrepo`) and `TestRealtoolHelm` runs `helm dependency update` / `lint` on an edited chart; not covered
    live: a very large index (`charts.helm.sh/stable`), redirects. Still skipped: `oci://` dependencies (OCI tags
    datasource), `@alias` / `alias:` repositories, version ranges, flow-style entries, and `created` dates for the cooldown.
232. **Helm: refresh `Chart.lock`.** The lock is reported stale in the PR body, not rewritten. Rewriting it needs
    `helm dependency update` (network, all repositories of the chart) or the digest algorithm reproduced exactly;
    decide which after 231 shows a real lock.
233. **Bundler: the unread forms.** The datasource, manager and docs are written from the vendored RubyGems guides
    ([docs/vendor/rubygems](docs/vendor/rubygems)). A `-tags live` test reads `rack` and `nokogiri` from the real
    rubygems.org (`internal/rubygems`) and `TestRealtoolBundler` runs `bundle lock` on an edited Gemfile; not covered
    live: rate limits, yanked versions. Two-segment `"~> 1.2"` and one-segment `"~> 1"` are read (a later major only; `bundle lock` checked both). Still skipped: `>=`/`>` floors (see 252),
    other constraints, four-segment versions, `gems.rb`, `.gemspec`
    dependencies, a private `source` (credentials), multi-line declarations, and `created_at` for the cooldown.
234. **Bundler: refresh `Gemfile.lock`.** The lock is reported stale in the PR body, not rewritten. Rewriting a lock
    entry without running bundler needs the new version's dependency set from the API; decide after 233 whether that
    is worth doing.
235. **Composer: the unread forms.** The datasource, manager and docs are written from the vendored Composer and
    Packagist pages ([docs/vendor/packagist](docs/vendor/packagist)). A `-tags live` test reads `monolog/monolog` from the
    real Packagist (`internal/packagist`) and `TestRealtoolComposer` runs `composer validate` / `update --dry-run` on an
    edited file; not covered live: rate limits, redirects. Two-segment `^1.2`, `~1.2`, `^0.3` and `~0.3`, and one-segment `^1`,
    `~1` and `^0` are read (a release outside the line only; real composer was asked what each locks). Still skipped: `||`
    alternatives (254), other constraints, four-segment versions, a private `repositories` entry (credentials in `auth.json`), `published-time`
    for the cooldown, and the `p2/<name>~dev.json` branches.
236. **Composer: refresh `composer.lock`.** The lock is reported stale in the PR body, not rewritten. Rewriting an entry
    without running Composer needs the new version's `require` set and its `dist` hashes from the metadata; decide after
    235 whether that is worth doing.
237. **NuGet: the unread forms.** The datasource, manager and docs are written from the vendored Microsoft Learn pages
    ([docs/vendor/nuget](docs/vendor/nuget)); a `-tags live` test (`internal/nuget`, `.github/workflows/live.yml`) reads
    the real nuget.org and a `-tags realtool` test ([docs/realtool.md](docs/realtool.md)) checks an edited project with
    `dotnet`. Not covered by either: rate limits and redirects. A `PackageReference`'s `<Version>` child element and a
    `PackageReference Update="Id"` that follows its `Include` in the same file are read (2026-11-27), after
    `TestRealtoolNugetItemForms` asked real `dotnet restore` what each resolves ([docs/nuget.md](docs/nuget.md)); the
    MSBuild item pages are vendored at [docs/vendor/msbuild](docs/vendor/msbuild). The follow-ups are 261 to 263. The forms
    skipped today: ranges and floating versions, property-indirected versions,
    prerelease and four-segment versions, `packages.config`, and `NuGet.Config` sources with package source mapping
    (today a private id is looked up on nuget.org only; see the dependency-confusion note in docs/nuget.md).
238. **NuGet: refresh `packages.lock.json`.** The lock is reported stale in the PR body, not rewritten. Rewriting it
    without running the SDK needs each package's dependency set and content hash from the registration and `.nupkg`;
    decide after 237 whether that is worth doing.
239. **Maven: the unread forms.** The datasource, manager and docs are written from the vendored Maven pages
    ([docs/vendor/maven](docs/vendor/maven)); a `-tags live` test (`internal/maven`, `.github/workflows/live.yml`) reads
    the real Maven Central and a `-tags realtool` test ([docs/realtool.md](docs/realtool.md)) checks an edited pom with
    `mvn`. Two-segment versions (`4.13`) and `${x}` properties declared once in the pom's own `<properties>` and used
    only by one artifact are read now (docs/maven.md). The forms skipped today: qualified versions, a property that
    several artifacts share (248), properties and versions managed by a parent or BOM (249), `<repositories>` and
    `settings.xml` mirrors, and Gradle (`build.gradle`, `libs.versions.toml`) as a sibling manager on this datasource.
248. **Maven: shared properties, and saying what was skipped.** `${jackson.version}` used by `jackson-core` and
    `jackson-databind` is left alone because ripple reads one artifact's releases and cannot tell the sibling has the new
    version. Check every artifact that uses the property (one metadata read each, same group) and move it only when all
    have the release. A skipped property is invisible today (the PR just never opens); report it per file. The
    command-line override of a property (`-Dx.version=`) is not seen, and Maven resolving a once-declared property as
    `pom-reference.txt` describes is that page's text, not run beyond the realtool pom.
249. **Maven: parents and BOMs.** A `<parent>` pom's `<properties>` and `<dependencyManagement>` are not followed, so a
    dependency whose version comes from `spring-boot-starter-parent` or an imported BOM stays unread. Reading the parent
    needs its pom from the repository (a second datasource read) and Maven's inheritance rules (vendored
    `pom-reference.txt` "Inheritance"), and the edit then belongs in the parent's file or is impossible.
240. **Terraform: the unread forms.** The datasource, manager and docs are written from the vendored HashiCorp pages
    ([docs/vendor/terraform](docs/vendor/terraform)); a `-tags live` test (`internal/terraform`,
    `.github/workflows/live.yml`) reads the real registry and a `-tags realtool` test
    ([docs/realtool.md](docs/realtool.md)) checks an edited configuration with `terraform`. `~> 1.2` is read now
    (docs/terraform.md). The forms skipped today: floors, ranges and other operators (250), `//subdir` module addresses, the legacy `aws = "~> 4.0"` provider form, hosted registries and
    `.terraformrc` mirrors, `*.tf.json`, `.terraform-version` and the `required_version` constraint, and OpenTofu's registry.
250. **Terraform: floors and ranges (`>= 1.0`, `>= 1.0, < 2.0`, `< 3.0`).** Left unread on purpose, not for lack of a
    parser. The vendored page's best practices say a reusable module "should constrain only [its] minimum allowed
    versions ... such as `>= 0.12.0`", so a floor states what the module needs, not which release it uses; raising it to
    the newest release would silently shrink what the module supports, and `>` would exclude the release it names. An
    upper bound (`< 2.0`) is the other half: moving it means widening a promise the author made. Decide per declaration
    kind before reading any: a root module's range `>= 1.2, < 2.0` could widen its ceiling when a new major exists
    (opt-in key, off by default), and `required_version` (Terraform itself) is a separate datasource. Nothing is
    verified about how a given range should move beyond the vendored operator table.
241. **Terraform: refresh `.terraform.lock.hcl`.** The lock is reported stale in provider PRs, not rewritten. Rewriting it
    without running Terraform needs the provider package checksums (`h1:` and `zh:`) per platform from the registry's
    download metadata; decide after 240 whether that is worth doing.
242. **Package cursors: size a real install's set and pick the slice default.** The engine now reads
    `RIPPLE_POLL_PACKAGES_PER_POLL` packages a poll through `ListCursorsPage` (at most that many plus one per read), but
    nobody has measured how large a real install's polled-package set gets, so the unset default (visit all) and a
    recommended slice size are guesses. Measure it on a real install, then document or change the default.
    The known gaps from the old entry stay: a fold racing a replica still on the old version can leave a package only in
    the legacy cursor until the next read folds it again, and cursors named `<group>#<key>` written before per-package
    keys carry no index attributes on DynamoDB, so the paged read does not see them.
243. **Head polling: verify against real Gerrit, SCM-Manager and CodeCommit.** `ListRepoHeads` is written from the vendored docs and tested against the fakes only. Unconfirmed: Gerrit's `GET projects/<p>/HEAD` then `branches/refs%2Fheads%2F<b>` answers on a real server (and the cost of two reads per project on a large instance); SCM-Manager's `GET repositories/{ns}/{name}/branches/{branch}` (media type `application/vnd.scmm-branch+json;v=2`, field `revision`) is from memory and not vendored; CodeCommit needs `codecommit:GetBranch` in the IAM policy and its `GetBranch`/`GetRepository` answers are unconfirmed. Vendor the SCM-Manager branch resource and drop the "assumed" note.
246. **Head polling: a real CodeCommit `BatchGetRepositories`, and Gerrit's default branches in bulk.** CodeCommit now reads default branches with `BatchGetRepositories` (100 names a call, `defaultBranch`), written from the vendored page and tested against the fake only; a real account is unconfirmed (IAM `codecommit:BatchGetRepositories`, whether `defaultBranch` is omitted for an empty repository). Gerrit still reads `HEAD` and the branch per project: `GET /projects/?b=<branch>` returns branch shas in bulk but needs a branch name, and whether `b=HEAD` is accepted is not in the vendored docs. Try it against a real server and, if it works, cut the two reads per project to one listing.
247. **Head polling for the remaining webhook hosts: Gitea, Bitbucket Cloud, Bitbucket Data Center, Azure DevOps.** GitHub is done (github.com and GHES: one GraphQL read per 100 repositories, ROADMAP 251 for what is unverified, `docs/platform-github-enterprise.md`), so read "GitHub" below as the model for a batch read. GitLab has it (one `GET .../repository/branches/:branch` per project; its `last_activity_at` was rejected as a head because issue and merge-request activity moves it). The others were left because none of their repo listings carries a commit and the call that does (GitHub `GET /repos/{o}/{r}/branches/{b}` or a GraphQL batch of `refs`, Gitea `GET /repos/{o}/{r}/branches/{b}`, Bitbucket Cloud `GET /repositories/{w}/{r}/refs/branches/{b}`, Data Center `GET .../branches?filterText=` or `/default-branch`, Azure DevOps `GET .../repositories/{id}/refs?filter=heads/<b>`) is not in `docs/vendor`; writing them from memory would be guessing. For each host vendor the page first, add `ListRepoHeads` the way the GitLab client does (listing, then one read per repo, a 429 ends the round, the learned default branch is reported by `ListRepos`), and decide whether the read per repo is worth it: GitHub installations can be large, so prefer a GraphQL batch there. Gitea and Data Center repo objects carry no head; Gitea and Bitbucket Cloud carry a push-time stamp (`updated_at`, `updated_on`) that is not a head for the same reason as `last_activity_at`.
251. **GitHub head polling: verify on a real App and a real GHES, and make the interval configurable.** `ListInstalledRepoHeads` is written from the vendored GraphQL schema and tested against the fake GitHub and GHES only (`docs/vendor/github/graphql-repository-heads.md`). Unconfirmed: that an aliased read of 100 repositories costs 1 point (the code stops below `rateLimit.remaining` 100 either way), the `RATE_LIMITED` error type of an exhausted GraphQL budget (assumed; a primary limit answered 200 with another type would be treated as a plain failure and retried every discovery), `defaultBranchRef` null for an empty repository, a GHES release with GraphQL disabled or without these fields, and the 10 second GraphQL timeout at 100 repositories on a slow instance (lower `headBatch` if a 502/504 shows). `Client.HeadEvery` (default 10 minutes) has no environment variable yet: add `RIPPLE_GITHUB_HEAD_EVERY` (and the `ghes` one) once a real install shows what it should be. No live test is possible without a token; a gated live path using a dedicated read-only App is the way to cover it.
252. **Bundler: `>=`/`>` floors.** A single `gem "x", ">= 1.0"` allows every later release, so there is nothing to write and ripple reads it as unread on purpose (asserted in `TestBundlerExtract`). Revisit only if users want the floor raised to the newest release, which is a policy choice (it narrows what the Gemfile allows) and needs an opt-in knob, not a default.
254. **Composer: `||` alternatives.** `"^1.0 || ^2.0"` is OR with AND binding tighter (vendored versions article), so the constraint allows each alternative's range. Reading it is easy; the open question is the edit policy, which is a human decision: raise the alternative that holds the newest release, append a new alternative (`|| ^3.0`), or replace the whole string. None is in the docs. Decide, then implement the chosen form opt-in.

261. **NuGet: `Update=` in another file than its `Include`.** Only an Update after its Include in the same file is read. An Include in `Directory.Build.props` or an imported `.props` and an Update in the project is real MSBuild usage; reading it needs the import graph (`Import`, `Directory.Build.props` lookup), which ripple does not evaluate. Ask `dotnet` which file wins with a two-file fixture before deciding how far to follow imports.
262. **NuGet: `Update=` of a `PackageVersion`, and wildcard Updates.** `Directory.Packages.props` central package management uses `PackageVersion Include`, and the vendored pages show `Update=` there once. Not run through `dotnet` yet, so `PackageVersion Update=`, `Update="A;B"` and `Update="A*"` stay unread. Add a realtool case for each, then read what it confirms.
263. **NuGet: `VersionOverride`.** `<PackageReference Include="X" VersionOverride="2.0.0" />` overrides a central `PackageVersion`. It makes the tag unread today. Ask `dotnet` with a central-package-management fixture, then edit the override or the central version.
280. ~~**Gitea/Forgejo: a repository empty at its first discovery is never registered.**~~ Shipped: `ListRepos` polls a repository that reports `empty=true` for the first time (`Client.EmptySettle`, 5 s), one that stays empty is waited for once, and the real-instance fixture no longer waits for `empty=false` ([docs/platform-gitea.md](docs/platform-gitea.md) "Real instances"). The `repository` created webhook path is ROADMAP 283.
281. **Gitea/Forgejo: create a missing label, and prove a deletion is noticed.** A label the repository lacks is dropped (`200 []`) and only logged; creating it (`POST /repos/{o}/{r}/labels`, needs write access) would make `labels:` work on a fresh repository. Separately, no instance announces a deleted repository (the system webhook sends `created` only), so removal relies on discovery: add a real-instance test that deletes a repository and waits for ripple to drop it.
282. **Gitea/Forgejo: what the real job does not cover.** Forgejo before 7 and Gitea before 1.27 (older Gitea images may differ in the auto-merge key and timeline), PostgreSQL and MySQL back ends, the 429 a real throttling proxy sends (no instance throttles; only the injected one is tested), a person's label, comment or rename on a pull request being read as a touch, a scheduled merge actually merging when checks pass, and archive flipping in `/user/repos`. Add legs or tests as each is wanted; `hosts-gitea.yml`'s matrix is the place.
270. **`container-runtimes`: if the reaped container is listed again.** `Reap` now looks again (four more tries, half a second apart) when its kill and remove find nothing, and the live check polls for 15s and logs `inspect` of what is still there. The one failure on record (run 45391499375) was never explained, so the retry is a fix for a plausible cause, not a proven one. If the check fails again, the log's `inspect` says which: still running means the runtime created the container later than the retries wait (lengthen, or have the engine Reap again once the client has exited); removing or exited means the list is slow and the test's bound is the thing to raise.
283. **Gitea/Forgejo: a `repository` created webhook for a repository that is still empty.** A system webhook announces `created` before the first push, and the event queues a scan of a repository with nothing in it. What the scan does with an empty clone, and whether the next discovery then rescans it once the push lands, is unconfirmed on a real instance (the listing path is fixed and proved, ROADMAP 280). Drive it: a system webhook to a real ripple, create and push in quick succession, assert one registration with its dependencies.
