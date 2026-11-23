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
- **Toolchain precedence.** A security `toolchain` job and a `go` directive job both rewrite the toolchain line of one
  go.mod. The security job goes first (the higher fixed version first); the other waits, with a reason naming the PR,
  until it is merged or closed. A PR already open is not closed for this: whoever merges second resolves the conflict.
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
newly seen in a repo (the ROADMAP names the sweep that is left).

## Failure behaviour

OSV runs on its own loop. A failed or slow OSV request is logged as `osv poll` and retried next interval; the feed poll
and job runs do not wait for it, and the cursor does not move past a failed record. A record that fails three polls
in a row is skipped so it cannot hold up the ones behind it. A record OSV answers 404 for is skipped.

## Limits

Every request is bounded: a 20s timeout, a 1 MiB cap on a record, a 4 MiB and 20000-line cap on the list (the Go list was about 10000 lines on 2026-11-23). At most one
list request and `RIPPLE_OSV_MAX_FETCH` record requests per interval.

What this has not been verified against, and the pinned contract, are in [UPSTREAM.md](../UPSTREAM.md); OSV's own
documentation and recorded answers are in [vendor/osv](vendor/osv/).
