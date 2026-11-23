<!-- vendored 2026-11-23 from https://developer.atlassian.com/cloud/bitbucket/rest/intro/; text extracted from the HTML, not edited -->

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

Authentication methods
Postman Collection
OpenAPI

The purpose of this section is to describe how to authenticate when making API calls using the Bitbucket REST API.

Access tokens

Repository access tokens

Project access tokens

Workspace access tokens

App passwords

API tokens

OAuth 2.0

Making requests

Repository cloning

Refresh tokens

Bitbucket OAuth 2.0 Scopes

Forge app and API token scopes

Access tokens

Access tokens are passwords (or tokens) that provide access to a single repository, project or workspace.
These tokens can authenticate with Bitbucket APIs for scripting, CI/CD tools, Bitbucket Cloud-connected apps,
and Bitbucket Cloud integrations.

Access tokens are linked to a repository, project, or workspace, not a user account.
The level of access provided by the token is set when a repository, or workspace admin creates it,
by setting privilege scopes.

There are three types of access token:

Repository access tokens can connect to a single repository, preventing them from accessing any other repositories or workspaces.

Project access tokens can connect to a single project, providing access to any repositories within the project.

Workspace access tokens can connect to a single workspace and have access to any projects and repositories within that workspace.

When using Bitbucket APIs with an access token, the token will be treated as the "user" in the
Bitbucket UI and Bitbucket logs. This includes when using the access token to leave a comment on a pull request,
push a commit, or merge a pull request. The Bitbucket UI and API responses will show the
repository/project/workspace access token as a user. The username shown in the Bitbucket UI is the Access
Token name, and a custom icon is used to differentiate it from a regular user in the UI.

Considerations for using access tokens

After creation, an access token can't be viewed or modified. The token's name, created date,
last accessed date, and scopes are visible on the repository, project, or workspace access tokens page.

Access tokens can access a limited set of Bitbucket's privilege scopes.

Provided you set the correct privilege scopes, you can use an access token to clone (
repository)
and push (
repository:write) code to the token's repository or the repositories the token can access.

You can't use an access token to log into the Bitbucket website.

Access tokens don't require two-step verification.

You can set privilege scopes (specific access rights) for each access token.

You can't use an access token to manipulate or query repository, project, or workspace permissions.

Access tokens are not listed in any repository or workspace permission API response.

Access tokens are deactivated when deleting the resource tied to it (a repository, project, or workspace).

Repository access tokens are also revoked when transferring the repository to another workspace.

Any content created by the access token will persist after the access token has been revoked.

Access tokens can interact with branch restriction APIs, but the token can't be configured as a user with merge access when using branch restrictions.

There are some APIs which are inaccessible for Access tokens, these are:

Add a repository deploy key

Update a repository deploy key

Delete a repository deploy key

Repository access tokens

For details on creating, managing, and using repository access tokens, visit
Repository access tokens.

The available scopes for repository access tokens are:

repository

repository:write

repository:admin

repository:delete

pullrequest

pullrequest:write

webhook

pipeline

pipeline:write

pipeline:variable

runner

runner:write

Project access tokens

For details on creating, managing, and using project access tokens, visit
Project access tokens.

The available scopes for project access tokens are:

project

repository

repository:write

repository:admin

repository:delete

pullrequest

pullrequest:write

webhook

pipeline

pipeline:write

pipeline:variable

runner

runner:write

Workspace access tokens

For details on creating, managing, and using workspace access tokens, visit
Workspace access tokens.

The available scopes for workspace access tokens are:

project

project:admin

repository

repository:write

repository:admin

repository:delete

pullrequest

pullrequest:write

webhook

account

pipeline

pipeline:write

pipeline:variable

runner

runner:write

App passwords

App passwords are deprecated. Use API tokens.

API tokens

API Tokens are personal access tokens that users can create to authenticate with Bitbucket's REST APIs
or interact with Git. They are designed as a long term replacement for app passwords, while retaining a
lot of the functionality you are already familiar with.

Some important points about API tokens:

To authenticate with an API token, use Basic HTTP Authentication as per RFC-2617, where
the username is your Atlassian email and password is the API token.

You cannot view an API token or adjust permissions after you create the API token. They are
designed to be disposable. If you need to change the scopes or you've lost the token, you should just create a new one.

API token require an expiry date at creation, with a maximum duration of 1 year.

You cannot use them to log into Bitbucket website.

API tokens are tied to an individual account's credentials and should not be shared. If you're sharing your API token
you're giving direct, authenticated access to everything that the token has been scoped
to do with Bitbucket's APIs.

You can set privilege scopes (specific access rights) for each API token.

For details on creating, managing, and using API tokens, visit
API tokens.

OAuth 2.0

Our OAuth 2 implementation is merged in with our existing OAuth 1 in
such a way that existing OAuth 1 consumers automatically become
valid OAuth 2 clients. The only thing you need to do is edit your
existing consumer and configure a callback URL.

Once that is in place, you'll have the following 2 URLs:

1
2
3

https://bitbucket.org/site/oauth2/authorize
https://bitbucket.org/site/oauth2/access_token

For obtaining access/bearer tokens, we support two of RFC-6749's grant flows, plus a custom Bitbucket flow
for exchanging JWT tokens for access tokens.
Note that Implicit Grant (4.2) and Resource Owner Password Credentials Grant (4.3) are no longer supported.

1. Authorization Code Grant (4.1)

The full-blown 3-LO flow. Request authorization from the end user by
sending their browser to:

1
2

https://bitbucket.org/site/oauth2/authorize?client_id={client_id}&response_type=code

The callback includes the 
?code={} query parameter that you can swap
for an access token:

1
2
3
4

$ curl -X POST -u "client_id:secret" \
 https://bitbucket.org/site/oauth2/access_token \
 -d grant_type=authorization_code -d code={code}

2. Client Credentials Grant (4.4)

Obtain an access token that represents not an end user, but the owner of the
client/consumer:

1
2
3
4

$ curl -X POST -u "client_id:secret" \
 https://bitbucket.org/site/oauth2/access_token \
 -d grant_type=client_credentials

3. Bitbucket Cloud JWT Grant (urn:bitbucket:oauth2:jwt)

If your Atlassian Connect add-on uses JWT authentication, you can swap a
JWT for an OAuth access token. The resulting access token represents the
account for which the add-on is installed.

Make sure you send the JWT token in the Authorization request header
using the "JWT" scheme (case sensitive). Note that this custom scheme
makes this different from HTTP Basic Auth (and so you cannot use "curl
-u").

1
2
3
4

$ curl -X POST -H "Authorization: JWT {jwt_token}" \
 https://bitbucket.org/site/oauth2/access_token \
 -d grant_type=urn:bitbucket:oauth2:jwt

Making Requests

As of May 4th, 2026, all OAuth 2.0 authenticated API requests must be directed at
https://api.bitbucket.org with the 
access token placed as a 
Bearer token in the
request 
Authorization header as per RFC-6750 (2.1)

1
2
3

$ curl -H "Authorization: Bearer {access_token}" \
 https://api.bitbucket.org/2.0/user

Repository Cloning

Since add-ons will not be able to upload their own SSH keys to clone
with, access tokens can be used as Basic HTTP Auth credentials to
clone securely over HTTPS. This is much like GitHub, yet slightly
different:

1
2

$ git clone https://x-token-auth:{access_token}@bitbucket.org/user/repo.git

The literal string 
x-token-auth as a substitute for username is
required (note the difference with GitHub where the actual token is in
the username field).

Refresh Tokens

Our access tokens expire in one hour. When this happens you'll get 401
responses.

Both the 
authorization_code and 
refresh_token access token grant
responses include a refresh token that can be used to generate a new access
token, without the need for end user participation:

1
2
3
4

$ curl -X POST -u "client_id:secret" \
 https://bitbucket.org/site/oauth2/access_token \
 -d grant_type=refresh_token -d refresh_token={refresh_token}

Once a refresh token has been used to issue a new access token, a new refresh
token will be issued as part of the response. The existing refresh token will
expire shortly after and should not be used again.

A refresh token which is not used to generate an access token will expire after
3 months. After this point the end user will be required to complete the full

authorization_code grant flow again.

Bitbucket OAuth 2.0 scopes

Bitbucket's API applies a number of privilege scopes to endpoints. In order to access an
endpoint, a request will need to have the necessary scopes.

OAuth 2.0 Scopes are applicable for OAuth 2 and access tokens
auth mechanisms as well as Bitbucket Connect apps.

Scopes are declared in the descriptor as a list of strings, with each string being the name of a unique scope.

A descriptor lacking the 
scopes element is implicitly assumed to require all scopes and as a result,
Bitbucket will require end users authorizing/installing the add-on to explicitly accept all scopes.

Our best practice suggests you add only the scopes your add-on needs, but no more than it needs.

Invalid scope strings will cause the descriptor to be rejected and the installation to fail.

The available scopes are:

project

project:write

project:admin

repository

repository:write

repository:admin

repository:delete

pullrequest

pullrequest:write

issue

issue:write

wiki

webhook

snippet

snippet:write

email

account

account:write

pipeline

pipeline:write

pipeline:variable

runner

runner:write

project

Provides access to view the project or projects.
This scope implies the 
repository scope, giving read access to all the repositories in a project or projects.

project:write

This scope is deprecated, and has been made obsolete by 
project:admin. Please see the deprecation notice here.

project:admin

Provides admin access to a project or projects. No distinction is made between public and private projects. This scope doesn't implicitly grant the 
project scope or the 
repository:write scope on any repositories under the project. It gives access to the admin features of a project only, not direct access to its repositories' contents.

ability to create the project

ability to update the project

ability to delete the project

repository

Provides read access to a repository or repositories.
Note that this scope does not give access to a repository's pull requests.

access to the repo's source code

clone over HTTPS

access the file browsing API

download zip archives of the repo's contents

the ability to view and use the issue tracker on any repo (created issues, comment, vote, etc)

the ability to view and use the wiki on any repo (create/edit pages)

repository:write

Provides write (not admin) access to a repository or repositories. No distinction is made between public and private repositories. This scope implicitly grants the 
repository scope, which does not need to be requested separately.
This scope alone does not give access to the pull requests API.

push access over HTTPS

fork repos

repository:admin

Provides admin access to a repository or repositories. No distinction is made between public and private repositories. This scope doesn't implicitly grant the 
repository or the 
repository:write scopes. It gives access to the admin features of a repo only, not direct access to its contents. This scope can be used or misused to grant read access to other users, who can then clone the repo, but users that need to read and write source code would also request explicit read or write.
This scope comes with access to the following functionality:

View and manipulate committer mappings

List and edit deploy keys

Ability to delete the repo

View and edit repo permissions

View and edit branch permissions

Import and export the issue tracker

Enable and disable the issue tracker

List and edit issue tracker version, milestones and components

Enable and disable the wiki

List and edit default reviewers

List and edit repo links (Jira/Bamboo/Custom)

List and edit the repository webhooks

Initiate a repo ownership transfer

repository:delete

Provides access to delete a repository or repositories.

pullrequest

Provides read access to pull requests.
This scope implies the 
repository scope, giving read access to the pull request's destination repository.

see and list pull requests

create and resolve tasks

comment on pull requests

pullrequest:write

Implicitly grants the 
pullrequest scope and adds the ability to create, merge and decline pull requests.
This scope also implicitly grants the 
repository:write scope, giving write access to the pull request's destination repository. This is necessary to allow merging.

merge pull requests

decline pull requests

create pull requests

approve pull requests

issue

Ability to interact with issue trackers the way non-repo members can.
This scope doesn't implicitly grant any other scopes and doesn't give implicit access to the repository.

view, list and search issues

create new issues

comment on issues

watch issues

vote for issues

issue:write

This scope implicitly grants the 
issue scope and adds the ability to transition and delete issues.
This scope doesn't implicitly grant any other scopes and doesn't give implicit access to the repository.

transition issues

delete issues

wiki

Provides access to wikis. This scope provides both read and write access (wikis are always editable by anyone with access to them).
This scope doesn't implicitly grant any other scopes and doesn't give implicit access to the repository.

view wikis

create pages

edit pages

push to wikis

clone wikis

webhook

Gives access to webhooks. This scope is required for any webhook-related operation.

This scope gives read access to existing webhook subscriptions on all
resources the authorization mechanism can access, without needing further scopes.
For example:

A client can list all existing webhook subscriptions on a repository. The 
repository scope is not required.

Existing webhook subscriptions for the issue tracker on a repo can be retrieved without the 
issue scope. All that is required is the 
webhook scope.

To create webhooks, the client will need read access to the resource. Such as: for 
issue:created, the client will need to
have both the 
webhook and the 
issue scope.

list webhook subscriptions on any accessible repository, user, team, or snippet

create/update/delete webhook subscriptions.

snippet

Provides read access to snippets.
No distinction is made between public and private snippets (public snippets are accessible without any form of authentication).

view any snippet

create snippet comments

snippet:write

Provides write access to snippets.
No distinction is made between public and private snippets (public snippets are accessible without any form of authentication).
This scope implicitly grants the 
snippet scope which does not need to be requested separately.

create snippets

edit snippets

delete snippets

email

Ability to see the user's primary email address. This should make it easier to use Bitbucket Cloud as a login provider for apps or external applications.

account

When used for:

user-related APIs — Gives read-only access to the user's account information.
Note that this doesn't include any ability to change any of the data. This scope allows you to view the user's:

email addresses

language

location

website

full name

SSH keys

user groups

workspace-related APIs — Grants access to view the workspace's:

users

user permissions

projects

account:write

Ability to change properties on the user's account.

delete the authorizing user's account

manage the user's groups

change a user's email addresses

change username, display name and avatar

pipeline

Gives read-only access to pipelines, steps, deployment environments and variables.

pipeline:write

Gives write access to pipelines. This scope allows a user to:

Stop pipelines

Rerun failed pipelines

Resume halted pipelines

Trigger manual pipelines.

This scope is not needed to trigger a build using a push. Performing a 
git push (or equivalent actions) will trigger the build. The token doing the push only needs the 
repository:write scope.

This doesn't give write access to create variables.

pipeline:variable

Gives write access to create variables in pipelines at the various levels:

Workspace

Repository

Deployment

runner

Gives read-only access to pipelines runners setup against a workspace or repository.

runner:write

Gives write access to create/edit/disable/delete pipelines runners setup against a workspace or repository.

Forge app and API token scopes

In order for a Forge app integration or an API token to access Bitbucket API endpoints, it needs to include certain privilege scopes. These are different from Bitbucket OAuth 2.0 scopes.

In the case of a Forge app, the privilege scopes need to be included in the app manifest.

Unlike OAuth 2.0 scopes, Forge app and API token scopes do not implicitly grant access to other scopes, for example, 
write:repository:bitbucket does not implicitly grant access to 
read:repository:bitbucket.

It's important to note that only a subset of all API endpoints are currently available for Forge app integrations. Each endpoint is clearly labeled, indicating whether it is available for Forge apps.

Our best practice recommends adhering to the principle of least privilege. You should only add the scopes that are necessary for your needs.

The available scopes are:

read:repository:bitbucket

write:repository:bitbucket

admin:repository:bitbucket

delete:repository:bitbucket

read:pullrequest:bitbucket

write:pullrequest:bitbucket

read:project:bitbucket

admin:project:bitbucket

read:workspace:bitbucket

admin:workspace:bitbucket

read:user:bitbucket

write:user:bitbucket

read:pipeline:bitbucket

write:pipeline:bitbucket

admin:pipeline:bitbucket

read:runner:bitbucket

write:runner:bitbucket

read:issue:bitbucket

write:issue:bitbucket

delete:issue:bitbucket

read:webhook:bitbucket

write:webhook:bitbucket

delete:webhook:bitbucket

read:snippet:bitbucket

write:snippet:bitbucket

delete:snippet:bitbucket

read:ssh-key:bitbucket

write:ssh-key:bitbucket

delete:ssh-key:bitbucket

read:gpg-key:bitbucket

write:gpg-key:bitbucket

delete:gpg-key:bitbucket

read:permission:bitbucket

write:permission:bitbucket

delete:permission:bitbucket

read:repository:bitbucket

Allows viewing of repository data. Note that this scope does not give access to a repository's pull requests.

access to the repository's source code

access the file browsing API

access to certain repository configurations such as branching model, default reviewers, etc.

write:repository:bitbucket

Allows modification of repository data. No distinction is made between public and private repositories. This scope does not imply the 
read:repository:bitbucket scope, so you need to request that separately if required. This scope alone does not give access to the pull request API.

update/delete source, branches, tags, etc.

fork repositories

admin:repository:bitbucket

Allows admin activities on repositories. No distinction is made between public and private repositories. This scope does not implicitly grant the 
read:repository:bitbucket or the 
write:repository:bitbucket scopes. It gives access to the admin features of a repository only, not direct access to its contents. This scope does not allow modification of repository permissions. This scope comes with access to the following functionality:

create repository

view repository permissions

view and edit branch restrictions

edit branching model settings

edit default reviewers

view and edit inheritance state for repository settings

delete:repository:bitbucket

Allows deletion of repositories.

read:pullrequest:bitbucket

Allows viewing of pull requests, plus the ability to comment on pull requests.

This scope does not imply the 
read:repository:bitbucket scope. With this scope, you could retrieve some data specific to the source/destination repositories of a pull request using pull request endpoints, but it does not give access to repository API endpoints.

write:pullrequest:bitbucket

Allows the ability to create, update, approve, decline, and merge pull requests.

This scope does not imply the 
write:repository:bitbucket scope.

read:project:bitbucket

Allows viewing of project and project permission data.

admin:project:bitbucket

Allows the ability to create, update, and delete project. No distinction is made between public and private projects.

This scope does not implicitly grant the 
read:project:bitbucket scope or any repository scopes. It gives access to the admin features of a project only, not direct access to its repositories' contents.

read:workspace:bitbucket

Allows viewing of workspace and workspace permission data.

admin:workspace:bitbucket

Allows the ability to create, update and delete the workspace. This scope does not implicitly grant the 
read:workspace:bitbucket scope or any repository scopes. It gives access to the admin features of a workspace only, not direct access to its workspaces' contents.

read:user:bitbucket

Allows viewing of data related to the current user.

write:user:bitbucket

Allows the ability to update data related to the current user.

This scope does not imply the 
read:user:bitbucket scope.

read:pipeline:bitbucket

Allows read access to all pipeline information (pipelines, steps, caches, artifacts, logs, tests, code-insights).

write:pipeline:bitbucket

Allows running pipelines (i.e., start/stop/create pipeline) and uploading tests/code-insights.

This scope does not imply the 
read:pipeline:bitbucket scope.

admin:pipeline:bitbucket

Allows admin activities, such as creating pipeline variables.

This scope does not implicitly grant the 
read:pipeline:bitbucket or the 
write:pipeline:bitbucket scopes.

read:runner:bitbucket

Allows viewing of runners information.

write:runner:bitbucket

Allows runners management.

This scope does not imply the 
read:runners:bitbucket scope.

read:issue:bitbucket

Allows the viewing of issues.

write:issue:bitbucket

Allows the ability to create and update issues.

This scope does not implicitly grant the 
read:issue:bitbucket scope.

delete:issue:bitbucket

Allows the deletion of issues.

read:webhook:bitbucket

Allows read access to webhooks information.

write:webhook:bitbucket

Allows the ability to create and update webhooks.

This scope does not implicitly grant the 
read:webhook:bitbucket scope.

delete:webhook:bitbucket

Allows the deletion of webhooks.

read:snippet:bitbucket

Allows the viewing of snippets.

write:snippet:bitbucket

Allows the ability to create and update snippets.

This scope does not implicitly grant the 
read:snippet:bitbucket scope.

delete:snippet:bitbucket

Allows the deletion of snippets.

read:ssh-key:bitbucket

Allows read access to information related to deploy keys and SSH keys.

write:ssh-key:bitbucket

Allows the ability to create and update deploy keys and SSH keys.

This scope does not implicitly grant the 
read:ssh-key:bitbucket scope.

delete:ssh-key:bitbucket

Allows the deletion of deploy keys and SSH keys.

read:gpg-key:bitbucket

Allows read access to information related to GPG keys.

write:gpg-key:bitbucket

Allows the ability to create and update GPG keys.

This scope does not implicitly grant the 
read:gpg-key:bitbucket scope.

delete:gpg-key:bitbucket

Allows the deletion of GPG keys.

read:permission:bitbucket

Allows read access to permissions data.

write:permission:bitbucket

Allows the ability to create and modify permissions related data.

This scope does not implicitly grant the 
read:permission:bitbucket scope.

delete:permission:bitbucket

Allows the deletion of permissions related data.

Filter and sort API objects

You can query the 2.0 API for specific objects using a simple language which resembles SQL.

Note that filtering and querying by username has been deprecated, due to privacy changes.
See the announcement
for details.

Supported endpoints

Operators

Data types

Querying

Sorting query results

Supported endpoints

Most 2.0 API resources that return paginated collections of objects support a single, shared, generic querying language that is used to filter down a result set.

This includes, but is in no way limited to:

1
2
3
4
5
6
7
8
9

/2.0/repositories/{username}
/2.0/repositories/{username}/{slug}/refs
/2.0/repositories/{username}/{slug}/refs/branches
/2.0/repositories/{username}/{slug}/refs/tags
/2.0/repositories/{username}/{slug}/forks
/2.0/repositories/{username}/{slug}/src
/2.0/repositories/{username}/{slug}/issues
/2.0/repositories/{username}/{slug}/pullrequests

Filtering and sorting supports several distinct operators and data types as well as basic features, like logical operators (AND, OR).
As examples, the following queries could be used on the issue tracker endpoint (
/2.0/repositories/{workspace}/{slug}/issues/):

1
2
3
4

(state = "open" OR state = "new") AND assignee = null
reporter.nickname != "evzijst" AND priority >= "major"
(title ~ "unicode" OR content.raw ~ "unicode") AND created_on > 2015-10-04T14:00:00-07:00

Filter queries can be added to the URL using the q= query parameter. To sort the response, add sort=. Note that the entire query string is put in the q parameter and hence needs to be URL-encoded as shown in the following example:

1
2

/2.0/repositories/foo/bar/issues?q=state="new"&sort=-updated_on

Operators

Filtering and sorting supports the following operators:

OperatorDefinitionExample
"="test for equality
nickname = "evzijst"
"!="not equal
is_private != true
"~"case-insensitive text contains
description ~ "beef"
"!~"case-insensitive not contains
description !~ "fubar"
">"greater than
priority > "major"
">="greater than or equal
priority <= "trivial"
"<"less than
id < 1234
"<="less than or equal
updated_on <= 2015-03-04
"IN"value present in list
state IN ("OPEN", "MERGED")
"NOT IN"value not present in list
state NOT IN ("DECLINED", "MERGED")

Data types

Filtering and sorting supports the following data types:

TypeDescriptionExample
Stringany text inside double quotes
"foo"
Numberarbitrary precision integers and floats
1, 
-10.302
Nullto test for the absence of a value
null
booleanthe unquoted strings true or false
true, 
false
datetimean unquoted ISO-8601 date time string with the timezone offset, milliseconds and entire time component being optional
2015-03-04T14:08:59.123+02:00, 
2015-03-04T14:08:59 Date time strings are assumed to be in UTC, unless an explicit timezone offset is provided
lista comma separated list of values enclosed in parentheses
("a", "b"), 
(1, 2)

Querying

Objects can be filtered based on their properties. In principle, every element in an object's JSON document schema can be used as a filter criterion.

Note that while the array of objects in a paginated response is wrapped in an
envelope with a 
values element, this prefix should not be included in the
query fields (so use 
/2.0/repositories/foo/bar/issues?q=state="new", not

/2.0/repositories/foo/bar/issues?q=values.state="new").

Examples

Fields that contain embedded instances of other object types (e.g. owner is an embedded user object, while parent is an embedded repository) can be traversed recursively. For instance:

1
2

parent.owner.nickname = "bitbucket"

To find pull requests which merge into master, come from a fork of the repo rather than a branch inside the repo, and on which I am a reviewer:

1
2

source.repository.full_name != "main/repo" AND state = "OPEN" AND reviewers.nickname = "evzijst" AND destination.branch.name = "master"

1
2

/2.0/repositories/main/repo/pullrequests?q=source.repository.full_name+%21%3D+%22main%2Frepo%22+AND+state+%3D+%22OPEN%22+AND+reviewers.nickname+%3D+%22evzijst%22+AND+destination.branch.name+%3D+%22master%22

To find new or on-hold issues related to the UI, created or updated in the last day (SF local time), that have not yet been assigned to anyone:

1
2

state IN ("new", "on hold") AND assignee = null AND component = "UI" and updated_on > 2015-11-11T00:00:00-07:00

1
2

/2.0/repositories/main/repo/issues?q=state%20IN%20%28%22new%22%2C%20%22on%20hold%22%29%20AND%20assignee%20%3D%20null%20AND%20component%20%3D%20%22UI%22%20and%20updated%5Fon%20%3E%202015%2D11%2D11T00%3A00%3A00%2D07%3A00

To find all tags with the string "2015" in the name:

1
2

name ~ "2015"

1
2

/2.0/repositories/{username}/{slug}/refs/tags?q=name+%7E+%222015%22

Or all my branches:

1
2

name ~ "erik/"

1
2

/2.0/repositories/{username}/{slug}/refs/?q=name+%7E+%22erik%2F%22

Sorting query results

You can sort result sets using the ?sort= query parameter, available on the same resources that support filtering:

In principle, every field that can be queried can also be used as a key for sorting.

By default the sort order is ascending. To reverse the order, prefix the field name with a hyphen (e.g. ?sort=-updated_on).

Only one field can be sorted on. Compound fields (e.g. sort on state first, followed by updated_on) are not supported.

Pagination

Endpoints that return collections of objects should always apply pagination.
Paginated collections are always wrapped in the following wrapper object:

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
 "size": 5421,
 "page": 2,
 "pagelen": 10,
 "next": "https://api.bitbucket.org/2.0/repositories/pypy/pypy/commits?page=3",
 "previous": "https://api.bitbucket.org/2.0/repositories/pypy/pypy/commits?page=1",
 "values": [
 ...
 ]
}

Pagination is often page-bound, with a query parameter page indicating which
page is to be returned.

However, clients are not expected to construct URLs themselves by manipulating
the page number query parameter. Instead, the response contains a link to the
next page. This link should be treated as an opaque location that is not to be
constructed by clients or even assumed to be predictable. The only contract
around the next link is that it will return the next chunk of results.

Lack of a next link in the response indicates the end of the collection.

The paginated response contains the following fields:

FieldValue

sizeTotal number of objects in the response. This is an optional element that is not provided in all responses, as it can be expensive to compute.

pagePage number of the current results. This is an optional element that is not provided in all responses.

pagelenCurrent number of objects on the existing page. Globally, the minimum length is 10 and the maximum is 100. Some APIs may specify a different default.

nextLink to the next page if it exists. The last page of a collection does not have this value. Use this link to navigate the result set and refrain from constructing your own URLs.

previousLink to previous page if it exists. A collections first page does not have this value. This is an optional element that is not provided in all responses. Some result sets strictly support forward navigation and never provide previous links. Clients must anticipate that backwards navigation is not always available. Use this link to navigate the result set and refrain from constructing your own URLs.

valuesThe list of objects. This contains at most 
pagelen objects.

The link to the next page is included such that you don't have to hardcode or construct any links. Only values and next are guaranteed (except the last page, which lacks next). This is because the previous and size values can be expensive for some data sets.

It is important to realize that Bitbucket support both list-based pagination and iterator-based pagination. List-based pagination assumes that the collection is a discrete, immutable, consistently ordered, finite array of objects with a fixed size. Clients navigate a list-based collection by requesting offset-based chunks. In Bitbucket Cloud, list-based responses include the optional size, page, and previous element. The the next and previous links typically resemble something like /foo/bar?page=4.

However, not all result sets can be treated as immutable and finite – much like how programming languages tend to distinguish between lists and arrays on one hand and iterators or stream on the other. Where an list-based pagination offers random access into any point in a collection, iterator-based pagination can only navigate forward one element at a time. In Bitbucket such iterator-based pagination contains the next link and pagelen elements, but not necessarily anything else. In these cases, the next link's value often contains an unpredictable hash instead of an explicit page number. The commits resource uses iterator-based pagination.

Partial responses

By default, each endpoint returns the full representation of a resource and in
some cases that can be a lot of data. For example, retrieving a list of pull
requests can amount to quite a large document.

For better performance, you can ask the server to only return the fields you
really need and to omit unwanted data. To request a partial response and to
add or remove specific fields from a response, use the 
fields query
parameter.

Example

Most API resources embed a substantial list of links pointing to related
resources. This saves the client from constructing its own URLs, but is
somewhat wasteful when the client doesn't need them.

To significantly reduce the size of the response, use 
?fields=-links:

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

$ curl https://api.bitbucket.org/2.0/users/evzijst?fields=-links
{
 "nickname": "evzijst",
 "account_status": "active",
 "website": "",
 "display_name": "Erik van Zijst",
 "uuid": "{a288a0ab-e13b-43f0-a689-c4ef0a249875}",
 "created_on": "2010-07-07T05:16:36+00:00",
 "location": null,
 "type": "user"
}

Fields parameter syntax

The 
fields parameter supports 3 modes of operation:

Removal of select fields (e.g. 
-links)

Pulling in additional fields not normally returned by an endpoint, while
still getting all the default fields (e.g. 
+reviewers)

Omitting all fields, except those specified (e.g. 
owner.display_name)

The fields parameter can contain a list of multiple comma-separated field names
(e.g. 
fields=owner.display_name,uuid,links.self.href). The parameter itself is
not repeated.

As discussed at Condensed Versus Full Objects,
most objects that are embedded inside other objects (like how 
owner is an
embedded 
user object in 
repository) appear in "condensed" form that omits
many fields. The 
fields parameter allows us to pull in additional fields in
such cases.

For example, the embedded repository object in a pull request does not normally
contain its 
owner. To add that in we can use:

+values.destination.repository.owner.

Wildcards

The asterisk can be used to match all fields on a particular level. For
example, removing all entries from the 
links element can be done like this:

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

$ curl https://api.bitbucket.org/2.0/users/evzijst?fields=-links.*
{
 "nickname": "evzijst",
 "account_status": "active",
 "website": "",
 "display_name": "Erik van Zijst",
 "uuid": "{a288a0ab-e13b-43f0-a689-c4ef0a249875}",
 "links": {},
 "created_on": "2010-07-07T05:16:36+00:00",
 "location": null,
 "type": "user"
}

Wildcards can be used in combination with exclusion and inclusion. For
instance, 
-*,+foo,+bar will remove all elements from the root level and then
add in 
foo and 
bar.

URL encoding

Be aware that when using the 
+foo.bar syntax in the query string, that the
"+" must be URL encoded as "%2B" and so the URL will be:

1
2

https://api.bitbucket.org/2.0/repositories/evzijst/interruptingcow?fields=%2Bowner.created_on

Without URL escaping, "+" is interpreted as an encoded space which will not
match any fields.

Field discovery

While a resource's 
self URL, as well its "collection" URL typically return
the full object with all its fields, there are some exceptions for fields that
are overly verbose or costly to generate.

For instance, a pull request contains the embedded lists of reviewers and
participants. These fields are included from the 
self URL, but not from the

/pullrequests collections resource, as it would impact performance too much.

To discover any additional fields that might not be included by default,

fields=* can be used.

More examples

If we want to get a list of all reviewer nicknames on pull requests I created,
we could combine a filter with a partial response. This will omit
all other data from the response:

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

/2.0/repositories/bitbucket/bitbucket/pullrequests?fields=values.id,values.reviewers.nickname,values.state&q=author.uuid%3D%22%7Bd301aafa-d676-4ee0-88be-962be7417567%7D%22
{
 "values": [
 {
 "reviewers": [
 {
 "nickname": "abhin"
 },
 {
 "nickname": "dtao"
 },
 {
 "nickname": "csomme"
 }
 ],
 "state": "OPEN",
 "id": 11355
 },
 {
 "reviewers": [
 {
 "nickname": "csomme"
 },
 {
 "nickname": "abhin"
 },
 {
 "nickname": "dstevens"
 }
 ],
 "state": "MERGED",
 "id": 11347
 },
 {
 "reviewers": [
 {
 "nickname": "csomme"
 },
 {
 "nickname": "jmooring"
 },
 {
 "nickname": "zdavis"
 },
 {
 "nickname": "flexbox"
 }
 ],
 "state": "OPEN",
 "id": 11344
 }
 ]
}

Schemas and Serialization

Open API Specification

JSON Schema

Condensed Versus Full Objects

Open API Specification

Bitbucket uses the Open API Specification (OAI,
formerly known as Swagger) to describe its APIs. Our OAI specification schema
is hosted at https://api.bitbucket.org/swagger.json
and serves as the canonical definition and comprehensive declaration of all
available endpoints.

The OAI specification makes writing client applications easier by:
auto-generating boilerplate code (like data object classes) and dealing with
authentication and error handling.

You can find a comprehensive set of open tools for the OAI specification at:
https://github.com/swagger-api.

JSON Schema

Bitbucket uses JSON Schema to describe the layout of every type of object
consumed or produced by the API. These schemas are collected under the

#definitions element of our swagger.json file.

When an endpoint expects an object as part of a POST or PUT, it also expects
the object to validate against the JSON schemas. The same applies to objects
returned by an endpoint.

Condensed Versus Full Objects

Most objects in Bitbucket come both in "full" and "partial" representation.
The full representation is when all elements are included. This is the layout
returned by a resource's 
self location (e.g. 
/2.0/repositories/foo/bar),
as well as resource collection endpoints (e.g. 
/2.0/repositories).

However, Bitbucket objects often embed other objects. For example, a 
repository
object embeds a 
user object for its owner. Likewise, a 
pullrequest object
embeds its 
repository object.

These related objects are embedded, or inlined, to reduce the "chatter" when
clients make frequent followup API calls to collect information on common,
related information.

Embedded related objects are typically limited in their fields to avoid such
object graphs from becoming too deep and noisy. They often exclude their own
nested objects in an attempt to strike a balance between performance and
utility.

An object's embedded or condensed representation tends to be standardized,
meaning the fields included is the same set, regardless of where the object
was embedded.

URI, UUID, and structures

You should be familiar with REST architecture before writing an integration. Read this overview page to gain a good understanding of Bitbucket's REST implementation.

URI structure

HTTP methods

UUID

User object and UUID

Repository object and UUID

Team object and UUID

Standard error responses

Standard ISO-8601 timestamps

URI structure

All Bitbucket Cloud requests start with the 
https://api.bitbucket.org/2.0 prefix (for the 2.0 API) and 
https://api.bitbucket.org/1.0 prefix (1.0 API).

The next segment of the URI path depends on the endpoint of the request. For example, using the curl command and the repositories endpoint you can list all the issues on Bitbucket's tutorial repository:

1
2

curl https://api.bitbucket.org/2.0/repositories/tutorials/tutorials.bitbucket.org

Given a specific endpoint, you can then drill down to a particular aspect or resource of that endpoint. The issues resource on a repository is an example:

1
2

curl https://api.bitbucket.org/1.0/repositories/tutorials/tutorials.bitbucket.org/issues

HTTP methods

A given endpoint or resource has a series of actions (or methods) associated with it. The Bitbucket service supports these standard HTTP methods:

CallDescription
GETRetrieves information.
PUTUpdates existing information.
POSTCreates new information.
DELETERemoves existing information.

For example, you can call use the POST action on the issues resource and create an issue on the issue tracker.

Specifying content length

You can get a 
411 Length Required response. If this happens, the API requires a Content-Length header but the client is not sending it. You should add the header yourself, for example using the curl client:

1
2

curl -r PUT --header "Content-Length: 0" -u user:app_password https://api.bitbucket.org/1.0/emails/rap@atlassian.com

Universally Unique Identifier

UUID's provide a single point of recognition for users, teams, and repositories. The UUID is distinct from the username, team name, and repository name fields and remains the same even when those fields change. For example when a user changes their username or moves a repository you will need to modify calls which use those identifiers but not if you are pointing to the UUID.

UUID examples and structure

UUID's work with both the 1.0 and 2.0 APIs for the user, team, and repository objects. The following examples the following characters are replacements for curly brackets: 
%7B replaces 
{ and 
%7D replaces 
}. You will see this structure in the following example sections.

User object and UUID

When you make a call using either the username or the UUID for that user the response is the same.

Call with username:

1
2

curl https://api.bitbucket.org/2.0/users/tutorials

*Call with UUID for the user:

1
2

curl https://api.bitbucket.org/2.0/users/%7Bc788b2da-b7a2-404c-9e26-d3f077557007%7D

Response

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
 "username": "tutorials",
 "nickname": "tutorials",
 "account_status": "active",
 "website": "https://tutorials.bitbucket.org/",
 "display_name": "tutorials account",
 "uuid": "{c788b2da-b7a2-404c-9e26-d3f077557007}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials"
 },
 "repositories": {
 "href": "https://api.bitbucket.org/2.0/repositories/tutorials"
 },
 "html": {
 "href": "https://bitbucket.org/tutorials"
 },
 "followers": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/followers"
 },
 "avatar": {
 "href": "https://bitbucket-assetroot.s3.amazonaws.com/c/photos/2013/Nov/25/tutorials-avatar-1563784409-6_avatar.png"
 },
 "following": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/following"
 }
 },
 "created_on": "2011-12-20T16:34:07.132459+00:00",
 "location": "Santa Monica, CA",
 "type": "user"
}

Repository object and UUID

Once you have the UUID for a repository you no longer need a username or team name to make the API call so long as you use an empty field. This helps you resolve repositories no matter if the username or team name changes.

Call with team name (1team) and repository name (moxie):

1
2

curl https://api.bitbucket.org/2.0/repositories/1team/moxie

Call with UUID and empty field:

1
2

curl https://api.bitbucket.org/2.0/repositories/%7B%7D/%7B21fa9bf8-b5b2-4891-97ed-d590bad0f871%7D

Call with UUID and teamname:

1
2

curl https://api.bitbucket.org/2.0/repositories/1team/%7B21fa9bf8-b5b2-4891-97ed-d590bad0f871%7D

Response

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

{
 "created_on": "2013-11-08T01:11:03.222520+00:00",
 "description": "",
 "fork_policy": "allow_forks",
 "full_name": "1team/moxie",
 "has_issues": false,
 "has_wiki": false,
 "is_private": false,
 "language": "",
 "links": {
 "avatar": {
 "href": "https://bitbucket.org/1team/moxie/avatar/32/"
 },
 "branches": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/refs/branches"
 },
 "clone": [
 {
 "href": "https://bitbucket.org/1team/moxie.git",
 "name": "https"
 },
 {
 "href": "ssh://git@bitbucket.org/1team/moxie.git",
 "name": "ssh"
 }
 ],
 "commits": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/commits"
 },
 "downloads": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/downloads"
 },
 "forks": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/forks"
 },
 "hooks": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/hooks"
 },
 "html": {
 "href": "https://bitbucket.org/1team/moxie"
 },
 "pullrequests": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/pullrequests"
 },
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie"
 },
 "tags": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/refs/tags"
 },
 "watchers": {
 "href": "https://api.bitbucket.org/2.0/repositories/1team/moxie/watchers"
 }
 },
 "name": "moxie",
 "owner": {
 "display_name": "the team",
 "links": {
 "avatar": {
 "href": "https://bitbucket.org/account/1team/avatar/32/"
 },
 "html": {
 "href": "https://bitbucket.org/1team/"
 },
 "self": {
 "href": "https://api.bitbucket.org/2.0/teams/1team"
 }
 },
 "type": "team",
 "username": "1team",
 "uuid": "{aa559944-83c9-4963-a9a8-69ac8d9cf5d2}"
 },
 "project": {
 "key": "PROJ",
 "links": {
 "avatar": {
 "href": "https://bitbucket.org/account/user/1team/projects/PROJ/avatar/32"
 },
 "html": {
 "href": "https://bitbucket.org/account/user/1team/projects/PROJ"
 }
 },
 "name": "Untitled project",
 "type": "project",
 "uuid": "{ab52aaeb-16ad-4fb0-bb1d-47e4f00367ff}"
 },
 "scm": "git",
 "size": 33348,
 "type": "repository",
 "updated_on": "2013-11-08T01:11:03.263237+00:00",
 "uuid": "{21fa9bf8-b5b2-4891-97ed-d590bad0f871}",
 "website": ""
}

Team object and UUID

This example shows a call for a list of team members using both the team name and with the UUID for the team object. As the call is unauthenticated in the following example the response object will only show members with public profiles. The response is the same in either case.

Call with teamname

1
2

curl https://api.bitbucket.org/2.0/teams/1team/members

Call with UUID for team object

1
2

curl https://api.bitbucket.org/2.0/teams/%7Baa559944-83c9-4963-a9a8-69ac8d9cf5d2%7D/members

Response

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

{
 "page": 1,
 "pagelen": 50,
 "size": 2,
 "values": [
 {
 "created_on": "2011-12-20T16:34:07.132459+00:00",
 "display_name": "tutorials account",
 "links": {
 "avatar": {
 "href": "https://bitbucket.org/account/tutorials/avatar/32/"
 },
 "followers": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/followers"
 },
 "following": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/following"
 },
 "hooks": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/hooks"
 },
 "html": {
 "href": "https://bitbucket.org/tutorials/"
 },
 "repositories": {
 "href": "https://api.bitbucket.org/2.0/repositories/tutorials"
 },
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials"
 },
 "snippets": {
 "href": "https://api.bitbucket.org/2.0/snippets/tutorials"
 }
 },
 "location": null,
 "type": "user",
 "username": "tutorials",
 "nickname": "tutorials",
 "account_status": "active",
 "uuid": "{c788b2da-b7a2-404c-9e26-d3f077557007}",
 "website": "https://tutorials.bitbucket.org/"
 },
 {
 "created_on": "2013-12-10T14:44:13+00:00",
 "display_name": "Dan Stevens [Atlassian]",
 "links": {
 "avatar": {
 "href": "https://bitbucket.org/account/dans9190/avatar/32/"
 },
 "followers": {
 "href": "https://api.bitbucket.org/2.0/users/dans9190/followers"
 },
 "following": {
 "href": "https://api.bitbucket.org/2.0/users/dans9190/following"
 },
 "hooks": {
 "href": "https://api.bitbucket.org/2.0/users/dans9190/hooks"
 },
 "html": {
 "href": "https://bitbucket.org/dans9190/"
 },
 "repositories": {
 "href": "https://api.bitbucket.org/2.0/repositories/dans9190"
 },
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/dans9190"
 },
 "snippets": {
 "href": "https://api.bitbucket.org/2.0/snippets/dans9190"
 }
 },
 "location": null,
 "type": "user",
 "username": "dans9190",
 "nickname": "dans9190",
 "account_status": "active",
 "uuid": "{1cd06601-cd0e-4fce-be03-e9ac226978b7}",
 "website": ""
 }
 ]
}

Standardized error responses

The 2.0 API standardizes the error response layout. The 2.0 API serves a JSON
object along with the appropriate HTTP status code. The JSON object provides a
detailed problem description.

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

{
 "type": "error",
 "error": {
 "message": "Bad request",
 "fields": {
 "src": [
 "This field is required."
 ]
 },
 "detail": "You must specify a valid source branch when creating a pull request.",
 "id": "d23a1cc5178f7637f3d9bf2d13824258",
 "data": {
 "extra": "Optional, endpoint-specific data to further augment the error."
 }
 }
}

This object contains an error element which contains the following nested
elements:

ElementDescription
messageA short description of the problem. This element is always present. Its value may be localized.
fieldsThis optional element is used in response to POST or PUT operations in which clients have provided invalid input. It contains a list of one or more client-provided fields that failed validation. The values may be localized.
detailAn optional detailed explanation of the failure. Its value may be localized.
idAn optional unique error identifier that identifies the error in Bitbucket's logging system. If you feel you hit a bug in an API and this field is provided, please mention it if you decide to contact support as it will greatly help us narrow down the problem.

Standard ISO-8601 timestamps

All 2.0 APIs use standardized ISO-8601 timestamps. In most cases, our APIs return UTC timestamps and for these, the timezone offset part will be 00:00. In rare cases where the original localized timestamp has significance, the timezone offset may identify the event's original timezone.

Cors and hypermedia

This section describes Cross-origin resource sharing (CORS), what content types we support in requests and responses, and hyperlinking resources in each json responses.

CORS

Supported content types

Resource links

Cors

The Bitbucket API supports Cross-origin resource sharing to allow requests for restricted resources across domains. For more information you can refer to:

Wikipedia article on CORS

W3C CORS recommendation

Sending a general request from the api to bitbucket.com:

curl -i https://api.bitbucket.org -H "origin: http://bitbucket.com"

Gives this result:

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

HTTP/1.1 302 FOUND
Server: nginx/1.6.2
Vary: Cookie
Cache-Control: max-age=900
Content-Type: text/html; charset=utf-8
Strict-Transport-Security: max-age=31536000
Date: Tue, 21 Jun 2016 17:54:37 GMT
Location: http://confluence.atlassian.com/x/IYBGDQ
X-Served-By: app-110
X-Static-Version: 2c820eb0d2b3
ETag: "d41d8cd98f00b204e9800998ecf8427e"
X-Content-Type-Options: nosniff
X-Render-Time: 0.00379920005798
Connection: Keep-Alive
X-Version: 2c820eb0d2b3
X-Frame-Options: SAMEORIGIN
X-Request-Count: 383
X-Cache-Info: cached
Content-Length: 0

Sending the same request with the CORS check -X OPTIONS in the call:

curl -i https://api.bitbucket.org -H "origin: http://bitbucket.com" -X OPTIONS

Gives this result:

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

HTTP/1.1 302 FOUND
Server: nginx/1.6.2
Vary: Cookie
Cache-Control: max-age=900
Content-Type: text/html; charset=utf-8
Access-Control-Expose-Headers: Accept-Ranges, Content-Encoding, Content-Length, Content-Type, ETag, Last-Modified
Strict-Transport-Security: max-age=31536000
Date: Tue, 21 Jun 2016 18:04:30 GMT
Access-Control-Max-Age: 86400
Location: http://confluence.atlassian.com/x/IYBGDQ
X-Served-By: app-111
Access-Control-Allow-Origin: *
X-Static-Version: 2c820eb0d2b3
ETag: "d41d8cd98f00b204e9800998ecf8427e"
X-Content-Type-Options: nosniff
X-Render-Time: 0.00371098518372
Connection: keep-alive
X-Version: 2c820eb0d2b3
X-Frame-Options: SAMEORIGIN
X-Request-Count: 357
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Accept, Authorization, Content-Type, If-Match, If-Modified-Since, If-None-Match, If-Unmodified-Since, Origin, Range, X-CsrftokenX-Requested-With
X-Cache-Info: not cacheable; request wasn't a GET or HEAD
Content-Length: 0

Supported content types

The default and primary content type for 2.0 APIs is JSON. This applies both to responses from the server and to the request bodies provided by the client.

Unless documented otherwise, whenever creating a new (POST) or modifying an existing (PUT) object, your client must provide the object's normal representation. Not every object element can be mutated. For example, a repository's created_on date is an auto-generated, immutable field. Your client can omit immutable fields from a request body.

In some cases, a resource might also accept regular application/x-www-url-form-encoded POST and PUT bodies. Such bodies can be more convenient in scripts and command line usage. Requests bodies can contain contain nested elements or they can be flat (without nested elements). Clients can send flat request bodies as either as application/json or as application/x-www-url-form-encoded. Nested objects always require JSON.

Resource links

Every 2.0 object contains a links element that points to related resources or alternate representations. Use links to quickly discover and traverse to related objects. Links serve a "self-documenting" function for each endpoint. For example, the following request for a specific user:

$ curl https://api.bitbucket.org/2.0/users/tutorials

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
 "username": "tutorials",
 "nickname": "tutorials",
 "account_status": "active",
 "website": "https://tutorials.bitbucket.org/",
 "display_name": "tutorials account",
 "uuid": "{c788b2da-b7a2-404c-9e26-d3f077557007}",
 "links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials"
 },
 "repositories": {
 "href": "https://api.bitbucket.org/2.0/repositories/tutorials"
 },
 "html": {
 "href": "https://bitbucket.org/tutorials"
 },
 "followers": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/followers"
 },
 "avatar": {
 "href": "https://bitbucket-assetroot.s3.amazonaws.com/c/photos/2013/Nov/25/tutorials-avatar-1563784409-6_avatar.png"
 },
 "following": {
 "href": "https://api.bitbucket.org/2.0/users/tutorials/following"
 }
 },
 "created_on": "2011-12-20T16:34:07.132459+00:00",
 "location": "Santa Monica, CA",
 "type": "user"
}

Links can be actual REST API resources or they can be informational. In this example, informative resources include the user's avatar and the HTML URL for the user's Bitbucket account. Your client should avoid hardcoding an API's URL and instead use the URLs returned in API responses.

A link's key is its 
rel (relationship) attribute and it contains a mandatory href element. For example, the following link:

1
2
3
4

"self": {
 "href": "https://api.bitbucket.org/api/2.0/users/tutorials"
}

The rel for this link is self and the href is https://api.bitbucket.org/api/2.0/users/tutorials. A single rel key can contain an list (array) of href objects. Your client should anticipate that any rel key can contain one or more href objects.

Finally, links can also contain optional elements. Two common optional elements are the name element and the title element. They are often used to disambiguate links that share the same rel key. In the example below, the repository object that contains a clone link with two href objects. Each object contains the optional name element to clarify its use.

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

"links": {
 "self": {
 "href": "https://api.bitbucket.org/2.0/repositories/evzijst/bitbucket"
 },
 "clone": [
 {
 "href": "https://api.bitbucket.org/evzijst/bitbucket.git",
 "name": "https"
 },
 {
 "href": "ssh://git@bitbucket.org/erik/bitbucket.git",
 "name": "ssh"
 }
 ],
 ...
}

Links can support URI Templates; Those that do contain a 
"templated": "true" element.

Integrating with Bitbucket Cloud

You can use Forge
or Atlassian Connect
to build apps which can connect with the Bitbucket UI and your own application set. An app could
be an integration with another existing service, new features for the Atlassian
application, or even a new product that runs within the Atlassian application.

For complete information see:
integrating with Bitbucket Cloud

Rate this page:

Unusable

Poor

Okay

Good

Excellent
