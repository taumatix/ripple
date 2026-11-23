# Bitbucket Cloud

Host `bitbucket`. Repositories are stored as `bitbucket:workspace/slug`; the receiver is `/webhook/bitbucket`.
Bitbucket Server / Data Center is a different API and a different roadmap entry.

## Configuration

| Variable | Meaning |
|---|---|
| `RIPPLE_BITBUCKET_TOKEN` | Repository access token or API token. Sent as `Authorization: Bearer`; git uses username `x-token-auth` with the token as password. Unset turns the host off. |
| `RIPPLE_BITBUCKET_WEBHOOK_SECRET` | Secret of the push webhooks. Required: Bitbucket's own secret is optional, ripple's is not. |
| `RIPPLE_BITBUCKET_WORKSPACES` | Comma-separated workspace slugs to discover. Required. |
| `RIPPLE_BITBUCKET_API_URL` | Default `https://api.bitbucket.org/2.0`. |
| `RIPPLE_BITBUCKET_GIT_URL` | Default `https://bitbucket.org`. |

Token scopes: repository read and write, pull request write. Webhook management needs the repository
scope (ripple does not create webhooks itself; do it by hand).

## Webhook

Per repository (or workspace): URL `https://<ripple>/webhook/bitbucket`, trigger `Repository > Push`,
and a secret equal to `RIPPLE_BITBUCKET_WEBHOOK_SECRET`. Deliveries are verified with
`X-Hub-Signature: sha256=<HMAC-SHA256 of the body>`; a missing or malformed header is refused before the
body is read, and a repeated `X-Request-UUID` is dropped.

## How it works

- **Discovery**: Bitbucket sends no event when a repository appears, so ripple lists each workspace
  (git repositories only) and records the default branch.
- **Changed files**: the `repo:push` payload has no file list. For a plain branch update ripple asks
  the diffstat of each listed commit (at most five). A new branch, a force push, a truncated commit
  list or a failed lookup leaves the file list empty, which forces a re-scan (always correct, dearer).
  This is why the conformance suite gained an optional `FilelessPushes` marker: such a fixture is held to
  "no re-scan scans", not "zero API calls".
- **Pagination** is by `page=N`; `next` links are never followed, so the token cannot be sent elsewhere.
- **Labels** are ignored: Bitbucket Cloud pull requests have none.
- **Rate limit**: 1000 requests per hour, rolling. A 429 holds the host back until `Retry-After`,
  else one minute (the 429 headers are undocumented).

## Auto-merge decision

Bitbucket Cloud has no auto-merge. Its merge endpoint merges immediately, which would skip checks, so
ripple never calls it blind. The capability is implemented through the **merge queue** only: if the
default branch has a queue in state ACTIVE or PAUSED, the PR is queued (`merge?async=true`, squash,
expecting 202). With no queue, or a DRAINING or SUSPENDED one, it returns `ErrNoAutoMerge` and the PR
stays open for a human. There is no merge-check poll and no immediate merge.

## Unverified

Written from vendored docs (`docs/vendor/bitbucket-cloud/`) against a fake; no real Bitbucket was used.
Unconfirmed: the 429 response headers, the 202 and Location of a queued merge, queue state names beyond
those documented, whether `x-token-auth` works with API tokens for git, whether payload `commits` lists
are always newest first, and the diffstat of merge commits.

## Superseded pull requests

When a newer release's PR replaces ripple's own earlier one, the old PR is declined
(`POST .../pullrequests/{id}/decline`) and a comment naming the new version is added, the decline first
so a retry never repeats the comment. The branch is not deleted. Setting `close-superseded-pull-requests: false`
turns it off.

It is declined only when nobody but ripple touched it. Touched means any of: the source branch is in
another repository; the PR does not have exactly one commit whose `author.raw` is ripple's identity; any
comment or task (`comment_count`, `task_count`, the comments list); any activity-log entry other than an
`update` (a comment, an approval, a request for changes); or an `update` that records a title,
description or source commit other than the current ones (a rename, an edited description, an extra or
rewritten commit). A failed read is an error and declines nothing; more than 250 activity entries counts
as touched.

Not detected: a reviewer added without activity, a force push the activity log does not record, and the
commit's committer (the API shows only the author). Everything here is verified only against the fake
(`internal/platform/bitbucket/fake_test.go`); the assumptions are listed in
`docs/vendor/bitbucket-cloud/pullrequests-supersede-excerpt.md`.
