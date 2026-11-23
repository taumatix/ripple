# GitLab excerpts for superseded merge requests

Excerpts retrieved 2026-11-23 from https://gitlab.com/gitlab-org/gitlab (branch master); the full "Retrieve merge request commits", "Update a merge request" (`state_event`) and "Retrieve merge request diff versions" sections are in api_merge_requests.md here. Excerpts are unmodified except for elision (...).

## doc/api/notes.md (https://docs.gitlab.com/api/notes/)

```
Some system-generated notes are tracked as separate resource events:

- [Resource label events](resource_label_events.md)
- [Resource state events](resource_state_events.md)
- [Resource milestone events](resource_milestone_events.md)
- [Resource weight events](resource_weight_events.md)
- [Resource iteration events](resource_iteration_events.md)

By default, `GET` requests return 20 results at a time, because the API results are paginated.
For more information, see [pagination](rest/_index.md#pagination).

### List all merge request notes

Lists all notes for a specified merge request.

```plaintext
GET /projects/:id/merge_requests/:merge_request_iid/notes
GET /projects/:id/merge_requests/:merge_request_iid/notes?sort=asc&order_by=updated_at
```

| Attribute           | Type              | Required | Description |
|---------------------|-------------------|----------|-------------|
| `id`                | integer or string | yes      | The ID or [URL-encoded path of the project](rest/_index.md#namespaced-paths) |
| `merge_request_iid` | integer           | yes      | The IID of a project merge request |
| `sort`              | string            | no       | Return merge request notes sorted in `asc` or `desc` order. Default is `desc` |
| `order_by`          | string            | no       | Return merge request notes ordered by `created_at` or `updated_at` fields. Default is `created_at` |

```shell
curl --header "PRIVATE-TOKEN: <your_access_token>" \
  --url "https://gitlab.example.com/api/v4/projects/5/merge_requests/11/notes"
```

  "author": {
    "id": 1,
    "username": "pipin",
    "email": "admin@example.com",
    "name": "Pip",
    "state": "active",
    "created_at": "2013-09-30T13:46:01Z"
  },
  "created_at": "2013-10-02T08:57:14Z",
  "updated_at": "2013-10-02T08:57:14Z",
  "system": false,
  "noteable_id": 2,
  "noteable_type": "MergeRequest",
  "project_id": 5,
  "noteable_iid": 2,
  "resolvable": false,
  "confidential": false,
  "internal": false
}
```

```shell
curl --header "PRIVATE-TOKEN: <your_access_token>" \
  --url "https://gitlab.example.com/api/v4/projects/5/merge_requests/11/notes/1"
```
### Create a merge request note

Creates a note for a specified merge request. Notes are not attached to specific
lines in a merge request. For other approaches with more granular control, see
[post comment to commit](commits.md#post-comment-to-commit) in the commits API,
and [create a new thread in the merge request diff](discussions.md#create-a-new-thread-in-the-merge-request-diff)
in the discussions API.

If you create a note where the body only contains an emoji reaction, GitLab returns this object.

```plaintext
POST /projects/:id/merge_requests/:merge_request_iid/notes
```

Parameters:

| Attribute                     | Type              | Required | Description |
|-------------------------------|-------------------|----------|-------------|
| `body`                        | string            | yes      | The content of a note. Limited to 1,000,000 characters. |
| `id`                          | integer or string | yes      | The ID or [URL-encoded path of the project](rest/_index.md#namespaced-paths) |
| `merge_request_iid`           | integer           | yes      | The IID of a project merge request |
| `created_at`                  | string            | no       | Date time string, ISO 8601 formatted. Example: `2016-03-11T03:45:40Z` (requires administrator or project/group owner rights) |
```

## doc/api/merge_request_approvals.md (https://docs.gitlab.com/api/merge_request_approvals/)

```
### Retrieve approval state for a merge request

Retrieves the approval state for a specified merge request.

In the response, `approved_by` contains information about all approvers of the merge request,
regardless of whether those approvals satisfy any approval rule. For more detailed information about
the approval rules in a merge request, and whether the approvals received satisfy those rules, see
the [`/approval_state` endpoint](#retrieve-approval-details-for-a-merge-request).

The attributes in the response depend on the deployed edition:

- GitLab Enterprise Edition returns every attribute in the following table, with or without a
  license. This includes GitLab.com and GitLab Dedicated.
- GitLab Community Edition returns only `approved`, `approved_by`, `user_can_approve`, and
  `user_has_approved`.

The `approved` field reflects whether the merge request meets its approval requirements, and its
value depends on the deployed edition:

- On GitLab Enterprise Edition, including without a license, `approved` is `true` when the configured
  approval rules are satisfied.
  When no approval rules apply, `approved` is `true` even when `approved_by` is empty.
- On GitLab Community Edition, `approved` is `true` only when at least one approval exists.

```plaintext
GET /projects/:id/merge_requests/:merge_request_iid/approvals
```

| `approvals_left`                      | integer      | Number of approvals still required before the merge request is approved. GitLab Enterprise Edition only. |
| `approvals_required`                  | integer      | Number of approvals that the approval rules require. GitLab Enterprise Edition only. |
| `approved`                            | boolean      | If `true`, the merge request is approved based on the requirements for the deployed GitLab edition. |
| `approved_by`                         | object array | Users who approved the merge request, regardless of whether their approvals satisfy an approval rule. |
```

## System note wording (GitLab source, not API documentation)

app/services/system_notes/issuables_service.rb and merge_requests_service.rb; the API documents that system notes carry `"system": true` but not their text:

```
    # Example Note text:
    #
    #   "changed title from **Old** to **New**"
    #
    # Returns the created Note object
    def change_title(old_title)
      new_title = noteable.title

      old_diffs, new_diffs = Gitlab::Diff::InlineDiff.new(old_title, new_title).inline_diffs

      marked_old_title = Gitlab::Diff::InlineDiffMarker.new(old_title).mark(old_diffs)
      marked_new_title = Gitlab::Diff::InlineDiffMarker.new(new_title).mark(new_diffs)

      body = "<p>changed title from <code class=\"idiff\">#{marked_old_title}</code> to <code class=\"idiff\">#{marked_new_title}</code></p>"

    #
    #   "changed the description"
    #
    # Returns the created Note object
    def change_description
      body = 'changed the description'

    def merge_when_checks_pass(sha)
      body = "enabled an automatic merge when all merge checks for #{sha} pass"

    def abort_auto_merge(reason, action: nil)
      body = "aborted the automatic merge because #{format_reason(reason)}"

    READY_NOTE = 'marked this merge request as **ready**'
    APPROVED_NOTE = 'approved this merge request'
```

## Assumed, unverified

- That the notes, versions and approvals endpoints answer the same on every self-managed version ripple supports.
- That a real force-push always creates a new diff version, and a title or description edit always a system note with the wording above.
- That a bot token (project or group access token) is allowed to read approvals and close merge requests; the docs state no permission for either here.
