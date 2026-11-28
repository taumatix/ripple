# Dockerfile manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: docker
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not
read Dockerfiles, Compose files or Kubernetes manifests.

## What is read

Beyond `FROM`, also read: `COPY --from=image:tag`; `ARG V=1.2` declared before the first `FROM` when used as the whole
tag of one image (`FROM image:${V}`; the ARG line moves, and an ARG feeding two different images, a missing default or
`${V}-alpine` is left alone); the `image:` key of Compose files (`docker-compose*.yml`, `compose*.yml`, `*.compose.yml`)
and of Kubernetes manifests (a `.yml`/`.yaml` below a `k8s`, `kubernetes`, `kube`, `manifests`, `deploy`, `deployment`
or `deployments` directory containing `apiVersion:` and `kind:`). YAML is edited line by line, keeping quotes,
indentation and comments; `image: ${IMG:-nginx:1.25}` is not tracked, and `golang` images in YAML are (the Go-version
follow code edits Dockerfiles only).

Also read, each edited on its tag line only (quotes, comments and layout kept):

- **Kustomize** `images:` in `kustomization.yaml`, `kustomization.yml` or `Kustomization`: `newTag` of an entry, with the
  image `newName` (else `name`). An entry without `newTag` is left alone. With a `newDigest: sha256:...` too, the pair moves together: the digest line
  takes the digest the registry reports for the new tag, and the edit is refused if none can be read.
- **Helm values** (`values.yaml`, `values-*.yaml`, `values.*.yaml`, `*-values.yaml`, `*.values.yaml`): a `repository:` and
  `tag:` pair that are siblings under a key whose name contains `image` (`image`, `initImage`, `sidecar.image`), with an
  optional `registry:` sibling (ingress-nginx's form, `registry:`, `image: path/name` and `tag:` under `image:` with no `repository:`, is the same pair). A pair with a `digest: sha256:...` moves with it, like Kustomize's; a templated value (`{{ }}`, `$`) or a URL
  repository is left alone, and so is a `tag` that is empty. A single-string `image: repo:tag` under any key whose name
  contains `image` (`image`, `initImage`, `sidecar.image`) is read too, quotes and comment kept.
- **Per-stage `ARG`**: `ARG V=1.2` declared after a `FROM` and used as the whole tag in a later `COPY --from=image:${V}` or
  `--mount=from=image:${V}` of the same stage (the ARG line moves; a stage's ARG is not seen by the next stage, and a bare
  `ARG V` in a stage takes the global default).
- **`RUN --mount=...,from=image:tag`**: the first `--mount` of the line that has a `from=`; a stage name there is no image.

An ARG that is the front of a tag (`FROM alpine:${V}-slim`, `ARG V=3.19`) is read: the dependency's tag is `3.19-slim`,
and the ARG takes the new tag without its literal suffix (a new tag that does not end in it is refused). A prefix
before the variable (`v${V}`) is not read.

`FROM [--platform=...] image[:tag][@sha256:...] [AS name]` in files named `Dockerfile` or `Containerfile`, `Dockerfile.*`,
`Containerfile.*`, `*.Dockerfile` or `*.Containerfile` under `directory` (not `.md`, `.yml` and similar).

| Form | Handling |
|---|---|
| `FROM nginx:1.25`, `FROM ghcr.io/org/img:v2.3.1-alpine`, `FROM ubuntu:22.04` | bumped to a newer tag of the **same shape** (a zero-padded second or third number, `22.04`, compares as 22.4) |
| `FROM img:1.25@sha256:...` | tag and digest move together; the digest is read from the registry for the new tag |
| `FROM img`, `img:latest`, `img:stable`, `img@sha256:...` (no tag) | ignored: no version to compare |
| `FROM ${BASE}:1`, `FROM scratch`, a stage name | ignored |
| `FROM golang:...` | ignored: the Go version files code already moves it |

Same shape means the same `v` prefix, the same number of numeric components and the same variant suffix: `1.25`
follows `1.26`, never `1.26.1`; `3.2-alpine` follows `3.3-alpine`, never `3.3-slim` or `3.3`. A pin whose digest
cannot be read fails the job rather than guessing one.

## Registries

Docker Hub, GHCR and any registry speaking the Docker Registry HTTP API v2. By default the client is
**anonymous** (the Bearer challenge is answered with an anonymous token): a private image answers 401/403 or 404, is
logged and left alone, and the others in the repo still move. The registry host comes from the repo's Dockerfile,
so the client refuses hosts that resolve to non-public addresses and plain-http realms or redirects. The
cooldown counts from the image's `created` time (read from its config: the index, the linux/amd64 manifest, then the
config blob); when the registry cannot say, it counts from when ripple first saw the tag.

### Docker Hub: newest tags first (opt-in)

The registry API lists tags by name, so for `nginx` at `1.25.3` ripple still reads every page from `1.100` up (see
`registry.TagFloor`). With `RIPPLE_REGISTRY_HUB_NEWEST=true`, an image on `docker.io` with a `owner/name` repository
(`library/nginx` for an official image) is read from Docker Hub's own listing instead
(`hub.docker.com/v2/namespaces/.../tags?ordering=last_updated`), newest push first, stopping one page (100 tags)
after the page that holds a tag a dependency is on. An image with a long history costs a page or two per poll.

It is a heuristic and says so: a tag pushed before your current one but versioned above it (a backport released
late) can lie beyond the margin page and is missed until the current tag moves. Ripple falls back to the registry list,
silently and with the same result as before, when the repository is not on Docker Hub, none of its tags is known, the
Hub listing is not strictly newest-first by `last_updated` (the `ordering` values are not documented, so this is
checked on every read; see below for what Hub really answers), it needs more than 20 pages, or Hub errors. A rate limit is not fallen back from; it is
reported like any other. ghcr.io and other registries have only the lexical list, so nothing changes for them. Default off; Hub's
own API is read without a login.

Checked against the real Hub on 2026-11-28 (`library/nginx`, `TestLiveHubTagListingOrdering`): `ordering=last_updated` is
newest first and `ordering=-last_updated` is oldest first, the reverse of what ripple first sent, so until this
fix the option fell back on every read and saved nothing. `page_size` is capped at 100 (250 returns 100), and an
unknown `ordering` is HTTP 500 with `httperror 400: Invalid ordering value`. Without `ordering` the first page is
nearly, not strictly, newest first. The fallback check stayed correct throughout. Not verified: Hub's rate limit on
this endpoint, authenticated reads, and whether the ordering stays this way.


`RIPPLE_REGISTRY_AUTH_FILE` names a Docker `config.json` (what `docker login` writes; a Kubernetes
`.dockerconfigjson` pull secret mounted as a file works as is):

```json
{"auths": {"ghcr.io": {"auth": "<base64 user:token>"}, "registry.example.com:5000": {"username": "u", "password": "p"}}}
```

It is read once at start; an unreadable file stops the server. A login is sent only to the registry it is saved for:
to its Basic challenge, or to the token endpoint a Bearer challenge names (Docker Hub's `auths` key
`https://index.docker.io/v1/` is `docker.io`). A host without an entry stays anonymous, so a Dockerfile naming another
registry never receives one. An `identitytoken` entry (an OAuth2 refresh token, what `az acr login` and some Docker Hub
setups write) is used for a Bearer challenge only: ripple POSTs the refresh-token grant (`grant_type=refresh_token`,
the challenge's `service` and `scope`, `client_id=ripple`) to the realm the challenge names, never follows a redirect
from it, and uses the access token it returns; a Basic challenge cannot be answered with one, and an entry that also has
a user and password uses the password. Not read: `credsStore` and `credHelpers` (external helper programs); the server
logs a warning and those registries stay anonymous. The logins are
never logged, never put in a PR body and never printed (`registry.Credentials` redacts itself); a refused login is
reported as "denied" with the registry's name, not its value. One login serves every repo the server reads, so use
a token with read-only pull scope. Verified against a fake registry (token, Basic and refresh-token grants); no real private
registry has been tried (ROADMAP 130).

Only images some repo uses are polled, once per poll, never a whole registry. Branches are `ripple/docker/...`.
See [UPSTREAM.md](../UPSTREAM.md) for what was not verified against a real registry.
