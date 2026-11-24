# OSV security advisories

Off by default. `RIPPLE_OSV=true` turns it on; it never changes what happens when it is off.

## What happens

1. Every `RIPPLE_OSV_INTERVAL` (default 10m) ripple reads `Go/modified_id.csv` from the OSV bucket. The file is newest
   first, so it reads only down to the position it stored last time: a quiet poll reads a few lines.
2. For each changed record, in order, at most `RIPPLE_OSV_MAX_FETCH` (default 100) per poll, it fetches
   `GET /v1/vulns/{id}`. A backlog drains over later polls. The position `(modified time, id)` is stored
   after every record, so a restart resumes and does not fire twice. A first run starts at the current time.
3. For each module a record names (ecosystem `Go`), ripple looks up the
   repos importing it and applies the OSV evaluation algorithm to the version in each `go.mod`. A repo whose version
   is affected gets a job for the lowest `fixed` version above it (the fix of the range the version lies in, so a v2
   backport is chosen over a later one). Unaffected repos, repos already at the fix, withdrawn records, records
   with no release `fixed` event produce nothing to merge. A record that has a release fix and a pseudo-version fix
   uses the release fix.
4. That job skips the cooldown (the server minimum and the repo's own) and nothing else: ignore rules, the schedule,
   groups, automerge and the open-PR limit still apply. The PR is titled `[security] Update ...`, labelled `security`,
   and its body links the advisory and shows its summary and severity when OSV gives one. Only the fixed version is
   urgent: a newer release of the same module still waits for its cooldown.
   An update entry can opt in to skipping more: `security-bypass: [open-pull-requests-limit]` opens the security PR even
   when the repo is at its cap (it is planned before ordinary updates, which still wait for a free slot, and one advisory
   that affects many modules can open that many PRs), and `security-bypass: [schedule]` opens it outside the schedule
   window. The default, no list, is the behaviour above.
   `security-bypass-severity: [high, critical]` (any of `low`, `moderate`, `high`, `critical`; it needs `security-bypass`)
   restricts both bypasses to advisories with that GitHub rating (`database_specific.severity`); an advisory with no
   rating (Go-database records carry none; ripple keeps the rated twin's note when both arrive) never bypasses while
   it is set.

If the release feed already queued the same bump behind its cooldown, the advisory makes that job due.

## Advisories with no fix ripple can propose

- **Pseudo-version fixes.** Opt in with `security-pseudo-versions: true` on a `gomod` entry. When the only fix is a
  pseudo-version, ripple proposes the newest stored release of that module above the current one, on the same major
  line, and the PR says the fix is a pseudo-version and must be checked. Such a PR is never automerged. Without the
  opt-in nothing is proposed.
- **Dependency at a pseudo-version, fixed by a release.** A pseudo-version is a commit someone chose, so ripple does
  not move it by default and the dashboard lists the advisory. Opt in with `security-from-pseudo-versions: true` on a
  `gomod` entry: ripple then proposes the lowest fixing release above the commit, on the same major, never automerged,
  and the PR says it leaves a pseudo-version. A release that is not an advisory fix still never replaces a
  pseudo-version.
- **Corrected records.** When OSV corrects a record so that it no longer affects the version a repo uses (or withdraws
  it), ripple finishes the job that was waiting for it as skipped, closes the PR it opened for it with a comment saying
  why, and drops its dashboard entry. A PR a person touched stays open, and so does every PR when
  `close-superseded-pull-requests: false`. A dependency that already reached the fix is not "corrected": that work was
  done. Pending jobs made before the per-advisory indexes are indexed once by a backfill that walks the repos after an upgrade (cursor `osv-jobs-backfill`); a done job from before them is not found, and a PR body is not rewritten. A repo that another run holds
for more than two seconds keeps its job and PR for now: the record is remembered (cursor `osv-reconcile-retry`) and applied
again, fetched fresh, at the start of the next polls until the repo is free. A record whose fix was raised is
  handled by the superseded-PR flow, which replaces the old PR when the new one opens.
- **Toolchain precedence.** A security `toolchain` job and a `go` directive job both rewrite the toolchain line of one
  go.mod. The security job goes first (the higher fixed version first); the other waits, with a reason naming the PR,
  until it is merged or closed. A directive or toolchain PR of ripple's that is already open and does not fix an advisory is closed, untouched and with a
comment, once the security PR has opened; its job goes back to waiting for that PR and is judged afresh when it is merged
or closed. A PR a person touched, a host that cannot close PRs, and `close-superseded-pull-requests: false` leave both
open, and then whoever merges second resolves the conflict.
- **No fix at all, or none ripple can propose.** With `dependency-dashboard: true` the dashboard gains a section
  "Advisories without a fix ripple can propose", linking the advisory. Entries disappear when a fix is proposed, the
  record is withdrawn, or the repo stops using that version. At most 100 per repo are kept. Without the dashboard
  nothing is recorded.
- **`stdlib` and `toolchain`.** Only for a repo that opted in with `go-toolchain:` (see
  [go-toolchain.md](go-toolchain.md)): an affected `toolchain` line gets a security PR for the lowest fixed Go release,
  skipping the cooldown. A repo that did not opt in, or that has only a `go` directive, gets nothing.

## Registration and gaps

When a scan finds a dependency (module, or the toolchain line) this repo has not been checked for, ripple asks
`POST /v1/querybatch` about the new name and version pairs (at most 500 per scan; toolchain lines as both `stdlib` and
`toolchain`), fetches the records returned (at most 100 distinct per scan, cached 10 minutes) and applies them to that
repo only, as the poller would. Checked pairs are stored per repo, only after the batch succeeded: an outage is
retried by the next scan, and an unchanged `go.mod` costs no request. A failure is logged (`advisory history not
checked`) and never fails the scan. This also covers an advisory missed during a long outage, but only for a version
newly seen in a repo, and by the sweep below.

When the poll reads the whole line cap (`truncated`, 20000 lines) an outage may have outrun it, so ripple sweeps: it
lists repos in pages (`RepoLister`: memory, Postgres and DynamoDB, which keeps one index item per repo), and each poll
re-checks `RIPPLE_OSV_SWEEP_BATCH` repos (default 10) with a fresh history check, the position kept in the
`osv-sweep` cursor until every repo was visited. A repo busy with a run is retried next poll. A repo with more pairs than
one round reads (500 queries, 100 records) gets up to 10 rounds in a poll and, if that is not enough, continues on the
next poll where it stopped (the cursor remembers that its memo was cleared). A scan still runs a single round.

## Failure behaviour

OSV runs on its own loop. A failed or slow OSV request is logged as `osv poll` and retried next interval; the feed poll
and job runs do not wait for it, and the cursor does not move past a failed record. A record that fails three polls
in a row is skipped so it cannot hold up the ones behind it. A record OSV answers 404 for is skipped.

## Limits

Every request is bounded: a 20s timeout, a 1 MiB cap on a record, a 4 MiB and 20000-line cap on the list (the Go list was about 10000 lines on 2026-11-23). At most one
list request and `RIPPLE_OSV_MAX_FETCH` record requests per interval; `RIPPLE_OSV_SWEEP_BATCH` sets the repos a sweep re-checks per poll.

The client, the list and a whole ripple run were also run against the real service on 2026-11-24 (`go test -tags live`,
run weekly by `.github/workflows/live.yml`): records parse, querybatch agrees with ripple's own matching, the list is newest
first and the early stop reads only its head, an unknown id is a 404, and a module with advisories fixed in several releases
ends with one open PR for the highest fix. No Go query returned a `next_page_token`, so pagination stays covered by the fake only.

What this has not been verified against, and the pinned contract, are in [UPSTREAM.md](../UPSTREAM.md); OSV's own
documentation and recorded answers are in [vendor/osv](vendor/osv/).
