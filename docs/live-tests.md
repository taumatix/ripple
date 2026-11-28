# Live tests against the real registries

Every package registry ripple reads exposes a public, unauthenticated API. The `live`-tagged tests
call those real endpoints through the same client code the engine uses, so a registry that changes
its schema, or a parser that read the schema wrong, fails a test instead of a user's run. The
fake-registry tests cannot catch either: they only prove the parser matches ripple's own idea of the
format.

## Running them

    go test -tags live -count=1 -v -run Live ./internal/...

or one datasource: `go test -tags live -count=1 -v -run Live ./internal/maven`.

They send no credential, make a handful of requests per test, wait between requests, name themselves
in the User-Agent (`internal/livetest`), honour `Retry-After`, and retry a 429 or a transient 5xx a
bounded number of times. A service that still refuses fails the test with the HTTP status. A test
never skips: CI fails on a skipped test. GitHub's anonymous API allows 60 requests an hour per
address, and `internal/platform/github` uses about a dozen of them. A GitHub-hosted runner shares
its address, so that package waits out an exhausted quota (up to about eight minutes) instead of
failing on it, and Maven Central, which answers 429 without `Retry-After` to a busy address, gets a
longer backoff; use `-timeout 25m` as the workflow does.

CI runs them in `.github/workflows/live.yml` daily, on `workflow_dispatch`, and on pull requests that
change a datasource package. That job is not a required check.

## What each one covers

| Datasource | Fixture | Asserts |
|---|---|---|
| npm (`npmreg`) | `left-pad`, `@types/ms`, `typescript` | deprecated flag read, scoped names, prereleases, not-found |
| PyPI (`pypi`) | `requests`, `django` | yanked flag, file-less releases, prereleases |
| crates.io (`cratesindex`) | `serde`, `log`, `cc` | yanked flag, prereleases, index path of every name length |
| Container registries (`registry`) | alpine, `ghcr.io/astral-sh/uv`, `quay.io/prometheus/node-exporter` | token challenge, tags, digest stability, creation time |
| Go proxy and index (`feed`) | `golang.org/x/text`, `go.mongodb.org/mongo-driver`, `github.com/prometheus/common` (its highest stable tag retracts itself), `github.com/go-git/go-git/v6` (prerelease-only until it goes stable; the test then fails on purpose) | index paging and inclusive `since`, retractions, `@latest` for a retracted-stable and a prerelease-only module, the far-major probe over a skipped major (`github.com/Masterminds/semver` v2 absent, v3 present) and its request cost |
| Go pseudo-version move (`engine`) | `github.com/pkg/errors` at `v0.8.1-0.20180217175701-e1ac100e4667` | the proxy derives that pseudo-version from commit e1ac100e4667; `from-pseudo-versions` accepts v0.8.1 and v0.9.0 and refuses v0.8.0 (v0.8.1 lacks the commit: known limit) |
| OSV (`advisory`, `e2e`) | `GO-2021-0113`, `GO-2022-0969`, `stdlib`, Debian:12 `chromium` (pagination), the 8 newest CSV ids | record parsing, affected ranges, no `next_page_token` for Go, pagination followed to the end and cut at 5 rounds, a withdrawn record, the CSV-to-record delay for ids older than 30 minutes |
| GitHub tags (`platform/github`) | `actions/labeler`, `git/git` | tags, lightweight and annotated tag commits |
| RubyGems (`rubygems`) | `rack`, `nokogiri` | versions, `prerelease`, `platform`, not-found |
| Packagist (`packagist`) | `monolog/monolog` | `p2` versions, `v` prefix handling, not-found |
| NuGet (`nuget`) | `Newtonsoft.Json` | service-index discovery, flat-container versions, not-found |
| Maven Central (`maven`) | `org.apache.commons:commons-lang3` | `maven-metadata.xml`, not-found |
| Terraform registry (`terraform`) | module `terraform-aws-modules/vpc/aws`, provider `hashicorp/random` | both response shapes, not-found |
| Helm (`helmrepo`) | `kubernetes.github.io/ingress-nginx` | `index.yaml` entries, not-found |

Assertions are on the schema and on immutable facts (an old pinned version exists, a flag is read),
never on a moving value such as the latest version.

## What they do not cover

- The Helm `deprecated` flag (common only in `charts.helm.sh/stable`, a 10 MB index) and OCI chart
  registries.
- NuGet's listed/unlisted state, Packagist's `abandoned`, Terraform's deprecation warnings: ripple
  does not read them.
- Maven repositories other than Central, private registries, and registries behind authentication.
- Anything that needs a token: GitHub's authenticated API, private registries. Those stay covered by
  fakes and, where a sandbox exists, the `sandbox` tests.
