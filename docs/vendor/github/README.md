# Vendored GitHub webhook payloads

Fetched 2026-11-23, read before the `repository` event mapping in `internal/webhook` was written.

| File | Source | Carries |
|---|---|---|
| `webhook-events-and-payloads-excerpt.txt` | docs.github.com/en/webhooks/webhook-events-and-payloads | the `push` and `repository` sections; a GitHub App needs the "Metadata" permission and the Repository event subscribed to receive `repository` |
| `repository-{renamed,transferred,edited,archived,unarchived,deleted}.schema.json` | github.com/octokit/webhooks `payload-schemas/api.github.com/repository/` (JSON Schema generated from GitHub's payloads) | `changes.repository.name.from` (renamed), `changes.owner.from` (transferred), `changes.default_branch.from` (edited), `installation` |
| `push.schema.json` | same repo, `push/event.schema.json` | `ref`, `deleted`, `repository` |
| `repository-lite.schema.json` | same repo, `common/repository-lite.schema.json` | `id`, `name`, `full_name`, `default_branch`, `archived` |
| `installation.created.schema.json`, `installation_repositories.added.schema.json` | same repo, `installation/created`, `installation_repositories/added` | `repositories[]` / `repositories_added[]` items require `id`, `node_id`, `name`, `full_name`, `private` |

Assumed, unverified against a real delivery (the docs page does not show these actions' bodies and
no live App was available):

- `changes.owner.from` in the schema holds only `user`; ripple also accepts `organization` (an
  org-to-org transfer is expected to send it). With neither login, the old name is unknown and the
  transferred repo is registered under its new name only.
- Whether a transfer to an owner without the App installed produces a `repository.transferred`
  delivery to this App at all; it is expected to be followed by `installation_repositories.removed`.
- That `repository.deleted`, `archived` and `unarchived` reach an App that subscribed to the event.
