# Source excerpts that fix the wire formats

Quoted from the public repositories (AGPL-3.0; short quotations for interoperability, fetched 2026-11-23).
Commits: `scm-review-plugin` d833d89b9dd1e379122cc9dac2b48ca6e04fefd9, `scm-webhook-plugin`
55d3605e9efa73af2c827ecbf316e6b4b2ebb5fe, `scm-manager` develop b14c0fb2f16eed8f6716e0377fe2007676b68fc1,
`renovate` main (lib/modules/platform/scm-manager/).

Section ids below are cited from tests as `source-excerpts.md#<id>`.

## pr-create
scm-review-plugin `.../pullrequest/api/PullRequestRootResource.java`:

    @POST @Path("{namespace}/{name}") @Consumes(PullRequestMediaType.PULL_REQUEST)
    @ApiResponse(responseCode = "201", description = "create success")
    @ApiResponse(responseCode = "409", description = "conflict, a similar pull request for these branches already exists")
    ... return Response.created(location).build();     // 201, empty body, Location: <.../pull-requests/ns/name/{id}>

`PullRequestDto` fields: `id, author, source, target, title, description, creationDate, status, reviewer, labels,
tasks, shouldDeleteSourceBranch, initialTasks, ...`; `@NotBlank source, target, title`.
`PullRequestStatus`: `DRAFT(false), OPEN(false), MERGED(true), REJECTED(true)` (the boolean is "closed").
Renovate (`scm-manager-helper.ts`): POST `pull-requests/<ns>/<name>` with `Content-Type` and `Accept`
`application/vnd.scmm-pullRequest+json;v=2`, body `{source, target, title, description, status: 'DRAFT'|'OPEN'}`, then GET the
`Location` header. Content types: `application/vnd.scmm-pullRequestCollection+json;v=2`,
`application/vnd.scmm-repositoryCollection+json;v=2`, `application/vnd.scmm-repository+json;v=2`,
`application/vnd.scmm-gitDefaultBranch+json;v=2`.

## pr-list
Same file: `GET {namespace}/{name}` produces `PullRequestMediaType.PULL_REQUEST_COLLECTION`;
`@QueryParam("status") @DefaultValue("IN_PROGRESS") PullRequestSelector`, `@DefaultValue("0") page`,
`@DefaultValue("10") pageSize`, `source`, `target`. `PullRequestSelector`: `ALL, IN_PROGRESS, OPEN (deprecated), MINE,
REVIEWER, MERGED, REJECTED`; `IN_PROGRESS` is `!closed`, so DRAFT and OPEN. The collection is HAL:
`{"page":0,"pageTotal":N,"_embedded":{"pullRequests":[{"id":"7","source":"...","target":"...","status":"OPEN",...}]}}`
(Renovate `schema.ts`: `PagedPullRequest`, `id` a string). Pages are zero-based
(`zeroBasedNumberedPaging`); an out of range page answers an empty collection.

## repo-list
`RepositoryCollectionResource.getAll`: `GET repositories` with `page` (default 0), `pageSize`, `sortBy`, `desc`,
`showArchived` (default true), `q`. Renovate `schema.ts` `Repo`: `namespace, name, type ('git'|'svn'|'hg'), archived`;
`PagedRepo`: `{page, pageTotal, _embedded: {repositories: [...]}}`. Renovate `getRepos` keeps `type === 'git'`.

## default-branch
`GitRepositoryConfigResource`: `GET v2/config/git/{namespace}/{name}/default-branch` produces
`GitVndMediaType.GIT_REPOSITORY_DEFAULT_BRANCH`; Renovate `DefaultBranch = {defaultBranch: string}`.

## auth
`BearerWebTokenGenerator`: `if (HttpUtil.AUTHORIZATION_SCHEME_BEARER.equalsIgnoreCase(scheme)) token = BearerToken.create(...)`.
`CookieBearerWebTokenGenerator`: reads cookie `HttpUtil.COOKIE_BEARER_AUTHENTICATION` (`X-Bearer-Token`, per
core-profile-api-keys.md). Renovate `utils.ts`: "SCM-Manager ignores the username for API keys, `x-access-token` is our
own choice and not documented by SCM-Manager".

## git-url
`HttpScmProtocol.getUrl`: `String.format("repo/%s/%s", HttpUtil.encode(namespace), HttpUtil.encode(name))` resolved against the
base path (the `/scm` context): the clone URL is `<base>/repo/<namespace>/<name>`, no `.git`.
`HttpProtocolServlet.PATH = "/repo"`.

## pr-web-url
scm-review-plugin `src/main/js/PullRequestList.tsx`:
`/repo/${repository.namespace}/${repository.name}/pull-request/${pullRequest.id}/comments/` (UI route, under the base URL).

## webhook-send
scm-webhook-plugin `SimpleWebHookExecutor`: with "every commit" off, one request per push to the URL built from
`${repository.*}` (and `first`/`last` changesets); `if (webHook.isSendCommitData()) execute(webHook, url, new Changesets(changesets))
else execute(webHook, url, null)`. `HttpMethod.auto`: POST when there is data, else GET.
`Changesets`: `@XmlElement(name = "changeset") changesetList` (JSON key `changeset`, see
webhook-plugin-configuration.md "Webhook payload"). With "every commit" on: one request per changeset, body the single
changeset. `SimpleWebHook.headers: List<WebhookHeader>`; `WebhookHeader {key, value, concealed}` ("Encrypt the header
value on storage", plugins.json `config.form.webhooks.configuration.headers`). `WebhookRequest.execute` logs the status and
never retries (`LOG.warn("webhook {} failed with statusCode {}")`).
`createBaseEnvironment`: `env.put("repository", new ImmutableEncodedRepository(repository))`: values are URL-encoded.

## no-automerge
`grep -ril 'automerge\|auto.merge\|merge when'` over scm-review-plugin `src` and `docs`: no hit. Merge strategies
(`MERGE_COMMIT, REBASE, FAST_FORWARD_IF_POSSIBLE, FAST_FORWARD_ONLY, SQUASH`) apply to an explicit merge by a user with the
`mergePullRequest` privilege.

## modifications
scm-manager `scm-webapp/.../api/v2/resources/ModificationsRootResource.java` (mounted by `RepositoryResource` at
`@Path("modifications/")`): `GET v2/repositories/{namespace}/{name}/modifications/{revision}`, `@Produces(VndMediaType.MODIFICATIONS)`
(`application/vnd.scmm-modifications+json;v=2`); 404 when the repository has no such changeset. `ModificationsDto`:
`revision` (string), `added`, `modified`, `removed` (each a list of path strings), `renamed` (list of `{oldPath, newPath}`).
The documents do not say which parent a merge commit is compared with (unverified; ripple treats the list as the
changeset's files and falls back to a re-scan when it cannot be had).
