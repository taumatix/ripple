<!-- vendored from https://forgejo.org/docs/latest/user/api/versions/, fetched 2026-11-23; text extracted from the HTML page, otherwise unmodified -->

            Code Search  
  Issue Search  
       Forgejo numbering scheme
     Obtaining the Forgejo version  
  Compatibility with Gitea  
  Semantic Version  
  Stable versions  
  Experimental and pre-release versions  
  Legacy forgejo numbering scheme  
       Forgejo numbering scheme 
 The Forgejo versions look like X.Y.Z+gitea-A.B.C
(e.g., 7.0.0+gitea-1.22.0). The Forgejo version is the first part,
before the ’+’ (e.g., 7.0.0), and the following metadata, after the ’+’,
shows the Gitea version with which the API is compatible
(e.g., 1.22.0).
Obtaining the Forgejo version
The Forgejo version is displayed in the footer of the web UI, in the
output of the forgejo --version CLI, and returned by two API
endpoints:
example.com/api/v1/version
example.com/api/forgejo/v1/version
For Forgejo 7.0.0 and above, the two endpoints return the same value
and they match what is displayed by the CLI or the web UI.
Compatibility with Gitea
As of Forgejo 7.0.0, tools designed to work with Gitea 1.22.0 and
below are compatible and do not need any modification to keep working.
In the future, if a tool wants to assert the level of compatibility of
a Forgejo version with Gitea, it can:
Obtain the version number example.com/api/v1/version using the same API endpoint as Gitea
If the returned version contains ‘+gitea-’, keep what follows as the compatible Gitea version number
For instance, Gitnex will not notice a difference when the version
number is bumped from Forgejo 1.21.7-0 to Forgejo 7.0.0. It only uses
version numbers to verify if a new feature is available in a newer
version. The last time such a check was added was with Gitea 1.17,
which was published in July 2022.
Semantic Version
SemVer allows users to understand the scope of a software update at first glance, based on the following:
<MAJOR> is increased for breaking changes.
<MINOR> is increased for backwards-compatible new features.
<PATCH> is increased for backwards-compatible bug fixes.
Forgejo changes reflected in the version could be:
a command, an option, or an argument, for a CLI
a route path, a query parameter, or a body property, for a REST API
a text node, a button, or a field, for a GUI
Since Forgejo has all of the above, changes to all of those components are taken into consideration when creating a new version number.
Stable versions
The structure of the version number is <MAJOR>.<MINOR>.<PATCH>+gitea-<GITEA VERSION>, where:
<MAJOR>.<MINOR>.<PATCH> is conformant to Semantic Versioning 2.0.0
gitea-<GITEA VERSION> is the Gitea version this Forgejo release is compatible with
Experimental and pre-release versions
The structure of the version number is <MAJOR>.<MINOR>.<PATCH>-dev-<SHA>+gitea-<GITEA VERSION>, where:
<MAJOR>.<MINOR>.<PATCH> is conformant to Semantic Versioning 2.0.0
<SHA> is the Git SHA from which the release was built
gitea-<GITEA VERSION> is the Gitea version this Forgejo release is compatible with
Legacy forgejo numbering scheme
In Forgejo versions prior to 7.0.0, i.e., from 1.18.0-1 to 1.21.7-0
included, the Forgejo version matched the Gitea version by removing
the string following the trailing dash (-) and the release cycles were
synchronized.
The version number A.B.C-N was displayed with the dash (-) replaced by a plus
(+) (e.g., Forgejo 1.18.0-1 was displayed as 1.18.0+1). The two API
endpoints returned different results:
example.com/api/v1/version returned {"version":"A.B.C+N"}
example.com/api/forgejo/v1/version returned {"version":"X.Y.Z+N-gitea-A.B.C"}
The Forgejo semantic number X.Y.Z could only be found in the second
endpoint and was used to prepare for switching to semantic
versioning. It was advertised in release notes and documented in the
user reference guide.
This page's content is available under the CC-BY-SA-4.0 license.
             Edit this page             Forgejo
    Releases  
  News  
  Fediverse (Mastodon)  
  Chat room (Matrix)  
  RSS feed  
   Community
    Code  
  Governance  
  Teams  
  Identity proofs  
   Contribute
    Issue tracker  
  Developer Matrix room  
  Contributor guide  
  Localization guide  
  Donate via Liberapay  
   Resources
    Status (Forgejo)  
  Status (Codeberg)  
  Documentation  
  Delightful Forgejo  
  Professional services  
   Legal
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
       