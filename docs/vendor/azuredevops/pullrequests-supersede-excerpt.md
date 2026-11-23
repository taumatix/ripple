# Azure DevOps: what was read to decide whether closing a superseded PR is possible

Sources (7.1, read 2026-11-23):
- Update (vendored): `PATCH .../pullrequests/{id}` can set Status (`abandoned`), Title, Description. Abandoning is possible.
- https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-threads/create?view=azure-devops-rest-7.1
  `POST .../pullRequests/{id}/threads` with `{"comments":[{"parentCommentId":0,"content":"...","commentType":1}],"status":1}` comments on the PR.
- https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-threads/list?view=azure-devops-rest-7.1
  `GET .../threads`: comments carry `commentType` (`text`, `codeChange`, `system`); system threads record reviewer
  changes, votes (`VoteUpdate`) and ref updates (`RefUpdate`) as `CodeReviewThreadType` properties.
- https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-commits/get-pull-request-commits?view=azure-devops-rest-7.1
  `GET .../commits`: `GitCommitRef` with `author` and `committer` (`email`, `name`).
- https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-iterations/list?view=azure-devops-rest-7.1
  `GET .../iterations`: `reason` is `create`, `push`, `forcePush`, `rebase`, ...

## Not retrievable

Nothing in these pages records that the title or the description was edited, by whom, or what they were
before, and a pull request carries no edit counter or original title. A rename or an edited description is
therefore not readable. (Whether the service writes a system thread for it is assumed, unverified, and not
relied on.)
