<!-- vendored 2026-11-23 from https://support.atlassian.com/bitbucket-cloud/docs/set-up-and-use-custom-merge-checks/; text extracted from the HTML, not edited -->

Set up and use custom merge checks | Bitbucket Cloud | Atlassian Support

Skip to main content

Bitbucket Cloud
Documentation

Get started with Bitbucket Cloud

Get started with version control
Types of version control
DVCS workflows for Bitbucket
Find the command line
Install and set up Git
Git commands

Get started setting up a repository
Create a repository in Bitbucket Cloud
Import a repository
Clone a repository

Get started working on a repository
Push code to Bitbucket
Pull code from Bitbucket
Create a work item in Bitbucket Cloud
Create or edit a wiki page

Get started with branches and pull requests
Create and push a branch
Check out a branch in Bitbucket Cloud
Create a pull request for review
Review a pull request
Merge a pull request in Bitbucket Cloud

Join or create and manage workspaces in Bitbucket Cloud
What is a workspace?
Join or create a workspace
Grant access to a workspace 
Organize workspace members into groups
Control access to private content in a workspace

Access tokens for a workspace
Require access token expiry
Create an access token for a workspace
Workspace-level access token permissions
Using access tokens for a workspace
Revoke an access token for a workspace
Rotate an access token for a workspace
Change a workspace ID
Transfer repositories and groups to a workspace
Publishing a Website on Bitbucket Cloud

Set up and use custom merge checks

Set up and use packages
Working with the container registry
Set up and use immutable tags in the container registry
Working with the Apache Maven registry
Working with the npm registry
Working with the NuGet registry
Working with the PyPI registry
Signed system commits and requiring signed commits

Set up and work on repositories in Bitbucket Cloud
Search in Bitbucket Cloud

Set up your repositories
Create a repository
Add unversioned code to a repository
Import or convert code from an existing tool
Import a repository from GitHub or GitLab

Work on a repository
README content
Markup comments
Split a repository in two
Snippets overview
Clone a Git repository
Push updates to a repository

Branch or fork your repository
Branch a repository
Fork a repository
List branches in a repository
Check out a branch
Manage unmerged branches

Learn how to use commits
Add, edit, and commit to source files
Configure your DVCS username for commits
Repository tags
Use GPG keys to sign commits
Use SSH keys to sign commits

Use pull requests for code review
Create a pull request
Review code in a pull request
Check build status in a pull request
Merge a pull request
Resolve merge conflicts
Decline a pull request
Draft pull requests
Manage pull requests with merge queues
Use the AI writing assistant to help write or edit content

Use Smart Mirroring for Bitbucket Cloud
Work with Bitbucket Smart Mirroring
Troubleshoot Bitbucket Smart Mirroring

Manage large files with Git Large File Storage (LFS)
Use Git LFS with Bitbucket
Use Git LFS with existing Bitbucket repositories
Use BFG to migrate a repo to Git LFS
Current limitations for Git LFS with Bitbucket
Storage policy for Git LFS with Bitbucket
Workflow for Git feature branching
Limits for viewing content and diffs

Configure repository settings
Set repository privacy and forking options
Grant repository access to users and groups
Set up Repository Access keys on macOS
Set up Repository Access keys on Windows
Set up Repository Access keys on Linux
Managing multiple Repository Access keys on one device
Map existing commits to username aliases
Link to a web service
Transfer repository ownership
Reduce repository size
Maintain a Git repository
Delete a repository
Pull request and merge settings
Git fast forwards and branch management
Use branch permissions
Suggest or require checks before a merge
Exclude files from pull request diffs
Change the remote URL to your repository
Manage webhooks
Event payloads
Create and trigger a webhook tutorial
Require signed commits for a repository

Access tokens for a repository
Create an access token for a repository
Repository-level access token permissions
Using access tokens for a repository
Revoke an access token for a repository
Rotate an access token for a repository
Set up and use code owners
View your notifications
Code insights

Build, test, and deploy with Pipelines

Get started with Bitbucket Pipelines
Configure your first pipeline
View your pipeline
Specify dependencies in your Pipelines build
Limitations of Bitbucket Pipelines
Bitbucket Pipelines glossary

Use Pipelines in different software languages
Run Docker commands in Bitbucket Pipelines
Javascript (Node.js) with Bitbucket Pipelines
Java with Bitbucket Pipelines
Laravel with Bitbucket Pipelines
PHP with Bitbucket Pipelines
Python with Bitbucket Pipelines
Ruby with Bitbucket Pipelines
Use Docker images as build environments
Expression language

Access Pipelines deployment guides
Deploy to AWS S3
Deploy to AWS with CodeDeploy
Deploy to AWS with Elastic Beanstalk
Deploy to AWS EKS (Kubernetes)
Deploy a Lambda function update to AWS
Deploy to Amazon ECS
Deploy to Firebase
Deploy to Google Cloud
Deploy to Heroku
Deploy to Kubernetes
Deploy to Microsoft Azure
Deploy to npm
Deploy with pull requests
Deploy using SCP
Deploy build artifacts to Bitbucket Downloads
Publish and link your build artifacts
Build and push a Docker image to a container registry

Bitbucket Pipelines configuration reference
Global options
Git clone behavior
Cache, service container, step config, and export pipelines definitions
Docker image options
Pipeline start conditions
Parallel step options
Child pipeline step options
Enable and use Runtime v3
Stage options
Step options
Use glob patterns on the Pipelines yaml file
YAML anchors

Deployments
Deploy on AWS using Bitbucket Pipelines OpenID Connect
Set up and monitor deployments
Bitbucket deployment guidelines
Concurrency control
Set custom deployment permissions for your environments
Track deployment status on pull requests
Step metrics
Scheduled and manually triggered pipelines
Variables and secrets

Using SSH keys in Bitbucket Pipelines
Set up Pipelines SSH keys on macOS
Set up Pipelines SSH keys on Windows
Set up Pipelines SSH keys on Linux
Use multiple SSH keys in your pipeline
Caches
Pipeline artifacts
Databases and service containers

Use pipes in Bitbucket Pipelines
What are pipes?
Write a pipe for Bitbucket Pipelines
Advanced techniques for writing pipes

Integrations
Integrate Jira and Pipelines
Integrate Pipelines with resource servers using OIDC
Use AWS ECR images in Pipelines with OpenID Connect
Integrate Slack with Pipelines
Connect to third-party services
Rate limiting with different third-party providers

Testing
Get started with tests in Pipelines
View and add tags to your tests
Understand and manage flaky tests in Bitbucket Pipelines
Quarantining flaky tests
Cross-platform testing in Bitbucket Pipelines
AI-driven flaky test remediation
Create Jira work items directly from a test
Optimize a test
Remove a test

Runners
Adding a new runner in Bitbucket
Configure your runner in bitbucket-pipelines.yml
 Set up runners for Linux Docker
Set up runners for Linux Shell
Set up runners for Windows
Set up runners for MacOS
Configure a runner to use a proxy
Configure logging in self-hosted runners
Configure runner concurrency and inspect step queue
IP addresses for runners behind corporate firewalls
Use your Docker images in self-hosted runners
Autoscaler for Runners on Kubernetes
Push back to your repository
Share Pipelines Configurations
Dynamic pipelines

Migrate to Bitbucket Pipelines
How to migrate from Bamboo to Bitbucket Pipelines
How to migrate from Jenkins to Bitbucket Pipelines

Agentic Pipelines
Agentic Pipelines: Step-by-step guide
Agent configuration
Interacting with Bitbucket via MCP
Rovo Dev: Advanced agentic configuration
Authentication and security for Agentic Pipelines
Example agents
Pipelines Troubleshooter: Fixing failed steps using Agentic Pipelines
Claude: Advanced agentic configuration
Codex: Advanced agentic configuration
Default system agents for Agentic Pipelines
Third-party agent providers and Bitbucket Agentic Pipelines
Dynamic step conditions
Parent/Child pipelines
Variable sharing
YAML templating

Manage your plans and settings in Bitbucket Cloud
Manage your plan and billing
Log into or connect to Bitbucket Cloud

Configure SSH and two-step verification
Set up personal SSH keys on macOS
Set up personal SSH keys on Windows
Set up personal SSH keys on Linux
Managing multiple Bitbucket user SSH keys on one device
Enable two-step verification
Supported SSH key formats
Set up SSH with Sourcetree

Manage Bitbucket and account settings
Access your personal settings
Update your username
Delete an account
Set email aliases
Manage email notifications for watched objects
Organize groups for your repositories
Control access to your private content
Upgrade to Atlassian account
Keyboard shortcuts
Change your theme
Access tokens

API tokens
Using API tokens
Create an API token
API token permissions
Revoke an API token
Add an API token to Sourcetree or another application
IP addresses and domains to allowlist in your corporate firewall

Integrate Bitbucket Cloud with apps and other products

Integrate Bitbucket and Jira
Connect Bitbucket Cloud to Jira Cloud
Connect Bitbucket Cloud to Jira Data Center
Use Jira Cloud spaces in Bitbucket Cloud
Create Jira work items from Bitbucket
 Transition Jira work items during a pull request merge
Configure automatic team invitations
Enable Smart Commits
Use Smart Commits
Connect your AI coding tools to Bitbucket Cloud

Integrate Bitbucket and Compass
Connect Bitbucket Cloud to Compass
Link repositories to Compass components

Use Bitbucket Cloud with Marketplace apps
Bitbucket Cloud apps overview
Integrate Bitbucket Cloud with Slack
Integrate Trello boards in Bitbucket
Integrate another application through OAuth
Install Cloud IDE add-ons
Integrate your build system with Bitbucket Cloud
Hyperlink to source code in Bitbucket
Enable Bitbucket Cloud development mode

Use the Atlassian for VS Code extension
Get started with VS Code
Jira work items in VS Code
Bitbucket pull requests in VS Code
Bitbucket Cloud pipelines in VS Code

Security
Add and configure security with Snyk

Build third-party apps with Bitbucket Cloud REST API
API request limits

Use OAuth on Bitbucket Cloud
OAuth consumer examples

Use Bitbucket REST API version 1
groups Endpoint
group-privileges Endpoint
invitations Endpoint
users Endpoint - 1.0
invitations Resource

Get advisories and other resources for Bitbucket Cloud

Access security advisories for Bitbucket Cloud
Security Advisory: Changes to how apps are installed by URL
Security Advisory - 2016-06-17 - Password Resets

View end of support announcements for Bitbucket Cloud
End of support for AWS CodeDeploy app removal - 2019-12-03

Tutorials

Tutorial: Learn Bitbucket with Git
Create a Git repository
Copy your Git repository and add files
Pull changes from your Git repository on Bitbucket Cloud
Use a Git branch to merge a file

Tutorial: Learn Bitbucket with Sourcetree
Create a new repository
Copy your repository and add files
Pull changes from your repository on Bitbucket
Use Sourcetree branches to merge an update

Tutorial: Learn about Bitbucket pull requests
Create a repository (and add a reviewer)
Clone and make a change on a new branch
Create a pull request to merge your change

Create and manage projects in Bitbucket Cloud

Access tokens for a project
Create an access token for a project
Project-level access token permissions
Using access tokens for a project
Revoke an access token for a project
Rotate an access token for a project

Configure project settings
Set up Project Access keys on macOS
Set up Project Access keys on Windows
Set up Project Access keys on Linux
Managing multiple Project Access keys on one device
Add default reviewers to a project
Add repositories to a project
Configure a project's branching model
Find and share projects
Manage and edit existing projects
Set up a merge strategy for a project
Updating project details
Configure a project's branch restrictions
Configure project permissions for users and groups
Create a project
Require signed commits for a project

Cloud

Data Center

Set up and use custom merge checks

Bitbucket Cloud offers built-in branch restrictions that allow your project and repository teams to set up prerequisites that must be met before merge commands are allowed for pull requests. 
If you find that the branch restrictions we offer out of the box don’t suit your organisation’s unique workflows, Bitbucket Cloud also supports custom merge checks. 
The benefit of using app-based custom merge checks is that you are in complete control of the these checks, allowing you to create consistent, safe, and quality focussed workflows regardless of your requirements. 
On premium plans, you can also set and enforce these checks at workspace, project, or repository levels, allowing your organisation to enforce quality standards as broadly or specifically as needed. 
Before you begin

Custom merge checks are an advanced feature. To use custom merge checks, you must first build and install a custom merge check Forge app. 
Use our developer tutorial to learn how to create a custom merge check app. 
Alternatively, if you’d like to install an app created by a third party vendor, keep reading to learn how to install a ready-made app. 
Install and enable custom merge checks

Forge apps are not supported on personal workspaces. See set up a shared team workspace.

You must be a Workspace admin to configure and set up custom merge checks.
To configure custom merge checks:

Install a custom merge check app in your workspace via one of the following two options:

Install an app you or your team has created via the Forge CLI 
forge install command if you have the app source code. Refer to the our developer documentation for more details on creating custom merge check apps. If you are looking for specific details about the Install command, refer to Install command.

Install an app that someone else has created via a direct distribution link given to you by the app developer. Refer to the Distribute your apps developer doc for more details.

Select Settings in the upper-right corner of the top navigation bar and select Workspace settings.

Select Custom merge checks under Workflow on the left navigation bar.

Select Enable to enable the custom merge check feature for your workspace.

Once enabled, all custom merge checks available from the installed forge apps will be displayed in Custom merge checks page under Workflow in workspace, project and repository settings pages.
Set up custom merge checks at the repository level
Checks enabled at the repository level will run against all pull requests contained within the repository, provided the pull request target branch matches a configured checks enabled branch.

You must be a repository admin to administer the custom checks at the repository level.
To administer custom merge checks associated with the repository:

Next to the name of your repository in the left sidebar, select More actions (…), and then select Settings.

Select Custom merge checks under Workflow on the left sidebar.

If you do not see a table, it means you do not have any Forge Apps installed which provide Custom Merge Checks. You will have to install an app into your workspace prior.

Select Add check on a Forge app shown in the table.

Select the name of the app's merge check you’d like to enable.

Select the target branch you’d like the custom check to be run against.

Select Save, which will cause that check to come into effect for all pull requests.

Premium-only feature

If you have a Premium plan, you can tick 
required which will enforce the check must pass before a pull request can be merged
Set up custom merge checks at the project level 
Checks enabled at the project level will run against all pull requests contained within all repositories contained within the project. Provided the pull request targets a branch which corresponds to the enabled custom checks.

You must be a project admin to administer the custom checks at the project level.

Premium-only feature

If you have a Premium plan, you are able to administer custom checks at the project level
To administer custom merge checks associated with the project:

Next to the name of your project in the left sidebar, select More actions (…), and then select Settings.

Select Custom merge checks under Workflow on the left sidebar.

If you do not see a table, it means you do not have any Forge Apps installed which provide Custom Merge Checks. You will have to install an app into your workspace prior.

Select Add check on a Forge app shown in the table.

Select the name of the app's merge check you’d like to enable.

Select the target branch you’d like the custom check to be run against.

Select whether or not the check is 
required

Select Save, which will cause that check to come into effect for all pull requests.
Set up custom merge checks at the workspace level
Checks enabled at the workspace level will run against all pull requests contained within all repositories contained within all projects contained within the workspace. Provided the pull request targets a branch which corresponds to the enabled custom checks.

You must be a workspace admin to administer the custom checks at the project level.

Premium-only feature

If you have a Premium plan, you are able to administer custom checks at the workspace level
To administer custom merge checks associated with the project:

Next to the name of your project in the left sidebar, select More actions (…), and then select Settings.

Select Custom merge checks under Workflow on the left sidebar.

If you do not see a table, it means you do not have any Forge Apps installed which provide Custom Merge Checks. You will have to install an app into your workspace prior.

Select Add check on a Forge app shown in the table.

Select the name of the app's merge check you’d like to enable.

Select the target branch you’d like the custom check to be run against.

Select whether or not the check is 
required

Select Save, which will cause that check to come into effect for all pull requests.
Using custom merge checks
Once you’ve installed a custom merge check app and enabled the feature at the workspace level, you can set up custom merge checks at each level of the Bitbucket resource hierarchy: workspace, project, and repository. 
This allows organisations (workspace level) to mandate a set of checks that must run across the entire workspace, and gives teams (projects) the ability to also mandate checks running within that project, and finally individual repositories may also set checks.
These checks are inherited. That is a check that is 
required at the workspace level can not be disabled at a lower level (project or repository).
The checks are also complementary. The same check can be (not that we advise it) at multiple levels, each producing an independent result in the pull request.
That enables a workspace to set a check as 
not required and a contained project (or repository) could enable the same check targeted against the same branch as 
required.

Workspace and project level settings, and 
required custom merge checks are only available on premium plans. 
Any pull request targeting the repository’s configured main branch will run the following merge checks.

Required number of builds passed

All tasks resolved (Inherited from the repository's project)

Minimum approvals (Inherited from the repository's workspace) 
And will require a passing result for both the inherited checks (as they’ve been configured as 
required).
Custom merge checks run in the context of a pull request. Once you’ve enabled a merge check, you will see results for the enabled check results in the Pull Request page. 

For pull requests created prior to the custom merge check was enabled, you may not immediately see a corresponding check result in the UI. However, when a merge is attempted, the merge attempt will be blocked as the corresponding result is missing. At this point, the missing check will be queued and executed. From this point forward, this pull request will remain in sync.
Any PR created from this point in time will automatically queue and execute any custom checks configured against the target branch.
Any check which is configured to be 
not required will be executed, but will not prevent a merge.
Types of custom merge checks
A custom check can be pre-merge, on-merge, or both. The type of the check depends on what trigger it is subscribing to in its Forge app manifest. The merge check type is displayed in the add/edit modal in the Custom merge checks setting table.
Pre-merge
Pre-merge checks are triggered as different events occur on an open PR prior to it being merged. For example, when code has been pushed to the source branch of the pull request (
on-code-pushed) or when an approval is changed (
on-reviewer-status-changed). Any custom merge check which is 
pre-merge must produce a successful result (if required) prior to a merge action being invoked.
On-merge
On-merge checks are integrated into the merge process itself, and are only triggered after the Merge button (or api) has been hit. Failed on-merge checks which are set as 
required will result in a pull request merge failure.

Was this helpful?Yes
No

It wasn't accurateIt wasn't clearIt wasn't relevantProvide feedback about this article

Still need help?
The Atlassian Community is here for you.
Ask the Community

Join or create and manage workspaces in Bitbucket Cloud
Show more
Transfer repositories and groups to a workspace
Publishing a Website on Bitbucket Cloud

Set up and use custom merge checks
Set up and use packages
Signed system commits and requiring signed commits
On this pageBefore you beginInstall and enable custom merge checksSet up custom merge checks at the repository levelSet up custom merge checks at the project level Set up custom merge checks at the workspace levelUsing custom merge checksTypes of custom merge checks
CommunityQuestions, discussions, and articles
