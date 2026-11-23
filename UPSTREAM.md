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
```

Not pinned, because they are not wire contracts: `git` (any version with `GIT_CONFIG_COUNT`,
2.31+), `golang.org/x/mod/modfile` (a Go module dependency, pinned in `go.mod`).
