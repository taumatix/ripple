<!-- Source: https://docs.renovatebot.com/modules/platform/scm-manager/ ; fetched 2026-11-23 ; navigation menu removed -->

# SCM-Manager¶

This feature is flagged as experimental

Experimental features might be changed or even removed at any time.

Renovate supports the SCM-Manager platform.
This platform is considered experimental.

## Authentication¶

- Create an API Key for your technical Renovate user in SCM-Manager

- The technical user must have a valid name and email address

- Put the API key in the RENOVATE_TOKEN environment variable, so that Renovate can use it

## Set correct platform¶
You must set the platform config option to scm-manager in your admin config file.

## Set permissions¶
The technical user must have the permissions to:

- read the repository

- pull/checkout the repository

- push/commit the repository

- create pull requests for the repository

Those permissions can be granted on a repository level within the permission settings of each repository.

## Install Review Plugin¶
To let Renovate access the Pull Request API, you must install the Review Plugin with at least version 3.11.0.
Find the list of available plugins by going to Administration → Plugins → Available.

## Supported versions of SCM-Manager¶
Renovate supports SCM-Manager major version 3.x.
The minimum version for the 3.x range is 3.10.0.

## Automerge¶
Currently, the Renovate automerge feature is not supported by the SCM-Manager platform.
Every pull request requires merging them manually for now.

  Back to top
