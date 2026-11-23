# Container registry API excerpts

Fetched 2026-11-23 (via a summarising fetch, so the wording is condensed, not copied byte for byte) and read before
`internal/registry` was written.

Sources: <https://github.com/opencontainers/distribution-spec/blob/main/spec.md> (OCI Distribution Spec) and
<https://distribution.github.io/distribution/spec/auth/token/> (Docker registry token authentication).

## Tags (end-8a, end-8b)

- `GET /v2/<name>/tags/list` -> 200 `{"name": "<name>", "tags": ["<tag1>", "<tag2>"]}`.
- `GET /v2/<name>/tags/list?n=<int>&last=<tagname>`: `n` is the page size, `last` the (exclusive) tag to start after.
- Results are in lexical order. With `n`, a response may hold fewer than `n` only if no more exist or a `Link` header is
  sent; a `Link` header with `rel="next"` means more content is available.

## Manifests (end-3)

- `GET` and `HEAD /v2/<name>/manifests/<tag-or-digest>` -> 200. A successful response carries `Docker-Content-Digest`,
  the digest of the manifest (for a multi-platform image, of the index), which is what a `@sha256:...` pin names.

## Anonymous token flow

```
Www-Authenticate: Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:samalba/my-app:pull,push"
GET https://auth.docker.io/token?service=registry.docker.io&scope=repository:samalba/my-app:pull
```

The reply is JSON with `token` and/or `access_token` (at least one), optionally `expires_in` and `issued_at`; it is sent
back as `Authorization: Bearer <token>`.

## Assumed, unverified (not in the excerpts above)

- Docker Hub's API host is `registry-1.docker.io` and unqualified images live under `library/`.
- GHCR answers the same challenge for anonymous pulls of public images.
- The `Accept` media types needed for `HEAD` to return the index digest: OCI index and image manifest, Docker manifest
  list and manifest v2.
- Rate limits and their status codes (Docker Hub limits anonymous pulls; ripple treats 429 as "try later").
