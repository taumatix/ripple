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
  (`change_title`) shows up. A push after the pull request opened shows up as a second `pull_push`: opening the
  pull request already writes the first one, so exactly one is ripple's own and more than one is a touch (a
  force-push counts, it adds one too);
- on Gitea, a `content_version` other than 0, which an edited description raises.

Not detected: on Forgejo, an edited description (its pull request has no `content_version`); a label added by a person
(the timeline's `label` entries are taken as ripple's own); an assignee or milestone change is read as a touch. The
timeline's type names are from Gitea's source, not its API document, so a wrong name makes ripple close less, never
more. Run for real on Gitea 1.27.3, Forgejo 17.0.2 and Forgejo 7.0.16 (`TestRealCloseUntouchedPR`,
`TestRealPullPushTimeline`): an untouched pull request is closed, and the timeline is one `pull_push` when opened,
two after a second commit, three after a force-push. A label or comment added by a person, a rename and an edited
description on a real instance are still verified only against the fake. The excerpts it relies on are in
`docs/vendor/gitea/supersede-excerpts.md` and `docs/vendor/forgejo/supersede-excerpts.md`.

## Unverified

## Real instances

`.github/workflows/hosts-gitea.yml` runs the whole conformance suite (`platformtest.Run`, including push to exactly one
pull request, supersede and the injected 429) and the `TestReal*` tests against Gitea 1.27.3, Forgejo 17.0.2 and
Forgejo 7.0.16, as containers with SQLite and no secrets, on every pull request touching the host, weekly and on
demand. The recorded answers are in `docs/vendor/gitea/real-answers.md`; the tests assert them. What reality changed:

- A repository reports `empty=true` for up to about 2 s after its first push, and discovery skips an empty
  repository, so one whose first push raced a discovery stayed unregistered until a later one. `ListRepos` now polls
  `GET /repos/{o}/{r}` for a repository it has not seen empty before, for up to `Client.EmptySettle` (5 s), and lists
  it once it settles; one that stays empty costs a single wait. The suite's fixture no longer waits, so the real
  legs prove it (ROADMAP 280). Not covered: a `repository` created webhook for a repository still empty (ROADMAP 283).
- A fresh pull request already has one `pull_push` (see above); the first version closed nothing.
- A label name the repository lacks is dropped with `200 []`; the client now logs "labels not applied" naming them.
- Forgejo 7.0.16 refuses label names (`422`, ids only). Labelling never worked there. The client now retries a 422 with the
  ids from `GET .../labels` (first 50 labels), so `labels:` works on 7.x; `TestRealCreatePRLabels` proves it on each leg.
- No instance rate-limits by default, so the 429 path is exercised through the fixture's proxy only; what a
  throttling reverse proxy in front of a real instance sends is not recorded.
- The git basic-auth user is irrelevant (any is accepted); the token is what authenticates.
- Both Forgejo releases send `X-Hub-Signature-256` as well as `X-Forgejo-Signature`.
- A system webhook announces a created repository but never a deleted one, so the receiver's removal path is proven
  against the fake only; a deleted repository is noticed by discovery.
- Forgejo 7.0.16 is detected as Forgejo (`+gitea-1.21.11`). Forgejo before 7 was not run and is still undetectable.

## Unverified

Not run for real: Forgejo before 7, Gitea before 1.27, the Gitea SSH and PostgreSQL/MySQL configurations, a person's
label or comment on a pull request counted as a touch, `merge_when_checks_succeed` actually merging once checks pass
(only the scheduling was recorded), the 429 headers a real proxy would send, the `repository` webhook's `deleted`
and `renamed` actions, and archive (`archived` in `/user/repos` was not flipped on a real instance).
