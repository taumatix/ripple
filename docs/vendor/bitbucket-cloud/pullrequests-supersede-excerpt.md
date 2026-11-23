<!-- Excerpt of what the supersede code (internal/platform/bitbucket/closer.go) relies on, taken from the vendored pullrequests-api.md (retrieved 2026-11-23 from https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/). Nothing here is recalled from memory. -->

Retrieved:

- "Decline a pull request": `POST /repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/decline`, "Declines the pull request"; scope `pullrequest:write`. No request body is documented as required.
- "Create a comment on a pull request": `POST .../pullrequests/{pull_request_id}/comments`, "Returns the newly created pull request comment". The body shape `{"content": {"raw": "..."}}` follows the comment objects in the activity example (`content.raw`, `content.markup`).
- "List comments on a pull request": `GET .../comments`; entries have `deleted` and `content`.
- "List commits on a pull request": `GET .../commits`: "A paginated list of commits made on the given pull request, in chronological order. This list will be empty if the source branch no longer exists."
- "List a pull request activity log": `GET .../activity`: "returns reviewer comments, updates, approvals and request changes". An `update` entry holds `title`, `description`, `reason`, `state`, `source` (commit and repository), `destination`, `author`, `date`; the example entries sit beside a `pull_request` object. Comment entries hold `comment`, approvals hold `approval`.
- Pull request object: `state` (OPEN), `title`, `description`, `source`, `comment_count`, `task_count`, `updated_on`.

Assumed, unverified (the fetched text does not show them):

- The commit object's `author.raw` is `Name <email>`. The commit schema was not retrievable (the commits reference page was truncated); a different shape reads as "someone else's commit", which only leaves the PR open.
- `source.repository.full_name` on the pull request object (it appears in the `update` example's `source`).
- The key for a request-for-changes activity entry, and whether the service logs an `update` entry when a pull request is created. Any key other than `update` and `pull_request` is treated as a person's activity.
- Whether a force push is logged as an `update` with a new source commit.
- Whether the service adds comments of its own to a pull request.
