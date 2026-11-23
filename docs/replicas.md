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
| Two replicas poll the release feed | Safe: enqueueing a job is insert-if-absent. |
| Events of one repo on different replicas | Order is not preserved across replicas (see ROADMAP). |
| Stale writer after a lost lease | `UpdateJob` is not fenced by the store; benign because the PR is idempotent by branch (see ROADMAP). |
| Rename, pause, dashboard writes | Use the in-process lock only (see ROADMAP). |

## Verification

The shared store suite (`internal/store/storetest`) covers exclusion, renewal, takeover and an 8-way race on
every store; `internal/e2e/replicas_test.go` runs two engines on one store and one fake GitHub, asserts one PR
per repo, and kills a replica between push and PR to assert takeover with exactly one PR. Those run on the
memory store and the in-process DynamoDB locally; Postgres runs in CI. Not verified: a real Postgres or
DynamoDB under real contention, or real clock skew.
