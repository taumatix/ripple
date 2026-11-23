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
    checked: 2026-11-22
  - name: github-rest-api
    kind: http-api
    reference: https://docs.github.com/en/rest
    api-version: "2026-03-10"
    header: "X-GitHub-Api-Version: 2026-03-10"
    used: [POST /app/installations/{id}/access_tokens, GET and POST /repos/{owner}/{repo}/pulls (draft), POST /repos/{owner}/{repo}/issues/{n}/labels, POST /graphql (enablePullRequestAutoMerge)]
    webhooks: [installation, installation_repositories, push, signature X-Hub-Signature-256 = sha256=HMAC-SHA256(body)]
    unverified: "enablePullRequestAutoMerge mutation shape; the labels endpoint creating missing labels (assumed); the fake GitHub in tests is written from the docs, not recorded"
    checked: 2026-11-22
  - name: go-module-proxy
    kind: http-api
    reference: https://go.dev/ref/mod#goproxy-protocol
    format: "GOPROXY protocol, used only by the go command that verifies a major bump (go mod tidy, build, vet); ripple itself does not call it"
    major-versions: https://go.dev/ref/mod#major-version-suffixes
    rules: "/vN suffix required from v2, /v1 not allowed, gopkg.in paths always .vN, +incompatible marks pre-module v2+ versions"
    verified-against: "the go command in internal/e2e against a fake proxy (internal/testutil/goproxy.go); not recorded from proxy.golang.org"
    checked: 2026-11-22
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
    unverified: "429 headers; queued-merge 202 and Location; x-token-auth with API tokens; merge-commit diffstat; the fake is written from docs, not recorded"
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
    unverified: "a repo name accepted as repositoryId; autoCompleteSetBy.id taken from the creator's id; the labels endpoint; commit changes for merge commits and paging; exact service-hook delivery headers; the fake Azure DevOps in tests is written from the docs, not recorded; names with spaces unsupported"
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
```

Not pinned, because they are not wire contracts: `git` (any version with `GIT_CONFIG_COUNT`,
2.31+), `golang.org/x/mod/modfile` (a Go module dependency, pinned in `go.mod`).
