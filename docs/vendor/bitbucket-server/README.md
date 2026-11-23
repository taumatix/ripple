# Vendored upstream documentation (Bitbucket Data Center / Server)

Fetched 2026-11-23 and read before `internal/platform/bitbucketserver` was written. Atlassian's own text and
OpenAPI document; each file names its source URL. Bitbucket Server was renamed Bitbucket Data Center; the
product is not Bitbucket Cloud (a different API, different webhooks).

| File | Source | Carries |
|---|---|---|
| `webhooks.md` | confluence.atlassian.com/bitbucketserver/manage-webhooks-938025878 | project vs repository hooks (a project hook is inherited by its repos), `X-Hub-Signature: sha256=<HMAC-SHA256 hex>` when a secret is set |
| `event-payload.md` | confluence.atlassian.com/bitbucketserver/event-payload-938025882 | `X-Request-Id`, `X-Event-Key`, `X-Hub-Signature`; the `repo:refs_changed` payload (no file list, at most 5 commits) |
| `http-access-tokens.md` | confluence.atlassian.com/bitbucketserver/http-access-tokens-939515499 | user, project and repository tokens; Basic auth (`x-token-auth` for project/repo tokens) and Bearer; a token cannot merge a PR |
| `rate-limiting.md` | confluence.atlassian.com/bitbucketserver/improving-instance-stability-with-rate-limiting-976171954 | token bucket per user, HTTP 429 |
| `openapi-excerpt.json` | developer.atlassian.com/server/bitbucket/10.0.swagger.v3.json (Bitbucket Data Center 10.0) | the paths ripple calls and their schemas: list repos, list/create PR, merge with `autoMerge`, default branch, compare changes |

The OpenAPI paths are `/api/latest/...`, served under `<instance>/rest/api/latest` and `<instance>/rest/api/1.0`.

Not found in these documents, so **assumed, unverified**: whether a 429 carries `Retry-After` (the rate-limiting
page names only the status; Jira's and Bamboo's pages for the same mechanism name `Retry-After` and
`X-RateLimit-*`); what the merge endpoint returns when `autoMerge` is set; which user performs a scheduled
auto-merge (the token page says a token cannot merge); that `draft` on create is honoured (the schema has it, the
release it arrived in was not found); the spelling of a change's `path` on a real server (`components` is
documented, `toString` is not); that `links.self[0].href` is filled in on a created PR (the webhook sample shows
`null`).
