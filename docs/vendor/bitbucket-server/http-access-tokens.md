<!-- Source: https://confluence.atlassian.com/bitbucketserver/http-access-tokens-939515499.html (Bitbucket Data Center 10.5)
     Fetched 2026-11-23 as the page's rendered text (navigation removed). Atlassian's text, vendored for reference. -->

HTTP access tokens in Bitbucket Data Center can be created for users as well as for teams working in projects and repositories. Use them in place of passwords for Git over HTTPS, or to authenticate when using the Bitbucket REST API.
You can't use a token to log in via the web UI.
You can't use a token to perform changes on behalf of a user (for example, create new tokens or update user account details).
You can’t use a token to merge a pull request as the merge creates a commit (only users with a valid e-mail address can create a commit).
In Git and API calls, you can use the project and repository level token only with Bearer authentication. Don't use this token with a username in Basic authentication.
On this page: 
Related pages:
Managing HTTP access tokens
Controlling access to code
SSH access keys for system use
Create HTTP access tokens
To create an HTTP access token for your user account:
Go to Profile picture > Manage account > HTTP access tokens.
Select Create token. 
Set the token name, permissions, and expiry.
Create HTTP access tokens for projects or repositories
A Data Center license is required to create HTTP access tokens for projects and repositories. Get an evaluation license to try it out, or purchase a license now.
HTTP access tokens can be created for teams to grant permissions at the project or repository level rather than for specific users.
Starting from Bitbucket 8.8, project admins can also restrict repository admins from managing repository-level tokens using the Restrict changes to repository settings dropdown. Note that when project admins restrict changes, any existing access tokens added by the repository admins are not deleted automatically.
To create an HTTP access token for a project or repository (requires project or repository admin permissions):
From either the Project or Repository settings, select HTTP access tokens.
Select Create token.
Set the token name, permissions, and expiry.
Permissions
Permissions restrict what a token can do. As tokens are like passwords, your token’s permissions will be set at your current level of access by default. We recommend, however, restricting your token’s permissions to only the level it will need.
Here are the permission combinations you can assign to a token:
Repo permissions are inherited from the project permissions
A token’s repository permission must be as high as its project permission.
If you give a token project write permission, you cannot give it only repository read permissions (it must be write-level or higher).
For repository tokens, you cannot give it any project-level permissions.
 | 
 | Project read | Project write | Project admin
 | Repository read | Pull and clone repositories | Combination not possible | Combination not possible
 | Repository write | 
 Perform pull request actions
 Push, pull, and clone repositories (except merge) | 
 Perform pull request actions
 Push, pull, and clone repositories (except merge) | Combination not possible
 | Repository admin | Perform pull request actions
 Update repository settings and permissions
 Push, pull, and clone repositories (except merge) | Perform pull request actions
 Update repository settings and permissions
 Push, pull, and clone repositories (except merge) | 
 Perform pull request actions
 Update repository settings and permissions
 Update project settings and permissions
 Push, pull, clone, and fork repositories (except merge)
 Create repositories
You can modify a token’s permissions or revoke a token from within the HTTP access tokens page list.
Expiry
For added security, when you’re creating a token you can also set it to automatically expire. This is optional, but if your administrator has made this a requirement you’ll need to choose an expiry date that’s within the limits they’ve set.
Once a token has been created, its expiry date cannot be changed. You can see the expiry dates for all your tokens in the HTTP access tokens page list.
Using HTTP access tokens
Map one token per integration
HTTP access tokens are a secure way to use scripts and to integrate external applications with Bitbucket. We recommend only mapping one token per integration. This way, if the system is compromised, you can simply revoke the token and not affect other integrations.
Project and repository tokens
When using basic auth with project or repository tokens, you must set the username to x-token-auth
For Git operations, you can use your user's access token as a substitute for your password. For example, to clone using an access token you can enter:
> git clone https://example.com/scm/projectname/teamsinspace.git
Cloning into 'teamsinspace'...
Username for 'https://example.com':username
Password for 'https://username@example.com':MDM0MjM5NDc2MDxxxxxxxxxxxxxxxxxxxxx
Or using Basic Auth:
git clone https://username:MDM0MjM5NDc2MDxxxxxxxxxxxxxxxxxxxxx@example.com/
scm/projectname/teamsinspace.git
In addition, for REST operations, you can use Basic Auth:
curl -u username:MDM0MjM5NDc2MDxxxxxxxxxxxxxxxxxxxxx https://example.com/rest/api/latest/resource/path
For project or repository tokens, can either use Basic Auth or you can use Bearer Auth without the username:
curl -H 'Authorization: Bearer MDM0MjM5NDc2MDxxxxxxxxxxxxxxxxxxxxx' https://example.com/rest/api/latest/resource/path
Bearer Auth example for HTTP Git operations:
git clone -c http.extraHeader='Authorization: Bearer MDM0MjM5NDc2MDxxxxxxxxxxxxxxxxxxxxx' https://bitbucketserver.com/scm/projectname/teamsinspace.git
There are known issues with Bearer Auth and Git LFS (BSERV-13613). Basic Auth is recommended when working with Git LFS. 
