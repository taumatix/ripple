# DynamoDB access patterns

One table, `PK`/`SK` as the key, one global secondary index `gsi1` (`GSI1PK`/`GSI1SK`).
Billing is on-demand. `Store.EnsureTable` creates both.

| Entity | PK | SK | GSI1PK | GSI1SK |
|---|---|---|---|---|
| repo | `REPO#<repo>` | `META` | | |
| dependency | `REPO#<repo>` | `DEP#<file>\x1f<ds>\x1f<pkg>[\x1e<version>]` | `PKG#<ds>#<line>[\x1f<shard>]` | `<repo>\x1f<file>` |
| release | `PKG#<ds>#<pkg>` | `REL#<version>` | | |
| job | `JOB#<key>` | `META` | `JOBS#pending[#<shard>]` (only while pending) | `<not_before, fixed width>\x1f<key>` |
| cursor | `CURSOR#<name>` | `META` | `CURSORS#<group>` (only a name `<group>#<key>`) | `<name>` |
| shard marks | `CONFIG#shards` | `META` | | |
| package shards | `PKGCOUNT#<ds>#<line>` | `META` | | |
| event | `EVENT#<id>` | `META` | `EVENTS` | `<seq, 20 digits>\x1f<id>` |
| dead event | `EVENT#<id>` | `META` | `EVENTS#dead` | `<dead_at, fixed width>\x1f<id>` |
| event marker | `REPOEVENTS#<repo>` | `<seq, 20 digits>\x1f<id>` | | |
| markers built | `EVENTMARKS` | `BUILT` | | |
| lease | `LEASE#<name>` | `META` | | |
| repo listing | `REPOS` | `<repo>` | | |
| repo listing built | `REPOSMETA` | `BUILT` | | |
| package index | `PKGS` | `<PKG#datasource#package>` | | |
| package index built | `PKGSMETA` | `BUILT` | | |

## Queries

| Question | Operation |
|---|---|
| Which repos import `pkg`? (the hot path) | Query `gsi1`, `GSI1PK = PKG#<ds>#<line>` (one query per dependent shard, default one) (line = module path without its `/vN` or `.vN` suffix, so every major of a module is one key) |
| What does `repo` depend on? | Query base table, `PK = REPO#<repo>`, `SK begins_with DEP#` |
| Replace a repo's dependencies | The query above, then a diffed BatchWrite (puts for new, deletes for gone) |
| Which jobs are due? | One Query per job shard on `gsi1`, `GSI1PK = JOBS#pending[#<n>]`, `GSI1SK <= <now>`, each limited to the caller's limit; the results are merged in `(not_before, key)` order |
| Registered repos in name order (`store.RepoLister`, the OSV sweep) | Query base table, `PK = REPOS`, `SK > <after>`, limited. One index item per repo is written beside its `META` item and deleted with it. A table from before the index has none: the first listing runs one Scan for `META` items, writes the missing index items and then a marker (`REPOSMETA`/`BUILT`), so the Scan happens once and only when something asks to list |
| Job by key / cursor / repo | GetItem, strongly consistent |
| Queued webhook events, oldest first (at start, then every 30s to find requeued ones) | Query `gsi1`, `GSI1PK = EVENTS` |
| Dead events, earliest first (`ripple events list`) | Query `gsi1`, `GSI1PK = EVENTS#dead` |
| Is there an older live event of this repo? (`HasOlderEvent`) | Query base table, `ConsistentRead`, `PK = REPOEVENTS#<repo>`, `SK < <seq>\x1f<id>`; each marker found is checked against its event with a GetItem, and a marker whose event is gone or dead is deleted |
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
  not available on it. A job marked done may appear in one more due-jobs query: harmless,
  because the job key is idempotent and a finished job is re-read with a strongly consistent
  GetItem before it runs. A dependency written a moment before a release lands is not missed:
  `ReplaceDependencies` writes a registration item (`PK=FRESH#<package>`, `SK=<repo>\x1f<dependency key>`,
  with the time) beside every dependency row new to the repo, and `ListDependents` adds the rows
  those items from the last five minutes name, read with strongly consistent `BatchGetItem`, to what
  the index returns. The row is the truth: a dependency dropped or re-versioned since is reported as
  it now is. Items older than the window are deleted by the read that finds them. A rescan that adds
  no row writes none.
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
      off by one. None of this affects what a read returns. Once a table has had the option on, the counters keep
      following writes with it off (no growth, since the rule needs a per-shard size), so turning it back on
      starts from true counts; a table that never had it on keeps none. Counters that drifted while earlier
      versions left them untouched need one recount.
    - **Recount.** `(*Dynamo).RecountDependents(ctx, ds, pkg)` reads every shard of the package from the index,
      sets `Deps` to the count, raises `Shards` if the count calls for it (never lowers it), and moves each
      dependent to the shard its repo hashes to (a conditional write per item, applied only to an item still
      where it was read). It is also run once, lazily, by the first change to a package whose counter item does
      not exist, so dependents written before the option was on, or before the counter was lost, are counted
      then. The index is eventually consistent, so a recount straight after a write can miss that write; the
      next recount fixes it. `ripple dynamodb check-counts` lists the packages whose counter disagrees with the
      index (`(*Dynamo).CheckDependentCounts`, read only) and `ripple dynamodb recount` recounts every package
      (`RecountAllDependents`, needs the option). Both list the packages from the package index (`PKGS`, one item per package that has had a dependent,
      written the first time a process adds one; a superset, which `recount` prunes) with one Query. A table from before
      the index is Scanned once to build it, then a marker (`PKGSMETA`/`BUILT`) is written; delete that item to rebuild
      (needed only if a replica of an older version added packages after the marker). They are
      operator commands; the server only reads (below), and never recounts on its own.
      `RIPPLE_DYNAMODB_SCAN_PAGE=<items>` and `RIPPLE_DYNAMODB_SCAN_PAUSE_MS=<ms>` (both default 0, unpaced) pace
      every such Scan (now only the one-off index build; the per-package Queries are paced by the pause), in the commands and in the server's check: a page reads at most that many items (before the
      filter, which is what consumes read capacity) and the next is requested the pause later; a recount or check
      also waits the pause between packages. Pick them from the table's provisioned read capacity: a page of
      100 items every 100ms is about 1000 items a second. `RIPPLE_DYNAMODB_DRIFT_CHECK_INTERVAL=<duration>`
      (default 0, off; `6h`, `1d`) has each server replica run the check that often, starting one period after
      it starts, and log `dependent counters drifted from the index` (a warning with the package count and the
      first three) or `dependent counters match the index`; it writes nothing, so repairing stays the operator's
      `ripple dynamodb recount`. Several replicas each run it; set it on one if the scan is dear.
    - **Counter stripes** (`RIPPLE_DYNAMODB_COUNTER_STRIPES=<n>`, default 1, 1-64). With more than one, each
      change ADDs to a random item `PKGCOUNT#<ds>#<line>` / `S#<i>` instead of `META`, so concurrent writers do not
      share an item's write limit. The dependents count is the sum of `Deps` over the partition, `META`
      included, so the option can be turned on or off on a live table. A stripe's own value times `n` is a cheap
      estimate; the partition is summed only when that estimate says the package may need more shards, and every
      later write re-checks, so growth can be delayed but not lost. A recount puts the truth in `META` and zeroes
      the stripes. Whether the default is too low, or `n` right for a given package, has not been measured on
      a real table.
    - A small package keeps one Query per `ListDependents`, plus one strongly consistent GetItem for its shard
      record.
  - **Concurrent shard queries.** A read runs its shard Queries at most `RIPPLE_DYNAMODB_QUERY_CONCURRENCY`
    (default 4, 1 is sequential) at a time. Results are merged by shard index and then sorted, so the order
    is the same whatever finishes first (`TestDynamoConcurrentReadsMatchSequential`); the first error, or a
    cancelled context, cancels the Queries still running and the read returns without waiting out the rest.
  - The test `TestDynamoPendingQueueIsSpread` counts items per index partition and fails if one holds
    more than 1.5x its share.
- **Per-repo open-job copies.** `ListOpenJobs` reads `REPO#<repo>` / `OPENJOB#<key>` items instead of scanning
  the job index. They are written after the job write (a Put while the job is pending or failed, a Delete otherwise),
  not atomically with it, so `EnsureTable` backfills them on every start from the pending index (each shard of the queue is read, existing copies are checked with BatchGetItem, and only missing ones are
  written, each with `attribute_not_exists`), and `ListOpenJobs` reconciles on
  read: it batch-reads the job behind each copy, drops a copy whose job is gone or no longer open, and deletes it
  (best effort). A job that failed before the copies existed is in no index, so start does not find it:
  `ripple dynamodb backfill-failed-jobs` (`BackfillFailedJobs`) finds failed jobs with one filtered Scan and
  writes their missing copies. Run it once on a table from before the copies; repeating it writes nothing.
- **Sparse index.** Only dependency and pending-job items carry `GSI1` attributes, so the index
  stays small and finished jobs drop out of it.
- **Releases are stored only for packages that have dependents.** The feed carries every
  module; keeping all of them would make the table a mirror of the proxy.

## Testing: the in-process fake and DynamoDB Local

`internal/store/dynamofake` is an HTTP server speaking DynamoDB's JSON 1.0 protocol that the real AWS SDK
client talks to, so the store runs unchanged. It implements what the store uses: CreateTable, DescribeTable,
DeleteTable, PutItem, GetItem, DeleteItem, UpdateItem (`SET`, `ADD`, return values `UPDATED_NEW` and
`UPDATED_OLD`), BatchWriteItem, BatchGetItem (100 keys, `UnprocessedKeys`), Scan of the base table, Query on the table and on
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

Where the fake differs from DynamoDB: it is strongly consistent unless a test calls `HoldIndex`, which freezes
what `gsi1` returns until `ReleaseIndex` (a model of index lag, not of its timing); it throttles only when a test
calls `ThrottleNext(op, n)` (the SDK's own retries then apply, and the error surfaces when they run out);
the only `UnprocessedItems` and `UnprocessedKeys` it returns are the ones `DropOneFromNextBatch` and
`DropOneFromNextBatchGet` ask for; it implements TransactWriteItems (Put, Delete, ConditionCheck), but no
Scan of an index and rejects other operations; it rejects every word on DynamoDB's reserved-word list as an
unaliased attribute name (the list is vendored in `docs/vendor/dynamodb/reserved-words.md`; that the match is
case-insensitive is assumed, not documented); like Local it rejects an empty string as a key condition's
partition value or sort-key bound, which an older fake accepted; item and page sizes are an approximation of
the documented accounting.

**Burst.** `TestDynamoBurstOfRegistrationsOnOneHotPackage` registers one package from 150 repos at once (adaptive
shards, 4 counter stripes) and requires every write to succeed, a complete `ListDependents` straight after and no
counter drift. Unverified: real throttling and real index lag (neither the fake nor Local produces them; ROADMAP 333).

**Parity check.** CI runs `scripts/check-dynamo-parity.sh test.log`, which compares the `fake` and
`dynamodb-local` subtest of every Dynamo test and fails when one passes and the other fails, skips or is missing.
That is how a gap in the fake becomes a visible failure instead of a silent one. It only compares
what the tests exercise, and it cannot run locally without DynamoDB Local (every `dynamodb-local` subtest skips).
