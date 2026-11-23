# Azure DevOps Services

Ripple can watch repositories in an Azure DevOps Services organisation (`dev.azure.com`). Azure DevOps
Server (on premises) is a different product and is not covered. This host is written from Microsoft's
documentation, vendored in [vendor/azuredevops](vendor/azuredevops), and tested against a fake; it has **not**
been run against a real organisation (see Unverified).

Repositories are `organization/project/repository`, stored as `azuredevops:org/project/repo`. Names may use
letters, digits, `.`, `_` and `-`; a project or repository with a space in its name is skipped with a log line.

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

## Behaviour

- Pull requests: created with `sourceRefName`/`targetRefName`, drafts as `isDraft`, labels added one by one.
- Auto-merge: sets auto-complete with a squash merge and source-branch deletion; branch policies decide when it completes.
- Rate limits: a 429, or a `Retry-After` header on any response (Azure sends it on a 200 before it delays), holds
  back every repository of the organisation until that time.
- Git: clones over HTTPS with the PAT as the password and an empty user name.

## Unverified

Not confirmed against a real organisation: that a repository name is accepted where the docs name a repository id;
that `autoCompleteSetBy.id` may be the pull request creator's id; the labels endpoint; the commit changes lookup (merge commits, paging); the service hook's
delivery headers; page-size behaviour; and the fake in `internal/testutil/azuredevops.go` itself, which is
written from the docs, not recorded. Entra ID bearer tokens are not supported (ROADMAP).
