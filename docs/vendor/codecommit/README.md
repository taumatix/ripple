# Vendored upstream documentation (AWS CodeCommit)

Fetched 2026-11-23 and read before `internal/platform/codecommit` was written. Each file starts
with a comment naming its source URL.

| Files | Carry |
|---|---|
| `api-API_*.md`, `api-CommonErrors.md`, `api-CommonParameters.md`, `api-Welcome.md` | the CodeCommit API operations ripple calls (ListRepositories, ListPullRequests, GetPullRequest, CreatePullRequest, GetDifferences) and the others read while designing it; request limits, error names |
| `iam-reference_sigv*.md` | SigV4 for the API calls (`codecommit` service, JSON 1.1) |
| `botocore-auth.py` | botocore's SigV4Auth, the reference for the signature maths |
| `grc-init.py`, `ug-setting-up-git-remote-codecommit.md` | git-remote-codecommit: the git password is a custom SigV4 signature over `GIT\n<path>\n\nhost:<host>\n\nhost\n`, user name `AK[%token]` |
| `sns-*.md` | SNS HTTPS subscriptions: headers, notification and confirmation JSON, the signature string, retries |
| `ug-monitoring-events.md`, `ug-how-to-notify*.md` | the EventBridge `referenceCreated` / `referenceUpdated` event |
| `ug-regions.md`, `ug-welcome.md`, `ug-setting-up-gc.md` | regions and git endpoints |
| `aws-blog-*.txt` | the July 2024 de-emphasis and the 24 Nov 2025 reopening to new customers |

Nothing here was recorded from a live AWS account.
