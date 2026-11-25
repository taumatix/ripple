# Vendored upstream documentation (Gitea and Forgejo)

Fetched 2026-11-23 and read before `internal/platform/gitea` was written. Each file names its source URL.

| File | Source | Carries |
|---|---|---|
| `gitea/webhooks.md` | docs.gitea.com/usage/webhooks | delivery headers, signature (`X-Gitea-Signature` hex HMAC-SHA256, `X-Hub-Signature-256` `sha256=`), events |
| `gitea/api-usage.md` | docs.gitea.com/development/api-usage | `Authorization: token ...`, pagination |
| `gitea/pull-request.md` | docs.gitea.com/usage/pull-request | draft = `WIP:` title prefix |
| `gitea/swagger-excerpt.json` | gitea.com/swagger.v1.json | merge body (`do`, `merge_when_checks_succeed`), PR, repo, labels |
| `forgejo/webhooks.md` | forgejo.org/docs/latest/user/webhooks | `X-Forgejo-*` headers, `X-Forgejo-Signature` in the PHP example |
| `forgejo/numbering-scheme.md` | forgejo.org/docs/latest/user/api/versions | `X.Y.Z+gitea-A.B.C`; `+gitea-` marks Forgejo |
| `forgejo/swagger-excerpt.json` | codeberg.org/swagger.v1.json | merge body key is `Do` (capital) |
| `*/config-excerpt.txt` | config cheat sheets | `MAX_RESPONSE_ITEMS` 50, default page 30, `WORK_IN_PROGRESS_PREFIXES` |

Recorded live answers (public instances, 2026-11-23):

- `GET https://gitea.com/api/v1/version` -> `{"version":"29.0.0+dev-160-g55186ca39b"}` (no `+gitea-`: Gitea)
- `GET https://codeberg.org/api/v1/version` -> `{"version":"17.0.0-121-39320c0+gitea-1.22.0"}` (Forgejo);
  its response carried `ratelimit-policy: "baseline";q=2000;w=600` and `ratelimit: "baseline";r=1997;t=600`.

Not found in either project's docs, so **assumed, unverified**: any API rate limit or its 429 shape
(Codeberg's proxy shows IETF `RateLimit` headers; ripple also honours `Retry-After`); the response
code of a scheduled (not immediate) auto-merge; whether Forgejo also sends `X-Hub-Signature-256`
or `X-Gitea-Signature` (the receiver accepts all three); the git basic-auth username a token needs.

## mockgen

`mockgen/README.md` is go.uber.org/mock's README (fetched 2026-11-23), the grammar source for the
`directives: extended` mockgen rewrite: package and archive mode take `<import path> <symbols>`, `-imports` takes
`foo=bar/baz` elements, `-self_package` is the generated package's import path. `golang-mock/README.md` is
github.com/golang/mock v1.6.0's README, which lists `-self_package`, `-exec_only`, `-prog_only` and the rest of
the older flag set the rewrite now reads. `stringer/flags.txt` is the flag set of `golang.org/x/tools/cmd/stringer`
v0.20.0: none takes an import path, so there is nothing to rewrite. **Assumed, unverified**: behaviour of flags
neither README lists, and what mockgen does with a `-source` file and positionals together (the rewrite leaves that
line alone).

## DynamoDB

`dynamodb/reserved-words.md` is the developer guide's "Reserved words in DynamoDB" page (fetched 2026-11-23), the
source of the 573 words `internal/store/dynamofake` rejects unaliased. A test parses the file and compares it with the
fake's list, so the two cannot drift. **Assumed, unverified**: that the match ignores case, and in which expression
clauses the service applies the check (the fake applies it to condition, filter, key-condition, projection and update
expressions); DynamoDB Local is the check on both.

## helm

`helm/chart_repository.md` and `helm/charts.md` are the helm-www pages (helm.sh/docs/topics/chart_repository,
helm.sh/docs/topics/charts), fetched 2026-11-25 as raw markdown from the helm-www repository and read before
`internal/helmrepo` and `manager.Helm` were written: the `index.yaml` shape (`entries`, `version`, `deprecated`) and
the `dependencies` fields of `Chart.yaml`. Not checked against a live repository, so unverified: index size and
redirect behaviour of real hosts, how `created` is filled, the full constraint grammar (only exact versions are used).
