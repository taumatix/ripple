# Review plugin: rejecting a pull request (source reading, not verbatim)

Summarised from the public source of `scm-manager/scm-review-plugin` on 2026-11-23
(`PullRequestResource.java`, `DefaultPullRequestService.java`), not copied: no verbatim excerpt
is saved here.

- `POST .../pull-requests/{id}/reject` exists and answers 204; it needs the modify-pull-request
  privilege. `setRejected()` sets the status to REJECTED.
- Sub-resources `comments/` and `changes/` (history) exist, and `PUT` updates title and description.
- `lastModified` moves on every update, so it cannot tell a person's edit from a system one.

Not retrieved, assumed, unverified: any official REST documentation for these endpoints (the
vendored README says there is none), the response shape of `comments/` and `changes/`, whether
a comment can be added to a rejected pull request, which pull request fields change on a rename or
description edit, and how a commit's author is reported per pull request.
