<!-- vendored from https://forgejo.org/docs/latest/user/pull-requests-and-git-flow/, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

Pull requests and Git flow | Forgejo – Beyond coding. We forge.

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

## Pull requests and Git flow
     Benefits of a pull-request-based workflow
  Create a pull request
  Reviews
  Review requests and code owners
  Keep it up-to-date: rebase pull requests to upstream
  A friendly note on owner rights and force push permissions

#  Pull requests and Git flow

## Benefits of a pull-request-based workflow

TLDR: Keep an eye on your repository and organization permissions. Don’t take sweets from strangers. Use pull requests. Easy to review, easy to manage, and only the project maintainers/owners have permission to merge them.

Although it is perfectly possible to use a Git project on Codeberg just as a single shared central repository for individuals and teams, a collaborative workflow based on pull requests provides many benefits:

The “hot” project repository requires only very few maintainers with full rights to sign off pull requests. Contributors can easily work on forked repositories.

Each pull request collects the full edit history for a fix or feature branch. Contributors can squash this, or keep it, just as they prefer.

## Create a pull request

Let’s say you would like to contribute to our “examples” project knut/examples.

First, fork the project you would like to work on by clicking the Fork button in the top-right corner of the project page:

Then, clone it onto your local machine. We assume that you have set up your SSH keys. This has to be done only once:

git clone git@codeberg.org:<YOURCODEBERGUSERNAME>/examples.git

Now, let’s create a feature branch, make some changes, commit, push, edit, commit, push, …, edit, commit, push:

git checkout -b feature
# make some changes
git commit -m "first feature"
git push    # here you get asked to set your upstream URL, just confirm
# do more work, edit...
git add new_file.png
git commit -m "second feature introducing a new file"
git push
# ...
git commit -m "more work, tidy-up"
git push

Now you can create a pull request by visiting the main repository page and clicking on the New Pull Request button.

This button is automatically shown if:

You are the pusher on a branch that still exists and that is not the default branch.

The push must have occurred within the last 6 hours.

There is no open pull request for this branch.

## Reviews

On the pull request page, the Files Changed tab shows a Review button that can be used to approve the pull request or request changes.

Next to each changed line, a plus button allows you to add a comment on that specific line, for instance, to suggest a modification.

To comment on several lines at once, hold Shift, press the plus button next to the first line of the range and drag down to its last line before releasing. The highlighted lines are then attached to a single comment.

When a pull request contains multiple commits, the button to the left of the Review button can be used to only review a single commit.

## Review requests and code owners

On the right-hand sidebar of the pull request page there is a Reviewers section. Clicking its title will open a dropdown menu using which you may request a review from one or more repository collaborators.

Review requests may additionally be automated by adding a CODEOWNERS file either to the root of the repository, or the docs or .forgejo directories.

Lines in this file represent rules. Each rule consists of a Go-formatted regular expression matching paths to changed files, followed by the names of one or more users and/or organization teams, all separated by whitespaces. The regular expression may also be prepended with an exclamation mark (!) to create a negative rule.

Users are referenced by their usernames. Teams are referenced by the name of the organization, followed by a slash (/) and the name of the team. Both may optionally be prepended with an @ sign.

When a pull request is submitted, the changes are checked against the rules in the CODEOWNERS file. When the path to any of the changed files matches the regular expression of a non-negative rule, a review of the pull request is automatically requested from all users and/or teams referenced by the rule. Negative rules do the opposite – reviews are requested when any of the changed files do not match the regular expression.

A CODEOWNERS file may contain empty lines and comments, which begin with a hash (#) character.

An example CODEOWNERS file may look like this:

# Request review from User001 whenever anything in `src` changes
src/.* @User001

# Request review from the editors team in MyOrg whenever anything
# in `docs` changes
docs/.* @MyOrg/editors

# Request review from User002 whenever anything but `README.md` changes
!README\.md User002

## Keep it up-to-date: rebase pull requests to upstream

Sometimes the upstream project repository is evolving while we are working on a feature branch, and we need to rebase and resolve merge conflicts for upstream changes into our feature branch. This is not hard:

In order to track the upstream repository, we’ll add a second remote that points to the original project. This has to be done only once:

git remote add upstream git@codeberg.org:knut/examples.git

You can also use the SSH variant here for public projects if you want to be
able to pull without specifying your credentials.

Now, let’s pull from upstream, and rebase our local branch against the latest HEAD of the upstream project repository (e.g., the main branch):

git pull --rebase upstream main
git pull

That’s it. You can now push your changes and create the pull request as usual by clicking on the “New Pull Request” button.

## A friendly note on owner rights and force push permissions

Please keep in mind that project owners can do everything, including editing and rewriting the history using force-push. In some cases, this is a useful feature
(for example, to undo accidental commits or clean up PRs),
but in most cases, a transparent history based on a pull-request-based workflow is surely preferable,
especially for the default branches of your project where other people rely on intact history.

WarningIf you accidentally leaked sensitive data, say, leaked credentials,
keep in mind that commits stay directly accessible, e.g., from the user
activity tab or a Pull Request feed, for a while.
Please contact your instance admin if you really need to remove such data from the public.

This page's content is available under the CC-BY-SA-4.0 license.
 It is derived from "Pull requests and Git flow" by Codeberg Docs Contributors.              Edit this page
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
