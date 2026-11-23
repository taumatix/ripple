# OSV security advisories

Off by default. `RIPPLE_OSV=true` turns it on; it never changes what happens when it is off.

## What happens

1. Every `RIPPLE_OSV_INTERVAL` (default 10m) ripple reads `Go/modified_id.csv` from the OSV bucket. The file is newest
   first, so it reads only down to the position it stored last time: a quiet poll reads a few lines.
2. For each changed record, in order, at most `RIPPLE_OSV_MAX_FETCH` (default 100) per poll, it fetches
   `GET /v1/vulns/{id}`. A backlog drains over later polls. The position `(modified time, id)` is stored
   after every record, so a restart resumes and does not fire twice. A first run starts at the current time.
3. For each module a record names (ecosystem `Go`; `stdlib` and `toolchain` are not modules), ripple looks up the
   repos importing it and applies the OSV evaluation algorithm to the version in each `go.mod`. A repo whose version
   is affected gets a job for the lowest `fixed` version above it (the fix of the range the version lies in, so a v2
   backport is chosen over a later one). Unaffected repos, repos already at the fix, withdrawn records, records
   with no `fixed` event and fixes that are pseudo-versions produce nothing.
4. That job skips the cooldown (the server minimum and the repo's own) and nothing else: ignore rules, the schedule,
   groups, automerge and the open-PR limit still apply. The PR is titled `[security] Update ...`, labelled `security`,
   and its body links the advisory and shows its summary and severity when OSV gives one. Only the fixed version is
   urgent: a newer release of the same module still waits for its cooldown.

If the release feed already queued the same bump behind its cooldown, the advisory makes that job due.

## Failure behaviour

OSV runs on its own loop. A failed or slow OSV request is logged as `osv poll` and retried next interval; the feed poll
and job runs do not wait for it, and the cursor does not move past a failed record. A record that fails three polls
in a row is skipped so it cannot hold up the ones behind it. A record OSV answers 404 for is skipped.

## Limits

Every request is bounded: a 20s timeout, a 1 MiB cap on a record, a 4 MiB and 2000-line cap on the list. At most one
list request and `RIPPLE_OSV_MAX_FETCH` record requests per interval.

What this has not been verified against, and the pinned contract, are in [UPSTREAM.md](../UPSTREAM.md); OSV's own
documentation and recorded answers are in [vendor/osv](vendor/osv/).
