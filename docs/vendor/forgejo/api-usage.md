<!-- vendored from https://forgejo.org/docs/latest/user/api-usage/, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

API Usage | Forgejo – Beyond coding. We forge.

Docs v17.0           Next
  Latest (v17.0.2)
  LTS (v15.0.12)

  News
  Releases
  FAQ
  Donate
         Source code
        RSS feed

                  Forgejo Administrator Guide                 Advanced              Adopt existing git directories
  Incoming Email
  Instance Commit Signing
  Interface customization
  JWT Key Management and Rotation
  Moderation tools
  OAuth2 provider
  OIDC Group Mappings
  Search Engines Indexation
  Soft-Quota

  Command-line interface
  Configuration Cheat Sheet
     Forgejo Actions administrator guide               Forgejo Runner Configuration
  Forgejo Runner Registration
    Installation              Installation from binary
  Installation from packaging
  Installation with Docker

  Securing Forgejo Actions Deployments
  Utilizing Docker within Actions

     Installation               Database Preparation
  Installation from binary
  Installation with Docker

  Release schedule
    Setup              Authentication
  Backups and Backup Restoration
  Email setup
  Recommended Settings and Tips
  Reverse proxy
  Storage settings

    Troubleshooting              Logging Configuration
  Profiling
  Seek Assistance

  Upgrade guide

     Forgejo Contributor Guide               Architecture
  Bugs, features and discussions
  Code of Conduct and moderation team
  code.forgejo.org
  Compiling from source
  Dependency management
  Developer Certificate of Origin (DCO)
  Development environment
  Finding what to work on in Forgejo
  Governance
  Hardware infrastructure
  Interface customization
     Localization               English base localization
  Weblate admin duties

  Managing dependencies between repositories
  Release management
  Repository Migration
  Secrets
  Static pages
  Testing
     Threat Analysis               Threat Analysis: Remote Login Propagation

  Web browser support
  Welcome to Forgejo

     Forgejo User Guide                 Actions              Forgejo Actions | Advanced features
  Forgejo Actions | Basic concepts
  Forgejo Actions | GitHub Actions
  Forgejo Actions | Quick start guide
  Forgejo Actions | Reference
  Forgejo Actions | Security
  Forgejo Actions | Security of Pull Requests
  Forgejo Actions | Security OpenID Connect
  Forgejo Actions | Troubleshooting
  Forgejo Actions | User guide
  Forgejo Actions | Using Actions

     API               API Usage
  Authorized Integrations
  Forgejo numbering scheme

    Authentication              Access Token Scope
  OAuth2 provider

    Collaboration              Automatically Linked References in Issues, Pull Requests, and Commit Messages
  Blocking a user
  Labels
  Profile customization
  Projects
  Pull requests and Git flow
  Repository Activity
  Repository Permissions

  Comment on issues and pull requests via email
  Forgejo Next
     Getting started               Email Settings
  Integrated Wiki
  The Basics of Issue Tracking
  Your First Repository

     Git CLI               AGit Workflow Usage
  Push Options

     Package Registry               Alpine Packages Repository
  ALT Package Registry
  Arch Package Registry
  Cargo Package Registry
  Chef Package Registry
  Composer Package Registry
  Conan Package Registry
  Conda Package Registry
  Container Registry
  CRAN Package Registry
  Debian Package Registry
  Generic Package Registry
  Go Package Registry
  Helm Chart Registry
  Maven Package Registry
  npm Package Registry
  NuGet Package Registry
  Pub Package Registry
  PyPI Package Registry
  RPM Package Registry
  RubyGems Packages Repository
  Swift Packages Repository
  Vagrant Packages Repository

    Repository              Blame File View
  Branch and tag protection
  FUNDING files
  Issue and Pull Request Templates
  Merge Message Templates
  Programming language detection
  README badges
  Tags and Releases
  Webhooks

  Repository Mirrors
    Search              Code Search
  Issue Search

## API Usage
     Enabling/configuring API access
  Authentication
  Generating and listing API tokens
  OAuth2 Provider
  More on the Authorization: header
  Sudo
  Pagination
  API Guide
  SDKs

#  API Usage
 The Forgejo API for all versions that have the same major number
(e.g. the major number of Forgejo 7.0.0 is 7) are compatible. There
are breaking changes (e.g. removal of an API endpoint) when the major
number changes and the release notes of this major version provide
explanations to help developers upgrade their software
accordingly. Read more about the Forgejo numbering scheme.

WarningThe following instructions use forgejo.example.org as placeholder for the Forgejo instance URL and other placeholders,
such as {yourusername} and {yourpassword}, which need to be replaced before running these commands.

## Enabling/configuring API access

By default, the API is enabled on Forgejo instances, but instance administrators may choose to disable it.

## Authentication

Forgejo supports these methods of API authentication:

HTTP basic authentication

Authorization: Bearer ... header in HTTP headers

Authorization: token ... header in HTTP headers

### Generating and listing API tokens

A new token can be generated with a POST request to
/users/:name/tokens.

Note that /users/:name/tokens is a special endpoint and requires you
to authenticate using BasicAuth and a password, as follows:

$ curl -H "Content-Type: application/json" -d '{"name":"test","scopes":["write:package"]}' -u {yourusername}:{yourpassword} https://forgejo.example.org/api/v1/users/{username}/tokens
{"id":1,"name":"test","sha1":"9fcb1158165773dd010fca5f0cf7174316c3e37d","token_last_eight":"16c3e37d","scopes":["write:package"]}

The keys of the JSON data object are name (the display name for the token) and scopes (list of Access Token scope names).

The sha1 (the token) is only returned once and is not stored in
plain-text. It will not be displayed when listing tokens with a GET
request; e.g.

$ curl --url https://{yourusername}:{yourpassword}@forgejo.example.org/api/v1/users/{username}/tokens
[{"name":"test","sha1":"","token_last_eight":"........"},{"name":"dev","sha1":"","token_last_eight":"........"}]

To use the API with basic authentication with two factor authentication (2FA)
enabled, you’ll need to send an additional header that contains the one-time password (6-digit rotating token).
An example of the header is X-Forgejo-OTP: 123456 where 123456
is where you’d place the code from your authenticator.
Here is how the request would look like in curl:

$ curl -H "X-Forgejo-OTP: 123456" --url https://{yourusername}:{yourpassword}@forgejo.example.org/api/v1/users/{username}/tokens

You can also create an API key token via your Forgejo installation’s web
interface: Settings | Applications | Generate New Token.

### OAuth2 Provider

Access tokens obtained from Forgejo’s OAuth2 provider are accepted by these methods:

Authorization: Bearer ... header in HTTP headers

token=... parameter in URL query string

access_token=... parameter in URL query string

#### More on the Authorization: header

For historical reasons, Forgejo needs the word token included before
the API key token in an authorization header, like this:

Authorization: token 65eaa9c8ef52460d22a93307fe0aee76289dc675

In a curl command, for instance, this would look like:

curl "https://forgejo.example.org/api/v1/repos/test1/test1/issues" \
    -H "accept: application/json" \
    -H "Authorization: token 65eaa9c8ef52460d22a93307fe0aee76289dc675" \
    -H "Content-Type: application/json" -d "{ \"body\": \"testing\", \"title\": \"test 20\"}" -i

As mentioned above, the token used is the same one you would use in
the token= string in a GET request.

### Sudo

The API allows admin users to sudo API requests as another user. Simply add either a sudo= parameter or Sudo: request header with the username of the user to sudo.

## Pagination

The API supports pagination. The page and limit parameters are used to specify the page number and the number of items per page. As well, the Link header is returned with the next, previous, and last page links if there are more than one page. The x-total-count is also returned to indicate the total number of items.

curl -v "https://forgejo.example.org/api/v1/repos/search?limit=1"
...
< link: <https://forgejo.example.org/api/v1/repos/search?limit=1&page=2>; rel="next",<https://forgejo.example.org/api/v1/repos/search?limit=1&page=5252>; rel="last"
...
< x-total-count: 5252

The default and maximum values for the page parameter can be obtained from the https://forgejo.example.org/api/v1/settings/api endpoint.

$ curl https://forgejo.example.org/api/v1/settings/api
{
  "max_response_items": 50,
  "default_paging_num": 30,
  "default_git_trees_per_page": 1000,
  "default_max_blob_size": 10485760
}

## API Guide

API Reference guide is auto-generated by Swagger and available on:
https://forgejo.example.org/api/swagger.
For instance, find the reference guide of Forgejo Next at https://try.next.forgejo.org/api/swagger.

The OpenAPI document is at:
https://forgejo.example.org/swagger.v1.json

## SDKs

Find clients using the API
and API client libraries
at the list of Delightful Forgejo.

This page's content is available under the Apache-2.0 license.
 It is derived from this original source.              Edit this page
##### Forgejo
    Releases
  News
  Fediverse (Mastodon)
  Chat room (Matrix)
  RSS feed

##### Community
    Code
  Governance
  Teams
  Identity proofs

##### Contribute
    Issue tracker
  Developer Matrix room
  Contributor guide
  Localization guide
  Donate via Liberapay

##### Resources
    Status (Forgejo)
  Status (Codeberg)
  Documentation
  Delightful Forgejo
  Professional services

##### Legal
    Imprint
  Privacy Policy
  Code of Conduct

Copyright © 2026 Forgejo authors. Content available under

CC BY-SA 4.0

, unless stated otherwise.

Forgejo mascot by David Revoy

,

CC BY 4.0

.
