# SCM-Manager 3.11.0: what a real instance answered

Recorded 2026-11-27 from `hosts-scmmanager.yml` (`go test -tags scmreal`, image
`docker.io/scmmanager/scm-manager:3.11.0`, Review and Webhook plugins installed through the plugin API).
Every line below is a `FACT` line of that run, trimmed. Nothing here is from memory.

## Bootstrap, headless

- `SCM_WEBAPP_INITIALUSER` / `SCM_WEBAPP_INITIALPASSWORD` create the first administrator; `GET /scm/api/v2/`
  answers 200 (or 401 with no credentials) once the server is up, which is what the job waits on.
- Most resources answer 406 to `Accept: application/json`; they want their versioned type
  (`application/vnd.scmm-index+json;v=2`, `...-repository+json;v=2`, ...). The webhook plugin's
  configuration resource is the exception and wants `application/json`.
- Plugins install headlessly: `POST /api/v2/plugins/available/{name}/install` (200) for each plugin, then
  `POST` the `execute` link of `GET /api/v2/plugins/pending`. Both plugins were installed after 7 to 10 s,
  with no non-200 answer from the index meanwhile. `install?restart=true` restarts at once, so a second
  install in the same breath gets EOF: do not use it for more than one plugin.
- API key: `POST /api/v2/users/{user}/api_keys`, `Content-Type: application/vnd.scmm-apiKey+json;v=2`, body
  `{"displayName":..., "permissionRole":"WRITE"}`. 201 with a `Location`; the body is the key itself as
  `text/plain`, not JSON.
- A user is created with `POST /api/v2/users` (201) and given `repository:create` with
  `PUT /api/v2/users/{user}/permissions` (204).

## Authentication (ROADMAP 18)

An API key is accepted as `Authorization: Bearer <key>`, as the cookie `X-Bearer-Token`, and as the basic-auth
password with any user name (`ripple` tried) on both the REST API and git over HTTP. No credentials: 401.

## Repositories, branches, pull requests

- Repositories are created under the creating user's namespace whatever namespace the request asks for
  (default namespace strategy); the `Location` of the 201 names the real path.
- `?initialize=true` creates the first commit; the default branch is `main`.
- `ListRepos`, `ListRepoHeads` (the branch resource, `revision` field), `DefaultBranch`, `CreatePR` (the 201
  `Location` carries the id), `ListOpenPRs`, a 409 for a duplicate pull request, and `PushFiles` (the
  modifications resource) all work unchanged against the real instance.
- A PUT of the repository object with `archived=true` is accepted (204); with `archived=false` it is refused
  (403 `repository is archived`). Leaving the archive takes the `unarchive` link (POST, 204); `archive` is its
  counterpart.

- Branch resource, `GET /api/v2/repositories/{ns}/{name}/branches/{branch}` (`application/vnd.scmm-branch+json;v=2`),
  200: `{"name":"main","revision":"<40 hex>","defaultBranch":true,"lastCommitDate":"2026-11-28T00:23:30Z",
  "lastCommitter":{"mail":"scm@example.com","name":"SCM Administrator"},"stale":false,"_links":{...}}`. `revision`
  equals the commit git pushed; a branch name with a slash is addressed as `ripple%2Ffields`
  (`TestRealPullRequestAndBranchFields`, 2026-11-28).
- An open pull request, in the list and on its own resource, has the fields `_embedded _links author closeDate
  creationDate description emergencyMerged id ignoredMergeObstacles initialTasks labels lastModified
  lastModifiedDateForSort markedAsReviewed reviewer reviser shouldDeleteSourceBranch source sourceRevision status
  target targetRevision tasks title` (the list item adds nothing). `sourceRevision` and `targetRevision` are `null`
  while it is open, and stay `null` after a second push to the source branch; `labels` is `[]`. No field holds the
  source commit.
- Archived repository, `GET /api/v2/repositories`: listed with `archived: true` by default and with
  `showArchived=true`; absent with `showArchived=false` (`TestRealArchivedListing`). `ListArchivedRepos` reports it,
  `ListRepos` omits it.

- Modifications of a merge commit, `GET .../modifications/{id}`: `{"revision":"<merge id>","added":["go.mod"],
  "modified":[],"removed":[],"renamed":[]}` for a merge of `feature` (adds `go.mod`) into a `main` that had added
  `other.txt` since: a diff against the first parent (`TestRealMergeCommitModifications`, 2026-11-28).
- Merging, the pull request's `merge` link `.../merge/{ns}/{name}/{id}?strategy=FAST_FORWARD_IF_POSSIBLE`
  (the strategy is in the link): `POST` with `application/vnd.scmm-mergeCommand+json;v=2` and
  `{"commitMessage":"merge","overrideMessage":false}` answers 204. A guessed `vnd.scmm-mergeCommit+json;v=2`
  gave 415 "illegal media type"; no other media type was tried. The source branch of a pull request created with `shouldDeleteSourceBranch: true` was
  still there 0, 2 and 5 seconds after the merge (`TestRealMergeDeletesSourceBranch`).

## Webhook plugin

- Global configuration lives at the index link `webHookConfig`. PUT (204):
  `{"webhooks":[{"name":"SimpleWebHook","configuration":{"urlPattern":...,"method":"AUTO",
  "executeOnEveryCommit":false,"sendCommitData":true,"headers":[{"key":...,"value":...,"concealed":true}]}}]}`.
  A flat entry is stored as `{"name":null,"unknown":true}`. GET returns a concealed value as `__DUMMY__`.
- A delivery is a POST to the URL pattern with `${repository.namespace}` and `${repository.name}` expanded,
  `Content-Type: application/json`, the configured custom header, and **no signature**.
- Body: `{"changeset":[{"author","branches":["main"],"date","description","id","parents","tags":null,
  "contributors":null,"signatures":[],"properties":null}]}`. No file list.
- One push is one delivery. The initialize commit sends none. A global webhook that exists before a
  repository's first push announces that push.

## Still not observed

- How a merge request asks for the source branch to be deleted.
- A real 429: no instance behind the job throttles; the rate-limit case injects one at a proxy.
- Plugins older or newer than the ones 3.11.0 installs.
