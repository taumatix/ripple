# UPSTREAM

ripple is bound to two external contracts. The block below is the source of truth; the
`checked` date is bumped on every maintenance pass, even when nothing changed.

```yaml
upstream:
  - name: go-module-index
    kind: http-feed
    reference: https://index.golang.org/index
    format: "GET /index?since=<RFC3339>&limit=<n>; new-line delimited JSON, oldest first, one object per line with Path, Version, Timestamp; entries with Timestamp >= since"
    docs: https://go.dev/ref/mod#module-proxy
    verified-against: "a real response from index.golang.org, copied into internal/feed/goindex_test.go"
    verified-live: "2026-11-24 (go test -tags live ./internal/feed, .github/workflows/live.yml): since is inclusive (the first entry is golang.org/x/text v0.3.0 at 2019-04-10T19:08:52Z), pages are oldest first, a recent window holds fresh releases"
    unverified: "behaviour at the rate limit; a feed gap or a rewritten past"
    checked: 2026-11-24
  - name: github-rest-api
    kind: http-api
    reference: https://docs.github.com/en/rest
    api-version: "2026-03-10"
    header: "X-GitHub-Api-Version: 2026-03-10"
    used: [POST /app/installations/{id}/access_tokens, GET and POST /repos/{owner}/{repo}/pulls (draft), POST /repos/{owner}/{repo}/issues/{n}/labels, GET /repos/{owner}/{repo}/pulls/{n} and PATCH (state: closed), GET .../pulls/{n}/commits|reviews|comments, GET and POST .../issues/{n}/comments, GET .../issues/{n}/events (a "renamed" event), GET and POST /repos/{owner}/{repo}/issues and PATCH .../issues/{n} (the dependency dashboard: state=all, labels, per_page=100, pull_request key), POST /repos/{owner}/{repo}/labels, POST /graphql (enablePullRequestAutoMerge), GET .../tags, .../git/ref/tags/{tag}, .../git/tags/{sha}, .../releases/tags/{tag} (the github-actions manager)]
    webhooks: [installation, installation_repositories, push, repository, signature X-Hub-Signature-256 = sha256=HMAC-SHA256(body)]
    verified-live: "2026-11-24 (go test -tags live ./internal/platform/github): unauthenticated GET /repos/{o}/{r}/tags?per_page=100, .../git/ref/tags/{tag} (lightweight and annotated, followed through .../git/tags/{sha}) and .../releases/tags/{tag} (404 for a tag with no release) on api.github.com accept X-GitHub-Api-Version 2026-03-10 and match the shapes the client reads; these are the github.com half of the actions manager's upstream reads"
    unverified: "everything above that needs an App token (pulls, issues, labels, GraphQL, access tokens, webhooks), none of it ever ran against github.com; the text of the push refusal without the workflows permission (matched on \"workflow\" and \"permission\"); enablePullRequestAutoMerge mutation shape; that issue events record every PR rename as event \"renamed\" and that App tokens may close a PR and comment with pull-requests: write alone (assumed from the docs); the labels endpoint creating missing labels (assumed); the issues endpoints need the App permission Issues: read and write, and a missing permission is assumed to answer 403 and disabled issues 410 (docs list both codes, not the text; see docs/vendor/github/issues-rest.md); the fake GitHub in tests is written from the docs, not recorded"
    checked: 2026-11-24
  - name: docker-registry-api
    kind: http-api
    reference: https://github.com/opencontainers/distribution-spec/blob/main/spec.md
    docs: https://distribution.github.io/distribution/spec/auth/token/
    format: "GET /v2/<name>/tags/list?n&last (Link rel=next), HEAD /v2/<name>/manifests/<tag> (Docker-Content-Digest), anonymous Bearer token from the Www-Authenticate realm"
    verified-against: "docs/vendor/registry excerpts read 2026-11-23; tests use internal/testutil/registry.go, a fake written from them"
    verified-live: "2026-11-24 (go test -tags live ./internal/registry, .github/workflows/live.yml): anonymous pulls of docker.io/library/alpine:3.18.0 (registry-1.docker.io, auth.docker.io token), ghcr.io/astral-sh/uv:0.4.0 and quay.io/prometheus/node-exporter:v1.6.0: each answers /v2/ with a Bearer challenge and gives a token without credentials; tags/list holds the tag (ghcr.io's list is large and slow, about 20s); HEAD of a manifest list or OCI index gives a stable Docker-Content-Digest with the Accept set ripple sends; the image config `created` is read through the index, the linux/amd64 manifest and the config blob (a redirect on Docker Hub) and falls in the year each image was built; a missing repository answers 401 or 404 (ErrDenied or ErrNotFound) and a missing tag 404"
    unverified: "429 and rate-limit statuses (Docker Hub's anonymous pull limit was never reached; ripple reports the status but does not wait); private images and authenticated pulls; any other registry (ECR, GAR, ACR, Harbor); an index with only attestation manifests"
    checked: 2026-11-24
  - name: npm-registry-api
    kind: http-api
    reference: https://github.com/npm/registry/blob/main/docs/responses/package-metadata.md
    docs: https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md
    format: "GET /<package> with Accept application/vnd.npm.install-v1+json (abbreviated metadata: name, modified, dist-tags, versions with deprecated), anonymous; scoped names as @scope%2Fname"
    verified-against: "docs/vendor/npm excerpts read 2026-11-23; tests use internal/testutil/npm.go, a fake written from them"
    verified-live: "2026-11-24 (go test -tags live ./internal/npmreg, .github/workflows/live.yml): registry.npmjs.org serves the abbreviated document (content type application/vnd.npm.install-v1+json) with dist-tags and versions; left-pad 1.3.0 is the latest tag and deprecated, and is not proposed; @types/ms works with %2F; a missing package, scoped or not, is 404; typescript prereleases are skipped and nothing above latest is proposed"
    unverified: "private packages (401/403 or 404), rate limits, a very large package (react, @types/node), .npmrc and node-semver grammar beyond docs/npm.md, versions carry no publication date"
    checked: 2026-11-24
  - name: cargo-sparse-index
    kind: http-api
    reference: https://doc.rust-lang.org/cargo/reference/registry-index.html
    docs: https://doc.rust-lang.org/cargo/reference/specifying-dependencies.html
    format: "GET {index}/{prefix}/{lowercase name} (1/, 2/, 3/{c}/, {aa}/{bb}/), anonymous; one JSON object per line with vers and yanked"
    verified-against: "docs/vendor/cargo excerpts read 2026-11-23; tests use internal/testutil/cratesindex.go, a fake written from them"
    verified-live: "2026-11-24 (go test -tags live ./internal/cratesindex): index.crates.io serves the 2, 3 and 4+ letter prefixes (cc, log, rand), a mixed-case name (Serde_Json) is read through the lowercase path, a missing crate is 404, serde 1.0.95 is yanked and serde 0.8.0-rc1 is skipped"
    unverified: "another registry, response size for the largest crates, conditional requests (ETag), rate limits; TOML is read by a line scanner as far as docs/cargo.md says, not parsed"
    checked: 2026-11-24
  - name: helm-chart-repository
    kind: http-api
    reference: https://helm.sh/docs/topics/chart_repository/
    docs: https://helm.sh/docs/topics/charts/
    format: "GET {repository}/index.yaml, anonymous; entries.<chart>[] with version, deprecated; Chart.yaml / requirements.yaml dependencies with name, version, repository"
    verified-against: "docs/vendor/helm pages fetched 2026-11-25; tests use internal/testutil/helmrepo.go, a fake written from them"
    unverified: "everything against a real repository: large indexes, redirects, authentication, the semantics of created; oci:// and @alias repositories and constraint ranges are skipped, not read"
    checked: 2026-11-25
  - name: rubygems-api
    kind: http-api
    reference: https://guides.rubygems.org/rubygems-org-api/
    docs: https://guides.rubygems.org/gemfile-and-gemspec/
    format: "GET https://rubygems.org/api/v1/versions/{gem}.json, anonymous; array of {number, platform, prerelease, ...}; Gemfile gem lines"
    verified-against: "docs/vendor/rubygems guides fetched 2026-11-25; tests use internal/testutil/rubygems.go, a fake written from them"
    unverified: "everything against the real service: rate limits, very large gems, whether yanked versions are listed; the Bundler Gemfile man page was not vendored, so the Gemfile grammar is read from the guides only"
    checked: 2026-11-25
  - name: packagist-api
    kind: http-api
    reference: https://packagist.org/apidoc
    docs: https://getcomposer.org/doc/articles/versions.md
    format: "GET https://repo.packagist.org/p2/{vendor}/{package}.json, anonymous; {packages: {name: [{version, ...}]}, minified: composer/2.0}; composer.json require / require-dev"
    verified-against: "docs/vendor/packagist pages fetched 2026-11-25; tests use internal/testutil/packagist.go, a fake written from them"
    unverified: "everything against the real service: rate limits, redirects, the minified encoding of very large packages (only the version key of each entry is read), the exact package-name pattern (taken from memory of Composer's schema, the vendored page says only lowercase words separated by - . _)"
    checked: 2026-11-25
  - name: nuget-api
    kind: http-api
    reference: https://learn.microsoft.com/en-us/nuget/api/overview
    docs: https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource
    format: "GET https://api.nuget.org/v3/index.json (PackageBaseAddress/3.0.0), then GET {base}/{lowercase id}/index.json, anonymous; {versions: [...]} lowercased, listed and unlisted; PackageReference / PackageVersion Version attributes in MSBuild project files"
    verified-against: "docs/vendor/nuget pages fetched 2026-11-25; tests use internal/testutil/nuget.go, a fake written from them"
    unverified: "everything against the real service: rate limits, redirects, the service index's resource list, the package id pattern (taken from the vendored pages and memory), NuGet.Config private feeds (not read)"
    checked: 2026-11-25
  - name: pypi-json-api
    kind: http-api
    reference: https://docs.pypi.org/api/json/
    docs: https://peps.python.org/pep-0440/
    format: "GET {index}/pypi/{PEP 503 normalized name}/json, anonymous; releases maps a version to its files, each with yanked"
    verified-against: "docs/vendor/pip excerpts read 2026-11-23; tests use internal/testutil/pypi.go, a fake written from them"
    verified-live: "2026-11-24 (go test -tags live ./internal/pypi): pypi.org answers the JSON API with `releases` (still present) for Typing_Extensions and zope.interface without the caller normalising; requests 2.32.0 and 2.32.1 are yanked and not proposed, 2.15.0 has no files and is not proposed; django pre-releases are skipped; a missing project is 404; every version of django, requests, numpy and setuptools parses with the PEP 440 code"
    unverified: "private indexes, rate limits, the largest projects, whether `releases` (documented as deprecated) is kept; PEP 440 ordering and specifiers were compared with packaging 26.4 through a recorded vector table (internal/pypi/testdata/pep440.json), not live; requirements files and TOML are read by a scanner as far as docs/pip.md says"
    checked: 2026-11-24
  - name: github-enterprise-server
    kind: http-api
    reference: https://docs.github.com/en/enterprise-server@latest/rest
    docs: https://docs.github.com/en/enterprise-server@latest/graphql
    format: "REST at https://HOST/api/v3, GraphQL at https://HOST/api/graphql, same paths as github-rest-api; X-GitHub-Api-Version 2026-03-10 and 2022-11-28 on 3.22, 410 for an unsupported one; webhooks add X-GitHub-Enterprise-Version and -Host"
    newest-read: "3.22"
    verified-against: "docs/vendor/ghes excerpts read 2026-11-23; tests use internal/testutil/ghes.go, a fake written from them"
    unverified: "never run against a real GHES: the `/api/v3/meta` answer (`installed_version`, unauthenticated) and ripple's own 3.9 warning floor; which release first accepts 2026-03-10; whether enablePullRequestAutoMerge exists on every release and the error shape of one that lacks it; the status of a disabled GraphQL or REST API (assumed 404); webhook payload parity with github.com"
    checked: 2026-11-23
  - name: go-release-list
    kind: http-feed
    reference: https://go.dev/dl/?mode=json
    format: "JSON array of releases, newest first, each with version (\"go1.N.P\"), stable, files; without include=all it lists only the supported stable minors, newest patch each; no release dates"
    docs: https://go.dev/doc/devel/release#policy
    verified-against: "the live endpoint's shape on 2026-11-23, reproduced by internal/testutil/godl.go; tests use that fake, not a recorded response"
    unverified: "that the list keeps omitting unsupported releases without include=all (the support policy is inferred from it)"
    checked: 2026-11-23
  - name: go-module-proxy
    kind: http-api
    reference: https://go.dev/ref/mod#goproxy-protocol
    format: "GOPROXY protocol, used only by the go command that verifies a major bump (go mod tidy, build, vet); ripple itself does not call it"
    major-versions: https://go.dev/ref/mod#major-version-suffixes
    rules: "/vN suffix required from v2, /v1 not allowed, gopkg.in paths always .vN, +incompatible marks pre-module v2+ versions"
    verified-against: "the go command in internal/e2e against a fake proxy (internal/testutil/goproxy.go)"
    verified-live: "2026-11-24 (go test -tags live ./internal/feed): feed.GoProxyLookup against proxy.golang.org: @latest for pkg/errors, Masterminds/semver (upper-case path escaped as !masterminds) and go-redis/redis/v8; an unknown module answers 404 with a text body and is not an error; @v/list is unsorted; go.mongodb.org/mongo-driver's go.mod retracts versions the list still shows"
    unverified: "a module whose @latest is retracted (the lookup does not read retractions); the pre-release-only fallback; Retry-After and 429 (ripple's clients do not honour them: ROADMAP entry below)"
    checked: 2026-11-24
  - name: container-runtimes
    kind: cli
    reference: https://github.com/apple/container
    docs: https://docs.docker.com/reference/cli/docker/container/run/
    cli-version: "container 1.4.1 (Apple)"
    used: ["container|docker|podman run --rm --name --label --network --read-only --tmpfs --cap-drop --user -v -w -e <image> <cmd>; Apple: --progress none -c -m; docker and podman: --pull --pids-limit --security-opt --cpus --memory; kill <name>; docker and podman: rm -f <name>"]
    verified-against: "Apple container 1.4.1: options from `container run --help`, run for real by internal/sandbox/container_live_darwin_test.go"
    unverified: "docker and podman: option names and semantics are from the documented CLI and a fake binary (internal/testutil/fakecontainer.go); no daemon has run them"
    checked: 2026-11-23
  - name: gitea
    kind: http-api
    reference: https://docs.gitea.com/api/1.24/
    docs: https://docs.gitea.com/usage/webhooks
    vendored: docs/vendor/gitea/
    used: [GET /api/v1/version, GET /api/v1/user/repos, GET and POST /repos/{owner}/{repo}/pulls (draft is a "WIP:" title prefix), POST /repos/{owner}/{repo}/pulls/{n}/merge ("do" and merge_when_checks_succeed), POST /repos/{owner}/{repo}/issues/{n}/labels]
    webhooks: [push, repository, "signature X-Gitea-Signature = hex HMAC-SHA256(body), no prefix; also X-Hub-Signature-256 = sha256=..."]
    auth: "Authorization: token <access token>"
    unverified: "rate-limit response shape (undocumented; Retry-After and IETF RateLimit t= assumed); status code of a scheduled merge; labels by name on create; git basic-auth username; the fake is written from docs, not recorded"
    checked: 2026-11-23
  - name: forgejo
    kind: http-api
    reference: https://forgejo.org/docs/latest/user/api-usage/
    docs: https://forgejo.org/docs/latest/user/webhooks/
    vendored: docs/vendor/forgejo/
    used: [same endpoints as gitea; merge body key is "Do" (capital D); flavour detected by "+gitea-" in the version string, Forgejo 7 and later]
    webhooks: [push, repository, "signature X-Forgejo-Signature = hex HMAC-SHA256(body); X-Gitea-* and X-Hub-Signature-256 also accepted"]
    unverified: "whether Forgejo sends X-Hub-Signature-256 (documented sample shows only X-Forgejo-Signature); Forgejo before 7 detection; rate-limit shape; the fake is written from docs, not recorded"
    checked: 2026-11-23
  - name: gitlab-rest-api
    kind: http-api
    reference: https://docs.gitlab.com/api/rest/
    api-version: "v4"
    docs-pinned: "GitLab 19.7.0-pre, doc/ at gitlab-org/gitlab master, vendored in docs/vendor/gitlab"
    used: [GET /projects?membership=true, GET and POST /projects/{url-encoded path}/merge_requests (Draft: title prefix, labels), PUT /projects/{id}/merge_requests/{iid}/merge (auto_merge, merge_when_pipeline_succeeds)]
    auth: "Authorization: Bearer <personal, project or group access token>; git basic auth oauth2:<token>"
    webhooks: [Push Hook, header X-Gitlab-Token or Standard Webhooks signature (webhook-id, webhook-timestamp, webhook-signature = v1,base64(HMAC-SHA256(key, id.timestamp.body)), key from whsec_ token)]
    unverified: "everything: no real GitLab instance was available. Request and response shapes, the 429 Retry-After handling, auto_merge acceptance and the signature scheme are from the vendored docs; the fake GitLab in internal/testutil is written from them, not recorded. The signature vector was computed with openssl, not taken from GitLab"
    checked: 2026-11-23
  - name: bitbucket-cloud
    kind: http-api
    reference: https://developer.atlassian.com/cloud/bitbucket/rest/intro/
    docs: https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/
    vendored: docs/vendor/bitbucket-cloud/
    api-version: "2.0"
    used: [GET /repositories/{workspace}, GET and POST /repositories/{ws}/{repo}/pullrequests, GET /repositories/{ws}/{repo}/merge-queues, POST /pullrequests/{n}/merge (queue only, async=true), GET /repositories/{ws}/{repo}/diffstat/{commit}]
    webhooks: [repo:push, "signature X-Hub-Signature = sha256=hex HMAC-SHA256(body); secret optional upstream, required by ripple"]
    auth: "Authorization: Bearer <access or API token>; git basic auth x-token-auth"
    unverified: "429 headers; queued-merge 202 and Location; x-token-auth with API tokens; merge-commit diffstat; whether a retry (X-Attempt-Number above 1) keeps the first attempt's X-Request-UUID (the docs call it the UUID \"of the request\" and do not say; ripple dedupes on it, so a retry under a new UUID is handled again, which is safe); the fake is written from docs, not recorded"
    checked: 2026-11-23
  - name: azure-devops-rest-api
    kind: http-api
    reference: https://learn.microsoft.com/en-us/rest/api/azure/devops/
    api-version: "7.1"
    vendored: docs/vendor/azuredevops
    used: [GET /{org}/_apis/projects, GET /{org}/{project}/_apis/git/repositories, GET and POST /{org}/{project}/_apis/git/repositories/{repo}/pullrequests, PATCH .../pullrequests/{id} (autoCompleteSetBy, completionOptions), POST .../pullrequests/{id}/labels, GET .../commits/{commitId}/changes]
    auth: "personal access token as HTTP Basic with an empty user name (REST and git); Entra ID tokens not supported"
    webhooks: [service hook git.push (resourceVersion 2.0 sample), authenticated by HTTP Basic basicAuthCredentials; no HMAC exists; payload lists commits but no changed files, resolved through the commit changes API]
    rate-limits: "429 TF400733 with Retry-After; Retry-After may arrive on a 200 and is honoured"
    unverified: "a repo name accepted as repositoryId; autoCompleteSetBy.id taken from the creator's id; the labels endpoint; commit changes for merge commits and paging; exact service-hook delivery headers; whether the payload id ripple dedupes on stays the same across a retry (the vendored docs describe the retry schedule, not the payload id: unverified, and a retry under a new id is handled again, which is safe); the fake Azure DevOps in tests is written from the docs, not recorded; names with spaces unsupported"
    checked: 2026-11-23
  - name: aws-codecommit
    kind: http-api
    reference: https://docs.aws.amazon.com/codecommit/latest/APIReference/
    api-version: "2015-04-13"
    docs: https://docs.aws.amazon.com/codecommit/latest/userguide/
    vendored: docs/vendor/codecommit/
    used: [ListRepositories, ListPullRequests, GetPullRequest, CreatePullRequest, GetDifferences, "git over HTTPS with the SigV4 git password (git-remote-codecommit algorithm)"]
    auth: "SigV4, service codecommit, JSON 1.1 with X-Amz-Target CodeCommit_20150413.<Op>; git user AK[%token] and password <timestamp>Z<signature>"
    events: [EventBridge referenceCreated and referenceUpdated via an SNS HTTPS subscription, "SNS signature SignatureVersion 1 (SHA1) and 2 (SHA256), RSA PKCS1v15"]
    unverified: "everything against real AWS: signature acceptance, cn and eusc endpoints, SNS certificate chain, title limit (100 or 150), EventBridge input shape through SNS, clock-skew rejection; the fake is written from the docs, not recorded"
    checked: 2026-11-23
  - name: scm-manager
    kind: http-api
    reference: https://scm-manager.org/docs/3.12.x/en/
    docs: https://scm-manager.org/plugins/scm-review-plugin/docs/3.17.x/en/
    vendored: docs/vendor/scm-manager/
    api-version: "v2 (vnd.scmm media types, ;v=2)"
    docs-pinned: "SCM-Manager docs 3.12.x, scm-review-plugin 3.17.x (source d833d89b), scm-webhook-plugin 3.2.x (source 55d3605e), scm-manager develop b14c0fb2"
    used: [GET /api/v2/repositories, GET /api/v2/config/git/{ns}/{name}/default-branch, GET and POST /api/v2/pull-requests/{ns}/{name} (review plugin >= 3.11.0, status OPEN or DRAFT), GET /api/v2/repositories/{ns}/{name}/modifications/{revision}, git over HTTP at /repo/{ns}/{name}]
    webhooks: ["webhook plugin, JSON {changeset: [...]} per push; no signature exists, ripple authenticates with a shared secret in the custom header X-Ripple-Webhook-Secret, and the repository comes from ?namespace=&name= in the URL"]
    auth: "API key as Authorization: Bearer on the API and as the git basic-auth password (username ignored)"
    capabilities: "no auto-merge (not implemented, reported absent), drafts yes, rate limit undocumented"
    unverified: "everything: no real SCM-Manager was available. Bearer on the REST API, git basic auth, webhook header support and changeset branches in the deployed plugin, the 201 Location, merge-commit modifications and the 429 answer are from docs and source; the fake in internal/platform/scmmanager is written from them, not recorded"
    checked: 2026-11-23
  - name: bitbucket-server
    kind: http-api
    reference: https://developer.atlassian.com/server/bitbucket/10.0.swagger.v3.json
    docs: https://confluence.atlassian.com/bitbucketserver/manage-webhooks-938025878.html
    vendored: docs/vendor/bitbucket-server/
    used: [GET /rest/api/1.0/repos?permission=REPO_WRITE, GET and POST /projects/{key}/repos/{slug}/pull-requests, GET and POST .../pull-requests/{id}/merge?version=N with autoMerge, GET .../default-branch, GET .../compare/changes]
    webhooks: ["repo:refs_changed (no file list, no default branch, at most 5 commits)", "signature X-Hub-Signature = sha256=<hex HMAC-SHA256(body)>; X-Request-Id; X-Event-Key"]
    auth: "Basic user:token (x-token-auth for project and repository tokens)"
    unverified: "Retry-After on 429; merge response and merger for autoMerge; draft support by release; path.toString; Basic vs Bearer for project tokens; the fake is written from docs, not recorded"
    checked: 2026-11-23
  - name: gerrit-rest-api
    kind: http-api
    reference: https://gerrit-review.googlesource.com/Documentation/
    api-version: "documentation snapshot 2026-11-23, no version number published"
    vendored: docs/vendor/gerrit
    used: [GET /a/projects/ (type=CODE, state=ACTIVE, n, S, p), GET /a/changes/ (q=status:open project:X), GET /a/changes/{project}~{branch}~{Change-Id}, POST .../wip, POST .../revisions/current/review, POST .../hashtags, GET /a/projects/{p}/commits/{c}/files/, git push refs/for/<branch>%topic=<t>]
    auth: "HTTP Basic with the account's HTTP password under /a/ (REST and git)"
    webhooks: [webhooks plugin ref-updated, batch-ref-updated and project-created; no signature is documented, so ripple requires a secret in the hook URL query]
    rate-limits: "429 with no documented Retry-After; one minute assumed"
    unverified: "everything against a real Gerrit; webhook payload shape and URL token; clone URL /a/<project>; change URL /c/<project>/+/<n>; WIP and hashtag permissions; no-new-changes on identical re-push; merge-commit file lists; the fake is written from the docs, not recorded"
    checked: 2026-11-23
  - name: osv
    kind: http-api
    reference: https://ossf.github.io/osv-schema/
    docs: https://google.github.io/osv.dev/api/
    api-version: "OSV schema 1.9.2 (records seen on 2026-11-23); API v1"
    vendored: docs/vendor/osv/
    used: [GET https://storage.googleapis.com/osv-vulnerabilities/Go/modified_id.csv (newest first, read until the stored position), "GET https://api.osv.dev/v1/vulns/{id}", "POST https://api.osv.dev/v1/querybatch (ids only, at most 1000 queries and 5 page rounds per request, version without a v prefix)"]
    unused: [POST /v1/query, all.zip]
    rate-limits: "none documented; ripple makes one list request and at most RIPPLE_OSV_MAX_FETCH record requests per RIPPLE_OSV_INTERVAL"
    verified-live: "2026-11-23, four read-only requests (one querybatch of five queries, one record, the list size, the list): querybatch accepts the version with and without a v prefix for Go and answers alike; stdlib and toolchain are both queryable names (stdlib 1.21.0: 90 ids; toolchain 1.21.0: 28); a stdlib record carries introduced 1.21.0-0 events (GO-2023-2041); modified_id.csv was 453940 bytes and 9989 lines; two more single-query querybatch requests (stdlib and toolchain 1.21.0) gave 90 and 28 ids with no next_page_token and the two id sets were disjoint, so a stdlib record was not also listed under toolchain for that version"
    verified-live-2: "2026-11-24 (go test -tags live ./internal/advisory ./internal/e2e): GO-2021-0113 and GO-2022-0969 parse and assess; GET /v1/vulns/GO-1900-0000 is 404 (ErrNotFound); querybatch gives the same affected set as ripple's own evaluator for pinned versions, with and without the v prefix, an unaffected version gives an empty result; stdlib 1.21.0 gave about 90 ids and unversioned stdlib 182, neither with next_page_token; modified_id.csv is newest first with nanosecond timestamps (about 457 KB, 10074 lines) and the early stop reads only the head; a ripple run (history check) pointed at real OSV for golang.org/x/text v0.3.6 opened security PRs and left one open, for the highest fix, in 7 requests"
    unverified: "that real querybatch ever returns next_page_token for Go (neither stdlib query did, so pagination is not testable live); querybatch limits beyond the documented 1000 queries; stdlib and toolchain records overlapping for versions other than 1.21.0; any rate limit or abuse blocking of api.osv.dev; whether modified_id.csv is ever rewritten out of order; withdrawn records; the delay between an id appearing in the CSV and /v1/vulns/{id} serving it (a 404 is skipped, so an advisory could be missed); database_specific.severity is a GitHub field outside the OSV schema; an 'introduced' only range that carries a 'limit'; SEMVER ranges of records that mix ecosystems"
    checked: 2026-11-24
  - name: aws-emulator
    kind: test-double
    reference: https://github.com/aws/aws-emulator
    docs: https://docs.aws.amazon.com/emulator/latest/userguide/what-is-service.html
    version: "1.0.598 (ran in CI 2026-11-24, release date 2026.11.24)"
    commit: fc05151cc5015eb457b31a79871539af51fb9fdd
    package: "https://downloads.aws.com/aws-emulator/packages/aws-emulator/LTS/latest/aws-emulator_amd64.deb (the versioned URLs in the user guide answered 403 on 2026-11-24, so CI installs latest and accepts major 1; the version that ran is in the job summary)"
    used: [".github/workflows/emulator.yml: DynamoDB (store suite, e2e, admin commands) and CodeCommit (host API, git credential) with --auth off --telemetry off"]
    verified-against: "CI run 2026-11-24: 95 store subtests plus e2e and app tests pass on emulator as on the fake (parity check green); release v1.0.597 scores.json.gz read 2026-11-24 (DynamoDB 2127/2398, CodeCommit 194/225); docs/aws-emulator.md"
    unverified: "throttling, index lag, latency, IAM, regions other than us-west-2, certificates; git clone --depth 1 not served; SNS receiver not driven"
    checked: 2026-11-24
```

Not pinned, because they are not wire contracts: `git` (any version with `GIT_CONFIG_COUNT`,
2.31+), `golang.org/x/mod/modfile` (a Go module dependency, pinned in `go.mod`).
