# Dockerfile manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: docker
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not
read Dockerfiles. Compose files are not read.

## What is read

`FROM [--platform=...] image[:tag][@sha256:...] [AS name]` in files named `Dockerfile` or `Containerfile`, `Dockerfile.*`,
`Containerfile.*`, `*.Dockerfile` or `*.Containerfile` under `directory` (not `.md`, `.yml` and similar).

| Form | Handling |
|---|---|
| `FROM nginx:1.25`, `FROM ghcr.io/org/img:v2.3.1-alpine` | bumped to a newer tag of the **same shape** |
| `FROM img:1.25@sha256:...` | tag and digest move together; the digest is read from the registry for the new tag |
| `FROM img`, `img:latest`, `img:stable`, `img@sha256:...` (no tag) | ignored: no version to compare |
| `FROM ${BASE}:1`, `FROM scratch`, a stage name | ignored |
| `FROM golang:...` | ignored: the Go version files code already moves it |

Same shape means the same `v` prefix, the same number of numeric components and the same variant suffix: `1.25`
follows `1.26`, never `1.26.1`; `3.2-alpine` follows `3.3-alpine`, never `3.3-slim` or `3.3`. A pin whose digest
cannot be read fails the job rather than guessing one.

## Registries

Docker Hub, GHCR and any registry speaking the Docker Registry HTTP API v2, **anonymously** (the Bearer challenge
is answered with an anonymous token). Credentials are not supported: a private image answers 401/403 or 404, is
logged and left alone, and the others in the repo still move. The registry host comes from the repo's Dockerfile,
so the client refuses hosts that resolve to non-public addresses and plain-http realms or redirects. The
cooldown runs from when ripple first saw the tag, as a registry publishes no release date.

Only images some repo uses are polled, once per poll, never a whole registry. Branches are `ripple/docker/...`.
See [UPSTREAM.md](../UPSTREAM.md) for what was not verified against a real registry.
