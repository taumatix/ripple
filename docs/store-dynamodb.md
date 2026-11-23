# DynamoDB access patterns

One table, `PK`/`SK` as the key, one global secondary index `gsi1` (`GSI1PK`/`GSI1SK`).
Billing is on-demand. `Store.EnsureTable` creates both.

| Entity | PK | SK | GSI1PK | GSI1SK |
|---|---|---|---|---|
| repo | `REPO#<repo>` | `META` | | |
| dependency | `REPO#<repo>` | `DEP#<file>\x1f<ds>\x1f<pkg>` | `PKG#<ds>#<line>[\x1f<shard>]` | `<repo>\x1f<file>` |
| release | `PKG#<ds>#<pkg>` | `REL#<version>` | | |
| job | `JOB#<key>` | `META` | `JOBS#pending[#<shard>]` (only while pending) | `<not_before, fixed width>\x1f<key>` |
| cursor | `CURSOR#<name>` | `META` | | |
| shard marks | `CONFIG#shards` | `META` | | |
| package shards | `PKGCOUNT#<ds>#<line>` | `META` | | |
| event | `EVENT#<id>` | `META` | `EVENTS` | `<seq, 20 digits>\x1f<id>` |
| dead event | `EVENT#<id>` | `META` | `EVENTS#dead` | `<dead_at, fixed width>\x1f<id>` |
| lease | `LEASE#<name>` | `META` | | |

## Queries

| Question | Operation |
|---|---|
| Which repos import `pkg`? (the hot path) | Query `gsi1`, `GSI1PK = PKG#<ds>#<line>` (one query per dependent shard, default one) (line = module path without its `/vN` or `.vN` suffix, so every major of a module is one key) |
| What does `repo` depend on? | Query base table, `PK = REPO#<repo>`, `SK begins_with DEP#` |
| Replace a repo's dependencies | The query above, then a diffed BatchWrite (puts for new, deletes for gone) |
| Which jobs are due? | One Query per job shard on `gsi1`, `GSI1PK = JOBS#pending[#<n>]`, `GSI1SK <= <now>`, each limited to the caller's limit; the results are merged in `(not_before, key)` order |
| Job by key / cursor / repo | GetItem, strongly consistent |
| Queued webhook events, oldest first (at start, then every 30s to find requeued ones) | Query `gsi1`, `GSI1PK = EVENTS` |
| Dead events, earliest first (`ripple events list`) | Query `gsi1`, `GSI1PK = EVENTS#dead` |
| Purge dead events older than `t` | Query `gsi1`, `GSI1PK = EVENTS#dead`, `GSI1SK < ts(t)`, then a delete conditional on `GSI1PK = EVENTS#dead` |
| Requeue a dead event | strongly consistent GetItem, then a Put conditional on `GSI1PK = EVENTS#dead` that moves it back to `EVENTS` with attempts 0 |

## Trade-offs

- **One partition for the event queue.** Every queued event shares `GSI1PK = EVENTS`. The
  receiver caps the backlog (`RIPPLE_EVENT_BACKLOG`, default 10000), and the index is read only
  at start and by a 30 second rescan, so the partition stays far below its throughput limit.
  A dead event moves to `EVENTS#dead` (an additive item attribute set, old items read as live)
  and out of the queue's partition, so it neither counts against the backlog caps nor is
  scanned by the rescan. Being a GSI, it may miss an
  event written a moment before a restart; that event is still in the table and is picked up by
  the next start.

- **Due jobs excluding in-flight repos filter after the read.** `ListDueJobsExcluding` applies the
  repo exclusion as a `FilterExpression`, which DynamoDB evaluates after `Limit`, so the store pages
  until it has the limit; a repo with a very large due backlog still costs reads, though it no longer
  starves the others.

- **Eventual consistency.** A GSI is always eventually consistent; `ConsistentRead` is
  not available on it. A dependency written a moment before a release lands may be missed
  by that release's fan-out, and a job marked done may appear in one more due-jobs query.
  The first is closed by the repo re-scan on push and by the roadmap's backfill entry; the
  second is harmless because the job key is idempotent and a finished job is re-read with a
  strongly consistent GetItem before it runs.
- **Hot partitions are sharded.** Two index partitions concentrate traffic: the pending-job queue
  and a very popular package's dependents. Both are spread over shards chosen by a stable hash
  (FNV-1a): a job by its key, a dependency by its repo.
  - `RIPPLE_DYNAMODB_JOB_SHARDS` (default 8, 1 to 64). A due-jobs poll is exactly that many Queries,
    each limited to the requested count, so it reads at most `shards x limit` items; the merge returns
    the same order one partition would (not-before, then key), so listing is as fair and ordered as before.
  - `RIPPLE_DYNAMODB_DEPENDENT_SHARDS` (default 1, 1 to 64). Every `ListDependents` costs that many
    Queries, for every package, popular or not, because the writer cannot know which will be.
    The default keeps today's single Query; raise it only when one package's dependents throttle
    (a partition serves about 1000 writes/s and 3000 reads/s). Or let a package earn its shards, below.
  - A read never costs more than `MaxShards` (64) Queries, and none scans.
  - Releases (`PKG#<ds>#<pkg>`, base table) are not sharded: one item is written per release.
  - **Existing tables need no migration.** Shard 0 is the old key (`JOBS#pending`, `PKG#<ds>#<line>`),
    so unsharded items are read as shard 0. They move to their hashed shard the next time they are
    written: a job on its next update, a repo's dependencies on its next scan.
  - **Changing the counts.** `EnsureTable` records the highest count ever used in `CONFIG#shards`
    and reads use `max(setting, recorded)`. Raising a setting takes effect at once for writes; lowering
    it never strands items in shards that are no longer read, it only stops using the shard count
    you asked for. Shrinking for good means dropping the `CONFIG#shards` item after the queue has
    drained and dependencies have been rescanned.
  - **Adaptive dependent sharding** (`RIPPLE_DYNAMODB_ADAPTIVE_DEPENDENTS=<k>`, default off). A package
    starts with one partition and its shard count doubles each time it has more than `k` dependents per
    shard: `n = 1; while count > k*n { n *= 2 }`, at most 64. `ReplaceDependencies` and `DeleteRepo` move a
    counter (`Deps` on `PKGCOUNT#<ds>#<line>`) with `ADD` and record the new shard count (`Shards`, only ever
    raised) in the same item. A package's shard count is `max(DEPENDENT_SHARDS, Shards)`; `ListDependents`
    reads it fresh, writers may use a cached, possibly lower, value. `CONFIG#shards` also records that
    adaptive sharding was ever on (`Adaptive`), so with the option off again reads and writes still consult
    the package item.
    - **Growing never loses or duplicates a read.** A dependency has one `GSI1PK`, so it is in one shard;
      items written under a lower count are in a shard below the new count, which reads cover; a rewrite
      replaces the item in place. Shard 0 is always the legacy key. `TestDynamoAdaptiveDependentsMigration`
      walks a legacy table through 1, 2, 4 and 8 shards and turns the option off, checking every dependent
      is listed once at each step.
    - **The counter only steers growth.** It is not transactional with the item writes, so a crash can leave it
      off by one; dependents that existed before the option was turned on are not counted until rewritten, and
      the counter is a single hot item per package. Roadmap entries 70 and 71 cover recount and striping.
      None of this affects what a read returns.
    - A small package keeps one Query per `ListDependents`, plus one strongly consistent GetItem for its shard
      record.
  - **Concurrent shard queries.** A read runs its shard Queries at most `RIPPLE_DYNAMODB_QUERY_CONCURRENCY`
    (default 4, 1 is sequential) at a time. Results are merged by shard index and then sorted, so the order
    is the same whatever finishes first (`TestDynamoConcurrentReadsMatchSequential`); the first error, or a
    cancelled context, cancels the Queries still running and the read returns without waiting out the rest.
  - The test `TestDynamoPendingQueueIsSpread` counts items per index partition and fails if one holds
    more than 1.5x its share.
- **Sparse index.** Only dependency and pending-job items carry `GSI1` attributes, so the index
  stays small and finished jobs drop out of it.
- **Releases are stored only for packages that have dependents.** The feed carries every
  module; keeping all of them would make the table a mirror of the proxy.

## Testing: the in-process fake and DynamoDB Local

`internal/store/dynamofake` is an HTTP server speaking DynamoDB's JSON 1.0 protocol that the real AWS SDK
client talks to, so the store runs unchanged. It implements what the store uses: CreateTable, DescribeTable,
DeleteTable, PutItem, GetItem, DeleteItem, UpdateItem (`SET`, `ADD`), BatchWriteItem, Query on the table and on
`gsi1`, with key-condition, filter, condition and projection expressions. It is written from the API
documentation, and follows the rules the store depends on: `Limit` counts items before the filter; a page
stops at 1 MB; `LastEvaluatedKey` is returned whenever `Limit` stopped the read, even when nothing remains;
`ConsistentRead` on a GSI is rejected; BatchWriteItem takes at most 25 requests, no duplicate keys, no
conditions and can return `UnprocessedItems`; an item is at most 400 KB; unused expression names and values
and unsupported syntax are `ValidationException`s; sort order is by UTF-8 bytes; a GSI is sparse.

Every Dynamo store test runs twice, as the subtests `fake` and `dynamodb-local`. The fake always runs, so a
plain `go test ./...` covers the sharding, concurrency and adaptive code. DynamoDB Local runs when
`RIPPLE_TEST_DYNAMODB_ENDPOINT` is set (`make test-services`, and CI, which fails on a skip) and is the
source of truth: a fake that passes proves the code agrees with the documentation as read, not with the
service.

Where the fake differs from DynamoDB: it is strongly consistent (a GSI is never stale, so the eventual-consistency
paths are not exercised); it never throttles; the only `UnprocessedItems` it returns are the ones the test hook
`DropOneFromNextBatch` asks for; it implements TransactWriteItems (Put, Delete, ConditionCheck) but has no Scan or BatchGetItem and rejects other
operations; it does not reject reserved words; item and page sizes are an approximation of the documented
accounting. Roadmap entry 72 tracks these.
