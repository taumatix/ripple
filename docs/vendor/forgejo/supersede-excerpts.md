# Forgejo: what closing a superseded pull request relies on

Retrieved 2026-11-23 from https://codeberg.org/swagger.v1.json (version 17.0.0-121-39320c0+gitea-1.22.0).
The same operation ids and paths as in docs/vendor/gitea/supersede-excerpts.md exist, with the same
fields: repoGetPullRequest, repoEditPullRequest (`state`), repoGetPullRequestCommits (`page`, `limit`,
`verification`, `files`), repoListPullReviews, issueGetComments, issueCreateComment (`body`),
issueGetCommentsAndTimeline (TimelineComment `type`).

Differences found: Forgejo's PullRequest and EditPullRequestOption have **no `content_version`**, so a
description edit leaves no trace that ripple can read there.

## Assumed, unverified

The timeline `type` values are not listed in the swagger; Forgejo inherits Gitea's names (see the Gitea
excerpt) and none was retrieved from Forgejo's source.
