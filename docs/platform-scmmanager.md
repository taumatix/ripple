# SCM-Manager

Host `scm-manager`. Repositories are stored as `scm-manager:namespace/name`; the receiver is
`/webhook/scm-manager`. Targets SCM-Manager 3.x with the **Review plugin 3.11.0 or later** (pull requests)
and the **Webhook plugin** (push events). Git repositories only; svn and hg are skipped.

## Configuration

| Variable | Meaning |
|---|---|
| `RIPPLE_SCMMANAGER_URL` | The instance's context path, e.g. `https://scm.example.com/scm`. The API is `<url>/api/v2`, git is `<url>/repo/<namespace>/<name>`. Required. |
| `RIPPLE_SCMMANAGER_TOKEN` | An API key (profile page, "API keys"). Sent as `Authorization: Bearer`; git uses it as the basic-auth password (the username is ignored for API keys). Unset turns the host off. |
| `RIPPLE_SCMMANAGER_WEBHOOK_SECRET` | Value of the `X-Ripple-Webhook-Secret` header every delivery must carry. Required. |

The key's user needs read, write and, for pull requests, the Review plugin's create privilege on every
repository ripple should serve (`docs/vendor/scm-manager/review-plugin-permissions.md`). The key is as
powerful as its user: give ripple its own account.

## Webhook

SCM-Manager repositories are not registered with ripple by an event; ripple lists them. For each repository
add a webhook (Settings > Webhooks) with:

- URL `https://<ripple>/webhook/scm-manager?namespace=<namespace>&name=<name>`. The plugin's payload names
  neither, so the URL does. (The plugin's URL variables `${repository.namespace}` and `${repository.name}`
  make one global webhook possible: `...?namespace=${repository.namespace}&name=${repository.name}`.)
- A header `X-Ripple-Webhook-Secret` with the secret, "encrypt on storage" ticked.
- Method POST (or AUTO), "send commit data" on, "execute per push" (not per commit).

The plugin has **no signature scheme**, so the shared header is the authentication. It is compared in
constant time before the body is read; a missing secret in ripple's own configuration refuses every
delivery. The secret rides in clear unless the webhook URL is https: use https.

## How it works

- **Discovery**: no event announces a new repository, so ripple lists them (`GET repositories`, git only,
  not archived) and asks each one's default branch once (`config/git/{ns}/{name}/default-branch`),
  remembering the answer. A repository with no branch yet is left out until it has one.
- **Head polling**: the client implements `app.HeadLister`, so a repository whose default branch moved is
  re-scanned at the next discovery even when its webhook never arrived. The listing carries no commit, so this is
  **one extra read per repository** each discovery, one at a time: `GET repositories/{ns}/{name}/branches/{default}`
  and its `revision` (the default branch is already remembered). A 429 ends the listing and the app keeps what it
  remembered; another failure leaves that repository out for the round. The read is not counted as scan cost by the
  conformance suite.
- **Changed files**: the payload has no file list. For a push to the default branch ripple asks the
  modifications of each changeset (added, modified, removed, renamed), up to 100 changesets; more, or any
  failed lookup, leaves the file list empty, which forces a re-scan (always correct, dearer). This is why the
  fixture carries the conformance suite's `FilelessPushes` marker.
- **A delivery with no body or no branch** (method GET, or "send commit data" off) is taken as a push to the
  default branch and re-scanned.
- **Pull requests** are opened with `POST pull-requests/{ns}/{name}`, `status` `DRAFT` or `OPEN`, and the
  source branch deleted on merge. The Review plugin may already hold a pull request for the same branches:
  that answers 409 and surfaces as an error. ripple lists `IN_PROGRESS` (open and draft) pull requests to
  find its own by branch prefix, so it does not normally try.
- **Labels** are ignored: the Review plugin's labels are chosen per repository, and ripple cannot know them.
- **Rate limit**: none is documented. A 429 (say from a proxy) holds the host back until `Retry-After`, else a
  minute.

## Auto-merge decision

SCM-Manager has no auto-merge, and neither the Review plugin's docs nor its source offer a merge-when-green
call (Renovate's page says the same). `Client` therefore does not implement `platform.AutoMerger`:
`automerge` in a repository's config opens the pull request and leaves the merge to a human. Merging at
once would skip the checks.

## Archive

Only GitHub reports archive and unarchive today (ripple pauses the repo on `archived`, resumes on `unarchived`). SCM-Manager: ripple consumes only the events listed here; an archive event was not verified (2026-11-23). The repository list carries an `archived` flag (`docs/vendor/scm-manager/source-excerpts.md`, "repo-list"), so the client implements `platform.ArchivedLister` on it: an archived git repository is paused (index and pending jobs kept) and resumed on unarchive instead of forgotten. Verified against the fake only.

## Superseded pull requests

Not implemented: when a newer ripple pull request replaces a stale one, the stale one stays open on
SCM-Manager and ripple does not comment on it. The review plugin has a reject endpoint
(`POST .../pull-requests/{id}/reject`) and comment and history sub-resources, but they exist only
in the plugin's source: neither the plugin documentation nor `docs/vendor/scm-manager/` describes
their request or response shapes. There is no documented, reliable read for "a person touched it":
`lastModified` moves on system updates too, the history and comment payloads are unverified, and
nothing reports a title or description edit. Closing on a guess could discard someone's review, so
ripple closes nothing here. A source note is in
`docs/vendor/scm-manager/review-plugin-reject-source-note.md`.

## Unverified

## Real instance

`hosts-scmmanager.yml` runs the conformance suite and `TestReal*` (build tag `scmreal`) against SCM-Manager
3.11.0 in CI, with the Review and Webhook plugins installed through the plugin API (no UI). Verified there,
recorded in `docs/vendor/scm-manager/real-answers.md`: the headless bootstrap and API key, `Authorization:
Bearer`, the cookie and git basic auth with an API key (any user name), the webhook plugin's custom header
and `branches` in changesets, one delivery per push, the 201 `Location` of a pull request, the branch
resource used for head polling, the modifications resource for a push, and archive and unarchive.

## Unverified

Not observed against a real instance (assumed, unverified):

- the modifications of a merge commit (which parent they are against);
- `shouldDeleteSourceBranch` honoured at merge;
- the 429 answer: nothing documents it and no instance throttles; the rate-limit case injects one at a proxy;
- plugin releases other than the ones 3.11.0 installs.
