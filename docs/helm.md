# Helm manager

Opt in per repo in `.ripple.yaml`:

```yaml
updates:
  - ecosystem: helm
    directory: /          # optional; default /
```

Only `directory` applies (and the shared keys such as cooldown and grouping). Without the entry ripple does not read
`Chart.yaml` or `requirements.yaml` files.

## What is read

The `dependencies:` list of `Chart.yaml` (apiVersion v2) and `requirements.yaml` (v1). A dependency is tracked when
all of these hold:

- `repository` is an `https://` URL. `oci://`, `file://`, `@alias` and `alias:` repositories are skipped.
- `version` is one exact version, optionally behind `=`, `^`, `~` and a `v` (`1.2.3`, `^1.2.3`, `~v1.2.3`). Ranges
  (`>=1.0.0`, `1.x`, `1.2.3 - 2.0.0`) and missing versions are skipped. A `~v3.11.0` was asked of real `helm dependency
  update` (`TestRealtoolHelmTilde`): it locks a 3.11 chart, and the edited `~v3.12.0` locks a 3.12 one.
- The entry is written in block style with plain or simply quoted values. Flow style (`{...}`) and values with escapes
  are skipped.

The package is named `<normalised repository URL>/<chart>` (`name`, not `alias`).

## What is proposed

The repository's `index.yaml` is read once per repository per poll. Deprecated versions, prereleases and versions
with build metadata are never proposed. The `cooldown` runs from the first poll that saw the version, because the
index's `created` date is not used.

Ripple edits only the version bytes of the entry (the prefix operator and `v` are kept), so comments, ordering and
quoting stay as written. If the file changed since the poll the job is stale and nothing is written.

## Lock files

`Chart.lock` and `requirements.lock` beside the edited file are not rewritten (they carry a digest of the dependency
list). The PR body names them as stale; run `helm dependency update` and push to the branch.

## Network

`index.yaml` is fetched from a URL written in the repository, so the default client refuses any address that is not
public at dial time (loopback, private, link-local), on every redirect too, and caps the index at 128 MiB.
Repositories needing credentials are not supported.

The branch prefix is `ripple/helm/`.

See [UPSTREAM.md](../UPSTREAM.md): nothing here is verified against a real Helm repository.
