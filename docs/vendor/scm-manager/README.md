# Vendored upstream documentation (SCM-Manager)

Fetched 2026-11-23 and read before `internal/platform/scmmanager` was written. The `*.md` files named
below are the text of SCM-Manager's own documentation pages (source URL on the first line of each).
SCM-Manager has **no published REST reference** in its docs; the REST API is described by OpenAPI
annotations in the source, so `source-excerpts.md` quotes the few lines of source that fix each wire
format, with repository and commit.

| File | Source | Carries |
|---|---|---|
| `core-profile-api-keys.md` | scm-manager.org/docs/3.12.x/en/user/profile/ | API keys: REST via cookie `X-Bearer-Token`; git via the key as password |
| `webhook-plugin-configuration.md` | scm-manager.org/plugins/scm-webhook-plugin/docs/3.2.x/en/configuration/ | webhook methods (GET/POST/PUT/AUTO), per push or per commit, commit data, URL builder `${repository.namespace}`, payload JSON |
| `webhook-plugin-permissions.md` | same, `/permissions/` | who may configure webhooks |
| `review-plugin-*.md` | scm-manager.org/plugins/scm-review-plugin/docs/3.17.x/en/ | pull requests: create (incl. draft), work, merge strategies, workflow engine, branch protection |
| `core-scm-server-config.md` | scm-manager.org/docs/3.12.x/en/administration/scm-server/ | server settings (searched for rate limiting: none) |
| `renovate-scm-manager.md` | docs.renovatebot.com/modules/platform/scm-manager/ | what a bot needs: API key, Review Plugin >= 3.11.0, SCM-Manager >= 3.10.0, no auto-merge |
| `source-excerpts.md` | github.com/scm-manager/{scm-review-plugin,scm-webhook-plugin,scm-manager}, renovatebot/renovate | quoted lines fixing the wire formats |

The sources of the plugins and of SCM-Manager are AGPL-3.0 and Renovate's is AGPL-3.0: only short quotations with attribution
are kept, no file is copied. ripple's own code is written from the facts, not from their code.

## What the documents say about the capabilities ripple asks about

- **Webhook signing: absent.** The webhook plugin documents no signature or secret. Its configuration lets an
  administrator add **custom headers** (source: `SimpleWebHook.headers`, `WebhookHeader{key,value,concealed}`);
  ripple authenticates a delivery by a shared secret in such a header. Whether the plugin release you run has the
  headers form is unverified (it is in the source at 55d3605, not in the 3.2.x user docs).
- **The payload names no repository and no ref.** Only the URL builder knows the repository, so the webhook URL
  carries `namespace` and `name`. A changeset lists `branches`; no file list.
- **Auto-merge: absent.** Renovate's page: "the Renovate automerge feature is not supported ... Every pull request
  requires merging them manually". The review plugin has merge strategies and a workflow engine, but no
  merge-when-green call (searched the plugin's source and docs for "auto merge" and "merge when").
- **Drafts: present** (`status: "DRAFT"`, create-pr page). **Labels:** the DTO has `labels`, but a repository
  configures which are available (`availableLabels`); ripple sends none.
- **Rate limiting: none documented** in the server config page, the plugin docs or the sources searched. ripple
  still honours a 429 with `Retry-After` (assumed, unverified).

## Not verified against a running SCM-Manager

Everything above comes from documents and source, never from a live instance. Specifically unverified: the
`Authorization: Bearer <api key>` header (the source accepts a bearer scheme, `BearerWebTokenGenerator`, and Renovate
sends it; the docs only show the cookie); the git basic-auth username (SCM-Manager ignores it for API keys; Renovate
uses `x-access-token`, "our own choice and not documented"); the exact JSON of a created pull request's `Location`
follow-up; the `branches` content of webhook changesets; webhook header support in the release you run.
