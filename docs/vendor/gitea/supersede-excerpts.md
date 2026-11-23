# Gitea: what closing a superseded pull request relies on

Retrieved 2026-11-23 from the instance's own OpenAPI document, https://gitea.com/swagger.v1.json
(version 29.0.0+dev-160-g55186ca39b). Operation ids as published:

| Operation | Method and path | Used for |
|---|---|---|
| repoGetPullRequest | GET /repos/{owner}/{repo}/pulls/{index} | `state`, `head.ref`, `head.repo.full_name`, `content_version` |
| repoEditPullRequest | PATCH /repos/{owner}/{repo}/pulls/{index} | EditPullRequestOption `state` ("The new state for the pull request") |
| repoGetPullRequestCommits | GET /repos/{owner}/{repo}/pulls/{index}/commits | `commit.author.email`, `commit.committer.email`; query `page`, `limit`, `verification`, `files` |
| repoListPullReviews | GET /repos/{owner}/{repo}/pulls/{index}/reviews | any entry |
| issueGetComments | GET /repos/{owner}/{repo}/issues/{index}/comments | any entry |
| issueCreateComment | POST /repos/{owner}/{repo}/issues/{index}/comments | CreateIssueCommentOption `body` |
| issueGetCommentsAndTimeline | GET /repos/{owner}/{repo}/issues/{index}/timeline | TimelineComment `type`, `old_title`, `new_title`, `old_ref`, `new_ref` |

PullRequest has `content_version` ("The version of the pull request content for optimistic
locking"); EditPullRequestOption accepts `content_version` ("to detect conflicts during editing").

## Assumed, unverified

The swagger types TimelineComment.type as a bare string with no list of values. The names ripple
relies on are Gitea's comment type names from its source (models/issues/comment.go), recalled, not
retrieved here:

- `label`, `pull_scheduled_merge`, `pull_cancel_scheduled_merge`: entries ripple's own actions leave.
- `change_title` (rename), `pull_push` (a push to the head branch after the PR opened, force-push
  included), `comment`, `review`, `code`: touches. Every type not in the first group counts as touched,
  so a wrong or new name makes ripple close less, never more.
- That `content_version` is 0 on a new pull request and rises when the description is edited.
- That a PR opened from a fork reports a different `head.repo.full_name`.
