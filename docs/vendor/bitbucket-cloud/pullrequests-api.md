<!-- vendored 2026-11-23 from https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/; text extracted from the HTML, not edited -->

The Bitbucket Cloud REST API

Developer

Documentation

Resources
News and Updates
Get Support

Sign in

Developer
Get Support

Sign in

DOCUMENTATION

Cloud

Data Center

Resources

News and Updates

Get support

Sign in

Developer

Sign in

DOCUMENTATION

Cloud

Data Center

Resources

News and Updates

Get support

Sign in

REST APIs
Authentication methods
Filter and sort API objects
Pagination
Partial responses
Schemas and Serialization
URI, UUID, and structures
Cors and hypermedia
Integrating with Bitbucket Cloud

Addon

Branch restrictions

Branching model

Commit statuses

Commits

Deployments

Downloads

GPG

Merge queues

Pipelines

Projects

Pullrequests

GETList pull requests that contain a commit
GETList default reviewers
GETGet a default reviewer
PUTAdd a user to the default reviewers
DELRemove a user from the default reviewers
GETList effective default reviewers
GETList pull requests
POSTCreate a pull request
GETList a pull request activity log
GETGet a pull request
PUTUpdate a pull request
GETList a pull request activity log
POSTApprove a pull request
DELUnapprove a pull request
GETList comments on a pull request
POSTCreate a comment on a pull request
GETGet a comment on a pull request
PUTUpdate a comment on a pull request
DELDelete a comment on a pull request
POSTResolve a comment thread
DELReopen a comment thread
GETList commits on a pull request
GETGet file conflicts for a pull request
POSTDecline a pull request
GETList changes in a pull request
GETGet the diff stat for a pull request
POSTMerge a pull request
GETGet the merge task status for a pull request
GETList pull request mergeability checks
GETGet the patch for a pull request
POSTRequest changes for a pull request
DELRemove change request for a pull request
GETList commit statuses for a pull request
GETList tasks on a pull request
POSTCreate a task on a pull request
GETGet a task on a pull request
PUTUpdate a task on a pull request
DELDelete a task on a pull request

Refs

Reports

Repositories

SSH

Snippets

Source

Users

Webhooks

Workspaces

Other operations

CloudBitbucket Cloud / Reference / REST APIs

Pullrequests
Postman Collection
OpenAPI

Pull requests are a feature that makes it easier for developers
to collaborate using Bitbucket. They provide a user-friendly web
interface for discussing proposed changes before integrating them
into the official project.

Operations
GET/repositories/{workspace}/{repo_slug}/commit/{commit}/pullrequestsGET/repositories/{workspace}/{repo_slug}/default-reviewersGET/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}PUT/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}DEL/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}GET/repositories/{workspace}/{repo_slug}/effective-default-reviewersGET/repositories/{workspace}/{repo_slug}/pullrequestsPOST/repositories/{workspace}/{repo_slug}/pullrequestsGET/repositories/{workspace}/{repo_slug}/pullrequests/activityGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/activityPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approveDEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approveGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/commentsPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/commentsGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolveDEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolveGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/commitsGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/conflictsPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/declineGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diffGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diffstatPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/mergeGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/merge/task-status/{task_id}GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/mergeability/checksGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/patchPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changesDEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changesGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/statusesGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasksPOST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasksGET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}

GET
List pull requests that contain a commit
Returns a paginated list of all pull requests as part of which this commit was reviewed. Pull Request Commit Links app must be installed first before using this API; installation automatically occurs when 'Go to pull request' is clicked from the web interface for a commit's details.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

workspace
stringRequired

repo_slug
stringRequired

commit
stringRequired

Query parametersExpand all

page
integer

pagelen
integer

Responses200OK

The paginated list of pull requests.

application/json
Paginated Pull Requests
A paginated list of pullrequests.
Show child properties202Accepted
404Not Found

GET/repositories/{workspace}/{repo_slug}/commit/{commit}/pullrequests

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/commit/{commit}/pullrequests' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {},
 "destination": {},
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
 }
 ]
}

GET
List default reviewers
Returns the repository's default reviewers.

These are the users that are automatically added as reviewers on every
new pull request that is created. To obtain the repository's default reviewers
as well as the default reviewers inherited from the project, use the
effective-default-reveiwers endpoint.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The paginated list of default reviewers

application/json
Paginated Accounts
A paginated list of accounts.
Show child properties403Forbidden

GET/repositories/{workspace}/{repo_slug}/default-reviewers

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/default-reviewers' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "type": "<string>",
 "links": {},
 "created_on": "<string>",
 "display_name": "<string>",
 "uuid": "<string>"
 }
 ]
}

GET
Get a default reviewer
Returns the specified reviewer.

This can be used to test whether a user is among the repository's
default reviewers list. A 404 indicates that that specified user is not
a default reviewer.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

target_username
stringRequired

workspace
stringRequired

Responses200OK

The specified user is a default reviewer

application/json
allOf [object, Account]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Account
An account object.
Show child properties403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9

{
 "type": "<string>",
 "links": {
 "avatar": {}
 },
 "created_on": "<string>",
 "display_name": "<string>",
 "uuid": "<string>"
}

PUT
Add a user to the default reviewers
Adds the specified user to the repository's list of default
reviewers.

This method is idempotent. Adding a user a second time has no effect.Scopes
OAuth 2.0 and Connect app scopes required:

repository:admin
Forge app and API Token scopes required:

admin:repository:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

target_username
stringRequired

workspace
stringRequired

Responses200OK

The specified user was successfully added to the default reviewers

application/json
allOf [object, Account]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Account
An account object.
Show child properties400Bad Request
403Forbidden
404Not Found

PUT/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request PUT \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9

{
 "type": "<string>",
 "links": {
 "avatar": {}
 },
 "created_on": "<string>",
 "display_name": "<string>",
 "uuid": "<string>"
}

DEL
Remove a user from the default reviewers
Removes a default reviewer from the repository.Scopes
OAuth 2.0 and Connect app scopes required:

repository:admin
Forge app and API Token scopes required:

admin:repository:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

target_username
stringRequired

workspace
stringRequired

Responses204No Content

The specified user successfully removed from the default reviewers403Forbidden
404Not Found

DEL/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/default-reviewers/{target_username}' \
 --header 'Authorization: Bearer <access_token>'

GET
List effective default reviewers
Returns the repository's effective default reviewers. This includes both default
reviewers defined at the repository level as well as those inherited from its project.

These are the users that are automatically added as reviewers on every
new pull request that is created.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The paginated list of effective default reviewers

application/json
Paginated Default Reviewer and Type
A paginated list of default reviewers with reviewer type.
Show child properties403Forbidden

GET/repositories/{workspace}/{repo_slug}/effective-default-reviewers

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/effective-default-reviewers' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23

{
 "pagelen": 20,
 "values": [
 {
 "user": {
 "display_name": "Patrick Wolf",
 "uuid": "{9565301a-a3cf-4b5d-88f4-dd6af8078d7e}"
 },
 "reviewer_type": "project",
 "type": "default_reviewer"
 },
 {
 "user": {
 "display_name": "Davis Lee",
 "uuid": "{f0e0e8e9-66c1-4b85-a784-44a9eb9ef1a6}"
 },
 "reviewer_type": "repository",
 "type": "default_reviewer"
 }
 ],
 "page": 1,
 "size": 2
}

GET
List pull requests
Returns all pull requests on the specified repository.

By default only open pull requests are returned. This can be controlled
using the 
state query parameter. To retrieve pull requests that are
in one of multiple states, repeat the 
state parameter for each
individual state.

This endpoint also supports filtering and sorting of the results. See
filtering and sorting for more details.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

workspace
stringRequired

Query parameters

state
string

Responses200OK

All pull requests on the specified repository.

application/json
Paginated Pull Requests
A paginated list of pullrequests.
Show child properties401Unauthorized
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {},
 "destination": {},
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
 }
 ]
}

POST
Create a pull request
Creates a new pull request where the destination repository is
this repository and the author is the authenticated user.

The minimum required fields to create a pull request are 
title and

source, specified by a branch name.

1
2
3
4
5
6
7
8
9
10
11
12

curl https://api.bitbucket.org/2.0/repositories/my-workspace/my-repository/pullrequests \
 -u my-username:my-password \
 --request POST \
 --header 'Content-Type: application/json' \
 --data '{
 "title": "My Title",
 "source": {
 "branch": {
 "name": "staging"
 }
 }
 }'

If the pull request's 
destination is not specified, it will default
to the 
repository.mainbranch. To open a pull request to a
different branch, say from a feature branch to a staging branch,
specify a 
destination (same format as the 
source):

1
2
3
4
5
6
7
8
9
10
11
12
13

{
 "title": "My Title",
 "source": {
 "branch": {
 "name": "my-feature-branch"
 }
 },
 "destination": {
 "branch": {
 "name": "staging"
 }
 }
}

Reviewers can be specified by adding an array of user objects as the

reviewers property.

1
2
3
4
5
6
7
8
9
10
11
12
13

{
 "title": "My Title",
 "source": {
 "branch": {
 "name": "my-feature-branch"
 }
 },
 "reviewers": [
 {
 "uuid": "{504c3b62-8120-4f0c-a7bc-87800b9d6f70}"
 }
 ]
}

Other fields:

description - a string

close_source_branch - boolean that specifies if the source branch should be closed upon merging

draft - boolean that specifies whether the pull request is a draft
Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

workspace
stringRequired

Request bodyapplication/json
The new pull request.

The request URL you POST to becomes the destination repository URL. For this reason, you must specify an explicit source repository in the request object if you want to pull from a different repository (fork).

Since not all elements are required or even mutable, you only need to include the elements you want to initialize, such as the source branch and the title.
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties
Responses201Created

The newly created pull request.

Headers

Location
string

application/json
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties400Bad Request
401Unauthorized

POST/repositories/{workspace}/{repo_slug}/pullrequests

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131
132
133
134
135
136

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}'

201Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131

{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}

GET
List a pull request activity log
Returns a paginated list of the pull request's activity log.

This handler serves both a v20 and internal endpoint. The v20 endpoint
returns reviewer comments, updates, approvals and request changes. The internal
endpoint includes those plus tasks and attachments.

Comments created on a file or a line of code have an inline property.

Comment example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98

{
 "pagelen": 20,
 "values": [
 {
 "comment": {
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695/comments/118571088"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695/_/diff#comment-118571088"
 }
 },
 "deleted": false,
 "pullrequest": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 },
 "content": {
 "raw": "inline with to a dn from lines",
 "markup": "markdown",
 "html": "<p>inline with to a dn from lines</p>",
 "type": "rendered"
 },
 "created_on": "2019-09-27T00:33:46.039178+00:00",
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "created_on": "2019-09-27T00:33:46.039178+00:00",
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "updated_on": "2019-09-27T00:33:46.055384+00:00",
 "inline": {
 "context_lines": "",
 "to": null,
 "path": "",
 "outdated": false,
 "from": 211
 },
 "type": "pullrequest_comment",
 "id": 118571088
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}

Updates include a state property of OPEN, MERGED, or DECLINED.

Update example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113

{
 "pagelen": 20,
 "values": [
 {
 "update": {
 "description": "",
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it",
 "destination": {
 "commit": {
 "type": "commit",
 "hash": "6a2c16e4a152",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/commit/6a2c16e4a152"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/commits/6a2c16e4a152"
 }
 }
 },
 "branch": {
 "name": "master"
 },
 "repository": {
 "name": "Atlaskit-MK-2",
 "type": "repository",
 "full_name": "atlassian/atlaskit-mk-2",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2"
 },
 "avatar": {
 "href": "https://bytebucket.org/ravatar/%7B%7D?ts=js"
 }
 },
 "uuid": "{}"
 }
 },
 "reason": "",
 "source": {
 "commit": {
 "type": "commit",
 "hash": "728c8bad1813",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/commit/728c8bad1813"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/commits/728c8bad1813"
 }
 }
 },
 "branch": {
 "name": "username/NONE-add-onClick-prop-for-accessibility"
 },
 "repository": {
 "name": "Atlaskit-MK-2",
 "type": "repository",
 "full_name": "atlassian/atlaskit-mk-2",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2"
 },
 "avatar": {
 "href": "https://bytebucket.org/ravatar/%7B%7D?ts=js"
 }
 },
 "uuid": "{}"
 }
 },
 "state": "OPEN",
 "author": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "date": "2019-05-10T06:48:25.305565+00:00"
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}

Approval example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54

{
 "pagelen": 20,
 "values": [
 {
 "approval": {
 "date": "2019-09-27T00:37:19.849534+00:00",
 "pullrequest": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 },
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 }
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}
Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The pull request activity log401Unauthorized
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/activity

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/activity' \
 --header 'Authorization: Bearer <access_token>'

GET
Get a pull request
Returns the specified pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The pull request object

application/json
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties401Unauthorized
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131

{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}

PUT
Update a pull request
Mutates the specified pull request.

This can be used to change the pull request's branches or description.

Only open pull requests can be mutated.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Request bodyapplication/json
The pull request that is to be updated.
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties
Responses200OK

The updated pull request

application/json
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties400Bad Request
401Unauthorized
404Not Found

PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131
132
133
134
135
136

curl --request PUT \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131

{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}

GET
List a pull request activity log
Returns a paginated list of the pull request's activity log.

This handler serves both a v20 and internal endpoint. The v20 endpoint
returns reviewer comments, updates, approvals and request changes. The internal
endpoint includes those plus tasks and attachments.

Comments created on a file or a line of code have an inline property.

Comment example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98

{
 "pagelen": 20,
 "values": [
 {
 "comment": {
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695/comments/118571088"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695/_/diff#comment-118571088"
 }
 },
 "deleted": false,
 "pullrequest": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 },
 "content": {
 "raw": "inline with to a dn from lines",
 "markup": "markdown",
 "html": "<p>inline with to a dn from lines</p>",
 "type": "rendered"
 },
 "created_on": "2019-09-27T00:33:46.039178+00:00",
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "created_on": "2019-09-27T00:33:46.039178+00:00",
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "updated_on": "2019-09-27T00:33:46.055384+00:00",
 "inline": {
 "context_lines": "",
 "to": null,
 "path": "",
 "outdated": false,
 "from": 211
 },
 "type": "pullrequest_comment",
 "id": 118571088
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}

Updates include a state property of OPEN, MERGED, or DECLINED.

Update example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113

{
 "pagelen": 20,
 "values": [
 {
 "update": {
 "description": "",
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it",
 "destination": {
 "commit": {
 "type": "commit",
 "hash": "6a2c16e4a152",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/commit/6a2c16e4a152"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/commits/6a2c16e4a152"
 }
 }
 },
 "branch": {
 "name": "master"
 },
 "repository": {
 "name": "Atlaskit-MK-2",
 "type": "repository",
 "full_name": "atlassian/atlaskit-mk-2",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2"
 },
 "avatar": {
 "href": "https://bytebucket.org/ravatar/%7B%7D?ts=js"
 }
 },
 "uuid": "{}"
 }
 },
 "reason": "",
 "source": {
 "commit": {
 "type": "commit",
 "hash": "728c8bad1813",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/commit/728c8bad1813"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/commits/728c8bad1813"
 }
 }
 },
 "branch": {
 "name": "username/NONE-add-onClick-prop-for-accessibility"
 },
 "repository": {
 "name": "Atlaskit-MK-2",
 "type": "repository",
 "full_name": "atlassian/atlaskit-mk-2",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2"
 },
 "avatar": {
 "href": "https://bytebucket.org/ravatar/%7B%7D?ts=js"
 }
 },
 "uuid": "{}"
 }
 },
 "state": "OPEN",
 "author": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 },
 "date": "2019-05-10T06:48:25.305565+00:00"
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}

Approval example:

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54

{
 "pagelen": 20,
 "values": [
 {
 "approval": {
 "date": "2019-09-27T00:37:19.849534+00:00",
 "pullrequest": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 },
 "user": {
 "display_name": "Name Lastname",
 "uuid": "{}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/%7B%7D"
 },
 "html": {
 "href": "https://bitbucket.org/%7B%7D/"
 },
 "avatar": {
 "href": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/:/128"
 }
 },
 "type": "user",
 "nickname": "Name",
 "account_id": ""
 }
 },
 "pull_request": {
 "type": "pullrequest",
 "id": 5695,
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/atlaskit-mk-2/pullrequests/5695"
 },
 "html": {
 "href": "https://bitbucket.org/atlassian/atlaskit-mk-2/pull-requests/5695"
 }
 },
 "title": "username/NONE: small change from onFocus to onClick to handle tabbing through the page and not expand the editor unless a click event triggers it"
 }
 }
 ]
}
Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The pull request activity log401Unauthorized
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/activity

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/activity' \
 --header 'Authorization: Bearer <access_token>'

POST
Approve a pull request
Approve the specified pull request as the authenticated user.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The 
participant object recording that the authenticated user approved the pull request.

application/json
allOf [object, Participant]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Participant
Object describing a user's role on resources like commits or pull requests.
Show child properties401Unauthorized
404Not Found

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approve

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approve' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10

{
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "role": "PARTICIPANT",
 "approved": true,
 "state": "approved",
 "participated_on": "<string>"
}

DEL
Unapprove a pull request
Redact the authenticated user's approval of the specified pull
request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses204No Content

An empty response indicating the authenticated user's approval has been withdrawn.400Bad Request
401Unauthorized
404Not Found

DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approve

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/approve' \
 --header 'Authorization: Bearer <access_token>'

GET
List comments on a pull request
Returns a paginated list of the pull request's comments.

This includes both global, inline comments and replies.

The default sorting is oldest to newest and can be overridden with
the 
sort query parameter.

This endpoint also supports filtering and sorting of the results. See
filtering and sorting for more
details.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

A paginated list of comments made on the given pull request, in chronological order.

application/json
Paginated Pull Request Comments
A paginated list of pullrequest comments.
Show child properties403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "type": "<string>",
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>"
 },
 "pending": true
 }
 ]
}

POST
Create a comment on a pull request
Creates a new pull request comment.

Returns the newly created pull request comment.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Request bodyapplication/json
The comment object.
allOf [allOf [object, Comment], Pull Request Comment]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Comment
The base type for all comments. This type should be considered abstract. Each of the "commentable" resources defines its own subtypes (e.g. 
issue_comment).
Show child properties
Pull Request Comment
A pullrequest comment.
Show child properties
Responses201Created

The newly created comment.

Headers

Location
string

application/json
allOf [allOf [object, Comment], Pull Request Comment]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Comment
The base type for all comments. This type should be considered abstract. Each of the "commentable" resources defines its own subtypes (e.g. 
issue_comment).
Show child properties
Pull Request Comment
A pullrequest comment.
Show child properties403Forbidden
404Not Found

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "created_on": "<string>"
 },
 "pending": true
}'

201Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47

{
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "created_on": "<string>"
 },
 "pending": true
}

GET
Get a comment on a pull request
Returns a specific pull request comment.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

comment_id
integerRequired

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The comment.

application/json
allOf [allOf [object, Comment], Pull Request Comment]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Comment
The base type for all comments. This type should be considered abstract. Each of the "commentable" resources defines its own subtypes (e.g. 
issue_comment).
Show child properties
Pull Request Comment
A pullrequest comment.
Show child properties403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47

{
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "created_on": "<string>"
 },
 "pending": true
}

PUT
Update a comment on a pull request
Updates a specific pull request comment.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

comment_id
integerRequired

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Request bodyapplication/json
The contents of the updated comment.
allOf [allOf [object, Comment], Pull Request Comment]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Comment
The base type for all comments. This type should be considered abstract. Each of the "commentable" resources defines its own subtypes (e.g. 
issue_comment).
Show child properties
Pull Request Comment
A pullrequest comment.
Show child properties
Responses200OK

The updated comment.

application/json
allOf [allOf [object, Comment], Pull Request Comment]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Comment
The base type for all comments. This type should be considered abstract. Each of the "commentable" resources defines its own subtypes (e.g. 
issue_comment).
Show child properties
Pull Request Comment
A pullrequest comment.
Show child properties403Forbidden
404Not Found

PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52

curl --request PUT \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "created_on": "<string>"
 },
 "pending": true
}'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47

{
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "pullrequest": {
 "type": "<string>"
 },
 "resolution": {
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "created_on": "<string>"
 },
 "pending": true
}

DEL
Delete a comment on a pull request
Deletes a specific pull request comment.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

comment_id
integerRequired

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses204No Content

Successful deletion.403Forbidden
404Not Found

DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}' \
 --header 'Authorization: Bearer <access_token>'

POST
Resolve a comment threadScopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

comment_id
integerRequired

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The comment resolution details.

application/json
Comment Resolution
The resolution object for a Comment.
Show child properties403Forbidden
404Not Found
409Conflict

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolve

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolve' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11

{
 "type": "<string>",
 "user": {
 "type": "<string>",
 "links": {},
 "created_on": "<string>",
 "display_name": "<string>",
 "uuid": "<string>"
 },
 "created_on": "<string>"
}

DEL
Reopen a comment threadScopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

comment_id
integerRequired

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses204No Content

The comment is reopened.403Forbidden
404Not Found

DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolve

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/comments/{comment_id}/resolve' \
 --header 'Authorization: Bearer <access_token>'

GET
List commits on a pull request
Returns a paginated list of the pull request's commits.

These are the commits that are being merged into the destination
branch when the pull requests gets accepted.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

A paginated list of commits made on the given pull request, in chronological order. This list will be empty if the source branch no longer exists.403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/commits

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/commits' \
 --header 'Authorization: Bearer <access_token>'

GET
Get file conflicts for a pull request
Redirects to the repository file conflicts
with the revspec that corresponds to the pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses302Moved Temporarily

Redirects to the repository file conflicts with the
revspec that corresponds to the pull request.

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/conflicts

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/conflicts' \
 --header 'Authorization: Bearer <access_token>'

POST
Decline a pull request
Declines the pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The pull request was successfully declined.

application/json
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties555

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/decline

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/decline' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131

{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}

GET
List changes in a pull request
Redirects to the repository diff
with the revspec that corresponds to the pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses302Moved Temporarily

Redirects to the repository diff with the
revspec that corresponds to the pull request.

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diff

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diff' \
 --header 'Authorization: Bearer <access_token>'

GET
Get the diff stat for a pull request
Redirects to the repository diffstat
with the revspec that corresponds to the pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses302Moved Temporarily

Redirects to the repository diffstat with
the revspec that corresponds to pull request.

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diffstat

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/diffstat' \
 --header 'Authorization: Bearer <access_token>'

POST
Merge a pull request
Merges the pull request.

If the destination branch has an active merge queue, the pull request
is added to the queue instead. The API returns a 202 with a polling
link to the task-status API in the Location header, even when 
async
is false. Calling it again on a queued pull request returns the same
link.

Merge queues support the 
merge_commit, 
squash and 
rebase_merge
strategies. If no strategy is given, the queue's default is used.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Query parameters

async
boolean

Request bodyapplication/jsonExpand all

type
stringRequired

message
string

close_source_branch
boolean

merge_strategy
string

Additional Properties
any

Responses200OK

The pull request object.

application/json
allOf [object, Pull Request]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Pull Request
A pull request object.
Show child properties202Accepted
400Bad Request
409Conflict
503Service Unavailable
555

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/merge

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/merge' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "type": "<string>",
 "message": "<string>",
 "close_source_branch": true,
 "merge_strategy": "merge_commit"
}'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47
48
49
50
51
52
53
54
55
56
57
58
59
60
61
62
63
64
65
66
67
68
69
70
71
72
73
74
75
76
77
78
79
80
81
82
83
84
85
86
87
88
89
90
91
92
93
94
95
96
97
98
99
100
101
102
103
104
105
106
107
108
109
110
111
112
113
114
115
116
117
118
119
120
121
122
123
124
125
126
127
128
129
130
131

{
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "commits": {
 "href": "<string>",
 "name": "<string>"
 },
 "approve": {
 "href": "<string>",
 "name": "<string>"
 },
 "diff": {
 "href": "<string>",
 "name": "<string>"
 },
 "diffstat": {
 "href": "<string>",
 "name": "<string>"
 },
 "comments": {
 "href": "<string>",
 "name": "<string>"
 },
 "activity": {
 "href": "<string>",
 "name": "<string>"
 },
 "merge": {
 "href": "<string>",
 "name": "<string>"
 },
 "decline": {
 "href": "<string>",
 "name": "<string>"
 },
 "mergeability_checks": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "id": 108,
 "title": "<string>",
 "rendered": {
 "title": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "description": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "reason": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 }
 },
 "summary": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "state": "OPEN",
 "author": {
 "type": "<string>"
 },
 "source": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "destination": {
 "repository": {
 "type": "<string>"
 },
 "branch": {
 "name": "<string>",
 "merge_strategies": [
 "merge_commit"
 ],
 "default_merge_strategy": "<string>"
 },
 "commit": {
 "hash": "<string>"
 }
 },
 "merge_commit": {
 "hash": "<string>"
 },
 "comment_count": 51,
 "task_count": 53,
 "close_source_branch": true,
 "closed_by": {
 "type": "<string>"
 },
 "reason": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>",
 "reviewers": [
 {
 "type": "<string>"
 }
 ],
 "participants": [
 {
 "type": "<string>"
 }
 ],
 "draft": true,
 "queued": true
}

GET
Get the merge task status for a pull request
When merging a pull request takes too long, the client receives a
task ID along with a 202 status code. The task ID can be used in a call
to this endpoint to check the status of a merge task.

1

curl -X GET https://api.bitbucket.org/2.0/repositories/atlassian/bitbucket/pullrequests/2286/merge/task-status/<task_id>

If the merge task is not yet finished, a PENDING status will be returned.

1
2
3
4
5
6
7
8
9

HTTP/2 200
{
 "task_status": "PENDING",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/bitbucket/pullrequests/2286/merge/task-status/<task_id>"
 }
 }
}

If the merge was successful, a SUCCESS status will be returned.

1
2
3
4
5
6
7
8
9
10

HTTP/2 200
{
 "task_status": "SUCCESS",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/atlassian/bitbucket/pullrequests/2286/merge/task-status/<task_id>"
 }
 },
 "merge_result": <the merged pull request object>
}

If the merge task failed, an error will be returned.

1
2
3
4
5
6

{
 "type": "error",
 "error": {
 "message": "<error message>"
 }
}

If the pull request was added to a merge queue, the task reports its
landing through the queue. A PENDING status is returned while it is in
the queue, with a 
queued_pullrequest object once the queue has it. A
SUCCESS status is returned once it merges, and an error if it leaves
the queue without merging. The error message can contain Markdown.

Unknown or expired task IDs return a PENDING status.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

task_id
stringRequired

workspace
stringRequired

Responses200OK

Returns a task status if the merge is either pending or successful, and if it is successful, a pull request400Bad Request
403Forbidden
409Conflict

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/merge/task-status/{task_id}

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/merge/task-status/{task_id}' \
 --header 'Authorization: Bearer <access_token>'

GET
List pull request mergeability checks
Returns the mergeability checks Bitbucket performs for this pull request, and whether each one
currently allows the pull request to be merged - simulating what the merge endpoint checks before
actually merging.

Checks cover pull request state, the current user's merge permissions, Git conflicts, configured
standard and custom merge checks, and merge queue status when available. Each check's 
blocking
attribute indicates whether it currently prevents merging. A failed check does not necessarily block a merge.

pullrequest_state_check, 
current_user_permission_check, and 
git_mergeability_check cover state,
permission, and Git eligibility. One 
standard_merge_check is included per configured branch/merge
restriction, and one 
custom_pre_merge_check/
custom_on_merge_check per activated custom check - both
omitted entirely, not reported as passing, when not configured. Not exhaustive: conditions Bitbucket can't
evaluate without attempting the merge (e.g. dependency-merge requirements) aren't included. See

PullRequestMergeabilityCheckSchema for per-check fields, and 
q below to exclude the checks that
are expensive to evaluate.

For repositories with merge queues enabled, a 
merge_queue_check reports queue availability and
whether the pull request is already queued. It is included for open, non-draft pull requests when the
destination branch requires queueing, and for already-queued pull requests with a configured queue.
The check is omitted when no merge queue is configured for the destination branch. When the check is
present, 
merge_queue contains that configured queue's UUID, name and state. Already-queued pull requests
return their blocking state check and any applicable queue check, without evaluating Git or other merge checks.
A failed queue configuration read fails the whole request with 
500, as for custom merge check results.

Returns 
500 if custom merge checks are configured but their results aren't currently retrievable,
since the real merge action requires them too and would fail the same way.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Query parameters

q
string

Responses200OK

The collection of checks that determine whether the specified pull request can be merged.

application/json
Pull Request Mergeability Checks
The collection of checks that determine whether the specified pull request can be merged. Every check has a 
type identifying its shape - see 
pullrequest_state_check, 
current_user_permission_check, 
git_mergeability_check, 
standard_merge_check, 
custom_merge_check, and 
merge_queue_check for what each type covers.
Show child properties400Bad Request
401Unauthorized
403Forbidden
404Not Found
429Too Many Requests
500Internal Server Error

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/mergeability/checks

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/mergeability/checks' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11

{
 "size": 2154,
 "values": [
 {
 "type": "pullrequest_state_check",
 "status": "PASSED",
 "required": true,
 "blocking": true
 }
 ]
}

GET
Get the patch for a pull request
Redirects to the repository patch
with the revspec that corresponds to pull request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses302Moved Temporarily

Redirects to the repository patch with
the revspec that corresponds to pull request.

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/patch

curl
Node.js
Java
Python
PHP

1
2
3

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/patch' \
 --header 'Authorization: Bearer <access_token>'

POST
Request changes for a pull requestScopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

read:pullrequest:bitbucket, 
write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses200OK

The 
participant object recording that the authenticated user requested changes on the pull request.

application/json
allOf [object, Participant]
object
Base type for most resource objects. It defines the common 
type element that identifies an object's type. It also identifies the element as Swagger's 
discriminator.
Show child properties
Participant
Object describing a user's role on resources like commits or pull requests.
Show child properties400Bad Request
401Unauthorized
404Not Found

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changes

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changes' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10

{
 "type": "<string>",
 "user": {
 "type": "<string>"
 },
 "role": "PARTICIPANT",
 "approved": true,
 "state": "approved",
 "participated_on": "<string>"
}

DEL
Remove change request for a pull requestScopes
OAuth 2.0 and Connect app scopes required:

pullrequest:write
Forge app and API Token scopes required:

write:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Responses204No Content

An empty response indicating the authenticated user's request for change has been withdrawn.400Bad Request
401Unauthorized
404Not Found

DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changes

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/request-changes' \
 --header 'Authorization: Bearer <access_token>'

GET
List commit statuses for a pull request
Returns all statuses (e.g. build results) for the given pull
request.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Query parametersExpand all

q
string

sort
string

Responses200OK

A paginated list of all commit statuses for this pull request.

application/json
Paginated Commit Statuses
A paginated list of commit status objects.
Show child properties401Unauthorized
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/statuses

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/statuses' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "type": "<string>",
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "commit": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "key": "<string>",
 "refname": "<string>",
 "url": "<string>",
 "state": "FAILED",
 "name": "<string>",
 "description": "<string>",
 "created_on": "<string>",
 "updated_on": "<string>"
 }
 ]
}

GET
List tasks on a pull request
Returns a paginated list of the pull request's tasks.

This endpoint supports filtering and sorting of the results by the 'task' field.
See filtering and sorting for more details.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Query parametersExpand all

q
string

sort
string

pagelen
integer

Responses200OK

A paginated list of pull request tasks for the given pull request.

application/json
Paginated Tasks
A paginated list of tasks.
Show child properties400Bad Request
403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21

{
 "size": 142,
 "page": 102,
 "pagelen": 159,
 "next": "<string>",
 "previous": "<string>",
 "values": [
 {
 "created_on": "<string>",
 "updated_on": "<string>",
 "state": "RESOLVED",
 "content": {},
 "creator": {
 "type": "<string>"
 },
 "comment": {
 "type": "<string>"
 }
 }
 ]
}

POST
Create a task on a pull request
Creates a new pull request task.

Returns the newly created pull request task.

Tasks can optionally be created in relation to a comment specified by the comment's ID which
will cause the task to appear below the comment on a pull request when viewed in Bitbucket.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

workspace
stringRequired

Request bodyapplication/jsonExpand all
The contents of the task

content
Task Raw ContentRequired

comment
allOf [object, Comment]

pending
boolean
Responses201Created

The newly created task.

Headers

Location
string

application/json
allOf [allOf [Task, Pull Request Task], Pull Request Comment Task]
Task
A task object.
Show child properties
Pull Request Task
A pull request task.
Show child properties
Pull Request Comment Task
A pullrequest comment task
Show child properties400Bad Request
403Forbidden
404Not Found

POST/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32
33
34
35
36
37
38
39
40
41
42
43
44
45
46
47

curl --request POST \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "content": {
 "raw": "<string>"
 },
 "comment": {
 "type": "<string>",
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "user": {
 "type": "<string>"
 },
 "deleted": true,
 "inline": {
 "from": 160,
 "to": 160,
 "start_from": 120,
 "start_to": 120,
 "path": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 },
 "code": {
 "href": "<string>",
 "name": "<string>"
 }
 }
 },
 "pending": true
}'

201Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32

{
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "state": "RESOLVED",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "creator": {
 "type": "<string>"
 },
 "pending": true,
 "resolved_on": "<string>",
 "resolved_by": {
 "type": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "comment": {
 "type": "<string>"
 }
}

GET
Get a task on a pull request
Returns a specific pull request task.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

task_id
integerRequired

workspace
stringRequired

Responses200OK

The task.

application/json
allOf [allOf [Task, Pull Request Task], Pull Request Comment Task]
Task
A task object.
Show child properties
Pull Request Task
A pull request task.
Show child properties
Pull Request Comment Task
A pullrequest comment task
Show child properties403Forbidden
404Not Found

GET/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}

curl
Node.js
Java
Python
PHP

1
2
3
4

curl --request GET \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32

{
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "state": "RESOLVED",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "creator": {
 "type": "<string>"
 },
 "pending": true,
 "resolved_on": "<string>",
 "resolved_by": {
 "type": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "comment": {
 "type": "<string>"
 }
}

PUT
Update a task on a pull request
Updates a specific pull request task.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

task_id
integerRequired

workspace
stringRequired

Request bodyapplication/jsonExpand all
The updated state and content of the task.

content
Task Raw Content

state
string

Responses200OK

The updated task.

application/json
allOf [allOf [Task, Pull Request Task], Pull Request Comment Task]
Task
A task object.
Show child properties
Pull Request Task
A pull request task.
Show child properties
Pull Request Comment Task
A pullrequest comment task
Show child properties400Bad Request
403Forbidden
404Not Found

PUT/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}

curl
Node.js
Java
Python
PHP

1
2
3
4
5
6
7
8
9
10
11

curl --request PUT \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}' \
 --header 'Authorization: Bearer <access_token>' \
 --header 'Accept: application/json' \
 --header 'Content-Type: application/json' \
 --data '{
 "content": {
 "raw": "<string>"
 },
 "state": "RESOLVED"
}'

200Response

1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18
19
20
21
22
23
24
25
26
27
28
29
30
31
32

{
 "id": 2154,
 "created_on": "<string>",
 "updated_on": "<string>",
 "state": "RESOLVED",
 "content": {
 "raw": "<string>",
 "markup": "markdown",
 "html": "<string>"
 },
 "creator": {
 "type": "<string>"
 },
 "pending": true,
 "resolved_on": "<string>",
 "resolved_by": {
 "type": "<string>"
 },
 "links": {
 "self": {
 "href": "<string>",
 "name": "<string>"
 },
 "html": {
 "href": "<string>",
 "name": "<string>"
 }
 },
 "comment": {
 "type": "<string>"
 }
}

DEL
Delete a task on a pull request
Deletes a specific pull request task.Scopes
OAuth 2.0 and Connect app scopes required:

pullrequest
Forge app and API Token scopes required:

read:pullrequest:bitbucket
Request

Path parametersExpand all

pull_request_id
integerRequired

repo_slug
stringRequired

task_id
integerRequired

workspace
stringRequired

Responses204No Content

Successful deletion.403Forbidden
404Not Found

DEL/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}

curl
Node.js
Java
Python
PHP

1
2
3

curl --request DELETE \
 --url 'https://api.bitbucket.org/2.0/repositories/{workspace}/{repo_slug}/pullrequests/{pull_request_id}/tasks/{task_id}' \
 --header 'Authorization: Bearer <access_token>'

Rate this page:

Unusable

Poor

Okay

Good

Excellent
