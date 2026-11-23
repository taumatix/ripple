# Bitbucket Server / Data Center

Host `bitbucket-server`; repositories are stored as `bitbucket-server:KEY/slug`. This is the self-hosted
product (REST under `<instance>/rest/api/1.0`), not Bitbucket Cloud, which has a different API and webhooks.

## Configuration

| Variable | Meaning |
|---|---|
| `RIPPLE_BITBUCKET_SERVER_URL` | Instance URL including any context path, e.g. `https://git.example.org/bitbucket`. Unset turns the host off. |
| `RIPPLE_BITBUCKET_SERVER_TOKEN` | HTTP access token with project/repository write. Sent as Basic `user:token`; git uses the same pair. |
| `RIPPLE_BITBUCKET_SERVER_WEBHOOK_SECRET` | Secret of the webhooks. Required. |
| `RIPPLE_BITBUCKET_SERVER_USER` | User sent with the token: the owner of a personal token, `x-token-auth` for a project or repository token. Default `ripple`. |

## How it works

- **Discovery**: no repository-created event exists, so ripple lists `GET /repos?permission=REPO_WRITE` (not
  archived) periodically. A repository rename (`repo:modified`) is left to that listing.
- **Webhooks**: add a project (inherited by its repos) or repository webhook for "Repository: Push"
  pointing at `/webhook/bitbucket-server`, with a secret. Deliveries are authenticated by
  `X-Hub-Signature: sha256=<HMAC-SHA256 hex>`; `X-Request-Id` dedupes.
- **Push lookups**: `repo:refs_changed` carries neither the default branch nor the changed files (and at most
  5 commits). The receiver asks `GET .../default-branch` (cached an hour per repo) and, for a push to it,
  `GET .../compare/changes` to get the files, so the shared "irrelevant push costs no calls" rule counts
  these two lookups apart (see `TestPushLookupsAreBounded`). Nothing is cloned or scanned for a push that
  changes no relevant file. If the file list cannot be fetched, the push forces a re-scan.
- **Pull requests**: `fromRef`/`toRef` carry full `refs/heads/...` ids; a draft sets `draft: true`.
  Bitbucket Data Center has no pull request labels, so `Labels` is not applied.
- **Auto-merge**: reads the PR's `version`, then `POST .../merge?version=N` with `{"autoMerge":true}`.
  A 403 means the repository's auto-merge setting is off; the error is returned.
- **Rate limits**: HTTP 429 holds the host back until `Retry-After`, else a minute.

Sources are vendored in `docs/vendor/bitbucket-server/` (see its README).

## Unverified

No real Bitbucket Server or Data Center instance has been tested; the fake in
`internal/testutil/bitbucketserver.go` is written from the docs. Unconfirmed: that a 429 carries
`Retry-After`; the merge response for `autoMerge`, and which user performs the scheduled merge (the token
page says a token cannot merge); that `draft` is honoured and from which release; `path.toString` in
`compare/changes` (the code falls back to `components`); `links.self[0].href` on a created PR (a built URL is
the fallback); Basic versus Bearer for project and repository tokens (the token page shows both); repositories
with no commits in listings; the `permission` filter on `/repos`.

## Archive

Only GitHub reports archive and unarchive today (ripple pauses the repo on `archived`, resumes on `unarchived`). Bitbucket Data Center: an archive event is not documented to ripple's knowledge and was not verified (2026-11-23). ripple maps none and nothing pauses on archive.

## Superseded pull requests

When a newer release's PR replaces ripple's own earlier one, the old PR is declined
(`POST .../pull-requests/{id}/decline?version=N`, with the version read just before) and a comment naming the
new version is added (`POST .../comments`), the decline first so a retry never repeats the comment. The branch
is not deleted. Setting `close-superseded-pull-requests: false` turns it off.

It is declined only when nobody but ripple touched it. Touched means any of: the source branch is in another
repository; the PR does not have exactly one commit whose author and committer are both ripple's identity; or
its activity log (`.../activities`) holds any action but `OPENED` (a comment, an approval, a review, an edit
of title or description, a rescope from a push or force push, a reviewer change). The API does not mark
system activity, so none is ignored. A failed read is an error and declines nothing; a concurrent edit makes
the decline answer 409, also an error with the PR left open.

Not detected: anything the activity log does not record. Everything here is verified only against the fake
(`internal/testutil/bitbucketserver.go`); the assumptions are listed in
`docs/vendor/bitbucket-server/pullrequests-supersede-excerpt.md`.
