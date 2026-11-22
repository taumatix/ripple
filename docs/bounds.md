# Bounding a scan

One repo must not be able to cost the server unbounded memory or time.

## Caps

| Variable | Default | Meaning |
|---|---|---|
| `RIPPLE_MAX_FILES` | 100000 | regular files walked in one scan (`.git`, `vendor` and `node_modules` are not walked) |
| `RIPPLE_MAX_DEPENDENCIES` | 5000 | dependencies extracted from one repo; a repo at exactly the cap is fine |

A repo over either cap is **skipped, not truncated**: it is registered with no dependencies (so no
update PRs, exactly like an invalid `.ripple.yaml`) and the engine logs a warning
`repo exceeds the file cap; no updates until it is under it` (or `dependency cap`) with
`repo=<owner/name> cap=<n>`. A partial scan would silently update some dependencies and ignore others, which
is harder to reason about than none. The next push re-scans the repo, so it recovers as soon as it is
under the cap. A repo file cannot raise a cap.

## Clone filter: what it saves

Clones use `--depth 1 --filter=blob:limit=1m`. Measured with git 2.50.1 on a repo holding a 3 MB blob at
HEAD (two commits, local `file://` server with `uploadpack.allowFilter`):

| Clone | `.git` size |
|---|---|
| `--depth 1` | 3052 KB |
| `--depth 1 --filter=blob:limit=1m` | 3068 KB (the checkout fetched the 3 MB blob lazily) |
| `--depth 1 --filter=blob:limit=1m --no-checkout` | 124 KB |

So **with a checkout the filter saves nothing today**: the checkout needs every blob at HEAD, and a
shallow clone has no other history to spare. It costs one extra on-demand fetch when HEAD holds a blob over
1 MB. It is kept because it is harmless, bounds blobs that are not at HEAD if the clone ever deepens, and is
the prerequisite for a sparse or `--no-checkout` scan (reading only the files managers match), which is
where the saving would come from. That is not done here. Files over 1 MiB are not read by ripple regardless.
