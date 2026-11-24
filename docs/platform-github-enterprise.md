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

## A second GitHub on the same server

One server can serve github.com and a GHES together (or two GitHubs of either kind): the `RIPPLE_GITHUB_*` settings
above name the default host, and a second GitHub is its own host called `ghes`:

```sh
export RIPPLE_GHES_URL=https://ghes.example.com   # the root, no path; https (http only on loopback)
export RIPPLE_GHES_APP_ID=...                     # the App created on that GHES
export RIPPLE_GHES_KEY_FILE=/path/to/ghes-app.pem
export RIPPLE_GHES_WEBHOOK_SECRET=...             # that App's own secret
export RIPPLE_GHES_API_VERSION=2022-11-28         # optional pin, as RIPPLE_GITHUB_API_VERSION
```

Point that App's webhook at `https://<ripple>/webhook/ghes`; the default host keeps `/webhook` and
`/webhook/github`. Each host verifies deliveries with its own secret only. Its repos are stored and shown as
`ghes:owner/repo` (the default host's stay plain `owner/repo`), a repository id is only matched within its own
host, and `RIPPLE_ACTIONS_UPSTREAM_URL` is shared. With `RIPPLE_GHES_URL` unset the host is off. Only one extra
GitHub is configurable, and its URL must be a GHES root (see the roadmap). Covered end to end against two fake
GitHubs; **nothing ran against a real GHES**.

## Actions that live on github.com

`uses: actions/checkout@v4` on a GHES resolves to github.com through GitHub Connect or a synced copy; ripple
reads the tags of an action from the GHES first. Set `RIPPLE_ACTIONS_UPSTREAM_URL=https://api.github.com` and a
repository the GHES does not have (its tags answer 404) is read from there instead: tags, the commit a tag
points at (for SHA pins) and the release (for the PR's release-notes link). A repository the GHES does have is
never read from upstream, even for a tag or release only upstream's copy has. Reads are anonymous unless
`RIPPLE_ACTIONS_UPSTREAM_TOKEN` is set, which is sent to that URL only (anonymous github.com allows 60
requests an hour per address; one poll reads about three per action). The GHES installation token is never sent
upstream. The pull request is still opened on the GHES. See [github-actions.md](github-actions.md).

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
