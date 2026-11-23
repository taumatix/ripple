# GitHub Enterprise Server: excerpts read before the GHES support was written

Fetched 2026-11-23 from docs.github.com/en/enterprise-server@latest (which names GHES 3.22 as the newest
release). Quotes are as the fetch tool returned them; each names its page.

| Page | Quote |
|---|---|
| `/rest/quickstart` | REST base URL is `http(s)://HOSTNAME/api/v3` |
| `/graphql/guides/forming-calls-with-graphql` | GraphQL endpoint is `http(s)://HOSTNAME/api/graphql` (not under `/api/v3`) |
| `/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation` | `POST http(s)://HOSTNAME/api/v3/app/installations/INSTALLATION_ID/access_tokens`; "The installation access token will expire after 1 hour." |
| `/rest/overview/api-versions` | supported versions `2026-03-10` and `2022-11-28` (the latter ends 2028-03-10); "Requests without the `X-GitHub-Api-Version` header will default to use the `2022-11-28` version."; an unsupported or closing-down version gets "a 410 Gone response"; "GitHub Enterprise Server versions are decoupled from REST API versions" and each release's notes say which API versions it supports |
| `/rest/meta/meta` | `GET /api/v3/meta` returns `installed_version` (GHES only) |
| `/webhooks/webhook-events-and-payloads` | headers `X-GitHub-Event`, `X-GitHub-Delivery`, `X-GitHub-Hook-ID`, `X-Hub-Signature-256` (HMAC-SHA256 hex), `X-Hub-Signature` (SHA-1, "for compatibility"), `X-GitHub-Enterprise-Version`, `X-GitHub-Enterprise-Host` (both GHES only), `User-Agent: GitHub-Hookshot/...` |
| `/rest/using-the-rest-api/rate-limits-for-the-rest-api` | "Rate limits are disabled by default for GitHub Enterprise Server." |

Not found in the docs, so **assumed, unverified**:

- Which GHES release first understands `2026-03-10` (3.22 lists it; older ones are assumed to answer 410, as the
  docs say for any unsupported version) and from which release the header is understood at all (an older
  release is assumed to ignore it, which is the same as `2022-11-28`).
- That `enablePullRequestAutoMerge` exists on every GHES release ripple can meet (it is a github.com feature
  that GHES gained in a release not found in these pages) and the shape of the error an older schema returns
  (assumed: HTTP 200 with a GraphQL `errors` entry whose message names the missing field).
- The exact status of a GHES with GraphQL or the REST API disabled (assumed 404).
- That the webhook payloads ripple parses (`installation`, `installation_repositories`, `push`, `repository`)
  are identical on GHES.
- Nothing here was run against a real GHES: the tests use a fake that serves only `/api/v3` and `/api/graphql`.
