# GitHub issues REST endpoints (dependency dashboard)

Read 2026-11-23 from docs.github.com/en/rest/issues/issues before `internal/platform/github/issues.go`
was written. This is a summary of what the page states, not a verbatim copy.

| Endpoint | Stated |
|---|---|
| `POST /repos/{o}/{r}/issues` | push access; 201; 410 when issues are disabled; also 403, 404, 422. Labels that do not exist are silently dropped. |
| `PATCH /repos/{o}/{r}/issues/{n}` | owner or push/triage access; 200; 403, 404, 410, 422. The endpoint also serves pull requests. |
| `GET /repos/{o}/{r}/issues` | 200, 404, 422; lists pull requests too, told apart by a `pull_request` key; `per_page` up to 100 (default 30); `state` is `open`, `closed` or `all`; `labels` is comma separated. |

How ripple uses it:

- Labels are dropped when missing, and the dashboard is found by label, so the client creates the
  label first (`POST /repos/{o}/{r}/labels`; 422 means it exists) and then the issue.
- 403, 404 and 410 on find, label or create all mean "issues unavailable for this repo". 404 or 410
  on an update means the issue is gone and is looked up again.
- A person's issue that carries the label but not the body marker is never touched.

Assumed, not verified against a live App:

- The App needs the Issues repository permission (read and write); the docs page lists 403 but not the
  message text or whether a missing permission answers 403 or 404. Both are treated alike.
- Disabled issues answer 410 on every endpoint including list (the page names 410 for create and update only).
- The label endpoint answers 422 for an existing label.
