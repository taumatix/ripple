# GitHub Actions manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: github-actions
    directory: /          # optional; default /
```

Only the `directory` key applies. Without the entry ripple does not read workflow files.

## What is read

`uses: owner/repo@ref` (also `owner/repo/path@ref`, and reusable workflows
`owner/repo/.github/workflows/x.yml@ref`) in `.github/workflows/*.yml|yaml` (direct children) and in any
`action.yml|yaml` (composite actions). Quoted values and trailing comments are fine.

| Form | Handling |
|---|---|
| `@v4`, `@v4.1`, `@v4.1.2` | bumped to a newer tag of the **same shape** |
| `@<40-hex sha> # v4.1.2` | the sha and the comment move together; the tag is resolved to a commit through the API, annotated tags dereferenced |
| `@<sha>` with no version comment, `@main`, other branch refs | ignored |
| `./local`, `docker://image` | ignored |

Same shape means the same `v` prefix and component count: `v4` follows `v5`, never `v5.0.1`, so a floating tag
stays floating. If upstream has no tag of that shape newer than the current one, the dependency is left alone;
with `dependency-dashboard: true` the dashboard lists it under "Actions left alone" with the newest tag it
saw, until upstream publishes the shape or the dependency moves.

Edits are exact-line: indentation, quoting and comments are kept. The result is re-parsed as YAML before it is
committed, and the PR links the upstream release (when it has a published one) under "Release notes". A sha pin
whose commit cannot be resolved fails the job rather than guessing one. Dependencies are polled on the same
cooldown as other updates, using the release's `published_at` or, without a release, when ripple first saw the tag.

## Required permission: `workflows`

GitHub refuses a push that changes anything under `.github/workflows/` unless the App has the **Workflows:
read and write** permission. Without it ripple pushes nothing it cannot finish: no PR is opened, the reason
("the GitHub App lacks the workflows permission", pointing here) is logged and shown on the dependency
dashboard as the job's reason, and the job is deferred 24 hours and retried, so granting the permission (and
accepting it on each installation) is enough. Composite `action.yml` files outside `.github/workflows` do not
need it.

## Limits

- The denial is recognised by git's message containing "workflow" and "permission". The exact wording GitHub
  returns is not verified against the real service (the tests use a fake written from its documented text).
- Tags are read from the repo's own host. On GitHub Enterprise Server an action that only exists on github.com is
  found when `RIPPLE_ACTIONS_UPSTREAM_URL` names its API (see
  [platform-github-enterprise.md](platform-github-enterprise.md)); without it the action is skipped with a
  warning. Verified against a fake only.
- Superseding follows the Go modules' rules (`close-superseded-pull-requests`, on by default): a PR for a newer
  tag closes the open older ripple PR of the same action and the same tag shape, unless a person touched it.
  PRs of different shapes (`v5` and `v5.1.0`) are separate dependencies and never close each other.

## The polled set

The actions the watcher reads tags for are recorded by each scan. An action no repository uses any more is dropped from that set on the next poll, so its tags stop being read. A repo that starts using it again adds it back at its next scan.
