# GitHub Enterprise Server

ripple's GitHub host talks to github.com by default and to a GitHub Enterprise Server (GHES) when pointed at one.
Written from GHES's own documentation ([docs/vendor/ghes](vendor/ghes/README.md)); **not run against a real GHES**.

## Configure

```sh
export RIPPLE_GITHUB_ENTERPRISE_URL=https://ghes.example.com   # the root, no path
```

That sets the REST base to `https://ghes.example.com/api/v3`, GraphQL to `https://ghes.example.com/api/graphql`
and git to `https://ghes.example.com`. Each can be overridden: `RIPPLE_GITHUB_API_URL`, `RIPPLE_GITHUB_GRAPHQL_URL`,
`RIPPLE_GITHUB_GIT_URL`. With only `RIPPLE_GITHUB_API_URL=https://HOST/api/v3` the GraphQL endpoint is still derived
correctly (`/api/v3` becomes `/api/graphql`); any other base gets `/graphql` appended, as on github.com. Plain `http`
is accepted only for loopback, as for every URL ripple is configured with. Create the App on the GHES instance and
point its webhook at `https://<ripple>/webhook`; installation tokens come from the same
`/app/installations/{id}/access_tokens` flow and last an hour.

## Differences handled

| Difference | What ripple does |
|---|---|
| REST and GraphQL live under different prefixes | derived per host, see above; nothing under a bare `/graphql` |
| A GHES release may not support `X-GitHub-Api-Version: 2026-03-10` (answer: 410) | the first 410 lowers the version to `2022-11-28` for the process and retries once. `RIPPLE_GITHUB_API_VERSION=2022-11-28` pins it and disables the fallback |
| GraphQL disabled, or a schema without `enablePullRequestAutoMerge` | auto-merge is skipped as on a host without it: the PR is opened and left for review, logged at info, not a warning |
| Webhook deliveries add `X-GitHub-Enterprise-Version`, `X-GitHub-Enterprise-Host`, `X-Hub-Signature` (SHA-1) | the receiver authenticates with `X-Hub-Signature-256` only and ignores the rest |
| The GHES release is not known up front | at start ripple reads `GET /api/v3/meta` (`installed_version`, unauthenticated) once and logs it at info; a release below ripple's own floor of 3.9 earns one warning. The floor is ripple's choice, not published by GitHub, and unverified. The release changes nothing else; a failed read is a warning and never blocks start |
| Rate limits are off by default | nothing to do; a 429 or 403 with `Retry-After` is still honoured if an administrator turns them on |

## Not covered

ripple uses only the REST endpoints and the one GraphQL mutation listed in [UPSTREAM.md](../UPSTREAM.md), so there is
no per-release feature table beyond the ones above. Unverified against a real instance: which release first accepts
`2026-03-10`, which first has the auto-merge mutation, the exact error each answers, and payload parity of the
webhook events.
