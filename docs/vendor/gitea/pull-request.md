<!-- vendored from https://docs.gitea.com/usage/pull-request, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

Pull Request | Gitea DocumentationSkip to contentTry Gitea Cloud ☁️ for 30 days → Accelerate your Development & Deploys!Gitea DocumentationDocsAPIRunnerCLIEnterpriseSearchCtrlKCancelSelect themeDarkLightAutoVersion29-dev28.4.01.27.31.26.41.25.51.24.71.23.81.22.6LanguageEnglish简体中文繁體中文Sign InMenuDocsAPIRunnerCLIEnterpriseWhat is Gitea?
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
Creating a pull request
Reviewing a pull request
Closing a pull request
“Work In Progress” pull requests
Default pull request title
Pull Request Templates
On this page
Overview
Creating a pull request
Reviewing a pull request
Closing a pull request
“Work In Progress” pull requests
Default pull request title
Pull Request Templates
Pull Request
A Pull Request (PR) is a way to propose changes to a repository.
It is a request to merge one branch into another, accompanied by a description of the changes that were made.
Pull Requests are commonly used as a way for contributors to propose changes and for maintainers to review and merge those changes.
Creating a pull request
Section titled “Creating a pull request”
To create a PR, you’ll need to follow these steps:
Fork the repository - If you don’t have permission to make changes to the repository directly, you’ll need to fork the repository to your own account.
This creates a copy of the repository that you can make changes to.
Create a branch (optional) - Create a new branch on your forked repository that contains the changes you want to propose.
Give the branch a descriptive name that indicates what the changes are for.
Make your changes - Make the changes you want, commit, and push them to your forked repository.
Create the PR - Go to the original repository and go to the “Pull Requests” tab. Click the “New Pull Request” button and select your new branch as the source branch.
Enter a descriptive title and description for your Pull Request and click “Create Pull Request”.
Reviewing a pull request
Section titled “Reviewing a pull request”
When a PR is created, it triggers a review process. The maintainers of the repository are notified of the PR and can review the changes that were made.
They can leave comments, request changes, or approve the changes.
If the maintainers request changes, you’ll need to make those changes in your branch and push the changes to your forked repository.
The PR will be updated automatically with the new changes.
If the maintainers approve the changes, they can merge the PR into the repository.
Closing a pull request
Section titled “Closing a pull request”
If you decide that you no longer want to merge a PR, you can close it.
To close a PR, go to the open PR and click the “Close Pull Request” button. This will close the PR without merging it.
“Work In Progress” pull requests
Section titled ““Work In Progress” pull requests”
Marking a pull request as being a work in progress will prevent that pull request from being accidentally merged.
To mark a pull request as being a work in progress, you must prefix its title by WIP: or [WIP] (case insensitive).
Those values are configurable in your app.ini file:
[repository.pull-request]WORK_IN_PROGRESS_PREFIXES=WIP:,[WIP]
The first value of the list will be used in helpers.
Default pull request title
Section titled “Default pull request title”
When opening a new pull request, Gitea pre-fills the title field. The source of that title is controlled by the DEFAULT_TITLE_SOURCE setting in app.ini:
[repository.pull-request]DEFAULT_TITLE_SOURCE = first-commit
Two modes are available:
first-commit (default): The title is taken from the summary line of the oldest commit in the branch. This applies regardless of how many commits are included in the PR.
auto: When the PR contains a single commit, its summary line is used as the title (same as first-commit for one commit). When the PR contains multiple commits, Gitea converts the branch name into a human-readable sentence: dashes, underscores, and camelCase word boundaries are replaced with spaces, and the first letter is capitalized.
Example: branch name fix-user-login-flow with multiple commits produces the title Fix user login flow under auto, but would use the oldest commit’s message under first-commit.
Pull Request Templates
Section titled “Pull Request Templates”
You can find more information about pull request templates at the page Issue and Pull Request templates.
Edit pagePreviousLabelsNextAutomatically Linked ReferencesCommunity
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
