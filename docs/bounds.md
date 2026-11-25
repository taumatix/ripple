# Bounding a scan

One repo must not be able to cost the server unbounded memory or time.

## Caps

| Variable | Default | Meaning |
|---|---|---|
| `RIPPLE_MAX_FILES` | 100000 | regular files walked in one scan (`.git`, `vendor` and `node_modules` are not walked) |
| `RIPPLE_MAX_DEPENDENCIES` | 5000 | dependencies extracted from one repo; a repo at exactly the cap is fine |
| `RIPPLE_MAX_BACKFILL` | 10 | jobs one scan creates for dependencies new to a repo; the unserved rest carries to the repo's next scan (not a skip: nothing is lost). Each scan also asks the module proxy at most 20 times for a module with no stored release |
| `RIPPLE_BACKFILL_MAJORS` | 3 | newer majors probed on a Go module's line when backfilling (max 50). Beyond three only the module proxy is asked, path by path, stopping after two unknown majors in a row; each ask counts against the 20 proxy requests of a scan |
| `RIPPLE_POLL_PACKAGES_PER_POLL` | 0 (all) | packages of one datasource (Actions, Docker, npm, cargo, pip, helm) a poll visits. Set, each poll takes the next that many in name order after where the previous poll stopped (`<cursor>!resume`) and wraps, so every package is visited once per cycle and a release is noticed up to a cycle later; one poll's registry requests and dependent lookups are capped. The position is stored before the slice is polled, so a poll that fails midway waits a cycle for that slice. The package set is still listed whole each poll (one index query on DynamoDB, a debug line gives its size and time); only the work done per package is bounded |

A repo over either cap is **skipped, not truncated**: it is registered with no dependencies (so no
update PRs, exactly like an invalid `.ripple.yaml`) and the engine logs a warning
`repo exceeds the file cap; no updates until it is under it` (or `dependency cap`) with
`repo=<owner/name> cap=<n>`. A partial scan would silently update some dependencies and ignore others, which
is harder to reason about than none. The next push re-scans the repo, so it recovers as soon as it is
under the cap. A repo file cannot raise a cap.

## Scan without a checkout

A scan clones `--depth 1 --filter=blob:none --no-checkout` (a *scan-only* workspace, `platform.WithScanOnly`).
`Walk` lists paths from `git ls-tree -r` (trees only, no blobs); the engine collects the paths a manager
matches, and `Prefetch` loads exactly those in one request: `git fetch origin --stdin` of the files' object ids
(the command git runs itself for a lazy fetch, with `--filter=blob:none` and `fetch.negotiationAlgorithm=noop`; it
needs git 2.35 or later for `--stdin`). No sparse checkout and no working tree are involved, so any path spelling
works and `ReadFile` reads the blobs from the object database. Anything not prefetched
(the config file read before the walk) is read with `git cat-file blob <oid>`, one lazy fetch each. Every one of these network commands runs with the per-command credential, so a private
repo works. An update (a PR) still takes a full checkout: `go mod tidy` needs the sources.

What the host must offer: protocol v2 `filter` (git's `gitprotocol-v2`: advertised "when the filter feature is
supported"; `uploadpack.allowFilter` on a stock git server) and fetching a blob by object id
(`uploadpack.allowAnySHA1InWant` or reachable-sha1). GitHub serves partial clones (measured below); for the other hosts this
is not verified here (a stock git server needs the two settings above). A host that does not support it ignores the filter, git warns, and the
clone carries every object (no saving, same result). When the host cannot do the follow-up fetch, or a matched
file is a symbolic link, the workspace returns `platform.ErrScanFallback` and the engine logs
`partial scan unavailable; scanning a full checkout` and repeats on the usual checkout. The scan never fails
for want of partial clone. Only GitHub was measured live; the others are from their documentation.

### Hosts that serve only part of it

Git protocol v2 serves any object a client names, so `uploadpack.allowAnySHA1InWant` is moot there; it matters on a host
that speaks protocol v0 (an older git, or a proxy that drops the `Git-Protocol` header). `TestScanOnHostsWithPartialCapabilities`
runs the fake server as such a host (`GitHTTP.SpeakProtocolV0`) in both half-way shapes:

| Host serves | Scan |
|---|---|
| filter, but refuses a blob by object id | the batched fetch fails, the scan falls back to a full checkout (logged once) |
| a blob by object id, but ignores the filter | the clone carries everything, no fallback, no saving |

The first shape exposed a bug: the full checkout also clones with `--filter=blob:limit=1m` and its checkout fetches the
omitted blobs by object id, so on such a host the fallback failed too and the scan never finished. A full checkout that
fails now repeats once with an unfiltered clone, which needs neither. Which real host (GitLab, Gitea/Forgejo, Bitbucket,
Azure DevOps, Gerrit, CodeCommit, SCM-Manager) falls in which row has not been measured; that needs each host.

### Why not sparse checkout

The first version fetched through `git sparse-checkout set --no-cone --stdin` and `git checkout HEAD`. Git marks
non-cone patterns deprecated, and on a `--no-checkout` clone it was also not doing the job: the helper that ran git
dropped the command's stdin, so the pattern list was empty and nothing was prefetched. Every blob was read by its own
lazy fetch, which the byte-budget test could not tell apart. `TestScanOnlyPrefetchIsOneFetchByObjectID` now counts
`git-upload-pack` requests: one for the batch, none for the reads after it.

Checked with git 2.50.1 on a `--depth 1 --filter=blob:none --no-checkout` clone: `git fetch origin --stdin` of blob ids
fetches them all in one request over `file://`, `git://` and smart HTTP (the test server). Plain `git fetch` of object
ids without `fetch.negotiationAlgorithm=noop` fails ("bad revision") because it tries to negotiate from a blob.
`git backfill` was not used: it needs a checkout's sparse patterns to name paths and fetches by path batches, which
is the sparse mechanism again. Not verified: github.com and the other hosts with this command (the earlier GitHub
measurements below are of the sparse version; the hosts must serve a blob by id either way, which the lazy fetch
needs too, so a host that served the old path serves this one).

Measured over the public internet with git 2.50.1, `RIPPLE_LIVE_SCAN_REPO=<repo> go test -tags live ./internal/platform/gitws -run LiveScan -v`
(Checkout, Walk, Prefetch of the default managers' matches, ReadFile; three runs each for `taumatix/ripple`, identical):

| Repo | Files | Clone | Pack downloaded | Workspace on disk | Time |
|---|---|---|---|---|---|
| taumatix/ripple | 261 | `blob:limit=1m` + checkout (before) | 1318 KiB | 6532 KiB | 1.1-1.2 s |
| taumatix/ripple | 261 | scan-only (after) | 17 KiB | 95 KiB | 1.3-1.4 s |
| spf13/cobra | 66 | before / after | 218 / 3 KiB | 943 / 38 KiB | 0.9 / 1.3 s |
| go-git/go-git | 791 | before / after | 1606 / 30 KiB | 7383 / 146 KiB | 1.4 / 1.3 s |

The saving is in bytes (about 98%) and disk, not in time: the scan makes one more round trip (clone, then the
batched blob fetch), which costs as much as the transfer on these small repos. It grows with the repo (the
bytes avoided are every blob nothing reads: a repo with a 3 MB asset downloads 3 MB less, asserted in
`TestScanDownloadsOnlyWhatManagersRead`); the time saving on a slow link or a very large repo is not measured.

## Clone filter for updates: what it saves

An update's clone is `--depth 1 --filter=blob:limit=1m` with a checkout. Measured with git 2.50.1 on a repo
holding a 3 MB blob at HEAD (two commits, local `file://` server with `uploadpack.allowFilter`):

| Clone | `.git` size |
|---|---|
| `--depth 1` | 3052 KB |
| `--depth 1 --filter=blob:limit=1m` | 3068 KB (the checkout fetched the 3 MB blob lazily) |
| `--depth 1 --filter=blob:limit=1m --no-checkout` | 124 KB |

So **with a checkout the filter saves nothing today**: the checkout needs every blob at HEAD, and a
shallow clone has no other history to spare. It costs one extra on-demand fetch when HEAD holds a blob over
1 MB. It is kept because it is harmless, bounds blobs that are not at HEAD if the clone ever deepens, and is
the prerequisite for a sparse or `--no-checkout` scan (reading only the files managers match), which is
where the saving would come from. That is done for scans, see above. Files over 1 MiB are not read by ripple regardless.
