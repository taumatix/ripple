# Bitbucket Data Center: pull request endpoints used to close a superseded PR

Source: the Bitbucket Data Center REST OpenAPI document (version 10.0), read 2026-11-23.
Base: `https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/`.

## Retrieved

- `POST /api/latest/projects/{projectKey}/repos/{repositorySlug}/pull-requests/{pullRequestId}/decline`
  with the query parameter `version`: "The current version of the pull request. If the server's version
  isn't the same as the specified version the operation will fail." Responses 200, 401, 404, 409.
- `POST .../pull-requests/{pullRequestId}/comments` with `{"text": "..."}` adds a general comment.
- `GET .../pull-requests/{pullRequestId}/activities`: paged (`start`, `limit`; default page 25) list of
  `RestPullRequestActivity`, each with an `action` enum (OPENED, COMMENTED, APPROVED, UNAPPROVED, UPDATED,
  RESCOPED, MERGED, DECLINED, REVIEWED, ...).
- `GET .../pull-requests/{pullRequestId}/commits`: paged list of `RestCommit` with `author` and
  `committer`, each carrying `emailAddress`.
- Page envelope: `values`, `isLastPage`, `nextPageStart`.
- `RestPullRequest` has no comment count.

## Assumed, unverified

- That the first activity of every pull request is `OPENED`, and that nothing the system does on creation
  adds another action (if it does, ripple leaves the PR open: the safe direction).
- That a force push appears as `RESCOPED` (or changes the commit list); either way it is caught.
- That `committer.emailAddress` is filled for every commit.
- That the `state` of a pull request is `OPEN`, `DECLINED` or `MERGED`.
- That the activity log marks no action as system-generated, so none is ignored.
