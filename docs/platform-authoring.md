# Adding a code host

This is for whoever adds GitLab, Gitea/Forgejo, Bitbucket Cloud, Bitbucket Server, Azure DevOps,
CodeCommit, Gerrit or SCM-Manager (ROADMAP entries 11–18). Several hosts are being added in parallel,
so a host touches **only files it creates**, plus `UPSTREAM.md`, its own ROADMAP entry and one
CHANGELOG line.
No shared file needs an edit to register a host.

## Before any code

Fetch the host's own documentation for webhook events and their signatures, the pull request API,
the auth model and rate limits, and vendor what you rely on under `docs/upstream/<host>/`. Pin the
API version you built against in `UPSTREAM.md`'s yaml block in the same PR. That is the one shared
file a host edits: append your entry at the end of the block, and when another host's branch merged
first, resolve the conflict by keeping both entries. Anything you did not read in the vendored text
is "assumed, unverified" in code comments and in the PR body.

## The files, and nothing else

| File | What goes in it |
|---|---|
| `internal/platform/<host>/client.go` | the `platform.Platform` (and any optional capabilities) |
| `internal/platform/<host>/receiver.go` | the webhook `http.Handler` that turns deliveries into `platform.Event`s (skip if the host has none) |
| `internal/platform/<host>/host.go` | `HostName` and `NewHost(...) *platform.Host` |
| `internal/platform/<host>/*_test.go` | unit tests for the client and receiver |
| `internal/platform/<host>/conformance_test.go` | a `platformtest.Fixture` over your fake, and `platformtest.Run` |
| `internal/testutil/<host>.go` | the fake server (REST API + bare git repos on disk); `testutil.GitHub` is the model |
| `internal/app/host_<host>.go` | the registration line and the builder that reads your settings |
| `docs/host-<host>.md` | your settings (`RIPPLE_<HOST>_*`), how to create the credential and the webhook |

**The single registration point** is one line in `internal/app/host_<host>.go`:

```go
func init() { RegisterHost(gitlab.HostName, buildGitLab) }
```

`buildGitLab(ctx, app.HostDeps) (*platform.Host, error)` reads its settings with `d.Getenv`
(never by adding fields to `config.Server`, which every branch would then edit), and returns
`nil, nil` when they are absent, so an unconfigured host costs nothing. `d.Git` carries the shared
checkout setup (work dir, module proxy, sandbox): copy it and set `GitBase`. `d.HTTP` is the shared client.

## What a host implements

Required, in `platform.Platform`:

- `Checkout`: call `gitws.Checkout(ctx, opts, cloneURL, repo.DefaultBranch, credFunc)`. Do not write
  your own clone: `gitws` holds the sandboxing, path confinement, credential redaction and the
  allow-listed `go` commands. `cloneURL` must start with `opts.GitBase`. Validate `repo.FullName`
  against the host's own path grammar before building any URL from it.
- `ListOpenPRs(repo, prefix)`: open PRs whose head branch starts with `prefix` (`ripple/`), every page.
- `CreatePR(repo, spec)`: honour `Draft`; labels are best effort; return `PR.ID` if a later call needs
  a handle other than `Number`.
- Return `*platform.RateLimitError{Until}` for the host's rate-limit answer, with `Until` computed
  from an injectable clock (the suite's clock is not the wall clock).

Optional, each a separate interface in `internal/platform/host.go`; implement it or leave it out:

| Interface | Implement when | If absent |
|---|---|---|
| `AutoMerger` | the host can merge on green (GitLab MWPS, Azure auto-complete, Gitea/Forgejo auto-merge) | PRs stay open for review; the job still succeeds |
| `PRCloser` | the host can tell whether a person touched a PR, comment on it and close it (see below) | a superseded PR stays open next to the new one; the job still succeeds |
| `IssueTracker` | the host has issues ripple may write (find by label and marker, create, update the body): the dependency dashboard | no dashboard; `ErrNoIssues`, never an error for the job |
| `IssueCloser` | the host can close an issue ripple opened (the dashboard, when a repo turns the key off); closing a closed issue is not an error | the issue is left open for a person to close |
| `RateLimiter` | always, if the host rate-limits. Record the last `Until` per credential scope | one limited host keeps getting calls until each one fails |
| `Lister` | the host has no install event (CodeCommit, Gerrit, SCM-Manager, a PAT-only setup) | repos register only through the receiver |
| `LegacyConfig` | never: `.github/ripple.yml` is GitHub's alone | only `.ripple.yaml` at the repo root is read |

`PRCloser.CloseUntouchedPR(repo, pr, comment)` is how ripple closes its own PR when a newer version of the
same module gets one (the engine retries a failed close on the next run of the repo and closes a stale PR that holds the open-PR limit's slot). The engine decides *which* PR is stale (a ripple branch of the same
module, strictly older version) and the host decides whether it is *safe*: close only when every commit on the
branch is ripple's (`gitws.AuthorEmail`), and there is no review, comment or rename; return `false, nil` and
change nothing otherwise, and return an error, with the PR left open, when any of those reads fails. Close
first, then comment (best effort, a failed comment is logged), so a retry never repeats a comment; do not delete
the branch. Also read what the host can report of an edited description, a rewritten branch and a rename. A host that cannot answer "did a person touch it" reliably should
not implement it. The conformance suite closes only where the interface is implemented, so a fixture whose
host implements it must report `Closed` and `Comments` in `PRs`.

Credentials: implement `platform.Credentials`, or use `platform.StaticCredential{Username, Secret}`
for a fixed token. Git sends it as HTTP basic auth (`oauth2:<token>` on GitLab, `x-token-auth` on
Bitbucket Cloud: check the vendored docs). It is asked for per request, so a minting source may rotate.

## Repo names and events

Your client sees host-native paths (`group/sub/project`, `org/project/repo`). The store sees
`<host>:<path>`; `platform.Mux` adds and strips the prefix, so your code never does. GitHub alone is
unprefixed. Everything up to the first `:` names the host, so your paths may contain `:` themselves.

The receiver authenticates a delivery from its headers before reading the body, bounds the body,
and calls `sink.HandleEvent` with `Kind` (`ReposAdded`, `ReposRemoved`, `Push`), the native repo
paths, and for a push the ref, default branch and changed files. Leave `Files` empty when the payload
does not list them: that forces a re-scan, which is correct but costs a clone. Set `Scope` to the
credential scope the event belongs to (0 if there is one credential); the queue shares workers
between scopes. `model.Repo.InstallationID` carries that scope back to your client.
Do not re-implement the hardening: `internal/webhook` provides `Guard` (read slots, per-body read deadline,
`ReadBody` for header-authenticated hosts, `ReadVerifiedStream` for an HMAC over a body the receiver can parse as it arrives (nothing is held; the parse is discarded on a forgery, and keeping at most 16 MiB of values bounds it), `ReadVerified` for an HMAC over a body that must be held whole, which streams the digest so a
forged body is not held in memory and spools anything over 64 KiB to a temp file, or holds it in memory under `RIPPLE_WEBHOOK_SPOOL=memory`) and `Dedupe` (a TTL table;
key it on the host's own delivery header, and `Release` the key if the sink fails so a redelivery is handled).
Embed a `Guard`, set `MaxInflight` from the receiver, and call `ReadBody`/`ReadVerified` where you read the body.
Where the scheme cannot be checked before the whole body (CodeCommit's SNS signature covers parsed JSON fields),
use `ReadBody` and say so: the bound is the size cap, the slots and the deadline.
The delivery headers used: GitHub `X-GitHub-Delivery`, GitLab `webhook-id` (then `Idempotency-Key`), Bitbucket
Cloud `X-Request-UUID`, Bitbucket Server `X-Request-Id`, CodeCommit SNS `MessageId`; Gitea/Forgejo `X-Forgejo-Delivery` or `X-Gitea-Delivery`.

Two more kinds are optional, for a host that reports them: `RepoRenamed` (`Repos` holds the old path,
`NewRepo` the new one; the stored repo, its dependencies and its pending jobs follow, no scan) and
`DefaultBranchChanged` (`DefaultBranch` is the new branch; one re-scan, future PRs target it, open PRs are
untouched). A host that cannot report them sends neither and behaves as before. Delete maps to
`ReposRemoved`. `RepoArchived` and `RepoUnarchived` are optional too: ripple keeps the archived repo's index
and pending jobs, does nothing to it while it is read-only, and resumes the jobs on unarchive with no scan. A
host that sends `ReposRemoved` for archive still has the repo forgotten, as before. A push event may carry
`RepoID`, the host's immutable repository id; a host that sets it vouches for the repo, and ripple registers a
repo it does not know from a verified push carrying one. A fixture that can deliver a rename implements the suite's
`ReportsRenames` marker and is then held to it; others skip it.

The receiver is served at `POST /webhook/<host>`.

## Done means the conformance suite passes

`internal/platform/platformtest` drives the real app against your fake: a release opens exactly one
PR in exactly the importing repos and costs a non-importing repo zero calls; a push re-scans, and an
irrelevant push costs nothing; `.ripple.yaml` means the same as on GitHub; auto-merge is requested
only when you implement it; a rate limit holds back your other repos until its time. The suite is
not to be weakened per host. If your host cannot do something the suite assumes (Gerrit has changes,
not branches), say so in the PR and extend the suite for every host rather than skipping it.

Run it with `go test ./internal/platform/<host>/ -run TestConformance -v`. `TestBareHostConforms`
in `platformtest` is the smallest host that passes it and shows a listing-only host's fixture.

## Known gaps in the seam

- `gitws` pushes to `refs/heads/<branch>` unless `Options.Proposal` is set (Gerrit: `refs/for/<base>` plus a
  Change-Id trailer). A host whose push itself creates the proposal also declares `PushCreatesProposals` in its
  fixture, so the rate-limit test expects the proposal that the push left behind.
- A `Lister` host with no push events is re-scanned only when it is re-registered; periodic
  re-scans are a ROADMAP entry.
- The previous listing is one store cursor per host, which fits tens of thousands of repo names,
  not more.
- `Receiver` is HTTP. CodeCommit's EventBridge→SNS fits it as an HTTPS subscription; SQS polling does not.
