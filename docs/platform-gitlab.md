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
