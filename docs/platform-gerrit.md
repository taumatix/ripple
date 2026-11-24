# Gerrit

Ripple can propose dependency updates to a Gerrit instance. Gerrit has changes, not pull requests, so ripple
maps its flow onto Gerrit's: a push to `refs/for/<branch>` creates or updates a change. This host is written
from Gerrit's documentation, vendored in [vendor/gerrit](vendor/gerrit), and tested against a fake plus a real git
server; it has **not** been run against a real Gerrit (see Unverified).

Repositories are Gerrit project names, stored as `gerrit:<project>`. Names may use letters, digits, `.`, `_`, `-`
and `/`; any other project is skipped with a log line.

## Settings

| Variable | |
|---|---|
| `RIPPLE_GERRIT_URL` | the Gerrit base URL, for example `https://gerrit.example.com` (required) |
| `RIPPLE_GERRIT_USER`, `RIPPLE_GERRIT_TOKEN` | the account and its HTTP password (Settings, HTTP Credentials), used for REST under `/a/` and for git (required) |
| `RIPPLE_GERRIT_WEBHOOK_SECRET` | secret of the webhook URL (required; see below) |
| `RIPPLE_GERRIT_PROJECT_PREFIX` | list only projects whose name starts with this |

The account needs read and push to `refs/for/*` on each project, and ideally permission to set WIP and hashtags.

## How it maps

- A proposal is `git push origin HEAD:refs/for/<base>%topic=<branch>`. The commit message is the title plus a
  `Change-Id: I<40 hex>` footer derived from the branch name, so the same branch always updates the same change
  (a new patch set) and never makes a second one. The branch is never created on the server.
- Open proposals are changes matching `status:open project:<p>` whose topic starts with ripple's branch prefix.
- The PR body is posted as a review message on the current patch set; a draft becomes work-in-progress; labels
  become hashtags (best effort).
- Branch names containing anything outside letters, digits, `.`, `_`, `-` and `/` are refused, since `%`, `,` and `=`
  would be read as push options.
- `gitws` gained an optional `Proposal` setting for this (ref and trailer functions); without it the push is the same
  force-push to `refs/heads/<branch>` every other host uses.

## Webhooks

Install the webhooks plugin and add a remote with URL `https://<ripple>/webhook/gerrit?token=<secret>` and events
`ref-updated` (or `batch-ref-updated`) and `project-created`. The plugin documents no signature, so the secret is
the only authentication and it appears in the URL: serve ripple over HTTPS only, keep it out of access logs, and use
a secret used nowhere else. It is compared in constant time before the body is read. The Basic-auth password of the
request is also accepted. Push events list no files, so ripple asks the commit files API for each pushed commit on
a branch (up to 20) and re-scans only when a file matters; on any doubt it re-scans.

## Not supported

- **Auto-merge.** Gerrit's docs describe submit through labels and submit rules, not a request that completes
  later, so no `AutoMerger` is offered and a request for auto-merge is reported unsupported.
- A merged or abandoned change with the same Change-Id rejects a later push of that branch name; use a branch
  prefix or names unique per proposal.
- Gerrit may refuse a re-push whose content is identical ("no new changes"); that surfaces as a push error.
- The default branch of a project is learned at its first scan, not at discovery.
- A draft is marked WIP after the push, so for a moment it is ready for review.

## Unverified

Everything here was written from the docs and a fake, not recorded from a real server: the webhooks payload shape
and whether the URL token reaches ripple through the plugin; a `Retry-After` on 429 (assumed one minute otherwise);
the clone URL `/a/<project>` and the change URL `/c/<project>/+/<n>`; WIP and hashtag permissions; the "no new
changes" refusal; file lists of merge commits; first-parent walking of multi-commit pushes.

## Archive

Only GitHub reports archive and unarchive today (ripple pauses the repo on `archived`, resumes on `unarchived`). Gerrit sends no event for a project's state (the stream/webhook events were not verified to report it, 2026-11-23), but a project set to `READ_ONLY` is the closest thing to an archive and `GET /projects/?state=READ_ONLY` lists it (`docs/vendor/gerrit/rest-api-projects.md`, "State(s)"). The client implements `platform.ArchivedLister` on that listing, so a project that moves between ACTIVE and READ_ONLY is paused (index and pending jobs kept) and resumed instead of forgotten. A HIDDEN project is in neither listing and is forgotten as before. Verified against the fake only.

## Superseded pull requests

When a newer release replaces an open ripple change, ripple abandons the stale change with the new version as
its abandon message (`POST /changes/{number}/abandon`, one request, so a failed abandon leaves neither the
abandon nor the comment). The topic and the Change-Id stay; nothing is deleted. Set
`close-superseded-pull-requests: false` to keep them open.

It abandons only an untouched change. **Touched** means: a patch set whose commit author or committer is not
ripple's identity; a non-zero vote on any label; a change message written by a person (one with an author and
without an `autogenerated:` tag; Gerrit leaves the author of its own messages unset); a published inline comment;
or a change that is no longer open or no longer has ripple's topic. Every read must succeed (`GET
/changes/{n}?o=ALL_REVISIONS&o=ALL_COMMITS&o=DETAILED_LABELS&o=MESSAGES`, `GET /changes/{n}/comments`) or
nothing is abandoned.

**Not detected:** a patch set a person uploaded under ripple's own commit identity, a reviewer added without a
message or vote, hashtags, the attention set, and a message from a tool that tags itself `autogenerated:` (the
API's only marker, so it is treated as system activity). All of this is verified only against the fake, written
from the vendored docs; the abandon message and permission to abandon are unverified on a real server.
