<!-- Source: https://confluence.atlassian.com/bitbucketserver/event-payload-938025882.html (Bitbucket Data Center 10.5): the whole page: headers, project, repository and pull request events
     Fetched 2026-11-23 as the page's rendered text (navigation removed). Atlassian's text, vendored for reference. -->

HTTP headers
All event payload requests may have the following HTTP headers:
 | HTTP header | Description
 | 
X-Request-Id | A unique UUID for each webhook request
 | 
X-Event-Key | 
The event that kicked off this webhook. For example, a repository push will have repo:refs_changed.
 | 
X-Hub-Signature | See Webhook secrets
 | Authorization | 
The Base64 encoded credentials that authenticate the webhook request.
This header will only be present if the webhook has basic authentication configured.
Project events
You can create project-level webhooks for the following events and all the events that occur in a repository.
Modified
A user updates the Name of a project. This payload, with an event key of project:modified , provides the following fields:
 | Parameter | Description
 | 
old | 
The details of the old version of the project.
 | 
new | The defaults of the current version of the project.
 | 
actor | 
The user who made the update.
 proj:modified payload
{
 "eventKey": "project:modified",
 "date": "2023-01-19T15:44:47+1100",
 "actor": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "active": true,
 "displayName": "Administrator",
 "id": 2,
 "slug": "admin",
 "type": "NORMAL"
 },
 "old": {
 "key": "PROJECT_1",
 "id": 7,
 "name": "Project 1",
 "public": false,
 "type": "NORMAL"
 },
 "new": {
 "key": "PROJECT_2",
 "id": 7,
 "name": "Project 2",
 "public": false,
 "type": "NORMAL"
 }
}
Repository events
You can create webhooks for the following events that occur in a repository.
Push
A user pushes one or more commits to a repository. This payload, with an event key of repo:refs_changed, provides the following fields:
 | Parameter | Description
 | 
actor | The user who pushed the commits. 
 | 
repository | The repository with the commits.
 | 
changes | The details of the push.
 | 
commits | 
The details of the commit pushed.
By default, the maximum number of commits included is 5. If the number of commits pushed is greater than the limit set, the list will include only the most recent commits.
 | 
toCommit | The details of the new head commit.
 repo:refs_changed payload
{
 "eventKey": "repo:refs_changed",
 "date": "2023-01-13T22:26:25+1100",
 "actor": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "active": true,
 "displayName": "Administrator",
 "id": 2,
 "slug": "admin",
 "type": "NORMAL"
 },
 "repository": {
 "slug": "rep_1",
 "id": 1,
 "name": "rep_1",
 "hierarchyId": "af05451fc6eb4bf4e0bd",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "PROJECT_1",
 "id": 1,
 "name": "Project 1",
 "description": "PROJECT_1",
 "public": false,
 "type": "NORMAL"
 },
 "public": false,
 "archived": false
 },
 "changes": [
 {
 "ref": {
 "id": "refs/heads/master",
 "displayId": "master",
 "type": "BRANCH"
 },
 "refId": "refs/heads/master",
 "fromHash": "197a3e0d2f9a2b3ed1c4fe5923d5dd701bee9fdd",
 "toHash": "a00945762949b7b787ecabc388c0e20b1b85f0b4",
 "type": "UPDATE"
 }
 ],
 "commits": [
 {
 "id": "a00945762949b7b787ecabc388c0e20b1b85f0b4",
 "displayId": "a0094576294",
 "author": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "authorTimestamp": 1673403328000,
 "committer": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "committerTimestamp": 1673403328000,
 "message": "My commit message",
 "parents": [
 {
 "id": "197a3e0d2f9a2b3ed1c4fe5923d5dd701bee9fdd",
 "displayId": "197a3e0d2f9"
 }
 ]
 }
 ],
 "toCommit": {
 "id": "a00945762949b7b787ecabc388c0e20b1b85f0b4",
 "displayId": "a0094576294",
 "author": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "authorTimestamp": 1673403328000,
 "committer": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "committerTimestamp": 1673403328000,
 "message": "My commit message",
 "parents": [
 {
 "id": "197a3e0d2f9a2b3ed1c4fe5923d5dd701bee9fdd",
 "displayId": "197a3e0d2f9",
 "author": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "authorTimestamp": 1673403292000,
 "committer": {
 "name": "Administrator",
 "emailAddress": "admin@example.com"
 },
 "committerTimestamp": 1673403292000,
 "message": "My commit message",
 "parents": [
 {
 "id": "f870ce6bf6fe633e1a2bbe655970bde25535669f",
 "displayId": "f870ce6bf6f"
 }
 ]
 }
 ]
 }
} 
Modified
A user updates the Name of a repository. This payload, with an event key of repo:modified , provides the following fields:
 | Parameter | Description
 | 
old | 
The details of the old version of the repository.
 | 
new | The defaults of the current version of the repository.
 | 
actor | 
The user who made the update.
 repo:modified payload
{ 
 "eventKey":"repo:modified",
 "date":"2017-09-19T09:51:20+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "old":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 },
 "new":{ 
 "slug":"repository2",
 "id":84,
 "name":"repository2",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
}
Fork
A user forks a repository. This payload, with an event key of repo:fork, provides the following fields:
 | Parameter | Description
 | 
actor | The user who forks the repository. This user is also the owner of the fork.
 | 
repository | The new repository.
 | repository.origin | The original repository that was forked.
 repo:forked payload
{ 
 "eventKey":"repo:forked",
 "date":"2017-09-19T09:48:26+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "repository":{ 
 "slug":"repository2",
 "id":86,
 "name":"repository2",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "origin":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 },
 "project":{ 
 "key":"~ADMIN",
 "id":22,
 "name":"Administrator",
 "type":"PERSONAL",
 "owner":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 }
 },
 "public":false
 }
}
Commit comment created
A user comments on a commit in a repository. This payload, with an event key of repo:comment:added, provides the following fields:
 | Parameter | Description
 | 
actor | The user who comments on the commit.
 | 
comment | The comment created.
 | 
repository | The repository with the commit. 
 | 
commit | The hash.
 repo:comment:added payload
{ 
 "eventKey":"repo:comment:added",
 "date":"2017-09-19T09:53:06+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "comment":{ 
 "properties":{ 
 "repositoryId":84
 },
 "id":42,
 "version":0,
 "text":"This is a great line of code!",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505778786337,
 "updatedDate":1505778786337,
 "comments":[ 
 ],
 "tasks":[ 
 ],
 "permittedOperations":{ 
 "editable":true,
 "deletable":true
 }
 },
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 },
 "commit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc"
}
Commit edit on comment
A user edits a comment on a commit in a repository. This payload, with an event key of repo:comment:edited, provides the following fields:
 | Parameter | Description
 | 
actor | The user who edits the commit.
 | 
comment | The comment edited.
 | previousComment | The text of the comment prior to editing.
 | 
repository | The repository with the commit. 
 | 
commit | The hash of the commit.
 repo:comment:edited payload
{ 
 "eventKey":"repo:comment:edited",
 "date":"2017-09-19T09:55:03+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "comment":{ 
 "properties":{ 
 "repositoryId":84
 },
 "id":42,
 "version":1,
 "text":"This is a okay line of code!",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505778786337,
 "updatedDate":1505778903525,
 "comments":[ 
 ],
 "tasks":[ 
 ],
 "permittedOperations":{ 
 "editable":true,
 "deletable":true
 }
 },
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 },
 "commit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
 "previousComment":"This is a great line of code!"
}
Comment deleted on commit
A user deletes a comment on a commit in a repository. This payload, with an event key of repo:comment:deleted, provides the following fields:
 | Parameter | Description
 | 
actor | The user who deletes the commit.
 | 
comment | The comment deleted.
 | 
repository | The repository with the commit. 
 | 
commit | The hash of the commit.
 repo:comment:deleted payload
{ 
 "eventKey":"repo:comment:deleted",
 "date":"2017-09-19T09:56:29+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "comment":{ 
 "id":42,
 "version":1,
 "text":"This is a okay line of code!",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505778786337,
 "updatedDate":1505778903525,
 "comments":[ 
 ],
 "tasks":[ 
 ]
 },
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 },
 "commit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc"
}
Mirror synchronized
A mirror has finished synchronizing this repository. This payload, with an event key of mirror:repo_synchronized, provides the following fields:
 | Parameter | Description
 | 
mirrorServer | The mirror which synchronized the changes. This JSON object contains both the name and the id of the mirrorServer which synchronized the changes.
 | 
syncType | 
The sync type the mirror used to synchronize the changes which are announced by this webhook.
This value can be snapshot or incremental for mirrors 6.7 and higher. It defaults to smartMirror for mirrors before version 6.7.
 | 
repository | The repository. 
 | repository.links | 
This JSON object contains the HTTP and SSH clone URLs of the primary server as well as the mirror that synchronized these changes. It also contains a link to view this repository in Bitbucket.
 | 
changes | The ref changes for this push.
 | refLimitExceeded | If this value is true, the list of changes will be empty because it exceeded the limit of refs that can be included.
 mirror:repo_synchronized
{
 "eventKey": "mirror:repo_synchronized",
 "date": "2019-07-11T16:18:20+1000",
 "mirrorServer": {
 "id": "B88H-IR7J-5PV0-VCNS",
 "name": "Mirror Name"
 },
 "syncType": "INCREMENTAL",
 "refLimitExceeded": false,
 "repository": {
 "slug": "testrepo",
 "id": 1,
 "name": "testrepo",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "TP",
 "id": 1,
 "name": "testp",
 "public": false,
 "type": "NORMAL"
 },
 "public": false,
"links": {
"clone": [
{
"href": "ssh://git@example.com:7997/project/repository.git",
"name": "ssh"
},
{
"href": "https://example.com/scm/project/repository.git",
"name": "http"
}
],
"self": [
{
"href": "https://example.com/projects/project/repos/repo/browse"
}
]
}
 },
 "changes": [
 {
 "ref": {
 "id": "refs/heads/master",
 "displayId": "master",
 "type": "BRANCH"
 },
 "refId": "refs/heads/master",
 "fromHash": "b5616b9",
 "toHash": "d055eca",
 "type": "UPDATE"
 }
 ]
}
Secret detected
A user pushes one or more commits that contain a secret to a repository. This payload, with an event key of repo:secret_detected, provides the following fields:
 | Parameter | Description
 | 
actor | The user who pushed the commits.
 | 
secretLocations | The details of the commits that contain a secret.
 repo:secret_detected payload
{
 "eventKey": "repo:secret_detected",
 "date": "2023-01-19T15:36:34+1100",
 "actor": {
 "id": 2,
 "username": "admin",
 "slug": "admin",
 "locale": null,
 "deletedDate": null,
 "timeZone": null,
 "active": false,
 "emailAddress": null,
 "crowdBacked": false,
 "backingCrowdUser": null,
 "name": "admin",
 "type": "NORMAL",
 "displayName": "admin"
 },
 "secretLocations": [
 {
 "commitId": "020629f5db6ab5e93db8ae3bacb8686d534e53a3",
 "path": "secret.txt",
 "line": 1,
 "repository": {
 "slug": "rep_1",
 "id": 6,
 "name": "rep_1",
 "hierarchyId": "53d427c717c35d3035e8",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "PROJECT_1",
 "id": 7,
 "name": "Project 1",
 "public": false,
 "type": "NORMAL",
 },
 "public": false,
 "archived": false,
 },
 "ruleName": "Bitbucket DC HTTP access token"
 }
 ]
} 
Pull request events
You can create webhooks for the following events that occur on a pull request.
Opened
 | Parameter | Description
 | 
actor | The user who created the pull request.
 | 
pullrequest | Details of the pull request created.
 pr:opened payload
{ 
 "eventKey": "pr:opened",
 "date": "2017-09-19T09:58:11+1000",
 "actor": { 
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 },
 "pullRequest": { 
 "id": 1,
 "version": 0,
 "title": "a new file added",
 "state": "OPEN",
 "open": true,
 "closed": false,
 "draft": false,
 "createdDate": 1505779091796,
 "updatedDate": 1505779091796,
 "fromRef": { 
 "id": "refs/heads/a-branch",
 "displayId": "a-branch",
 "latestCommit": "ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "repository": { 
 "slug": "repository",
 "id": 84,
 "name": "repository",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": { 
 "key": "PROJ",
 "id": 84,
 "name": "project",
 "public": false,
 "type": "NORMAL"
 },
 "public": false
 }
 },
 "toRef": { 
 "id": "refs/heads/master",
 "displayId": "master",
 "latestCommit": "178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
 "repository": { 
 "slug": "repository",
 "id": 84,
 "name": "repository",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": { 
 "key": "PROJ",
 "id": 84,
 "name": "project",
 "public": false,
 "type": "NORMAL"
 },
 "public": false
 }
 },
 "locked": false,
 "author": { 
 "user": { 
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 },
 "role": "AUTHOR",
 "approved": false,
 "status": "UNAPPROVED"
 },
 "reviewers": [ 
 ],
 "participants": [ 
 ],
 "links": { 
 "self": [ 
 null
 ]
 }
 }
}
Source branch updated
 | Parameter | Description
 | 
actor | The user who created the pull request.
 | 
pullrequest | Details of the pull request created.
 | previousFromHash | Previous from-ref hash
 pr:from_ref_updated payload
{
 "eventKey": "pr:from_ref_updated",
 "date": "2020-02-20T14:49:41+1100",
 "actor": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/users/admin"
 }
 ]
 }
 },
 "pullRequest": {
 "id": 2,
 "version": 16,
 "title": "Webhook",
 "state": "OPEN",
 "open": true,
 "closed": false,
 "draft": false,
 "createdDate": 1582065825700,
 "updatedDate": 1582170581372,
 "fromRef": {
 "id": "refs/heads/pr-webhook",
 "displayId": "pr-webhook",
 "latestCommit": "aab847db240ccae221f8036605b00f777eba95d2",
 "repository": {
 "slug": "dvcs",
 "id": 33,
 "name": "dvcs",
 "hierarchyId": "09992c6ad9e001f01120",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "GIT",
 "id": 62,
 "name": "Bitbucket",
 "public": false,
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT"
 }
 ]
 }
 },
 "public": false,
 "links": {
 "clone": [
 {
 "href": "ssh://git@localhost:7999/git/dvcs.git",
 "name": "ssh"
 },
 {
 "href": "http://localhost:7990/bitbucket/scm/git/dvcs.git",
 "name": "http"
 }
 ],
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/browse"
 }
 ]
 }
 }
 },
 "toRef": {
 "id": "refs/heads/master",
 "displayId": "master",
 "latestCommit": "86448735f9dee9e1fb3d3e5cd9fbc8eb9d8400f4",
 "repository": {
 "slug": "dvcs",
 "id": 33,
 "name": "dvcs",
 "hierarchyId": "09992c6ad9e001f01120",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "GIT",
 "id": 62,
 "name": "Bitbucket",
 "public": false,
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT"
 }
 ]
 }
 },
 "public": false,
 "links": {
 "clone": [
 {
 "href": "ssh://git@localhost:7999/git/dvcs.git",
 "name": "ssh"
 },
 {
 "href": "http://localhost:7990/bitbucket/scm/git/dvcs.git",
 "name": "http"
 }
 ],
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/browse"
 }
 ]
 }
 }
 },
 "locked": false,
 "author": {
 "user": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/users/admin"
 }
 ]
 }
 },
 "role": "AUTHOR",
 "approved": false,
 "status": "UNAPPROVED"
 },
 "reviewers": [],
 "participants": [],
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/pull-requests/2"
 }
 ]
 }
 },
 "previousFromHash": "99f3ea32043ba3ecaa28de6046b420de70257d80"
}
Target branch updated
The Target branch updated webhook isn't available due to the following bug: 
 BSERV-14136
 -
 Getting issue details...
 STATUS
 . Share your feedback in this Jira issue or start watching it to follow the updates.
 | Parameter | Description
 | 
actor | The user who created the pull request.
 | 
pullrequest | Details of the pull request created.
 | previousToHash | Previous to-ref hash
 pr:to_ref_updated payload
{
 "eventKey": "pr:from_ref_updated",
 "date": "2020-02-20T14:49:41+1100",
 "actor": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/users/admin"
 }
 ]
 }
 },
 "pullRequest": {
 "id": 2,
 "version": 16,
 "title": "Webhook",
 "state": "OPEN",
 "open": true,
 "closed": false,
 "draft": false,
 "createdDate": 1582065825700,
 "updatedDate": 1582170581372,
 "fromRef": {
 "id": "refs/heads/pr-webhook",
 "displayId": "pr-webhook",
 "latestCommit": "aab847db240ccae221f8036605b00f777eba95d2",
 "repository": {
 "slug": "dvcs",
 "id": 33,
 "name": "dvcs",
 "hierarchyId": "09992c6ad9e001f01120",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "GIT",
 "id": 62,
 "name": "Bitbucket",
 "public": false,
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT"
 }
 ]
 }
 },
 "public": false,
 "links": {
 "clone": [
 {
 "href": "ssh://git@localhost:7999/git/dvcs.git",
 "name": "ssh"
 },
 {
 "href": "http://localhost:7990/bitbucket/scm/git/dvcs.git",
 "name": "http"
 }
 ],
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/browse"
 }
 ]
 }
 }
 },
 "toRef": {
 "id": "refs/heads/master",
 "displayId": "master",
 "latestCommit": "86448735f9dee9e1fb3d3e5cd9fbc8eb9d8400f4",
 "repository": {
 "slug": "dvcs",
 "id": 33,
 "name": "dvcs",
 "hierarchyId": "09992c6ad9e001f01120",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "GIT",
 "id": 62,
 "name": "Bitbucket",
 "public": false,
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT"
 }
 ]
 }
 },
 "public": false,
 "links": {
 "clone": [
 {
 "href": "ssh://git@localhost:7999/git/dvcs.git",
 "name": "ssh"
 },
 {
 "href": "http://localhost:7990/bitbucket/scm/git/dvcs.git",
 "name": "http"
 }
 ],
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/browse"
 }
 ]
 }
 }
 },
 "locked": false,
 "author": {
 "user": {
 "name": "admin",
 "emailAddress": "admin@example.com",
 "id": 1,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL",
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/users/admin"
 }
 ]
 }
 },
 "role": "AUTHOR",
 "approved": false,
 "status": "UNAPPROVED"
 },
 "reviewers": [],
 "participants": [],
 "links": {
 "self": [
 {
 "href": "http://localhost:7990/bitbucket/projects/GIT/repos/dvcs/pull-requests/2"
 }
 ]
 }
 },
 "previousToHash": "5a68d32291b91cfbc18f99731ea93cce25601942"
}
Modified
 | Parameter | Description
 | 
actor | The user who created the pull request.
 | 
pullrequest | Details of the pull request created.
 | previousTitle | Previous title of the pull request, may not have changed
 | previousDescription | Previous description of the pull request, may not have changed
 | previousTarget | Previous target of the pull request, may not have changed
 | previousDraft | 
Previous draft status of the pull request, may not have changed
Learn more about draft pull requests
 pr:modified payload
{
 "eventKey": "pr:modified",
 "date": "2018-04-24T10:15:30+1000",
 "actor": {
 "name": "Administrator",
 "emailAddress": "example@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "pathompson",
 "type": "NORMAL"
 },
 "pullRequest": {
 "id": 1,
 "version": 1,
 "title": "A new title",
 "description": "A new description",
 "state": "OPEN",
 "open": true,
 "closed": false,
 "draft": false,
 "createdDate": 1524528879329,
 "updatedDate": 1524528930110,
 "fromRef": {
 "id": "refs/heads/new-branch",
 "displayId": "new-branch",
 "latestCommit": "5a705e60111a4213da46839d9cbf4fc43639b771",
 "repository": {
 "slug": "example",
 "id": 12087,
 "name": "example",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "~ADMIN",
 "id": 8504,
 "name": "Administrator",
 "type": "PERSONAL",
 "owner": {
 "name": "Administrator",
 "emailAddress": "example@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 }
 },
 "public": false
 }
 },
 "toRef": {
 "id": "refs/heads/master",
 "displayId": "master",
 "latestCommit": "860c4eb4ed0f969b47144234ba13c31c498cca69",
 "repository": {
 "slug": "example",
 "id": 12087,
 "name": "example",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "~ADMIN",
 "id": 8504,
 "name": "Administrator",
 "type": "PERSONAL",
 "owner": {
 "name": "Administrator",
 "emailAddress": "example@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 }
 },
 "public": false
 }
 },
 "locked": false,
 "author": {
 "user": {
 "name": "Administrator",
 "emailAddress": "example@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 },
 "role": "AUTHOR",
 "approved": false,
 "status": "UNAPPROVED"
 },
 "reviewers": [
 {
 "user": {
 "name": "User",
 "emailAddress": "user@atlassian.com",
 "id": 36303,
 "displayName": "User",
 "active": true,
 "slug": "user",
 "type": "NORMAL"
 },
 "role": "REVIEWER",
 "approved": false,
 "status": "UNAPPROVED"
 }
 ],
 "participants": [
 ]
 },
 "previousTitle": "A cool PR",
 "previousDescription": "A neat description",
 "previousDraft": false,
 "previousTarget": {
 "id": "refs\/heads\/master",
 "displayId": "master",
 "type": "BRANCH",
 "latestCommit": "860c4eb4ed0f969b47144234ba13c31c498cca69",
 "latestChangeset": "860c4eb4ed0f969b47144234ba13c31c498cca69"
 }
}
Reviewers Updated
 | Parameter | Description
 | 
actor | The user who created the pull request.
 | 
pullrequest | Details of the pull request created.
 | removedReviewers | Users that are no longer reviewers
 | addedReviewers | Users that have been added as reviewers
 pr:reviewer:updated payload
{
 "eventKey": "pr:reviewer:updated",
 "date": "2018-04-24T10:20:07+1000",
 "actor": {
 "name": "Administrator",
 "emailAddress": "admin@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 },
 "pullRequest": {
 "id": 1,
 "version": 2,
 "title": "A title",
 "description": "A description",
 "state": "OPEN",
 "open": true,
 "closed": false,
 "draft": false,
 "createdDate": 1524528879329,
 "updatedDate": 1524529207598,
 "fromRef": {
 "id": "refs/heads/new-branch",
 "displayId": "new-branch",
 "latestCommit": "5a705e60111a4213da46839d9cbf4fc43639b771",
 "repository": {
 "slug": "example",
 "id": 12087,
 "name": "example",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "~ADMIN",
 "id": 8504,
 "name": "Administrator",
 "type": "PERSONAL",
 "owner": {
 "name": "admin",
 "emailAddress": "example@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 }
 },
 "public": false
 }
 },
 "toRef": {
 "id": "refs/heads/master",
 "displayId": "master",
 "latestCommit": "860c4eb4ed0f969b47144234ba13c31c498cca69",
 "repository": {
 "slug": "example",
 "id": 12087,
 "name": "example",
 "scmId": "git",
 "state": "AVAILABLE",
 "statusMessage": "Available",
 "forkable": true,
 "project": {
 "key": "~ADMIN",
 "id": 8504,
 "name": "Administrator",
 "type": "PERSONAL",
 "owner": {
 "name": "Administrator",
 "emailAddress": "admin@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 }
 },
 "public": false
 }
 },
 "locked": false,
 "author": {
 "user": {
 "name": "Administrator",
 "emailAddress": "admin@atlassian.com",
 "id": 110653,
 "displayName": "Administrator",
 "active": true,
 "slug": "admin",
 "type": "NORMAL"
 },
 "role": "AUTHOR",
 "approved": false,
 "status": "UNAPPROVED"
 },
 "reviewers": [
 {
 "user": {
 "name": "pathompson_admin",
 "emailAddress": "pathompson@atlassian.com",
 "id": 129659,
 "displayName": "Paul Thompson Admin",
 "active": true,
 "slug": "pathompson_admin",
 "type": "NORMAL"
 },
 "role": "REVIEWER",
 "approved": false,
 "status": "UNAPPROVED"
 }
 ],
 "participants": [
 ]
 },
 "addedReviewers": [
 {
 "name": "new user",
 "emailAddress": "user2@atlassian.com",
 "id": 129659,
 "displayName": "New User",
 "active": true,
 "slug": "new_user",
 "type": "NORMAL"
 }
 ],
 "removedReviewers": [
 {
 "name": "user",
 "emailAddress": "user@atlassian.com",
 "id": 36303,
 "displayName": "User",
 "active": true,
 "slug": "user",
 "type": "NORMAL"
 }
 ]
}
Approved
A user approves a pull request for a repository. This payload, with an event key of pr:reviewer:approved, provides the following fields:
 | Parameter | Description
 | 
actor | The user which made the approval.
 | 
pullrequest | Details of the pull request approved.
 | 
participant | Details of the PR participant status of the user making the change
 | 
previousStatus | The state of the approval before this change
 pr:reviewer:approved
{ 
 "eventKey":"pr:reviewer:approved",
 "date":"2017-09-19T10:10:01+1000",
 "actor":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":1,
 "version":1,
 "title":"a new file added",
 "description":"A new description, added a user",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505779091796,
 "updatedDate":1505779257496,
 "fromRef":{ 
 "id":"refs/heads/a-branch",
 "displayId":"a-branch",
 "latestCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":true,
 "status":"APPROVED"
 }
 ],
 "participants":[ 
 ],
 "links":{ 
 "self":[ 
 null
 ]
 }
 },
 "participant":{ 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":true,
 "status":"APPROVED"
 },
 "previousStatus":"UNAPPROVED"
}
Unapproved
A user removes an approval from a pull request for a repository. This payload, with an event key of pr:reviewer:unapproved, provides the following fields:
 | Parameter | Description
 | 
actor | The user which removed the approval.
 | 
pullrequest | Details of the pull request unapproved.
 | 
participant | Details of the PR participant status of the user making the change
 | 
previousStatus | The state of the approval before this change
 pr:reveiwer:unapproved payload
{ 
 "eventKey":"pr:reviewer:unapproved",
 "date":"2017-09-19T10:13:43+1000",
 "actor":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":1,
 "version":1,
 "title":"a new file added",
 "description":"A new description, added a user",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505779091796,
 "updatedDate":1505779257496,
 "fromRef":{ 
 "id":"refs/heads/a-branch",
 "displayId":"a-branch",
 "latestCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":false,
 "status":"UNAPPROVED"
 }
 ],
 "participants":[ 
 ]
 },
 "participant":{ 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "previousStatus":"APPROVED"
}
Changes requested
A user marks a pull request as changes requested. This payload, with an event key of pr:reviewer:needs_work, provides the following fields:
 | Parameter | Description
 | 
actor | The user who marked the PR as "Changes requested".
 | 
pullrequest | Details of the pull request marked "Changes requested".
 | 
participant | Details of the PR participant status of the user making the change.
 | 
previousStatus | The state of the approval before this change.
 pr:reviewer:needs_work payload
{ 
 "eventKey":"pr:reviewer:changes_requested",
 "date":"2017-09-19T10:14:47+1000",
 "actor":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":1,
 "version":1,
 "title":"a new file added",
 "description":"A new description, added a user",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505779091796,
 "updatedDate":1505779257496,
 "fromRef":{ 
 "id":"refs/heads/a-branch",
 "displayId":"a-branch",
 "latestCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":false,
 "status":"NEEDS_WORK"
 }
 ],
 "participants":[ 
 ]
 },
 "participant":{ 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "lastReviewedCommit":"ef8755f06ee4b28c96a847a95cb8ec8ed6ddd1ca",
 "role":"REVIEWER",
 "approved":false,
 "status":"NEEDS_WORK"
 },
 "previousStatus":"UNAPPROVED"
}
Merged
A user merges a pull request for a repository. This payload, with an event key of pr:merged, provides the following fields:
 | Parameter | Description
 | 
actor | The user who merged the pull request.
 | 
pullrequest | Details of the pull request merged.
 | autoMerge | Indicates whether the pull request was automatically merged.
 pr:merged payload
{ 
 "eventKey":"pr:merged",
 "date":"2017-09-19T10:39:36+1000",
 "actor":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":9,
 "version":2,
 "title":"file edited online with Bitbucket",
 "state":"MERGED",
 "open":false,
 "closed":true,
 "draft": false,
 "createdDate":1505781560908,
 "updatedDate":1505781576361,
 "closedDate":1505781576361,
 "fromRef":{ 
 "id":"refs/heads/admin/file-1505781548644",
 "displayId":"admin/file-1505781548644",
 "latestCommit":"45f9690c928915a5e1c4366d5ee1985eea03f05d",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"8d2ad38c918fa6943859fca2176c89ea98b92a21",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 ],
 "participants":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "role":"PARTICIPANT",
 "approved":false,
 "status":"UNAPPROVED"
 }
 ],
 "properties":{ 
 "mergeCommit":{ 
 "displayId":"7e48f426f0a",
 "id":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c"
 }
 },
 "autoMerge" : "false"
 }
}
Declined
A user declines a pull request for a repository. This payload, with an event key of pr:declined, provides the following fields:
 | Parameter | Description
 | 
actor | The user who declined the pull request.
 | 
pullrequest | Details of the pull request declined.
 pr:declined
{ 
 "eventKey":"pr:declined",
 "date":"2017-09-19T11:14:43+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":10,
 "version":2,
 "title":"Commit message",
 "state":"DECLINED",
 "open":false,
 "closed":true,
 "draft": false,
 "createdDate":1505783668760,
 "updatedDate":1505783683969,
 "closedDate":1505783683969,
 "fromRef":{ 
 "id":"refs/heads/decline-me",
 "displayId":"decline-me",
 "latestCommit":"2d9fb6b9a46eafb1dcef7b008d1a429d45ca742c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "role":"REVIEWER",
 "approved":false,
 "status":"UNAPPROVED"
 }
 ],
 "participants":[ 
 ]
 }
}
Deleted
A user deletes a pull request for a repository. This payload, with an event key of pr:deleted, provides the following fields:
 | Parameter | Description
 | 
actor | The user who deleted the pull request.
 | 
pullrequest | Details of the pull request deleted.
 pr:deleted
{ 
 "eventKey":"pr:deleted",
 "date":"2017-09-19T11:16:17+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":10,
 "version":3,
 "title":"Commit message",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505783668760,
 "updatedDate":1505783750704,
 "fromRef":{ 
 "id":"refs/heads/decline-me",
 "displayId":"decline-me",
 "latestCommit":"2d9fb6b9a46eafb1dcef7b008d1a429d45ca742c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 { 
 "user":{ 
 "name":"user",
 "emailAddress":"user@example.com",
 "id":2,
 "displayName":"User",
 "active":true,
 "slug":"user",
 "type":"NORMAL"
 },
 "role":"REVIEWER",
 "approved":false,
 "status":"UNAPPROVED"
 }
 ],
 "participants":[ 
 ]
 }
}
Comment added
A user comments on a pull request. This payload, with an event key of pr:comment:added, provides the following fields:
 | Parameter | Description
 | 
actor | The user that created the comment.
 | 
pullRequest | The pull request comment on.
 | 
comment | The comment created.
 | 
commentParentId | Id of the parent comment if one exists.
 pr:comment:added payload
{ 
 "eventKey":"pr:comment:added",
 "date":"2017-09-19T11:21:06+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":11,
 "version":1,
 "title":"A cool PR",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505783860548,
 "updatedDate":1505783878981,
 "fromRef":{ 
 "id":"refs/heads/comment-pr",
 "displayId":"comment-pr",
 "latestCommit":"ddc19f786996396d57e17c8f6d1d05d00318ad10",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 ],
 "participants":[ 
 ]
 },
 "comment":{ 
 "properties":{ 
 "repositoryId":84
 },
 "id":62,
 "version":0,
 "text":"I am a PR comment",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505784066751,
 "updatedDate":1505784066751,
 "comments":[ 
 ],
 "tasks":[ 
 ]
 },
 "commentParentId":43
}
Comment edited
This payload, with an event key of pr:comment:edited, provides the following fields:
 | Parameter | Description
 | 
actor | The user that edited the comment.
 | 
pullRequest | The pull request where the comment exists.
 | 
comment | The comment edited.
 | 
commentParentId | Id of the parent comment if one exists.
 | previousComment | Text of the previous comment.
 pr:comment:edited payload
{ 
 "eventKey":"pr:comment:edited",
 "date":"2017-09-19T11:24:19+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":11,
 "version":1,
 "title":"A cool PR",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505783860548,
 "updatedDate":1505783878981,
 "fromRef":{ 
 "id":"refs/heads/comment-pr",
 "displayId":"comment-pr",
 "latestCommit":"ddc19f786996396d57e17c8f6d1d05d00318ad10",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 ],
 "participants":[ 
 ]
 },
 "comment":{ 
 "properties":{ 
 "repositoryId":84
 },
 "id":62,
 "version":1,
 "text":"I am a PR comment that was edited",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505784066751,
 "updatedDate":1505784259446,
 "comments":[ 
 ],
 "tasks":[ 
 ]
 },
 "commentParentId":43,
 "previousComment":"I am a PR comment"
}
Comment deleted
A user deletes a comment on a pull request. This payload, with an event key of pr:comment:deleted, provides the following fields:
 | Parameter | Description
 | 
actor | The user that deleted the comment.
 | 
pullRequest | The pull request where the comment existed.
 | 
comment | The comment deleted.
 | 
commentParentId | Id of the parent comment if one exists.
 pr:comment:deleted payload
{ 
 "eventKey":"pr:comment:deleted",
 "date":"2017-09-19T11:25:47+1000",
 "actor":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "pullRequest":{ 
 "id":11,
 "version":1,
 "title":"A cool PR",
 "state":"OPEN",
 "open":true,
 "closed":false,
 "draft": false,
 "createdDate":1505783860548,
 "updatedDate":1505783878981,
 "fromRef":{ 
 "id":"refs/heads/comment-pr",
 "displayId":"comment-pr",
 "latestCommit":"ddc19f786996396d57e17c8f6d1d05d00318ad10",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "toRef":{ 
 "id":"refs/heads/master",
 "displayId":"master",
 "latestCommit":"7e48f426f0a6e47c5b5e862c31be6ca965f82c9c",
 "repository":{ 
 "slug":"repository",
 "id":84,
 "name":"repository",
 "scmId":"git",
 "state":"AVAILABLE",
 "statusMessage":"Available",
 "forkable":true,
 "project":{ 
 "key":"PROJ",
 "id":84,
 "name":"project",
 "public":false,
 "type":"NORMAL"
 },
 "public":false
 }
 },
 "locked":false,
 "author":{ 
 "user":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "role":"AUTHOR",
 "approved":false,
 "status":"UNAPPROVED"
 },
 "reviewers":[ 
 ],
 "participants":[ 
 ]
 },
 "comment":{ 
 "id":62,
 "version":1,
 "text":"I am a PR comment that was edited",
 "author":{ 
 "name":"admin",
 "emailAddress":"admin@example.com",
 "id":1,
 "displayName":"Administrator",
 "active":true,
 "slug":"admin",
 "type":"NORMAL"
 },
 "createdDate":1505784066751,
 "updatedDate":1505784259446,
 "comments":[ 
 ],
 "tasks":[ 
 ]
 },
 "commentParentId":43
}
 Last modified on Nov 4, 2024
Was this helpful?
 Yes
 No
 It wasn't accurate
 It wasn't clear
 It wasn't relevant
 Provide feedback about this article
Related content
No related content found
 Powered by Confluence and Scroll Viewport.


