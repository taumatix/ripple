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
    checkout (the emulator refuses shallow and serves the whole clone and the push, so only real AWS can say that shallow is served). Residuals: one AWS account per ripple, labels and auto-merge do not exist on the
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
123. **Dashboard: read-back edge cases against a real host.** The refresh and watch sets are now one cursor per repo, so
    two replicas marking at once cannot lose an entry. Read-back finds the issue by its marker, so an
    edit that removes the marker is repaired by number and one that deletes the issue is recreated, both
    covered only against the fake.
125. **OSV: sweep residuals.** The DynamoDB repo index (`REPOS` items) is built by one Scan the first time a listing is asked
    for; that Scan and the listing are verified on the in-process fake (including that the build runs once and reruns after a failed one), and DynamoDB Local runs them only in CI. A scan
    (not the sweep) still runs one history round, so a repo with over 500 pairs or 100 records is completed only by a sweep
    or a go.mod change; consider a few rounds per scan once real repo sizes are known. Postgres `ListRepos` is CI-only.
126. **OSV: reconcile older cases.** A pending job made before the `osv-jobs` index is now indexed by a one-time backfill; a
    done job (and so its open PR) from before the index is found by the opt-in `RIPPLE_OSV_BACKFILL_OPEN_PRS` pass, only for a go job whose
    PR is still open (the branch names the job) and never verified against a real host; a PR body is not rewritten when a record is corrected (the fix version or
    summary in it can be stale), and a record shared by twin GHSA and GO notes is reconciled per note. The busy-repo retry
    list holds at most 100 ids.
127. **OSV: conflict and pseudo-version residuals.** The beaten-PR close covers a non-security PR when a security
    toolchain PR opens (a lower security toolchain PR beside a higher one is closed by the supersede flow, pinned by an e2e
    test), and a toolchain PR opened by an older run than the security job is only held, never closed, unless the security PR opens in a later run with it open.
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
    live, so it stays open). A release whose `rust_version` is newer than the repo's `rust-version` is now skipped, but the
    newest release that does build is not proposed instead. index.crates.io is verified live (2026-11-24); not verified:
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
179. **Supersede on a later run.** Within one run, PRs opened for the same module are now closed by the next one (fixed
    with the live OSV run). A sweep on a later run did not close lower PRs in one observed case after a rerun and a tick;
    reproduce it with a fake and either fix it or explain it in docs/osv.md.
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
197. **`container-runtimes (docker)`: the reaped container that was still listed.** `TestContainerRuntimeLive` failed once
    ("the container survived its timeout (Reap left it)", run 45391499375, head 0fd4151) and passed on retry. Two causes fit
    and the log cannot tell them apart: (a) Docker removes a `--rm` container itself when `kill` stops it, so `rm -f` answers
    "removal already in progress" and a list taken at once still shows it as "removing" (a test-side race, now polled for
    15s); (b) the 4s timeout killed the client before the daemon had created the container, so `Reap` found no such
    container and the daemon then started it (a product race, harmless for a minutes-long job timeout, real for a short
    one). The live test now logs `inspect` of a container still present after the wait: still running means (b), in which
    case `Reap` should look again after a short delay, bounded. Act on the next failure's log, not before.
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
204. **Repository ids on the other hosts, and a missed rename by id.** A signed push whose repository id differs from
    the stored one is now settled by asking the host (`platform.RepoIdentifier`, GitHub's `GET /repositories/{id}`),
    but only when the id names this repo's own name (a recreated repo). Not done: the host answering with a
    different name for the stored id (a rename missed twice), and an implementation for GitLab, Gitea/Forgejo and
    the others, which keep ignoring a mismatch as before. Only the GitHub lookup ran, against the fake host.
205. **Webhooks: the rest of the streamed-parse cost.** One string value or key is now capped at 16 KiB while the signature is unchecked, so a forged body with one huge `ref` costs 16 KiB, not up to 16 MiB; the whole budget (`maxExtract`, 16 MiB, reachable by many fields each under the cap) is unchanged and unmeasured on a busy receiver. Azure DevOps, Gerrit, SCM-Manager and CodeCommit authenticate from headers and read whole, bounded by their `maxBody`.
206. **DynamoDB hygiene: measure it, and decide on self-repair.** The hygiene commands and the server's drift check now list packages from the `PKGS` index (one Query; a table from before it is Scanned once, ROADMAP 176) instead of Scanning every item for `DEP#` keys. Still nobody has timed the check or a per-package recount on a table with millions of items or on real AWS with provisioned capacity, so the suggested page and pause are arithmetic, not measurement; every `PKGS` item shares one partition key, which a table with millions of packages would make a hot read partition (shard it by hash if it shows), and a package an older-version replica introduces after the marker is missed until the marker is deleted. The server only reports drift; whether it should recount a package it finds drifted on its own (rate-limited, one replica at a time under the lease) is undecided.
219. **Corpus checks: from a real sample, and what the first one found.** `internal/manager/testdata/corpus` holds manifests written
    the way ripgrep, tokio, express, bitnami and ingress-nginx write theirs, with reviewed `.want` files (`go test ./internal/manager
    -run TestCorpus -update-corpus` rewrites them); they were written from memory and the documented formats, not downloaded, so the
    miss rate over real crates.io, npm and Hub files is still unmeasured. Not read, found by the corpus: an ingress-nginx style values
    block with `registry:` and `image: path/name` and no `repository` key, and a calendar tag with a leading zero (`ubuntu:22.04`, the
    registry tag parser needs a semver-shaped number). npm still classifies `0.2.3` -> `0.3.0` as minor (Cargo uses leftmost-nonzero);
    a Dockerfile `FROM` with a `v${V}` ARG prefix and Helm `global.imageRegistry` indirection stay unread. The Cargo `*`/lone `<`
    and npm hyphen, lone `>`, peer and `overrides` forms stay unread on purpose.
211. **pip: drop `releases` for the PEP 691 index; rate limits; private indexes.** PEP 440 ordering and specifier
    matching now agree with `packaging` on a recorded vector table (internal/pypi/testdata/pep440.json, regenerate with
    scripts/gen-pep440-vectors.py). Still open: `releases` is documented as deprecated in favour of the PEP 691 index and
    could be dropped, 429 handling for PyPI, and a private index (needs credentials, so a human decision).
212. **OSV: cap the PRs a security bypass opens.** `security-bypass: [open-pull-requests-limit]` (with the optional
    `security-bypass-severity`) still opens one PR per advisory past the limit, so one advisory that affects many modules
    opens that many. A per-advisory cap was left out until real use shows a repo wants it.
213. **Archive on SCM-Manager and Gerrit against real instances.** Both hosts implement `platform.ArchivedLister` from vendored
    docs and the fakes: SCM-Manager from the `archived` field of `GET repositories`, Gerrit from `GET /projects/?state=READ_ONLY`.
    Check on real instances that an archived SCM-Manager repository still lists with `archived: true` (and `showArchived`
    does not change it), that a Gerrit read-only project is absent from the `ACTIVE` listing and present in `READ_ONLY`,
    and whether Gerrit's webhooks plugin or stream events can report the state change so the listing is not the only path.
    Bitbucket Cloud and Azure DevOps document no archive in `docs/vendor`; nothing to build for them.
220. **Package cursors: use the paged listing in the engine and measure.** `store.PagedCursorLister` (`ListCursorsPage`, after-name and limit; Memory, Postgres, DynamoDB's group index) exists, but `polledPackages` still lists the whole set before `nextSlice` cuts it. Read the next `RIPPLE_POLL_PACKAGES_PER_POLL` after the resume cursor with `ListCursorsPage` instead (the legacy newline cursor and a store without the interface keep the whole read). Also unmeasured: how large a real install's set gets, and the right default. A fold racing a replica still running the old version can leave a package only in the legacy cursor until the
    next read folds it again; cursors named `<group>#<key>` written before per-package keys carry no index attributes on DynamoDB.
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
217. **pip: retarget to the newest release the repo's Python can run.** A release whose `Requires-Python` excludes the
    repo's lowest declared Python is skipped, so a project whose newest release needs a newer interpreter gets no PR
    even when an older release would also be an update (as for a Cargo `rust-version`). Fixing it means the watcher
    must know each dependent's Python (read at scan time and stored on the dependency, or read through the workspace in
    the poll) and pass it to `NewestWhere`. Also unmeasured: only the floor is tested, so a repo supporting `>=3.9` is
    skipped for a release needing `>=3.9.1`; whether that is too strict is for a real repo to say.
218. **pip: what is still left unread on purpose.** `setup.cfg` (`install_requires`, `setup_requires`, `tests_require`,
    `extras_require`) and `==1.*`/`==1.2.*` are read now (docs/pip.md). `setup.py` is never read or run, a lone `<`/`<=`,
    `!=`, a bare `*` and Poetry's array-of-constraints form (one entry per Python or platform) have no single floor to
    raise and stay unread; an array could move each entry whose `python` marker the repo's Python matches. A
    `setup.cfg`'s `python_requires` does not yet feed the Requires-Python guard (only `pyproject.toml`, `Pipfile` and
    `.python-version` do). The setup.cfg reader is line-based and was checked against hand-written files, not a sample of
    real projects (see the corpus entry for pip once one is run).
221. **Implement `app.HeadLister` in the Lister hosts.** `App.Discover` re-scans a listed repo whose default-branch head moved once its host reports heads (`ListRepoHeads`), but no host does. Add it to the hosts with no push events (Gerrit, SCM-Manager, CodeCommit and any other `platform.Lister`) from the listing call if it carries the head, else one extra read per repo, bounded by the host's rate limit.
