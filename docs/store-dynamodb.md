# DynamoDB access patterns

One table, `PK`/`SK` as the key, one global secondary index `gsi1` (`GSI1PK`/`GSI1SK`).
Billing is on-demand. `Store.EnsureTable` creates both.

| Entity | PK | SK | GSI1PK | GSI1SK |
|---|---|---|---|---|
| repo | `REPO#<repo>` | `META` | | |
| dependency | `REPO#<repo>` | `DEP#<file>\x1f<ds>\x1f<pkg>` | `PKG#<ds>#<line>` | `<repo>\x1f<file>` |
| release | `PKG#<ds>#<pkg>` | `REL#<version>` | | |
| job | `JOB#<key>` | `META` | `JOBS#pending` (only while pending) | `<not_before, fixed width>\x1f<key>` |
| cursor | `CURSOR#<name>` | `META` | | |
| event | `EVENT#<id>` | `META` | `EVENTS` | `<seq, 20 digits>\x1f<id>` |

## Queries

| Question | Operation |
|---|---|
| Which repos import `pkg`? (the hot path) | Query `gsi1`, `GSI1PK = PKG#<ds>#<line>` (line = module path without its `/vN` or `.vN` suffix, so every major of a module is one key) |
| What does `repo` depend on? | Query base table, `PK = REPO#<repo>`, `SK begins_with DEP#` |
| Replace a repo's dependencies | The query above, then a diffed BatchWrite (puts for new, deletes for gone) |
| Which jobs are due? | Query `gsi1`, `GSI1PK = JOBS#pending`, `GSI1SK <= <now>` |
| Job by key / cursor / repo | GetItem, strongly consistent |
| Queued webhook events, oldest first (read once at start) | Query `gsi1`, `GSI1PK = EVENTS` |

## Trade-offs

- **One partition for the event queue.** Every queued event shares `GSI1PK = EVENTS`. The
  receiver caps the backlog (`RIPPLE_EVENT_BACKLOG`, default 10000), and the index is read only
  at start, so the partition stays far below its throughput limit. Being a GSI, it may miss an
  event written a moment before a restart; that event is still in the table and is picked up by
  the next start.

- **Eventual consistency.** A GSI is always eventually consistent; `ConsistentRead` is
  not available on it. A dependency written a moment before a release lands may be missed
  by that release's fan-out, and a job marked done may appear in one more due-jobs query.
  The first is closed by the repo re-scan on push and by the roadmap's backfill entry; the
  second is harmless because the job key is idempotent and a finished job is re-read with a
  strongly consistent GetItem before it runs.
- **Hot partition.** All pending jobs share the partition `JOBS#pending`, and a very
  popular package concentrates reads on its `PKG#` partition. At the scale ripple targets
  this is far below the per-partition limits; sharding the key (`JOBS#pending#<n>`) is on the
  roadmap rather than built speculatively.
- **Sparse index.** Only dependency and pending-job items carry `GSI1` attributes, so the index
  stays small and finished jobs drop out of it.
- **Releases are stored only for packages that have dependents.** The feed carries every
  module; keeping all of them would make the table a mirror of the proxy.
