# Answers recorded from real instances

Recorded by `.github/workflows/hosts-gitea.yml` (`go test -tags giteareal ./internal/platform/gitea`) on 2026-11-27 against
Gitea 1.27.3, Forgejo 17.0.2+gitea-1.22.0 and Forgejo 7.0.16+gitea-1.21.11, each a public image run as a container with SQLite.
Trimmed: no tokens, ids or host names of the runner. The tests assert these values, so a change in a later release fails the job.

| Question | Real answer (all three unless stated) |
|---|---|
| Rate limit | 600 authenticated reads in a row: 600 x 200, no `Retry-After`, no `RateLimit-*` header. No instance throttles by default; the 429 the conformance suite sees is injected by its counting proxy. |
| Scheduled merge | `POST .../pulls/{n}/merge` with `{"do":"squash","merge_when_checks_succeed":true}` (and `"Do"`): `201`, empty body. PR stays `open`, `merged=false`, timeline gains `pull_scheduled_merge`. Repeating it: `409 {"message":"pull request is already scheduled to auto merge when checks succeed [pull_id: N]"}`. The key is accepted in either case. |
| Labels by name | Gitea 1.27.3 and Forgejo 17.0.2: label absent `200 []`, nothing applied, no error; label present `200 [{"id":1,"name":"dependencies",...}]`. Forgejo 7.0.16 (Gitea 1.21): names are refused, `422 {"message":"[]: json: cannot unmarshal string into Go struct field IssueLabelsOption.Labels of type int64"}`; the same call with `[<id>]` (from `GET .../labels`) is `200` listing the label. |
| git basic auth | Private repository, token as password: user `ripple`, `oauth2`, `x-access-token`, empty and the account name are all accepted. A wrong token: refused (`Credentials are incorrect or have expired`). |
| Webhook headers | `type=gitea`: `X-Gitea-Event`, `X-Gitea-Signature`, `X-Hub-Signature-256` (and `X-Github-*`, `X-Gogs-*`). `type=forgejo`: the same plus `X-Forgejo-Event`, `X-Forgejo-Signature`, `X-Forgejo-Delivery`. Forgejo 7 and 17 both send `X-Hub-Signature-256`. |
| `repository` system webhook | Create: `action="created"`, delivered. Delete: nothing within 15 s. |
| Flavour detection | `/api/v1/version` = `17.0.2+gitea-1.22.0` and `7.0.16+gitea-1.21.11`: both detected as Forgejo. Gitea 1.27.3 has no `+gitea-` suffix. |
| Fresh pull request timeline | `[pull_push]`. After a second commit: `[pull_push, pull_push]`. After a force-push: three `pull_push`. |
| Pull request fields | `state=open`, `head.ref`, `head.repo.full_name` set. `content_version`: `0` on Gitea 1.27.3, absent on both Forgejo. |
| Empty repository | `empty=true` in `GET /repos/{o}/{r}` for up to about 2 s (10 polls of 200 ms at most seen) after the first push returns. Discovery (`/user/repos`) was not measured separately; the conformance "repo registered" step timed out until the fixture waited for `empty=false`. |
