# Gitea and Forgejo

One host, `gitea`, serves both. Repositories of this host are stored as `gitea:owner/repo`.

## Configuration

| Variable | Meaning |
|---|---|
| `RIPPLE_GITEA_URL` | Instance URL, e.g. `https://codeberg.org`. Unset turns the host off. |
| `RIPPLE_GITEA_TOKEN` | Access token (scopes: repository read/write). Sent as `Authorization: token ...`; git uses it as the basic-auth password. |
| `RIPPLE_GITEA_WEBHOOK_SECRET` | Secret of the push webhooks. Required. |
| `RIPPLE_GITEA_USER` | Git basic-auth user, default `ripple`. |
| `RIPPLE_GITEA_FLAVOUR` | `auto` (default), `gitea` or `forgejo`. |

## How it works

- **Flavour** comes from `GET /api/v1/version`: Forgejo 7+ reports `X.Y.Z+gitea-A.B.C`. It only changes
  the auto-merge body (`do` on Gitea, `Do` on Forgejo). Set the variable for Forgejo before 7.
- **Discovery**: neither project has an install event, so ripple lists `GET /api/v1/user/repos`
  (non-archived, non-empty, push permission) and re-scans on push webhooks. Add a repository webhook
  (type Gitea or Forgejo, JSON, push events) pointing at ripple's `/webhook/gitea` receiver.
- **Signatures**: `X-Gitea-Signature` or `X-Forgejo-Signature` (hex HMAC-SHA256, no prefix) or
  `X-Hub-Signature-256` (`sha256=`). Any one that verifies authenticates the delivery.
- **Draft PRs** are a `WIP: ` title prefix; the API has no draft field.
- **Auto-merge** is "merge when checks succeed" (squash). Rate limits (429, `Retry-After`, or the IETF
  `RateLimit` header's `t=`) hold the host back.

Sources are vendored in `docs/vendor/` (see its README).

## Archive

ripple pauses a repo on archive and resumes it on unarchive (no scan, pending jobs kept). Gitea's and Forgejo's webhook documentation list no archive event (checked 2026-11-23 against docs/vendor/gitea/webhooks.md and docs/vendor/forgejo/webhooks.md, both vendored), so ripple reads the `archived` flag of each repository in `GET /user/repos` on every discovery: a repo whose flag flips is paused or resumed, not removed or added. It is seen within one discovery interval. Not verified against a real Gitea or Forgejo; only the fake has run.

## Superseded pull requests

When a newer release replaces ripple's own pull request for an older version of the same module, ripple closes the
older one (`PATCH .../pulls/{index}` with `state: closed`) and leaves one comment naming the new version, unless
`close-superseded-pull-requests: false`. The branch is kept. Ripple closes only a pull request nobody else has
touched, and never on doubt: if any read fails it is left open and the error is logged. Touched means any of:

- the head branch is not in this repository, or the pull request does not have exactly one commit, or that
  commit's author or committer is not ripple's commit identity;
- a review or an issue comment;
- a timeline entry other than ripple's own (a label, a scheduled or cancelled auto-merge). That is how a rename
  (`change_title`) and a push to the branch after it opened (`pull_push`, a force-push included) show up;
- on Gitea, a `content_version` other than 0, which an edited description raises.

Not detected: on Forgejo, an edited description (its pull request has no `content_version`); a label added by a person
(the timeline's `label` entries are taken as ripple's own); an assignee or milestone change is read as a touch. The
timeline's type names are from Gitea's source, not its API document, so a wrong name makes ripple close less, never
more. All of this is verified only against the fake; the excerpts it relies on are in
`docs/vendor/gitea/supersede-excerpts.md` and `docs/vendor/forgejo/supersede-excerpts.md`.

## Unverified

No real Gitea or Forgejo instance has been tested; the fake in `internal/testutil/gitea.go` is written
from the docs. Unconfirmed: the rate-limit response shape (undocumented by both), the status code of a
scheduled merge, whether Forgejo also sends `X-Hub-Signature-256`, label creation by name, the git
basic-auth username, detection of Forgejo before 7, and `repository` webhook events.
