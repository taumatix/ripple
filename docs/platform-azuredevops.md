# Azure DevOps Services

Ripple can watch repositories in an Azure DevOps Services organisation (`dev.azure.com`). Azure DevOps
Server (on premises) is a different product and is not covered. This host is written from Microsoft's
documentation, vendored in [vendor/azuredevops](vendor/azuredevops), and tested against a fake; it has **not**
been run against a real organisation (see Unverified).

Repositories are `organization/project/repository`, stored as `azuredevops:org/project/repo`. The
organisation uses letters, digits, `.`, `_` and `-`. A project or repository may also have single spaces inside its
name (not leading, trailing or doubled); they are escaped as `%20` in REST and clone URLs. Any other character
skips the project or repository with a log line.

## Settings

| Variable | |
|---|---|
| `RIPPLE_AZUREDEVOPS_ORG` | the organisation (required, with the PAT) |
| `RIPPLE_AZUREDEVOPS_PAT` | personal access token, scopes Code (Read & write) and Pull Request Threads (Read & write) |
| `RIPPLE_AZUREDEVOPS_WEBHOOK_PASSWORD` | password of the service hook's basic authentication |
| `RIPPLE_AZUREDEVOPS_WEBHOOK_USER` | its user name, default `ripple` |
| `RIPPLE_AZUREDEVOPS_API_URL`, `RIPPLE_AZUREDEVOPS_GIT_URL` | default `https://dev.azure.com`; for tests |

The host is off unless the organisation and token are set.

## How repositories are found and kept current

Azure DevOps has no install event, so ripple lists the organisation's projects and their enabled, non-empty
repositories on every discovery pass and watches what appears. Create one service hook subscription per project
for **Code pushed** (`git.push`) with the Web Hooks consumer: URL `https://<ripple>/webhook/azuredevops`,
**basic authentication** with the user and password above. Service hooks have no HMAC signature, so serve ripple
over HTTPS only, and use a password used nowhere else. The push payload lists commits but no changed files, so
ripple asks the commit changes API for each pushed commit on the default branch (up to 20 commits) and
re-scans only when a changed file matters. If that lookup fails, is truncated, or the push is larger, the
repository is re-scanned instead.

Subscribe also to **Repository created** (`git.repo.created`), **Repository deleted** (`git.repo.deleted`) and
**Repository renamed** (`git.repo.renamed`) with the same URL and credentials so a change shows without waiting for
the next listing: created registers the repository (an empty one, with no default branch, is left to its first
listing), deleted forgets it, renamed moves its record and pending jobs to the new name without a scan. Without
them the listing finds the same changes, but a rename then looks like a delete and an add and costs a scan.

## Behaviour

- Pull requests: created with `sourceRefName`/`targetRefName`, drafts as `isDraft`, labels added one by one.
- Auto-merge: sets auto-complete with a squash merge and source-branch deletion; branch policies decide when it completes.
- Rate limits: a 429, or a `Retry-After` header on any response (Azure sends it on a 200 before it delays), holds
  back every repository of the organisation until that time.
- Git: clones over HTTPS with the PAT as the password and an empty user name.
- Microsoft Entra: a `platform.Credential` with `Scheme: platform.SchemeBearer` is sent as `Authorization: Bearer`
  on REST and git. Only the library side exists; no setting builds one yet (ROADMAP 224).

## Unverified

Not confirmed against a real organisation: that a repository name is accepted where the docs name a repository id;
that `autoCompleteSetBy.id` may be the pull request creator's id; the labels endpoint; the commit changes lookup (merge commits, paging); the service hook's
delivery headers; whether the payload `id` ripple dedupes on is the same across a retry (the vendored
[troubleshooting page](vendor/azuredevops/service-hooks-troubleshoot.md) gives the schedule: up to eight retries on
408, 502, 503 and 504, then probation, and says nothing of the id; a retry under a new id is handled again, which
costs a repeated scan and nothing else); page-size behaviour; and the fake in `internal/testutil/azuredevops.go` itself, which is
written from the docs, not recorded. The `git.repo.*` payloads, the escaped names and the bearer scheme were exercised only against the fake. Entra tokens cannot be configured yet (ROADMAP 224).

## Archive

Only GitHub reports archive and unarchive today (ripple pauses the repo on `archived`, resumes on `unarchived`). Azure DevOps: ripple consumes only the service hook events listed here; none reports a repository being disabled or archived, and this was not verified against the service hook docs (2026-11-23). Nothing pauses on archive.

## Superseded pull requests

Not implemented: when a newer release's PR replaces ripple's earlier one on Azure DevOps, the old PR stays
open. Abandoning it is possible (`PATCH .../pullrequests/{id}` with `status: abandoned`) and so are the reads
for commits, threads (`commentType` separates people from `system`) and iterations (`forcePush`), but the
API gives no way to tell that a person renamed the PR or edited its description: the pull request holds
only the current title and description, with no edit history or original to compare against. Closing without
that check could discard a person's edits, and ripple never closes on doubt. If a future API version exposes
the edit history, this can be added; see `docs/vendor/azuredevops/pullrequests-supersede-excerpt.md`.
