<!-- vendored from https://docs.gitea.com/usage/webhooks, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

Webhooks | Gitea DocumentationSkip to contentTry Gitea Cloud ☁️ for 30 days → Accelerate your Development & Deploys!Gitea DocumentationDocsAPIRunnerCLIEnterpriseSearchCtrlKCancelSelect themeDarkLightAutoVersion29-dev28.4.01.27.31.26.41.25.51.24.71.23.81.22.6LanguageEnglish简体中文繁體中文Sign InMenuDocsAPIRunnerCLIEnterpriseWhat is Gitea?
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
ConfigurationConfiguring a webhook
Branch filters
Authorization header

DeliveryDelivery behavior
Delivery headers

EventsRepository Events
Issue Events
Pull Request Events
Workflow Events

Testing, recent deliveries, and replay
Administration notes

## On this page
Overview
ConfigurationConfiguring a webhook
Branch filters
Authorization header

DeliveryDelivery behavior
Delivery headers

EventsRepository Events
Issue Events
Pull Request Events
Workflow Events

Testing, recent deliveries, and replay
Administration notes

# Webhooks
Gitea can send outbound webhooks for repository activity. Repository webhooks are
configured at /:username/:reponame/settings/hooks by a repository admin.
Equivalent webhook pages also exist for organizations, users, and system
administration.

Webhook configuration is available at four scopes:

Repository webhooks: Trigger only for activity in one repository.

Organization webhooks: Trigger for activity in repositories owned by that
organization.

User webhooks: Trigger for activity in repositories owned by that user.

System webhooks: Trigger for all eligible activity on the instance.

Gitea also supports admin-defined default webhooks. These are not an extra
delivery scope. Instead, they are copied into newly created repositories and
then behave like ordinary repository webhooks.

Gitea supports these outgoing webhook integrations:

Gitea

Gogs

Slack

Discord

Dingtalk

Telegram

Microsoft Teams

Feishu

Matrix

Wechatwork

Packagist

The Gitea and Gogs webhook types send generic webhook payloads. The chat and
service integrations listed above transform the same internal event into a
service-specific request body.

## Configuration
Section titled “Configuration”
This section covers the webhook settings you choose when creating or editing a
webhook.

### Configuring a webhook
Section titled “Configuring a webhook”
When creating a webhook, the main options are:

Target URL: The endpoint that receives the delivery.

HTTP Method: Usually POST for generic webhooks.

POST Content Type: application/json or
application/x-www-form-urlencoded for generic webhooks.

Secret: Used to sign the raw request body with HMAC.

Authorization Header: Optional custom Authorization header to send with
each request.

Branch Filter: Optional glob filter for branch and tag related events.

Trigger On: Push Events, All Events, or a custom event selection.

Active: Whether the webhook is enabled.

Note
Older examples may still show a secret field inside the JSON payload. Current
Gitea versions do not send the webhook secret in the payload body. Always verify
the request by checking the signature headers instead.

### Branch filters
Section titled “Branch filters”
The branch filter uses glob syntax compatible with
github.com/gobwas/glob.

Empty, *, or ** matches everything.

A plain branch name such as main matches that branch.

Full refs such as refs/tags/v* are also supported.

Brace expressions such as {main,release/*} are supported.

The filter only applies to events that carry a git ref, such as create,
delete, and push.

Events without a ref, such as issues or releases, ignore the branch filter.

Examples:

main

{main,feature/*}

{refs/heads/feature/*,refs/tags/release/*}

### Authorization header
Section titled “Authorization header”
Gitea can be configured to send a custom
Authorization header
with each webhook delivery. This is independent from the webhook secret:

Use the secret to verify integrity with HMAC.

Use the Authorization header when the receiving endpoint requires
application-level authentication.

## Delivery
Section titled “Delivery”
This section describes how Gitea sends webhook deliveries and how receivers can
identify and verify them.

### Delivery behavior
Section titled “Delivery behavior”

Webhooks are delivered asynchronously over HTTP.

Generic Gitea and Gogs webhooks support POST and GET; POST is the
normal choice.

For POST requests, the payload can be sent either as JSON
(application/json) or as a form field named payload
(application/x-www-form-urlencoded).

Provider-specific integrations may use the HTTP method and body format
required by that provider.

### Delivery headers
Section titled “Delivery headers”
Every delivery includes a unique delivery ID and event headers. For
GitHub-compatible integrations, Gitea also sends the corresponding GitHub and
Gogs header names.

Header
Description

X-Gitea-Delivery
Unique delivery UUID for this attempt.

X-Gitea-Event
Normalized event name, such as push, issues, or pull_request.

X-Gitea-Event-Type
More specific event type, such as issue_assign or pull_request_review_comment.

X-Gitea-Signature
Hex-encoded HMAC-SHA256 of the raw request body, without a prefix.

X-Gitea-Hook-Installation-Target-Type
Where the webhook is defined: typically repository, organization, user, or system. Default webhooks are copied into repositories before delivery, so they are typically delivered as repository.

X-Gogs-Delivery, X-Gogs-Event, X-Gogs-Event-Type, X-Gogs-Signature
Compatibility headers with the same values as the Gitea variants.

X-GitHub-Delivery, X-GitHub-Event, X-GitHub-Event-Type
GitHub-style compatibility headers.

X-GitHub-Hook-Installation-Target-Type
GitHub-style compatibility header for the webhook scope.

X-Hub-Signature
GitHub-compatible HMAC-SHA1 header in the form sha1=<digest>.

X-Hub-Signature-256
GitHub-compatible HMAC-SHA256 header in the form sha256=<digest>.

If no secret is configured, the signature headers are still present, but their
digest values are empty.

#### Event versus Event-Type
Section titled “Event versus Event-Type”
Some Gitea webhook subscriptions are grouped together under one normalized event
name. For example, an issue assignment delivery uses the issue event group:

X-Gitea-Event: issuesX-Gitea-Event-Type: issue_assignX-GitHub-Event: issuesX-GitHub-Event-Type: issue_assign

Use X-Gitea-Event-Type when you need the exact trigger that fired the webhook.

#### Validating deliveries
Section titled “Validating deliveries”
Gitea signs the raw request body with your webhook secret. To validate a
delivery:

Read the request body exactly as it was received.

Compute the HMAC-SHA256 digest with your webhook secret.

Compare the result with X-Gitea-Signature or with the GitHub-compatible
X-Hub-Signature-256 header.

Use a constant-time comparison when possible.

Important details:

X-Gitea-Signature contains only the lowercase hexadecimal SHA-256 digest.

X-Hub-Signature-256 contains the same digest with a sha256= prefix.

X-Hub-Signature is also sent for compatibility and uses SHA-1.

The body must be verified before JSON parsing or any other modification.

##### PHP example
Section titled “PHP example”
The following example verifies a generic Gitea webhook sent as
application/json.

<?php
$secret = '123';
if ($_SERVER['REQUEST_METHOD'] !== 'POST') {    http_response_code(405);    exit('Only POST is allowed');}
$payload = file_get_contents('php://input');$signature = $_SERVER['HTTP_X_GITEA_SIGNATURE'] ?? '';
if ($payload === false || $signature === '') {    http_response_code(400);    exit('Missing payload or signature');}
$expected = hash_hmac('sha256', $payload, $secret);
if (!hash_equals($expected, $signature)) {    http_response_code(401);    exit('Invalid signature');}
$event = $_SERVER['HTTP_X_GITEA_EVENT'] ?? '';$eventType = $_SERVER['HTTP_X_GITEA_EVENT_TYPE'] ?? '';$data = json_decode($payload, true);
if (!is_array($data)) {    http_response_code(400);    exit('Invalid JSON payload');}
http_response_code(204);

## Events
Section titled “Events”
This section follows the same event-by-event style used by GitHub’s webhook
documentation: each event describes when it occurs and what the top-level
payload contains.

The event groups match the webhook settings UI: Repository Events,
Issue Events, Pull Request Events, and Workflow Events.

### Repository Events
Section titled “Repository Events”

create, delete, fork, push, wiki, repository, release, package, status

#### create
Section titled “create”
This event occurs when a branch or tag is created.

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

sha
string
Required. The object ID of the created reference.

ref
string
Required. The created branch or tag name.

ref_type
string
Required. The reference type, such as branch or tag.

repository
object
Required. The repository where the reference was created.

sender
object
Required. The user who created the reference.

#### delete
Section titled “delete”
This event occurs when a branch or tag is deleted.

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

ref
string
Required. The deleted branch or tag name.

ref_type
string
Required. The reference type, such as branch or tag.

pusher_type
string
Required. The actor type that deleted the ref. Current Gitea payloads use user.

repository
object
Required. The repository where the reference was deleted.

sender
object
Required. The user who deleted the reference.

#### fork
Section titled “fork”
This event occurs when a repository is forked.

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

forkee
object
Required. The newly created fork repository.

repository
object
Required. The original repository that was forked.

sender
object
Required. The user who created the fork.

#### push
Section titled “push”
This event occurs when commits are pushed to a branch or tag.

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

ref
string
Required. The full pushed ref, such as refs/heads/main.

before
string
Required. The commit SHA before the push.

after
string
Required. The commit SHA after the push.

compare_url
string
Required. URL to compare before and after.

commits
array
Required. Commits included in the push.

total_commits
integer
Required. Number of commits in the push.

head_commit
object
The most recent commit in the push.

repository
object
Required. The repository that received the push.

pusher
object
Required. The user who performed the push.

sender
object
Required. The user who triggered the webhook.

#### wiki
Section titled “wiki”
This event occurs when a wiki page is created, edited, or deleted.

Action type: created, edited, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The wiki page action.

repository
object
Required. The repository that owns the wiki.

sender
object
Required. The user who changed the wiki page.

page
string
Required. The wiki page name.

comment
string
The wiki commit message or comment.

#### repository
Section titled “repository”
This event occurs when a repository is created or deleted.

Action type: created, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The repository action.

repository
object
Required. The repository that was created or deleted.

organization
object
Present when the repository belongs to an organization.

sender
object
Required. The user who performed the action.

#### release
Section titled “release”
This event occurs when a release is published, updated, or deleted.

Action type: published, updated, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The release action.

release
object
Required. The release that was acted on.

repository
object
Required. The repository containing the release.

sender
object
Required. The user who performed the action.

#### package
Section titled “package”
This event occurs when a package is created or deleted.

Action type: created, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The package action.

repository
object
The repository associated with the package, when applicable.

package
object
Required. The package that was acted on.

organization
object
Present when the package owner is an organization.

sender
object
Required. The user who performed the action.

#### status
Section titled “status”
This event occurs when a commit status is created or updated through the API.

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

commit
object
The commit associated with the status.

context
string
Required. The status context, such as ci/build.

created_at
string
Required. The time the status was created.

description
string
Status description text.

id
integer
Required. The status identifier.

repository
object
Required. The repository containing the commit.

sender
object
Required. The user who created the status.

sha
string
Required. The commit SHA.

state
string
Required. The state, such as pending, success, error, or failure.

target_url
string
Target URL associated with the status.

updated_at
string
The time the status was last updated.

Unlike most other payloads, this event does not use an action field. The
state transition is represented by state.

### Issue Events
Section titled “Issue Events”

issues, issue_assign, issue_label, issue_milestone, issue_comment

#### issues
Section titled “issues”
This event occurs when an issue is opened, closed, reopened, edited, or deleted.

Action type: opened, closed, reopened, edited, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The issue action.

number
integer
Required. The issue number.

changes
object
Optional. Previous values for edited fields or label deltas.

issue
object
Required. The issue that was acted on.

repository
object
Required. The repository containing the issue.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the issue action, if applicable.

#### issue_assign
Section titled “issue_assign”
This event occurs when an issue is assigned or unassigned.

Action type: assigned, unassigned

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The assignment action.

number
integer
Required. The issue number.

changes
object
Optional. Previous values for edited fields or label deltas.

issue
object
Required. The issue that was acted on.

repository
object
Required. The repository containing the issue.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the issue action, if applicable.

#### issue_label
Section titled “issue_label”
This event occurs when issue labels are updated or cleared.

Action type: label_updated, label_cleared

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The label update action.

number
integer
Required. The issue number.

changes
object
Optional. Previous values for edited fields or label deltas.

issue
object
Required. The issue that was acted on.

repository
object
Required. The repository containing the issue.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the issue action, if applicable.

#### issue_milestone
Section titled “issue_milestone”
This event occurs when an issue is milestoned or demilestoned.

Action type: milestoned, demilestoned

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The milestone action.

number
integer
Required. The issue number.

changes
object
Optional. Previous values for edited fields or label deltas.

issue
object
Required. The issue that was acted on.

repository
object
Required. The repository containing the issue.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the issue action, if applicable.

#### issue_comment
Section titled “issue_comment”
This event occurs when a comment on an issue is created, edited, or deleted.

Action type: created, edited, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The comment action.

issue
object
Required. The issue that the comment belongs to.

pull_request
object
Present only when the comment is on a pull request timeline.

comment
object
Required. The comment that was created, edited, or deleted.

changes
object
Optional. Previous comment body when the action is edited.

repository
object
Required. The repository containing the issue.

sender
object
Required. The user who performed the action.

is_pull
boolean
Required. Whether the comment is on a pull request timeline.

### Pull Request Events
Section titled “Pull Request Events”

pull_request, pull_request_assign, pull_request_label, pull_request_milestone, pull_request_comment, pull_request_review, pull_request_review_approved, pull_request_review_rejected, pull_request_review_comment, pull_request_sync, pull_request_review_request

#### pull_request
Section titled “pull_request”
This event occurs when a pull request is opened, closed, reopened, edited, or deleted.

Action type: opened, closed, reopened, edited, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The pull request action.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was acted on.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the pull request action, if applicable.

review
object
Present for pull request review events.

#### pull_request_assign
Section titled “pull_request_assign”
This event occurs when a pull request is assigned or unassigned.

Action type: assigned, unassigned

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The assignment action.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was acted on.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the pull request action, if applicable.

review
object
Present for pull request review events.

#### pull_request_label
Section titled “pull_request_label”
This event occurs when pull request labels are updated or cleared.

Action type: label_updated, label_cleared

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The label update action.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was acted on.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the pull request action, if applicable.

review
object
Present for pull request review events.

#### pull_request_milestone
Section titled “pull_request_milestone”
This event occurs when a pull request is milestoned or demilestoned.

Action type: milestoned, demilestoned

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The milestone action.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was acted on.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the pull request action, if applicable.

review
object
Present for pull request review events.

#### pull_request_comment
Section titled “pull_request_comment”
This event occurs when a timeline comment on a pull request is created, edited, or deleted.

Action type: created, edited, deleted

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The comment action.

issue
object
Required. The related issue record for the pull request.

pull_request
object
Required. The pull request the timeline comment belongs to.

comment
object
Required. The comment that was created, edited, or deleted.

changes
object
Optional. Previous comment body when the action is edited.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

is_pull
boolean
Required. Always true for this event.

#### pull_request_review
Section titled “pull_request_review”
This is a subscription-only umbrella event in the webhook settings UI.

It does not have its own delivery payload. When selected, Gitea delivers the
more specific events pull_request_review_approved,
pull_request_review_rejected, and pull_request_review_comment.

#### pull_request_review_approved
Section titled “pull_request_review_approved”
This event occurs when a pull request review is submitted with approval.

Action type: reviewed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. Always reviewed.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was reviewed.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who submitted the review.

commit_id
string
The commit SHA associated with the review event, if applicable.

review
object
Required. The review payload. For this event, review.type is approved.

#### pull_request_review_rejected
Section titled “pull_request_review_rejected”
This event occurs when a pull request review is submitted with a rejection or a
request for changes.

Action type: reviewed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. Always reviewed.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was reviewed.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who submitted the review.

commit_id
string
The commit SHA associated with the review event, if applicable.

review
object
Required. The review payload. For this event, review.type is rejected.

#### pull_request_review_comment
Section titled “pull_request_review_comment”
This event occurs when a pull request review is submitted as a comment.

Action type: reviewed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. Always reviewed.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was reviewed.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who submitted the review.

commit_id
string
The commit SHA associated with the review event, if applicable.

review
object
Required. The review payload. For this event, review.type is comment.

#### pull_request_sync
Section titled “pull_request_sync”
This event occurs when a pull request is synchronized after new commits are pushed.

Action type: synchronized

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. Always synchronized.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was synchronized.

requested_reviewer
object
Present for review request events.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the synchronization.

commit_id
string
The commit SHA associated with the synchronization event, if applicable.

review
object
Present for pull request review events.

#### pull_request_review_request
Section titled “pull_request_review_request”
This event occurs when a reviewer is requested or a review request is removed.

Action type: review_requested, review_request_removed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The review request action.

number
integer
Required. The pull request number.

changes
object
Optional. Previous values for edited fields or label deltas.

pull_request
object
Required. The pull request that was acted on.

requested_reviewer
object
The reviewer that was requested or removed.

repository
object
Required. The repository containing the pull request.

sender
object
Required. The user who performed the action.

commit_id
string
The commit SHA associated with the pull request action, if applicable.

review
object
Present for pull request review events.

### Workflow Events
Section titled “Workflow Events”

workflow_run, workflow_job

#### workflow_run
Section titled “workflow_run”
This event occurs when a Gitea Actions workflow run changes status.

Action type: queued, waiting, in_progress, completed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The workflow run status transition.

workflow
object
Required. The workflow definition.

workflow_run
object
Required. The workflow run that was acted on.

pull_request
object
Present when the workflow run is associated with a pull request.

organization
object
Present when the repository owner is an organization.

repository
object
Required. The repository containing the workflow.

sender
object
Required. The user who triggered the workflow run update.

#### workflow_job
Section titled “workflow_job”
This event occurs when a Gitea Actions workflow job changes status.

Action type: queued, waiting, in_progress, completed

##### Payload parameters
Section titled “Payload parameters”

Name
Type
Description

action
string
Required. The workflow job status transition.

workflow_job
object
Required. The workflow job that was acted on.

pull_request
object
Present when the workflow job is associated with a pull request.

organization
object
Present when the repository owner is an organization.

repository
object
Required. The repository containing the workflow job.

sender
object
Required. The user who triggered the workflow job update.

## Testing, recent deliveries, and replay
Section titled “Testing, recent deliveries, and replay”
Each webhook page includes:

Test Delivery, which sends a synthetic push event for the repository.

Recent Deliveries, which shows request and response details.

Redelivery, which replays an earlier webhook delivery.

If the repository has no commits yet, the test delivery uses a generated fake
commit so the webhook can still be exercised.

## Administration notes
Section titled “Administration notes”
Administrators can further control webhook delivery with instance settings such
as host allow lists, delivery timeouts, and cleanup policies. See the
Webhook section of the configuration cheat sheet.

Edit pagePreviousCode OwnersNextMigrationCommunity
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
