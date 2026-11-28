# Gerrit

Ripple can propose dependency updates to a Gerrit instance. Gerrit has changes, not pull requests, so ripple
maps its flow onto Gerrit's: a push to `refs/for/<branch>` creates or updates a change. This host is written
from Gerrit's documentation, vendored in [vendor/gerrit](vendor/gerrit), and tested against a fake plus a real git
server. Since 2026-11-27 the conformance suite also runs against a real Gerrit 3.15.0 container with the bundled
webhooks plugin (`.github/workflows/hosts-gitlab-gerrit.yml`, `go test -tags gerrit`), see Verified and Unverified.

Repositories are Gerrit project names, stored as `gerrit:<project>`. Names may use letters, digits, `.`, `_`, `-`
and `/`; any other project is skipped with a log line.

## Settings

| Variable | |
|---|---|
| `RIPPLE_GERRIT_URL` | the Gerrit base URL, for example `https://gerrit.example.com` (required) |
| `RIPPLE_GERRIT_USER`, `RIPPLE_GERRIT_TOKEN` | the account and its HTTP password (Settings, HTTP Credentials), used for REST under `/a/` and for git (required) |
| `RIPPLE_GERRIT_WEBHOOK_SECRET` | secret of the webhook URL (required; see below) |
| `RIPPLE_GERRIT_PROJECT_PREFIX` | list only projects whose name starts with this |
| `RIPPLE_GERRIT_AUTHOR_EMAIL` | commit email of ripple's changes (default `ripple@users.noreply.invalid`); see below |

The account needs read and push to `refs/for/*` on each project, and ideally permission to set WIP and hashtags.

**Author email.** Gerrit rejects a pushed commit whose author email is not registered on the pushing account
unless the account holds "Forge Author" and "Forge Committer" (recorded: `email address ... is not registered in your account, and you
lack 'forge committer' permission`; Gerrit checks the committer identity, which ripple sets to the same email), and its validator refuses to register the default's `.invalid` domain (`PUT
/accounts/self/emails/...` answers 400 `invalid email address`). Either grant Forge Author and Forge Committer on `refs/heads/*` (recorded on Gerrit 3.15.0: with only Forge Author the push is then refused with `... you lack 'forge committer' permission` for ripple's committer identity) or
register an address on the account and set `RIPPLE_GERRIT_AUTHOR_EMAIL` to it. When Gerrit refuses a push for this
reason, ripple's error says so and names `RIPPLE_GERRIT_AUTHOR_EMAIL`. The default is unchanged (additive only).
The CI job checks both: an account with Forge Author pushes with the default email, an account without it is
refused with the message above.

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

## Head polling

Verified on Gerrit 3.15.0 (2026-11-28, `TestRealListRepoHeads`): the two reads below return the pushed commit for a fresh project and follow a second push. What is not measured is the cost of two reads per project on a large instance.

The client implements `app.HeadLister`, so a project whose default branch moved is re-scanned at the next
discovery even when its webhook never arrived. The project listing carries no commit, so this is **two extra reads
per project** each discovery, one project at a time: `GET /projects/<p>/HEAD` (the branch) then
`GET /projects/<p>/branches/refs%2Fheads%2F<branch>` (its `revision`). A 429 ends the listing and the app keeps
what it remembered; another failure leaves that project out for the round. The first head listing after a start
already reports every head (the app lists once more in that round to get the branches just learned), so a head that
moved while ripple was down is re-scanned on the first round. Gerrit has no call that names the default branch
of many projects at once (`GET /projects/?b=<branch>` needs the branch name), so the two reads per project stay.
The two reads are not counted as scan cost by the conformance suite. Verified against a real Gerrit 3.15.0 (the project state PUT, `GET /projects/?state=READ_ONLY`).

## Not supported

- **Auto-merge.** Gerrit's docs describe submit through labels and submit rules, not a request that completes
  later, so no `AutoMerger` is offered and a request for auto-merge is reported unsupported.
- A merged or abandoned change with the same Change-Id rejects a later push of that branch name; use a branch
  prefix or names unique per proposal.
- Gerrit may refuse a re-push whose content is identical ("no new changes"); that surfaces as a push error.
- The default branch of a project is learned at its first scan or its first head listing, not by the project listing.
- A draft is marked WIP after the push, so for a moment it is ready for review.

## Verified on a real Gerrit 3.15.0

The `gerrit` job of `hosts-gitlab-gerrit.yml` runs `gerritcodereview/gerrit` (image digest in the job's `gerrit-image.txt`
artifact) with its bundled webhooks plugin, as the image's administrator, and drives `platformtest.Run` through ripple's
own host: a release opens exactly one change in each importing project and none elsewhere; a push to a default branch
is rescanned; the `refs/for` push with `%topic` and the Change-Id footer; `go.mod` content on the change's revision;
the change URL `/c/<project>/+/<n>`; the clone URL `/a/<project>`; a superseded change abandoned with the new version
as its message; READ_ONLY pausing and ACTIVE resuming; the dependency dashboard skipped (no issues); no auto-merge.
The webhooks plugin posts `ref-updated` to the configured URL with the `?token=` query intact; its body is exactly
the shape the receiver reads (`internal/platform/gerrit/testdata/real-ref-updated-*.json`, recorded and trimmed to
what the plugin sent), including events for `refs/changes/*` and `refs/meta/config`, which the receiver ignores.
The workflow starts the image with `GERRIT_INIT_ARGS=--install-plugin=webhooks`; the plugin is also bundled in the image.

## Unverified

Not checked against a real server (the CI run is as an ordinary account, `ripple-svc`, with read, push on
`refs/for/refs/*` Forge Author and Forge Committer on `refs/heads/*`, never an administrator; the throwaway install's administrator only bootstraps): a `Retry-After` on
429 (assumed one minute otherwise; Gerrit cannot be made to rate-limit on demand); the "no new changes" refusal; file lists of merge commits; first-parent
walking of multi-commit pushes; `batch-ref-updated` and `project-created` events (the CI hook subscribes to
`ref-updated` only).

## Archive

Only GitHub reports archive and unarchive today (ripple pauses the repo on `archived`, resumes on `unarchived`). Gerrit sends no event for a project's state (the stream/webhook events were not verified to report it, 2026-11-23), but a project set to `READ_ONLY` is the closest thing to an archive and `GET /projects/?state=READ_ONLY` lists it (`docs/vendor/gerrit/rest-api-projects.md`, "State(s)"). The client implements `platform.ArchivedLister` on that listing, so a project that moves between ACTIVE and READ_ONLY is paused (index and pending jobs kept) and resumed instead of forgotten. A HIDDEN project is in neither listing and is forgotten as before. Run for real on Gerrit 3.15.0 (2026-11-28, `TestRealArchivedListing`, and `ArchivePausesAndUnarchiveResumes` of the conformance suite): a READ_ONLY project is absent from the ACTIVE listing, present in `?state=READ_ONLY`, and returns to ACTIVE when set back. The webhooks plugin delivered **one** event within 10 s of the state change; its type was not recorded, so whether the state change can be told from other events is unverified.

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
API's only marker, so it is treated as system activity). The abandon and its message are verified on a real Gerrit 3.15.0 as an ordinary account; the rest of this
list is verified only against the fake, written from the vendored docs.
