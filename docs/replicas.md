# Running more than one replica

Replicas share one store (Postgres or DynamoDB; the memory store is single-process). Nothing needs
configuring: each process picks a random id at start and logs it (`replica` line, with the lease TTL).
`RIPPLE_REPLICA` names it instead (up to 64 characters; useful when logs should carry a stable name) and
`RIPPLE_LEASE_TTL` sets how long a claim lasts without renewal (Go duration, default 30s, minimum 10s). One
replica behaves exactly as before. A lease taken over from a holder that did not release it is logged
(`lease taken over`, with the lease name and token); a release deletes the row, so a token above 1 only
appears after a holder died or stalled.

## Design

A **lease** is a named, expiring claim with an owner and a fencing token (`store.Leases`):

- `AcquireLease` succeeds when the lease is absent or expired, or already held by the same owner. A takeover
  bumps the token; a same-owner re-acquire while live keeps it.
- `RenewLease` and `ReleaseLease` succeed only while owner and token still match.
- `store.Held` renews every ttl/3 in the background and cancels its context with `ErrLeaseLost` when a renewal
  is refused. A store error is tolerated until the lease's own expiry. `Fence()` is a synchronous renewal used
  before an irreversible step.

Two lease names are used:

| Lease | Taken by | Held while |
|---|---|---|
| `event:<id>` | the event queue, before processing | the event is processed |
| `repo:<name>` | a scan (waits for it) or a job run (tries once, skips a repo held elsewhere) | the repo's scan or jobs run |

The in-process per-repo exclusion stays; the lease makes it hold across replicas. After taking a lease the
replica **re-reads** what it claimed (`GetEvent`, `GetJob`) because its listing may be stale: an event another
replica finished is dropped, a job another replica finished is not run again (which could reopen a merged PR).

Before `CreatePR` the job run calls `Fence()`; a lost lease aborts without opening it.

## Idempotent PR on takeover

A job that pushed its branch and died before opening the PR is rerun by the new holder. The push is a force
push of the same branch name, and `execute` first looks for an open PR on that head and marks the job done when
one exists, so the outcome is one branch and one PR.

## Clocks

Lease times are written and compared by the store where it has a clock: Postgres uses `now()` in the
upsert, so replica clock skew does not matter. DynamoDB has no server time in a conditional write, so the
client wall clock is used, and a takeover requires `now > expires + skew` (default 5s, `WithLeaseSkew`).
Assumption: replica clocks agree within that skew and the TTL is well above it. The memory store uses the
local clock.

## Failure modes

| Failure | Outcome |
|---|---|
| Replica dies mid-job | Its repo lease lapses after the TTL; another replica claims the repo and reruns the pending jobs. |
| Dies between branch push and PR | Takeover re-pushes the same branch and opens the one PR (or finds it open). |
| Lease lost while the job is slow | Renewal is refused, the job's context is cancelled with `ErrLeaseLost`, the job is left for the new holder and not marked failed. `Fence()` before `CreatePR` closes the window up to that call. |
| Store blip during renewal | Tolerated until the lease's own expiry; after that treated as lost. |
| Clock skew | Postgres: none (store time). DynamoDB: absorbed up to the skew margin; beyond it a lease may be taken early, which is safe by the idempotent PR but wasteful. |
| Same event delivered to two replicas | The second fails to take `event:<id>` and holds the entry for ttl/3, then finds it gone and drops it. |
| Stale listing of due jobs | Re-read under the lease; finished jobs are skipped. |
| Two replicas poll the release feed | One replica is elected (lease `feed:<name>`, ttl twice the poll interval) and polls; the others skip and take over once its claim lapses or it steps down on shutdown. Safe even if two poll: enqueueing a job is insert-if-absent. The toolchain, GitHub Actions, Docker, npm, Cargo and pip pollers are elected the same way, each on its own lease (`feed:go-toolchain`, `feed:github-actions`, `feed:docker`, `feed:npm`, `feed:cargo`, `feed:pip`, `feed:osv`), so two replicas starting together may split the sources between them. The OSV advisory poll is elected too, with a claim of twice `RIPPLE_OSV_INTERVAL`. |
| Events of one repo on different replicas | A claimed event asks the store (`HasOlderEvent`) for an older live event of the same repo and, if there is one, is released and held for ttl/3. `Seq` is numbered by the store (`store.EventSequencer`: a one-row counter in Postgres, updated in the insert's own statement; a counter item moved by the same transaction as the event put in DynamoDB; a counter in memory), so replica clocks do not matter; events stored before the counter existed keep their clock `Seq` and sort first. On DynamoDB the check is a consistent Query of the repo's marker items (`REPOEVENTS#<repo>`, one per live event, written and removed in the same transaction as the event; see [store-dynamodb.md](store-dynamodb.md)), so it does not lag the index. Markers for events from before are written by one backfill on the first check, which reads the index; roll every replica to this version before relying on the check, and a marker left by an older replica is removed when its event is found gone. |
| Stale writer after a lost lease | The runner's job writes go through `UpdateJobFenced`: the write applies only while the lease name, owner and token are unchanged, and is refused with `ErrLeaseLost` otherwise. The advisory pass's job advance, a queue replica's event update and delete (`UpdateEventFenced`, `DeleteEventFenced`, on the `event:<id>` lease) and the cursor writes of pause, resume, remove and rename (`SetCursorFenced`, on the repo lease) are fenced the same way, one atomic statement or transaction per store. Not fenced, because idempotent: `ForgetRename`, `AdoptRepoID` and the sweep memo cursors, which write the same value whoever runs them. |
| Rename, pause, resume, remove, dashboard writes | Take the repo lease (rename takes two, in sorted order) and wait for a holder to finish. The dashboard flush skips a repo another replica holds; the holder syncs it when its run ends. |

## Verification

The shared store suite (`internal/store/storetest`) covers exclusion, renewal, takeover and an 8-way race on
every store, plus the fenced job update, the older-event query and a contention race (6 goroutines, 8 rounds,
exactly one holder at a time and no lost update); `internal/e2e/replicas_test.go` runs two engines on one store
and one fake GitHub, asserts one PR per repo, kills a replica between push and PR to assert takeover with exactly
one PR, and stalls a replica past its lease to assert its late job write is refused and it opens no second PR. `internal/e2e/replicas_feed_test.go` asserts that of two replicas one polls the feed and the other takes over when it steps down; the engine and app tests assert that a stale replica cannot write cursors, advance an advisory job or finish an event. Those run on the
memory store and the in-process DynamoDB locally; Postgres runs in CI. Not verified: a real Postgres or
DynamoDB under real contention, or real clock skew.

## Observing replicas

`RIPPLE_METRICS=true` (default off) also serves `GET /metrics` on the same listener in the Prometheus text format, with
`RIPPLE_METRICS_TOKEN` as an optional bearer token a scrape must send: `ripple_events_dead_lettered_total`,
`ripple_events_dead`, `ripple_lease_takeovers_total` (the same numbers as below) and `ripple_http_request_duration_seconds`,
a histogram of the time to serve a request by route (`webhook`, `healthz`, `replica`, `metrics`, `other`). It times
the webhook acknowledgement, not the work an event queues. Without the setting the path answers 404.

`GET /replica` on the webhook port answers one replica as JSON:
`replica` (its name), `feed_poller` (whether it holds the feed election), `pollers` (every source it polls now: `feed`, `go-toolchain`, `github-actions`, `docker`, `npm`, `cargo`, `pip`, `helm`, `rubygems`, `packagist`, `nuget`, `maven`, `osv`; only configured ones), `lease_takeovers` (leases this
process took over from a holder that died or stalled since it started; also logged as `lease taken over`) and
`lease_ttl_seconds`; and the dead letters: `events_dead_lettered` (events this process gave up on since it started, a counter to alert a rise of; also logged as `event failed; dead-lettered`) and `events_dead` (how many dead events the store holds now, every replica's, falling when one is requeued or purged; left out when listing them fails, and it lists them, so scrape it at minutes, not seconds).

Every replica also beats on a row of its own in the store (`store.ReplicaTable`: a Postgres `replicas` table,
migration 008; DynamoDB items `PK=REPLICAS`, `SK=R#<id>`), every third of the lease TTL, and the row lapses after
one TTL, so a replica that died drops out by itself and one that shuts down cleanly removes its row. From those
rows `GET /replica` adds `replicas_live` (replicas whose beat has not lapsed), `feed_pollers` (how many of them
poll at least one source; more than one is normal at a hand-over and when replicas split the sources, a persistent 0 means nothing is polled) and `lease_takeovers_all_replicas` (the sum of the live replicas' takeover counts; a replica
that died takes its count with it, so alert on a rise rather than on the absolute number). A fourth, `sources_unpolled`, lists the sources some live
replica has configured and none polls now (a heartbeat carries each replica's polled and configured sources; migration 011
adds the two Postgres columns, DynamoDB stores them as attributes); it is absent when empty, so alert on its presence. It
reads empty while any live replica still runs a version that does not report sources. The fields are
left out when the store has no replica table. Rows lapsed over an hour are deleted when replicas are listed.
Postgres judges liveness by the database clock; DynamoDB by the replicas' wall clocks, as for leases.
