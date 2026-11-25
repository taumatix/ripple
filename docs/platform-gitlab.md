# GitLab

ripple works against gitlab.com and self-managed GitLab. It opens merge requests, can ask GitLab to merge
them when the pipeline succeeds, and learns about pushes from a project or group webhook.

**Status: written from GitLab's documentation, never run against a real GitLab.** See
[What is unverified](#what-is-unverified). The docs it follows are vendored in
[docs/vendor/gitlab](vendor/gitlab) and pinned in [UPSTREAM.md](../UPSTREAM.md).

## Settings

The host is off until a token is set.

| Variable | Meaning |
|---|---|
| `RIPPLE_GITLAB_TOKEN` | Default token, used for every project no scoped token covers. |
| `RIPPLE_GITLAB_TOKENS` | `scope=token,scope=token`. A scope is a group (`team/sub`) or a project (`team/sub/app`); the token is used for everything under it, and the longest matching scope wins. |
| `RIPPLE_GITLAB_URL` | The instance, default `https://gitlab.com`. A self-managed instance under a relative URL root works (`https://example.com/gitlab`). |
| `RIPPLE_GITLAB_GIT_URL` | Clone URL prefix when it differs from the instance URL. |
| `RIPPLE_GITLAB_WEBHOOK_SECRET` | The webhook's **Secret token**. |
| `RIPPLE_GITLAB_WEBHOOK_SIGNING_TOKEN` | The webhook's **Signing token** (`whsec_...`, GitLab 19.0 and later). When set, a valid signature is required and the secret token alone is refused. |

At least one of the two webhook settings is required; ripple refuses to start GitLab with neither, because its
receiver would refuse every delivery. Prefer the signing token: it authenticates the body, and ripple also
rejects a timestamp more than five minutes from now.

## Tokens

Any of a personal, project or group access token works; ripple sends it as `Authorization: Bearer <token>`
over the API and as the password (user `oauth2`) for git over HTTPS.
Give it the `api` scope (merge requests, listing) and `write_repository` (push the branch); the role must be
Developer or higher, and the target branch must allow that user's merge requests.

Project and group tokens reach only their project or group, which is what `RIPPLE_GITLAB_TOKENS` is for: one
token per group, or per project where a group token is not allowed. Each token is its own bot user and has its
own rate-limit allowance, so a 429 answered to one token holds back only the repos that token serves.

## Repositories and webhooks

GitLab has no "app installed" event, so ripple finds repos by listing the projects each token is a **member**
of (`GET /projects?membership=true&archived=false`), on the same periodic schedule as any listing host. A new
project appears within one listing; a removed or archived one is forgotten.

Add a webhook to each project (or to the group) with the URL `https://<your ripple>/webhook/gitlab`, the secret
or signing token above, and **Push events** ticked. Other events are acknowledged and ignored. A push re-scans
only the repo it names, and only when it touches a file ripple reads. A push of more than 20 commits (GitLab
lists the newest 20) or a push with none (a new branch) re-scans the repo in full.

Repos are stored as `gitlab:group/sub/project`. Subgroups of any depth (GitLab allows 20) are supported; project
paths are validated before they reach a URL, and the API id is the path URL-encoded (`group%2Fsub%2Fapp`).

## Merge requests

- A draft is a `Draft: ` title prefix (the create call has no draft field).
- Labels travel with the create call, comma-joined; GitLab creates the ones that do not exist. A label that
  contains a comma cannot be expressed and is dropped.
- `automerge` in `.ripple.yaml` asks GitLab to merge when the pipeline succeeds (`auto_merge`, with the
  deprecated `merge_when_pipeline_succeeds` sent too for older instances). If every check already passed GitLab
  merges at once, as it would for a person clicking the button; if the project forbids it, the request is an error in
  the job's log and the merge request stays open.
- The legacy `.github/ripple.yml` alias is not read.

## Archive

ripple pauses a repo on archive and resumes it on unarchive (no scan, pending jobs kept). GitLab's system hooks list no archive event, and the documentation does not say whether archiving fires `project_update` (checked 2026-11-23, docs.gitlab.com/administration/system_hooks), so ripple reads it from the membership listing instead: each discovery also lists `GET /projects?membership=true&archived=true` (documented in `docs/vendor/gitlab/api_projects.md`) and a project that moves between the two listings is archived or unarchived, not removed or added. It is seen within one discovery interval, not at once. Not verified against a real GitLab; only the fake has run. `project_destroy` still forgets the repo.

## Superseded pull requests

When a newer release replaces ripple's own merge request for an older version of the same module, ripple closes the
older one (`state_event=close`) and leaves one note naming the new version, unless `close-superseded-pull-requests:
false`. The branch is kept. Ripple closes only a merge request nobody else has touched, and never on doubt: if any
read fails it is left open and the error is logged. Touched means any of:

- the source branch is not in this project, or the merge request does not have exactly one commit, or that commit's
  author or committer is not ripple's commit identity;
- more than one diff version (a push after it opened, a force-push included);
- an approval;
- a note a person wrote, or a system note other than ripple's own auto-merge notes ("enabled an automatic merge",
  "aborted the automatic merge"). GitLab reports a changed title and an edited description only as system notes, so
  any other system note counts as a touch.

Not detected: a changed label, assignee or reviewer (GitLab records those as resource events, which are not read), a
branch rewritten and force-pushed without changing the diff version count, and a system note worded like ripple's
own. The wording of system notes is from GitLab's source, not its API documentation, so a change there makes ripple
close less, never more. All of this is verified only against the fake GitLab; the excerpts it relies on are in
`docs/vendor/gitlab/supersede-excerpts.md`.

## Head polling

The client implements `app.HeadLister`, so a project whose default branch moved is re-scanned at the next discovery
even when its push webhook never arrived. The project listing carries no commit, so this is **one extra read per
project** each discovery, one at a time: the listing without `simple` (it carries `default_branch`,
`docs/vendor/gitlab/api_projects.md`) then `GET /projects/:id/repository/branches/:branch` (`commit.id`,
`docs/vendor/gitlab/api_branches.md`). A 429 ends the listing and the app keeps what it remembered; another failure
leaves that project out for the round, and an empty project (no default branch) is left out. The first discovery
after a start already reports every head, so a head that moved while ripple was down is re-scanned on the first
round. The branch reads are not counted as scan cost by the conformance suite. The project's `last_activity_at` is
deliberately not used as a stand-in: it moves on issues, comments and merge requests, including ripple's own, which
would re-scan every project it touched. Verified against the fake only.

## What is unverified

No GitLab instance was available when this was written. Everything below is from the vendored docs and is
exercised only against the fake GitLab in `internal/testutil/gitlab.go`, which is itself written from those docs:

- the exact response shapes of the merge request and project listing endpoints (only `iid`, `web_url`,
  `source_branch` and `path_with_namespace` are read);
- that `Authorization: Bearer` is accepted for project and group access tokens on every supported version
  (the docs say so; an older instance may want `PRIVATE-TOKEN`);
- `auto_merge` and the draft title prefix behaving as documented, and the error when a project forbids merging
  when the pipeline succeeds;
- the `Retry-After` handling of a real 429, and how often GitLab.com limits these calls;
- the signature scheme: the test vector was computed with openssl from the documented algorithm, not taken from
  a delivery GitLab made;
- clone over HTTPS with `oauth2:<token>` for project and group tokens (the git transport is tested against local
  repositories, not GitLab's).

What the tests do cover end to end: the shared conformance suite (release fan-out, push re-scan, config,
auto-merge, rate limit) through the real app against the fake, with every repo in a subgroup; wire formats with
literals from the docs; per-scope tokens and rate limits; receiver authentication, including secret token,
signature, stale timestamp and tampering.
