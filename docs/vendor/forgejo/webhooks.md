<!-- vendored from https://forgejo.org/docs/latest/user/webhooks/, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

Webhooks | Forgejo – Beyond coding. We forge.

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

## Webhooks
     Raw event as JSON or FORM payload:
  Dedicated integration:
  Generic notification messages in chosen channel/chat:
  Event information
  Example
  Authorization header

#  Webhooks
 Forgejo supports webhooks for repository events. This can be configured on the settings
page /:username/:reponame/settings/hooks by a repository admin. Webhooks can also be configured on a per-organization and whole-system basis (default webhooks on repository creation or instance-wide webhooks).
The currently supported webhook types are:

### Raw event as JSON or FORM payload:

Forgejo (GET or POST)

Gitea (GET or POST)

Gogs (POST)

### Dedicated integration:

Packagist: ask Packagist to refresh the given package

SourceHut Builds: submit build manifests (only on push)

### Generic notification messages in chosen channel/chat:

Matrix

Slack

Discord

Dingtalk

Telegram

Microsoft Teams

Feishu / Lark Suite

WeCom (Wechat Work)

These channels don’t implement all webhook types.
Those types that are implemented have to be manually tested.
Try them and open an issue if they do not work for your use case.

### Event information

The following is an example of event information that will be sent by Forgejo to
a Payload URL:

X-Forgejo-Delivery: f6266f16-1bf3-46a5-9ea4-602e06ead473
X-Forgejo-Event: push
X-GitHub-Delivery: f6266f16-1bf3-46a5-9ea4-602e06ead473
X-GitHub-Event: push
X-Gogs-Delivery: f6266f16-1bf3-46a5-9ea4-602e06ead473
X-Gogs-Event: push
X-Gitea-Delivery: f6266f16-1bf3-46a5-9ea4-602e06ead473
X-Gitea-Event: push

{
  "ref": "refs/heads/develop",
  "before": "28e1879d029cb852e4844d9c718537df08844e03",
  "after": "bffeb74224043ba2feb48d137756c8a9331c449a",
  "compare_url": "http://localhost:3000/forgejo/webhooks/compare/28e1879d029cb852e4844d9c718537df08844e03...bffeb74224043ba2feb48d137756c8a9331c449a",
  "commits": [
    {
      "id": "bffeb74224043ba2feb48d137756c8a9331c449a",
      "message": "Webhooks Yay!",
      "url": "http://localhost:3000/forgejo/webhooks/commit/bffeb74224043ba2feb48d137756c8a9331c449a",
      "author": {
        "name": "Forgejo",
        "email": "someone@forgejo.org",
        "username": "forgejo"
      },
      "committer": {
        "name": "Forgejo",
        "email": "someone@forgejo.org",
        "username": "forgejo"
      },
      "timestamp": "2017-03-13T13:52:11-04:00"
    }
  ],
  "repository": {
    "id": 140,
    "owner": {
      "id": 1,
      "login": "forgejo",
      "full_name": "Forgejo",
      "email": "someone@forgejo.org",
      "avatar_url": "https://localhost:3000/avatars/1",
      "username": "forgejo"
    },
    "name": "webhooks",
    "full_name": "forgejo/webhooks",
    "description": "",
    "private": false,
    "fork": false,
    "html_url": "http://localhost:3000/forgejo/webhooks",
    "ssh_url": "ssh://forgejo@localhost:2222/forgejo/webhooks.git",
    "clone_url": "http://localhost:3000/forgejo/webhooks.git",
    "website": "",
    "stars_count": 0,
    "forks_count": 1,
    "watchers_count": 1,
    "open_issues_count": 7,
    "default_branch": "master",
    "created_at": "2017-02-26T04:29:06-05:00",
    "updated_at": "2017-03-13T13:51:58-04:00"
  },
  "pusher": {
    "id": 1,
    "login": "forgejo",
    "full_name": "Forgejo",
    "email": "someone@forgejo.org",
    "avatar_url": "https://localhost:3000/avatars/1",
    "username": "forgejo"
  },
  "sender": {
    "id": 1,
    "login": "forgejo",
    "full_name": "Forgejo",
    "email": "someone@forgejo.org",
    "avatar_url": "https://localhost:3000/avatars/1",
    "username": "forgejo"
  }
}

### Example

This is an example of how to use webhooks to run a PHP script upon push requests to the repository.
In your repository Settings, under Webhooks, set up a Forgejo webhook as follows:

Target URL: http://example.com/webhook.php

HTTP Method: POST

POST Content Type: application/json

Secret: 123

Trigger On: Push Events

Active: Checked

Now, on your server, create the PHP file webhook.php.

<?php

$secret_key = '123';

// check for POST request
if ($_SERVER['REQUEST_METHOD'] != 'POST') {
    error_log('FAILED - not POST - '. $_SERVER['REQUEST_METHOD']);
    exit();
}

// get content type
$content_type = isset($_SERVER['CONTENT_TYPE']) ? strtolower(trim($_SERVER['CONTENT_TYPE'])) : '';

if ($content_type != 'application/json') {
    error_log('FAILED - not application/json - '. $content_type);
    exit();
}

// get payload
$payload = trim(file_get_contents("php://input"));

if (empty($payload)) {
    error_log('FAILED - no payload');
    exit();
}

// get header signature
$header_signature = isset($_SERVER['HTTP_X_FORGEJO_SIGNATURE']) ? $_SERVER['HTTP_X_FORGEJO_SIGNATURE'] : '';

if (empty($header_signature)) {
    error_log('FAILED - header signature missing');
    exit();
}

// calculate payload signature
$payload_signature = hash_hmac('sha256', $payload, $secret_key, false);

// check payload signature against header signature
if ($header_signature !== $payload_signature) {
    error_log('FAILED - payload signature');
    exit();
}

// convert json to array
$decoded = json_decode($payload, true);

// check for json decode errors
if (json_last_error() !== JSON_ERROR_NONE) {
    error_log('FAILED - json decode - '. json_last_error());
    exit();
}

// success, do something

There is a Test Delivery button in the webhook settings that allows testing the configuration as well as a list of the most Recent Deliveries.

### Authorization header

Forgejo webhooks can be configured to send an authorization header to the target.

The authentication string is stored encrypted in the database.

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
