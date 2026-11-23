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

## Unverified

Written from SCM-Manager's own documents and the plugins' public source (`docs/vendor/scm-manager/`,
`UPSTREAM.md`) against a fake; **no real SCM-Manager was used**. Unconfirmed against a real instance:

- `Authorization: Bearer <api key>` on the REST API (the source accepts a bearer scheme and Renovate sends it;
  the docs show only the `X-Bearer-Token` cookie);
- git basic auth with an API key as password, whatever the username;
- that the webhook plugin release you run has the custom header table, and sends `branches` in changesets;
- the 201 `Location` of a created pull request (a missing one falls back to a list lookup);
- the modifications of a merge commit (which parent they are against);
- `shouldDeleteSourceBranch` honoured at merge;
- the 429 answer: assumed, nothing documents it.
