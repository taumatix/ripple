# Migration policy and notes

ripple is configured through `.ripple.yaml` and `RIPPLE_*` environment variables, and exposes Go packages
and a CLI. This page says how those may change and records every deprecation that is live.

## Policy

Versions follow SemVer. Pre-1.0, a minor release is where a deprecation may be removed; the rules below
still apply, and a break is never made silently.

1. **Additive first.** A new config key, environment variable, CLI flag or exported identifier arrives
   alongside the old one. A plain per-repo `.ripple.yaml` that worked before keeps its meaning; no
   default changes what an existing file does.
2. **Deprecate, then remove.** An item is first marked deprecated in a release, with its replacement
   working in the same release. It is removed no earlier than the next minor release after the one that
   deprecated it (deprecated in 0.N.0, removal not before 0.(N+1).0), and the release before removal
   must still accept it and warn.
3. **Say it everywhere a user looks.** A deprecated config item logs a warning on use and
   `ripple config validate` shows it; the warning names the replacement and the earliest removal.
   CHANGELOG lists it under `### Deprecated`, the README documents the replacement, and this page
   carries an entry.
4. **Removal is a break.** Removal ships with an entry moved to "Removed" below, a `### Removed` line in
   CHANGELOG, and, once the module is at v1 or later, only in a major version. Before then it is a
   minor release that has a last warning release before it.
5. **Unavoidable breaks** (a security fix, an upstream API that vanished) skip the grace period only
   with an explicit entry here saying why, and the release that carries it is called out in its
   release notes.
6. **Config-schema changes** follow the same rules: unknown keys are errors, so a key's removal turns
   into a validation error naming the key; that is why it needs the warning period first.

### Marking deprecated code

A deprecation in code carries a comment line `ripple:deprecated <id>` where `<id>` is a lowercase
slug. Each id needs an entry below with a line ``**Marker:** `<id>` ``. `scripts/check-migration.sh`
(run by `make check-migration` and by CI) fails if a marker has no entry, or an active entry has no
marker in the code. Deprecations of third-party names (for example GitLab's
`merge_when_pipeline_succeeds`) are not ripple's and are not tracked here.

## Active deprecations

### `.github/ripple.yml` config path

- **Marker:** `alias-github-ripple-yml`
- **Introduced in:** v0.2.0
- **Removal not before:** v0.3.0 (ROADMAP entry 36)
- **Replacement:** `.ripple.yaml` at the repository root, with identical meaning, the same on every host.
- **Behaviour now:** still read on GitHub only; a warning is logged on each scan of such a repo and by
  `ripple config validate`. If both files exist `.ripple.yaml` wins, the alias is ignored (not merged)
  and a warning says so.
- **To migrate:** `git mv .github/ripple.yml .ripple.yaml`.

### `Event.Ref` carrying a rename or a default branch

- **Marker:** `event-ref-overload`
- **Introduced in:** the release after v0.2.0 (ROADMAP entry 56)
- **Removal not before:** the minor release after that one
- **Replacement:** the typed `model.Event` fields `NewRepo` (a `repo_renamed` event's new name) and `DefaultBranch`
  (a `default_branch_changed` event's new branch), persisted by all three stores.
- **Behaviour now:** new events carry both the typed field and the old `Ref` use; a handler prefers the typed
  field and falls back to `Ref` for events queued before upgrading, so a rolling upgrade or a downgrade loses nothing.
  `Ref` stays populated for `repo_renamed` and `default_branch_changed` (covered by a test) until it is removed.
  Internal packages only: no config key, flag or public Go API changes.
- **To migrate:** nothing to do. The queued-event schema grows by columns/attributes with empty defaults
  (Postgres migration `004_event_typed_fields.sql`).

## Removed

Nothing has been removed yet.
