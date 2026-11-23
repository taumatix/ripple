# Vendored OSV documentation

Fetched 2026-11-23 and read before `internal/advisory` was written. Each file names its source in its
own header or here.

| File | Source | Carries |
|---|---|---|
| `schema.md` | ossf/osv-schema `docs/schema.md` (main) | record fields, `affected[].ranges[].events` (`introduced`, `fixed`, `last_affected`, `limit`), SEMVER has no leading `v`, the **Evaluation** pseudocode, `severity[]` (CVSS vector strings, not numbers), `withdrawn`, ecosystem `Go` = module path |
| `data.md` | google/osv.dev `docs/data.md` | the `gs://osv-vulnerabilities` bucket, per-ecosystem `modified_id.csv` (reverse chronological, `<iso>,<id>`), `ecosystems.txt` |
| `api-index.md`, `post-v1-query.md`, `post-v1-querybatch.md`, `get-v1-vulns.md`, `api-quickstart.md` | google/osv.dev `docs/api/` | `POST /v1/query`, `POST /v1/querybatch`, `GET /v1/vulns/{id}`; "currently there are no limits on the API"; 32 MiB response cap on HTTP/1.1; case-sensitive names |
| `swagger-full.json` | google/osv.dev `docs/osv_service_v1.swagger.json` | OpenAPI of the above |
| `rest-api.md` | google/osv.dev `docs/rest-api.md` | (data-source contribution doc, kept for completeness) |

## Recorded live answers (2026-11-23, public, unauthenticated)

| File | Request |
|---|---|
| `samples/Go-modified_id.head.csv` | first 8 lines of `GET https://storage.googleapis.com/osv-vulnerabilities/Go/modified_id.csv` (the whole file was 9989 lines, ~454 KB; supports `Range`; header `last-modified`) |
| `samples/GO-2026-6894.json` | `GET https://api.osv.dev/v1/vulns/GO-2026-6894` |
| `samples/GHSA-2wrx-84qj-4pcg.trimmed.json` | the same advisory as GitHub published it; only `details` trimmed. Carries `database_specific.severity` ("MODERATE") and `severity[]` CVSS_V3 |
| `samples/GO-2024-3228.json` | three `introduced`/`fixed` pairs in one range (backported fixes) |
| `samples/GO-2026-6608.json` | package name `stdlib` (also seen: `toolchain`) |
| `samples/GO-2026-4740.withdrawn.json` | carries `withdrawn` |

## What the 399 Go advisories sampled from the head of the Go file showed

All 557 `affected` entries had ecosystem `Go` and range type `SEMVER`; events were only `introduced` and
`fixed` (no `last_affected`, no `limit`); `fixed` carries no `v` prefix; 46 of 490 `fixed` values are
pseudo-versions (`1.61.1-0.20260911233437-76452a2c8a2f`) which ripple does not propose; 59 entries are
`stdlib` and 18 `toolchain`, which are not go.mod modules.

## Not found in the docs, so assumed or unverified

- Any rate limit on api.osv.dev or on the bucket (the docs say there is none today; ripple bounds itself anyway).
- Whether `modified_id.csv` is ever rewritten non-monotonically (ripple reads only until it meets a timestamp it has seen).
- Latency between an id appearing in `modified_id.csv` and `GET /v1/vulns/{id}` serving it (ripple treats 404 as "skip").
- `database_specific.severity` is a GitHub-advisory field seen in recorded data, not in the OSV schema; ripple only displays it.
