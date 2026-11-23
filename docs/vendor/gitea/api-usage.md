<!-- vendored from https://docs.gitea.com/development/api-usage, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

API Usage | Gitea DocumentationSkip to contentTry Gitea Cloud ☁️ for 30 days → Accelerate your Development & Deploys!Gitea DocumentationDocsAPIRunnerCLIEnterpriseSearchCtrlKCancelSelect themeDarkLightAutoVersion29-dev28.4.01.27.31.26.41.25.51.24.71.23.81.22.6LanguageEnglish简体中文繁體中文Sign InMenuDocsAPIRunnerCLIEnterpriseWhat is Gitea?
InstallationCompared to other Git hosting
Database Preparation
Installation from binary
Installation from Package
Installation from source
Run as a Linux service
Running Gitea with SELinux
Register as a Windows service
Installation with Docker (rootless)
Installation with Podman Quadlet
Installation with Docker
Install on Kubernetes
Upgrade from an old Gitea

AdministrationGitea Command Line
Authentication
Environment variables
Backup and Restore
Email setup
Git LFS setup
HTTPS Setup
Fail2ban Setup
Reverse Proxies
Embedded data extraction tool
Configuration Cheat Sheet
Logging Configuration
Audit Logging
Mail templates
Repository indexer
GPG/SSH Commit Signatures
External renderers
Search Engines Indexation
Customizing Gitea
Adding Legal Pages

UsageRepositoryMarkdown
Blame File View
Push
Clone filters (partial clone)
Code Owners
Webhooks
Migration
Profile READMEs
Template Repository
Mirror Repository
Incoming Email

Issues & Pull RequestsAGit
Labels
Pull Request
Automatically Linked References
Issue and Pull Request Templates
Merge Message templates

ActionsOverview
Quick Start
Gitea Runner
Variables
Actions job token permissions (`GITEA_TOKEN`)
Secrets
Scoped Workflows
Artifacts
Badge
Compared to GitHub Actions
Design of Gitea Actions
Frequently Asked Questions

PackagesOverview
Storage
Alpine Package Registry
Arch package registry
Cargo Package Registry
Chef Package Registry
Composer Package Registry
Conan Package Registry
Conda Package Registry
Container Registry
CRAN Package Registry
Debian Package Registry
Go Package Registry
Helm Chart Registry
Maven Package Registry
NPM Package Registry
NuGet Package Registry
Pub Package Registry
PyPI Package Registry
RPM Package Registry
RubyGems Package Registry
Swift Package Registry
Terraform State Registry
Vagrant Package Registry
Generic Package Registry

Access ControlPermissions
Blocking a user
Protected branches
Protected tags

User SettingMulti-factor Authentication (MFA)

DevelopmentAPI Usage
OAuth2 Provider
Migration Interfaces
Integrations

HelpFAQ
Support Options

Select themeDarkLightAutoVersion29-dev28.4.01.27.31.26.41.25.51.24.71.23.81.22.6LanguageEnglish简体中文繁體中文Sign InOn this pageOverview
Enabling/configuring API access
Authentication
Generating and listing API tokensUser Interface
Token API

OAuth2 ProviderMore on the Authorization: header

Pagination
API Guide
Sudo
SDKs

## On this page
Overview
Enabling/configuring API access
Authentication
Generating and listing API tokensUser Interface
Token API

OAuth2 ProviderMore on the Authorization: header

Pagination
API Guide
Sudo
SDKs

# API Usage

## Enabling/configuring API access
Section titled “Enabling/configuring API access”
By default, ENABLE_SWAGGER is true, and MAX_RESPONSE_ITEMS is set to 50. See Config Cheat Sheet for more information.

## Authentication
Section titled “Authentication”
Gitea supports these methods of API authentication:

HTTP basic authentication

token=... parameter in URL query string

access_token=... parameter in URL query string

Authorization: token ... header in HTTP headers

HTTP signatures using an SSH public key or SSH certificate

All of these methods accept the same API key token type. You can
better understand this by looking at the code – as of this writing,
Gitea parses queries and headers to find the token in
modules/auth/auth.go.

Gitea can also authenticate API requests using an SSH key or SSH certificate via
HTTP signatures. The SSH public key (or certificate) must be registered to the
user account in Gitea, and the client signs requests with the corresponding
private key. The client signs requests using the SSH private key following the
draft-cavage-http-signatures
specification (not RFC 9421). The signature is sent in the Signature header,
and SSH certificates additionally include an x-ssh-certificate header. The
official go-sdk implements this flow if you
need a reference implementation.

## Generating and listing API tokens
Section titled “Generating and listing API tokens”
API tokens can be created either in the user interface or via the API. Tokens have by default limited permissions and it is important to create tokens with the correct permissions for your task.

### User Interface
Section titled “User Interface”
Tokens can be created via the Manage Access Tokens dialog, accessed via User Settings / Applications or via the link gitea-domain.example/user/settings/applications. The interface allows you to create tokens and manage their permissions via the Select permissions sub-menu.

Once created, the token is displayed in a toast message above the Manage Access Tokens dialog. Please note, that you can view this toast only once and it is not possible to redisplay the token for security reasons.

### Token API
Section titled “Token API”
A new token can be generated with a POST request to
/users/:name/tokens.

Note that /users/:name/tokens is a special endpoint and requires you
to authenticate using BasicAuth and a password, as follows:

Terminal window$ curl -H "Content-Type: application/json" \-d '{"name":"test_token","scopes":["read:activitypub","read:issue", "write:misc", "read:notification", "read:organization", "read:package", "read:repository", "read:user"]}' \-u 'username:password' "https://gitea.your.host/api/v1/users/{username}/tokens"{"id":1,"name":"test_token","sha1":"9fcb1158165773dd010fca5f0cf7174316c3e37d","token_last_eight":"16c3e37d"}

The sha1 (the token) is only returned once and is not stored in
plain-text.  It will not be displayed when listing tokens with a GET
request; e.g.

Terminal window$ curl --url https://yourusername:password@gitea.your.host/api/v1/users/<username>/tokens[{"name":"test","sha1":"","token_last_eight:"........":},{"name":"dev","sha1":"","token_last_eight":"........"}]
/tokens[{"name":"test","sha1":"","token_last_eight:"........":},{"name":"dev","sha1":"","token_last_eight":"........"}]">
By default, this creates a token with very limited permissions. To complete your tasks, your token may require extra permissions. These permissions are created via the json variable scopes, which takes an array of permissions as strings. Eg.: "scopes":["all"]. Possible permissions, as reflected in the user interface are:

activitypub

admin

issue

misc

notification

organization

package

repository

user

Each permission may be set to read or write. write implies both Read & Write. To set permissions you write the permissions string as <read or write>:<permission name>, eg.: write:package or read:notification. A properly formatted API call may look like:

Terminal window$ curl -H "Content-Type: application/json" -d '{"name":"test", "scopes":["write:package", "read:notification"]}' -u username:password https://gitea.your.host/api/v1/users/<username>/tokens
/tokens">
Special permissions all may be specified as "scopes":["all"], which sets all permissions to both Read & Write.

To use the API with basic authentication with two factor authentication
enabled, you’ll need to send an additional header that contains the one
time password (6 digitrotating token).
An example of the header is X-Gitea-OTP: 123456 where 123456
is where you’d place the code from your authenticator.
Here is how the request would look like in curl:

Terminal window$ curl -H "X-Gitea-OTP: 123456" --url https://yourusername:yourpassword@gitea.your.host/api/v1/users/yourusername/tokens

You can also create an API key token via your Gitea installation’s web
interface: Settings | Applications | Generate New Token.

## OAuth2 Provider
Section titled “OAuth2 Provider”
Access tokens obtained from Gitea’s OAuth2 provider are accepted by these methods:

Authorization bearer ... header in HTTP headers

token=... parameter in URL query string

access_token=... parameter in URL query string

### More on the Authorization: header
Section titled “More on the Authorization: header”
For historical reasons, Gitea needs the word token included before
the API key token in an authorization header, like this:

Terminal windowAuthorization: token 65eaa9c8ef52460d22a93307fe0aee76289dc675

In a curl command, for instance, this would look like:

Terminal windowcurl "http://localhost:4000/api/v1/repos/test1/test1/issues" \    -H "accept: application/json" \    -H "Authorization: token 65eaa9c8ef52460d22a93307fe0aee76289dc675" \    -H "Content-Type: application/json" -d "{ \"body\": \"testing\", \"title\": \"test 20\"}" -i

As mentioned above, the token used is the same one you would use in
the token= string in a GET request.

## Pagination
Section titled “Pagination”
The API supports pagination. The page and limit parameters are used to specify the page number and the number of items per page. As well, the Link header is returned with the next, previous, and last page links if there are more than one pages. The x-total-count is also returned to indicate the total number of items.

Terminal windowcurl -v "http://localhost/api/v1/repos/search?limit=1"...< link: <http://localhost/api/v1/repos/search?limit=1&page=2>; rel="next",<http://localhost/api/v1/repos/search?limit=1&page=5252>; rel="last"...< x-total-count: 5252
; rel="next",; rel="last"...

## API Guide
Section titled “API Guide”
API Reference guide is auto-generated by swagger and available on:
https://gitea.your.host/api/swagger
or on the
Gitea instance

The OpenAPI document is at:
https://gitea.your.host/swagger.v1.json

## Sudo
Section titled “Sudo”
The API allows admin users to sudo API requests as another user. Simply add either a sudo= parameter or Sudo: request header with the username of the user to sudo.

## SDKs
Section titled “SDKs”

Official go-sdk

more

Edit pagePreviousMulti-factor Authentication (MFA)NextOAuth2 ProviderCommunity
Awesome Gitea
Stack Overflow
Discord
Forum
Twitter
Mastodon
Bluesky
Code
GitHub
Gitea
Tea CLI
More
Blog
Gitea Cloud
Enterprise
