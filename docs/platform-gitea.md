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

## Unverified

No real Gitea or Forgejo instance has been tested; the fake in `internal/testutil/gitea.go` is written
from the docs. Unconfirmed: the rate-limit response shape (undocumented by both), the status code of a
scheduled merge, whether Forgejo also sends `X-Hub-Signature-256`, label creation by name, the git
basic-auth username, detection of Forgejo before 7, and `repository` webhook events.
