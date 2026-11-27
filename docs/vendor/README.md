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
| `gitea/swagger-branch.json` | gitea.com/swagger.v1.json | `GET /repos/{o}/{r}/branches/{b}`, `commit.id` (head polling) |
| `forgejo/swagger-branch.json` | codeberg.org/swagger.v1.json | the same call on Forgejo |
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

## rubygems

`rubygems/rubygems-org-api.md` (the `/api/v1/versions/<gem>.json` shape), `rubygems/versioning.md` (the `~>`
operator) and `rubygems/gemfile-and-gemspec.md` (`gem "name", "version"` lines) are the rubygems/guides pages, fetched
2026-11-25 as raw markdown. `rubygems/gemfile.md` is the Bundler `Gemfile` man page as served at
https://guides.rubygems.org/gemfile/ (fetched 2026-11-26, HTML stripped to text): its "Version" section defines `~> 2.0.3`
as `>= 2.0.3, < 2.1` and `~> 2.1` as `>= 2.1, < 3.0`. The rest of the Gemfile grammar ripple reads (one-line `gem`
declarations, `source`, `git`/`path` blocks and options) is taken from these guides and from memory: unverified.

## packagist

`packagist/packagist-api.txt` is the text of https://packagist.org/apidoc (the `p2` metadata endpoint), with the HTML
tags stripped. `packagist/composer-versions.md`, `composer-schema.md` and `composer-repositories.md` are
`doc/articles/versions.md`, `doc/04-schema.md` and `doc/05-repositories.md` of github.com/composer/composer (main),
fetched 2026-11-25. Not checked against the live service.

## helm

`helm/chart_repository.md` and `helm/charts.md` are the helm-www pages (helm.sh/docs/topics/chart_repository,
helm.sh/docs/topics/charts), fetched 2026-11-25 as raw markdown from the helm-www repository and read before
`internal/helmrepo` and `manager.Helm` were written: the `index.yaml` shape (`entries`, `version`, `deprecated`) and
the `dependencies` fields of `Chart.yaml`. Not checked against a live repository, so unverified: index size and
redirect behaviour of real hosts, how `created` is filled, the full constraint grammar (only exact versions are used).

## nuget

`nuget/nuget-service-index.md`, `nuget-pkgbase.md` (package base address, the flat container),
`nuget-package-versioning.md`, `nuget-package-references.md` and `nuget-cpm.md` (central package management) are the
Microsoft Learn pages of the same names from https://learn.microsoft.com/en-us/nuget, fetched 2026-11-25. Not checked
against the live service.

## maven

`maven/repository-layout.txt` and `maven/repository-metadata.txt` are the text of
https://maven.apache.org/repositories/layout.html and https://maven.apache.org/ref/3.9.9/maven-repository-metadata/repository-metadata.html
with the HTML tags stripped; `maven/junit-maven-metadata.xml` is the `maven-metadata.xml` of `junit:junit` on Maven
Central. All fetched 2026-11-25. `maven/pom-reference.txt` is the text of https://maven.apache.org/pom.html (the
POM Reference: "Properties", "Version Order Specification"), tags stripped, fetched 2026-11-26.

## terraform

`terraform/module-registry-protocol.txt`, `terraform/provider-registry-protocol.txt`, `terraform/version-constraints.txt`
and `terraform/dependency-lock.txt` are the text of the matching pages on https://developer.hashicorp.com/terraform
(internals/module-registry-protocol, internals/provider-registry-protocol, language/expressions/version-constraints,
language/files/dependency-lock) with the HTML tags stripped, fetched 2026-11-25. The raw docs paths on GitHub returned
404, so they were not used. No real registry answer is vendored; the fake is written from the protocol text alone.

## msbuild

`msbuild/msbuild-items.md` and `msbuild/build-process-overview.md` are copies of `docs/msbuild/msbuild-items.md` and
`docs/msbuild/build-process-overview.md` from https://github.com/MicrosoftDocs/visualstudio-docs (the Microsoft Learn
source), fetched 2026-11-27. `msbuild-items.md` states that item metadata can be a child element (around line 105) and
describes `Update` (lines 391 to 560); `build-process-overview.md` states that items are processed in the order they
appear (line 91). Neither page says how `PackageReference Update=` combines with an earlier `Include` of the same id, or which of an
attribute and a child element wins: those two answers are `dotnet restore`'s, recorded by `TestRealtoolNugetItemForms`
(docs/nuget.md).
