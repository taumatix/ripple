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
