<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-requests/create?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Pull Requests - Create 
  Service:  Git    API Version:  7.1      
Create a pull request.
 

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests?api-version=7.1
```
 
 
 With optional parameters:  

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullrequests?supportsIterations={supportsIterations}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
organization   | path  |  True   |  
 string 
   |  
The name of the Azure DevOps organization.
  
 
  |  
repositoryId   | path  |  True   |  
 string 
   |  
The repository ID of the pull request's target branch.
  
 
  |  
project   | path  |    |  
 string 
   |  
Project ID or project name
  
 
  |  
api-version   | query  |  True   |  
 string 
   |  
Version of the API to use. This should be set to '7.1' to use this version of the api.
  
 
  |  
supportsIterations   | query  |    |  
 boolean 
   |  
If true, subsequent pushes to the pull request will be individually reviewable. Set this to false for large pull requests for performance reasons if this functionality is not needed.
  
  

## Request Body  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Links to other related objects.
  
 
  |  artifactId   |  
 string 
   |  
A string which uniquely identifies this pull request. To generate an artifact ID for a pull request, use this template: `vstfs:///Git/PullRequestId/{projectId}/{repositoryId}/{pullRequestId}`
  
 
  |  autoCompleteSetBy   |  
 IdentityRef 
   |  
If set, auto-complete is enabled for this pull request and this is the identity that enabled it.
  
 
  |  closedBy   |  
 IdentityRef 
   |  
The user who closed the pull request.
  
 
  |  closedDate   |  
 string (date-time) 
   |  
The date when the pull request was closed (completed, abandoned, or merged externally).
  
 
  |  codeReviewId   |  
 integer (int32) 
   |  
The code review ID of the pull request. Used internally.
  
 
  |  commits   |  
 GitCommitRef[] 
   |  
The commits contained in the pull request.
  
 
  |  completionOptions   |  
 GitPullRequestCompletionOptions 
   |  
Options which affect how the pull request will be merged when it is completed.
  
 
  |  completionQueueTime   |  
 string (date-time) 
   |  
The most recent date at which the pull request entered the queue to be completed. Used internally.
  
 
  |  createdBy   |  
 IdentityRef 
   |  
The identity of the user who created the pull request.
  
 
  |  creationDate   |  
 string (date-time) 
   |  
The date when the pull request was created.
  
 
  |  description   |  
 string 
   |  
The description of the pull request.
  
 
  |  forkSource   |  
 GitForkRef 
   |  
If this is a PR from a fork this will contain information about its source.
  
 
  |  hasMultipleMergeBases   |  
 boolean 
   |  
Multiple mergebases warning
  
 
  |  isDraft   |  
 boolean 
   |  
Draft / WIP pull request.
  
 
  |  labels   |  
 WebApiTagDefinition[] 
   |  
The labels associated with the pull request.
  
 
  |  lastMergeCommit   |  
 GitCommitRef 
   |  
The commit of the most recent pull request merge. If empty, the most recent merge is in progress or was unsuccessful.
  
 
  |  lastMergeSourceCommit   |  
 GitCommitRef 
   |  
The commit at the head of the source branch at the time of the last pull request merge.
  
 
  |  lastMergeTargetCommit   |  
 GitCommitRef 
   |  
The commit at the head of the target branch at the time of the last pull request merge.
  
 
  |  mergeFailureMessage   |  
 string 
   |  
If set, pull request merge failed for this reason.
  
 
  |  mergeFailureType   |  
 PullRequestMergeFailureType 
   |  
The type of failure (if any) of the pull request merge.
  
 
  |  mergeId   |  
 string (uuid) 
   |  
The ID of the job used to run the pull request merge. Used internally.
  
 
  |  mergeOptions   |  
 GitPullRequestMergeOptions 
   |  
Options used when the pull request merge runs. These are separate from completion options since completion happens only once and a new merge will run every time the source branch of the pull request changes.
  
 
  |  mergeStatus   |  
 PullRequestAsyncStatus 
   |  
The current status of the pull request merge.
  
 
  |  pullRequestId   |  
 integer (int32) 
   |  
The ID of the pull request.
  
 
  |  remoteUrl   |  
 string 
   |  
Used internally.
  
 
  |  repository   |  
 GitRepository 
   |  
The repository containing the target branch of the pull request.
  
 
  |  reviewers   |  
 IdentityRefWithVote[] 
   |  
A list of reviewers on the pull request along with the state of their votes.
  
 
  |  sourceRefName   |  
 string 
   |  
The name of the source branch of the pull request.
  
 
  |  status   |  
 PullRequestStatus 
   |  
The status of the pull request.
  
 
  |  supportsIterations   |  
 boolean 
   |  
If true, this pull request supports multiple iterations. Iteration support means individual pushes to the source branch of the pull request can be reviewed and comments left in one iteration will be tracked across future iterations.
  
 
  |  targetRefName   |  
 string 
   |  
The name of the target branch of the pull request.
  
 
  |  title   |  
 string 
   |  
The title of the pull request.
  
 
  |  url   |  
 string 
   |  
Used internally.
  
 
  |  workItemRefs   |  
 ResourceRef[] 
   |  
Any work item references associated with this pull request.
  
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 GitPullRequest 
   |  
successful operation
  
  

## Security 

### oauth2 
 Type:  oauth2
 Flow:  accessCode
 Authorization URL:  https://app.vssps.visualstudio.com/oauth2/authorize&response_type=Assertion
 Token URL:  https://app.vssps.visualstudio.com/oauth2/token?client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer&grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer
 
 

#### Scopes  
  | Name  | Description 
 
  |  vso.code_write   |  Grants the ability to read, update, and delete source code, access metadata about commits, changesets, branches, and other version control artifacts. Also grants the ability to create and manage pull requests and code reviews and to receive notifications about version control events via service hooks.  
  

## Examples 
 

#### Sample request 
  
-  HTTP    

```
POST https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullrequests?api-version=7.1

{
  "sourceRefName": "refs/heads/npaulk/my_work",
  "targetRefName": "refs/heads/new_feature",
  "title": "A new feature",
  "description": "Adding a new feature",
  "reviewers": [
    {
      "id": "d6245f20-2af8-44f4-9451-8107cb2767db"
    }
  ]
}

```
    
 

#### Sample response 
  Status code:   201    

```
{
  "repository": {
    "id": "3411ebc1-d5aa-464f-9615-0b527bc66719",
    "name": "2016_10_31",
    "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719",
    "project": {
      "id": "a7573007-bbb3-4341-b726-0c4148a07853",
      "name": "2016_10_31",
      "description": "test project created on Halloween 2016",
      "url": "https://dev.azure.com/fabrikam/_apis/projects/a7573007-bbb3-4341-b726-0c4148a07853",
      "state": "wellFormed",
      "revision": 7
    },
    "remoteUrl": "https://dev.azure.com/fabrikam/_git/2016_10_31"
  },
  "pullRequestId": 22,
  "codeReviewId": 22,
  "status": "active",
  "createdBy": {
    "id": "d6245f20-2af8-44f4-9451-8107cb2767db",
    "displayName": "Normal Paulk",
    "uniqueName": "fabrikamfiber16@hotmail.com",
    "url": "https://dev.azure.com/fabrikam/_apis/Identities/d6245f20-2af8-44f4-9451-8107cb2767db",
    "imageUrl": "https://dev.azure.com/fabrikam/_api/_common/identityImage?id=d6245f20-2af8-44f4-9451-8107cb2767db"
  },
  "creationDate": "2016-11-01T16:30:31.6655471Z",
  "title": "A new feature",
  "description": "Adding a new feature",
  "sourceRefName": "refs/heads/npaulk/my_work",
  "targetRefName": "refs/heads/new_feature",
  "mergeStatus": "queued",
  "mergeId": "f5fc8381-3fb2-49fe-8a0d-27dcc2d6ef82",
  "lastMergeSourceCommit": {
    "commitId": "b60280bc6e62e2f880f1b63c1e24987664d3bda3",
    "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/commits/b60280bc6e62e2f880f1b63c1e24987664d3bda3"
  },
  "lastMergeTargetCommit": {
    "commitId": "f47bbc106853afe3c1b07a81754bce5f4b8dbf62",
    "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/commits/f47bbc106853afe3c1b07a81754bce5f4b8dbf62"
  },
  "reviewers": [
    {
      "reviewerUrl": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullRequests/22/reviewers/d6245f20-2af8-44f4-9451-8107cb2767db",
      "vote": 0,
      "id": "d6245f20-2af8-44f4-9451-8107cb2767db",
      "displayName": "Normal Paulk",
      "uniqueName": "fabrikamfiber16@hotmail.com",
      "url": "https://dev.azure.com/fabrikam/_apis/Identities/d6245f20-2af8-44f4-9451-8107cb2767db",
      "imageUrl": "https://dev.azure.com/fabrikam/_api/_common/identityImage?id=d6245f20-2af8-44f4-9451-8107cb2767db"
    }
  ],
  "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullRequests/22",
  "_links": {
    "self": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullRequests/22"
    },
    "repository": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719"
    },
    "workItems": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullRequests/22/workitems"
    },
    "sourceBranch": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/refs"
    },
    "targetBranch": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/refs"
    },
    "sourceCommit": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/commits/b60280bc6e62e2f880f1b63c1e24987664d3bda3"
    },
    "targetCommit": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/commits/f47bbc106853afe3c1b07a81754bce5f4b8dbf62"
    },
    "createdBy": {
      "href": "https://dev.azure.com/fabrikam/_apis/Identities/d6245f20-2af8-44f4-9451-8107cb2767db"
    },
    "iterations": {
      "href": "https://dev.azure.com/fabrikam/_apis/git/repositories/3411ebc1-d5aa-464f-9615-0b527bc66719/pullRequests/22/iterations"
    }
  },
  "supportsIterations": true,
  "artifactId": "vstfs:///Git/PullRequestId/a7573007-bbb3-4341-b726-0c4148a07853%2f3411ebc1-d5aa-464f-9615-0b527bc66719%2f22"
}
```
  

## Definitions  
  | Name  | Description 
 
  |  ChangeCountDictionary   |   
 
  |  GitChange   |   
 
  |  GitCommitRef   |  
Provides properties that describe a Git commit and associated metadata.
  
 
  |  GitForkRef   |  
Information about a fork ref.
  
 
  |  GitPullRequest   |  
Represents all the data associated with a pull request.
  
 
  |  GitPullRequestCompletionOptions   |  
Preferences about how the pull request should be completed.
  
 
  |  GitPullRequestMergeOptions   |  
The options which are used when a pull request merge is created.
  
 
  |  GitPullRequestMergeStrategy   |  
Specify the strategy used to merge the pull request during completion. If MergeStrategy is not set to any value, a no-FF merge will be created if SquashMerge == false. If MergeStrategy is not set to any value, the pull request commits will be squashed if SquashMerge == true. The SquashMerge property is deprecated. It is recommended that you explicitly set MergeStrategy in all cases. If an explicit value is provided for MergeStrategy, the SquashMerge property will be ignored.
  
 
  |  GitPushRef   |   
 
  |  GitRepository   |   
 
  |  GitRepositoryRef   |   
 
  |  GitStatus   |  
This class contains the metadata of a service/extension posting a status.
  
 
  |  GitStatusContext   |  
Status context that uniquely identifies the status.
  
 
  |  GitStatusState   |  
State of the status.
  
 
  |  GitTemplate   |   
 
  |  GitUserDate   |  
User info and date for Git operations.
  
 
  |  IdentityRef   |   
 
  |  IdentityRefWithVote   |  
Identity information including a vote on a pull request.
  
 
  |  ItemContent   |   
 
  |  ItemContentType   |   
 
  |  ProjectState   |  
Project state.
  
 
  |  ProjectVisibility   |  
Project visibility.
  
 
  |  PullRequestAsyncStatus   |  
The current status of the pull request merge.
  
 
  |  PullRequestMergeFailureType   |  
The type of failure (if any) of the pull request merge.
  
 
  |  PullRequestStatus   |  
The status of the pull request.
  
 
  |  ReferenceLinks   |  
The class to represent a collection of REST reference links.
  
 
  |  ResourceRef   |   
 
  |  TeamProjectCollectionReference   |  
Reference object for a TeamProjectCollection.
  
 
  |  TeamProjectReference   |  
Represents a shallow reference to a TeamProject.
  
 
  |  VersionControlChangeType   |  
The type of change that was made to the item.
  
 
  |  WebApiTagDefinition   |  
The representation of a tag definition which is sent across the wire.
  
  

### ChangeCountDictionary 
Object 

### GitChange 
Object  
  | Name  | Type  | Description 
 
  |  changeId   |  
 integer (int32) 
   |  
ID of the change within the group of changes.
  
 
  |  changeType   |  
 VersionControlChangeType 
   |  
The type of change that was made to the item.
  
 
  |  item   |  
 string (T) 
   |  
Current version.
  
 
  |  newContent   |  
 ItemContent 
   |  
Content of the item after the change.
  
 
  |  newContentTemplate   |  
 GitTemplate 
   |  
New Content template to be used when pushing new changes.
  
 
  |  originalPath   |  
 string 
   |  
Original path of item if different from current path.
  
 
  |  sourceServerItem   |  
 string 
   |  
Path of the item on the server.
  
 
  |  url   |  
 string 
   |  
URL to retrieve the item.
  
  

### GitCommitRef 
Object 
Provides properties that describe a Git commit and associated metadata.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
A collection of related REST reference links.
  
 
  |  author   |  
 GitUserDate 
   |  
Author of the commit.
  
 
  |  changeCounts   |  
 ChangeCountDictionary 
   |  
Counts of the types of changes (edits, deletes, etc.) included with the commit.
  
 
  |  changes   |  
 GitChange[] 
   |  
An enumeration of the changes included with the commit.
  
 
  |  comment   |  
 string 
   |  
Comment or message of the commit.
  
 
  |  commentTruncated   |  
 boolean 
   |  
Indicates if the comment is truncated from the full Git commit comment message.
  
 
  |  commitId   |  
 string 
   |  
ID (SHA-1) of the commit.
  
 
  |  commitTooManyChanges   |  
 boolean 
   |  
Indicates that commit contains too many changes to be displayed
  
 
  |  committer   |  
 GitUserDate 
   |  
Committer of the commit.
  
 
  |  parents   |  
 string[] 
   |  
An enumeration of the parent commit IDs for this commit.
  
 
  |  push   |  
 GitPushRef 
   |  
The push associated with this commit.
  
 
  |  remoteUrl   |  
 string 
   |  
Remote URL path to the commit.
  
 
  |  statuses   |  
 GitStatus[] 
   |  
A list of status metadata from services and extensions that may associate additional information to the commit.
  
 
  |  url   |  
 string 
   |  
REST URL for this resource.
  
 
  |  workItems   |  
 ResourceRef[] 
   |  
A list of workitems associated with this commit.
  
  

### GitForkRef 
Object 
Information about a fork ref.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
The class to represent a collection of REST reference links.
  
 
  |  creator   |  
 IdentityRef 
   |   
 
  |  isLocked   |  
 boolean 
   |   
 
  |  isLockedBy   |  
 IdentityRef 
   |   
 
  |  name   |  
 string 
   |   
 
  |  objectId   |  
 string 
   |   
 
  |  peeledObjectId   |  
 string 
   |   
 
  |  repository   |  
 GitRepository 
   |  
The repository ID of the fork.
  
 
  |  statuses   |  
 GitStatus[] 
   |  
This class contains the metadata of a service/extension posting a status.
  
 
  |  url   |  
 string 
   |   
  

### GitPullRequest 
Object 
Represents all the data associated with a pull request.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Links to other related objects.
  
 
  |  artifactId   |  
 string 
   |  
A string which uniquely identifies this pull request. To generate an artifact ID for a pull request, use this template: `vstfs:///Git/PullRequestId/{projectId}/{repositoryId}/{pullRequestId}`
  
 
  |  autoCompleteSetBy   |  
 IdentityRef 
   |  
If set, auto-complete is enabled for this pull request and this is the identity that enabled it.
  
 
  |  closedBy   |  
 IdentityRef 
   |  
The user who closed the pull request.
  
 
  |  closedDate   |  
 string (date-time) 
   |  
The date when the pull request was closed (completed, abandoned, or merged externally).
  
 
  |  codeReviewId   |  
 integer (int32) 
   |  
The code review ID of the pull request. Used internally.
  
 
  |  commits   |  
 GitCommitRef[] 
   |  
The commits contained in the pull request.
  
 
  |  completionOptions   |  
 GitPullRequestCompletionOptions 
   |  
Options which affect how the pull request will be merged when it is completed.
  
 
  |  completionQueueTime   |  
 string (date-time) 
   |  
The most recent date at which the pull request entered the queue to be completed. Used internally.
  
 
  |  createdBy   |  
 IdentityRef 
   |  
The identity of the user who created the pull request.
  
 
  |  creationDate   |  
 string (date-time) 
   |  
The date when the pull request was created.
  
 
  |  description   |  
 string 
   |  
The description of the pull request.
  
 
  |  forkSource   |  
 GitForkRef 
   |  
If this is a PR from a fork this will contain information about its source.
  
 
  |  hasMultipleMergeBases   |  
 boolean 
   |  
Multiple mergebases warning
  
 
  |  isDraft   |  
 boolean 
   |  
Draft / WIP pull request.
  
 
  |  labels   |  
 WebApiTagDefinition[] 
   |  
The labels associated with the pull request.
  
 
  |  lastMergeCommit   |  
 GitCommitRef 
   |  
The commit of the most recent pull request merge. If empty, the most recent merge is in progress or was unsuccessful.
  
 
  |  lastMergeSourceCommit   |  
 GitCommitRef 
   |  
The commit at the head of the source branch at the time of the last pull request merge.
  
 
  |  lastMergeTargetCommit   |  
 GitCommitRef 
   |  
The commit at the head of the target branch at the time of the last pull request merge.
  
 
  |  mergeFailureMessage   |  
 string 
   |  
If set, pull request merge failed for this reason.
  
 
  |  mergeFailureType   |  
 PullRequestMergeFailureType 
   |  
The type of failure (if any) of the pull request merge.
  
 
  |  mergeId   |  
 string (uuid) 
   |  
The ID of the job used to run the pull request merge. Used internally.
  
 
  |  mergeOptions   |  
 GitPullRequestMergeOptions 
   |  
Options used when the pull request merge runs. These are separate from completion options since completion happens only once and a new merge will run every time the source branch of the pull request changes.
  
 
  |  mergeStatus   |  
 PullRequestAsyncStatus 
   |  
The current status of the pull request merge.
  
 
  |  pullRequestId   |  
 integer (int32) 
   |  
The ID of the pull request.
  
 
  |  remoteUrl   |  
 string 
   |  
Used internally.
  
 
  |  repository   |  
 GitRepository 
   |  
The repository containing the target branch of the pull request.
  
 
  |  reviewers   |  
 IdentityRefWithVote[] 
   |  
A list of reviewers on the pull request along with the state of their votes.
  
 
  |  sourceRefName   |  
 string 
   |  
The name of the source branch of the pull request.
  
 
  |  status   |  
 PullRequestStatus 
   |  
The status of the pull request.
  
 
  |  supportsIterations   |  
 boolean 
   |  
If true, this pull request supports multiple iterations. Iteration support means individual pushes to the source branch of the pull request can be reviewed and comments left in one iteration will be tracked across future iterations.
  
 
  |  targetRefName   |  
 string 
   |  
The name of the target branch of the pull request.
  
 
  |  title   |  
 string 
   |  
The title of the pull request.
  
 
  |  url   |  
 string 
   |  
Used internally.
  
 
  |  workItemRefs   |  
 ResourceRef[] 
   |  
Any work item references associated with this pull request.
  
  

### GitPullRequestCompletionOptions 
Object 
Preferences about how the pull request should be completed.
  
  | Name  | Type  | Description 
 
  |  autoCompleteIgnoreConfigIds   |  
 integer[] (int32) 
   |  
List of any policy configuration Id's which auto-complete should not wait for. Only applies to optional policies (isBlocking == false). Auto-complete always waits for required policies (isBlocking == true).
  
 
  |  bypassPolicy   |  
 boolean 
   |  
If true, policies will be explicitly bypassed while the pull request is completed.
  
 
  |  bypassReason   |  
 string 
   |  
If policies are bypassed, this reason is stored as to why bypass was used.
  
 
  |  deleteSourceBranch   |  
 boolean 
   |  
If true, the source branch of the pull request will be deleted after completion.
  
 
  |  mergeCommitMessage   |  
 string 
   |  
If set, this will be used as the commit message of the merge commit.
  
 
  |  mergeStrategy   |  
 GitPullRequestMergeStrategy 
   |  
Specify the strategy used to merge the pull request during completion. If MergeStrategy is not set to any value, a no-FF merge will be created if SquashMerge == false. If MergeStrategy is not set to any value, the pull request commits will be squashed if SquashMerge == true. The SquashMerge property is deprecated. It is recommended that you explicitly set MergeStrategy in all cases. If an explicit value is provided for MergeStrategy, the SquashMerge property will be ignored.
  
 
  |  squashMerge   |  
 boolean 
   |  
SquashMerge is deprecated. You should explicitly set the value of MergeStrategy. If MergeStrategy is set to any value, the SquashMerge value will be ignored. If MergeStrategy is not set, the merge strategy will be no-fast-forward if this flag is false, or squash if true.
  
 
  |  transitionWorkItems   |  
 boolean 
   |  
If true, we will attempt to transition any work items linked to the pull request into the next logical state (i.e. Active -> Resolved)
  
 
  |  triggeredByAutoComplete   |  
 boolean 
   |  
If true, the current completion attempt was triggered via auto-complete. Used internally.
  
  

### GitPullRequestMergeOptions 
Object 
The options which are used when a pull request merge is created.
  
  | Name  | Type  | Description 
 
  |  conflictAuthorshipCommits   |  
 boolean 
   |  
If true, conflict resolutions applied during the merge will be put in separate commits to preserve authorship info for git blame, etc.
  
 
  |  detectRenameFalsePositives   |  
 boolean 
   |   
 
  |  disableRenames   |  
 boolean 
   |  
If true, rename detection will not be performed during the merge.
  
  

### GitPullRequestMergeStrategy 
Enumeration 
Specify the strategy used to merge the pull request during completion. If MergeStrategy is not set to any value, a no-FF merge will be created if SquashMerge == false. If MergeStrategy is not set to any value, the pull request commits will be squashed if SquashMerge == true. The SquashMerge property is deprecated. It is recommended that you explicitly set MergeStrategy in all cases. If an explicit value is provided for MergeStrategy, the SquashMerge property will be ignored.
  
  | Value  | Description 
 
  |  noFastForward   |  
A two-parent, no-fast-forward merge. The source branch is unchanged. This is the default behavior.
  
 
  |  squash   |  
Put all changes from the pull request into a single-parent commit.
  
 
  |  rebase   |  
Rebase the source branch on top of the target branch HEAD commit, and fast-forward the target branch. The source branch is updated during the rebase operation.
  
 
  |  rebaseMerge   |  
Rebase the source branch on top of the target branch HEAD commit, and create a two-parent, no-fast-forward merge. The source branch is updated during the rebase operation.
  
  

### GitPushRef 
Object  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
The class to represent a collection of REST reference links.
  
 
  |  date   |  
 string (date-time) 
   |   
 
  |  pushId   |  
 integer (int32) 
   |   
 
  |  pushedBy   |  
 IdentityRef 
   |   
 
  |  url   |  
 string 
   |   
  

### GitRepository 
Object  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
The class to represent a collection of REST reference links.
  
 
  |  defaultBranch   |  
 string 
   |   
 
  |  id   |  
 string (uuid) 
   |   
 
  |  isDisabled   |  
 boolean 
   |  
True if the repository is disabled. False otherwise.
  
 
  |  isFork   |  
 boolean 
   |  
True if the repository was created as a fork.
  
 
  |  isInMaintenance   |  
 boolean 
   |  
True if the repository is in maintenance. False otherwise.
  
 
  |  name   |  
 string 
   |   
 
  |  parentRepository   |  
 GitRepositoryRef 
   |   
 
  |  project   |  
 TeamProjectReference 
   |  
Represents a shallow reference to a TeamProject.
  
 
  |  remoteUrl   |  
 string 
   |   
 
  |  size   |  
 integer (int64) 
   |  
Compressed size (bytes) of the repository.
  
 
  |  sshUrl   |  
 string 
   |   
 
  |  url   |  
 string 
   |   
 
  |  validRemoteUrls   |  
 string[] 
   |   
 
  |  webUrl   |  
 string 
   |   
  

### GitRepositoryRef 
Object  
  | Name  | Type  | Description 
 
  |  collection   |  
 TeamProjectCollectionReference 
   |  
Team Project Collection where this Fork resides
  
 
  |  id   |  
 string (uuid) 
   |   
 
  |  isFork   |  
 boolean 
   |  
True if the repository was created as a fork
  
 
  |  name   |  
 string 
   |   
 
  |  project   |  
 TeamProjectReference 
   |  
Represents a shallow reference to a TeamProject.
  
 
  |  remoteUrl   |  
 string 
   |   
 
  |  sshUrl   |  
 string 
   |   
 
  |  url   |  
 string 
   |   
  

### GitStatus 
Object 
This class contains the metadata of a service/extension posting a status.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Reference links.
  
 
  |  context   |  
 GitStatusContext 
   |  
Context of the status.
  
 
  |  createdBy   |  
 IdentityRef 
   |  
Identity that created the status.
  
 
  |  creationDate   |  
 string (date-time) 
   |  
Creation date and time of the status.
  
 
  |  description   |  
 string 
   |  
Status description. Typically describes current state of the status.
  
 
  |  id   |  
 integer (int32) 
   |  
Status identifier.
  
 
  |  state   |  
 GitStatusState 
   |  
State of the status.
  
 
  |  targetUrl   |  
 string 
   |  
URL with status details.
  
 
  |  updatedDate   |  
 string (date-time) 
   |  
Last update date and time of the status.
  
  

### GitStatusContext 
Object 
Status context that uniquely identifies the status.
  
  | Name  | Type  | Description 
 
  |  genre   |  
 string 
   |  
Genre of the status. Typically name of the service/tool generating the status, can be empty.
  
 
  |  name   |  
 string 
   |  
Name identifier of the status, cannot be null or empty.
  
  

### GitStatusState 
Enumeration 
State of the status.
  
  | Value  | Description 
 
  |  notSet   |  
Status state not set. Default state.
  
 
  |  pending   |  
Status pending.
  
 
  |  succeeded   |  
Status succeeded.
  
 
  |  failed   |  
Status failed.
  
 
  |  error   |  
Status with an error.
  
 
  |  notApplicable   |  
Status is not applicable to the target object.
  
  

### GitTemplate 
Object  
  | Name  | Type  | Description 
 
  |  name   |  
 string 
   |  
Name of the Template
  
 
  |  type   |  
 string 
   |  
Type of the Template
  
  

### GitUserDate 
Object 
User info and date for Git operations.
  
  | Name  | Type  | Description 
 
  |  date   |  
 string (date-time) 
   |  
Date of the Git operation.
  
 
  |  email   |  
 string 
   |  
Email address of the user performing the Git operation.
  
 
  |  imageUrl   |  
 string 
   |  
Url for the user's avatar.
  
 
  |  name   |  
 string 
   |  
Name of the user performing the Git operation.
  
  

### IdentityRef 
Object  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
This field contains zero or more interesting links about the graph subject. These links may be invoked to obtain additional relationships or more detailed information about this graph subject.
  
 
  |  descriptor   |  
 string 
   |  
The descriptor is the primary way to reference the graph subject while the system is running. This field will uniquely identify the same graph subject across both Accounts and Organizations.
  
 
  |  directoryAlias   |  
 string 
   |  
Deprecated - Can be retrieved by querying the Graph user referenced in the "self" entry of the IdentityRef "_links" dictionary
  
 
  |  displayName   |  
 string 
   |  
This is the non-unique display name of the graph subject. To change this field, you must alter its value in the source provider.
  
 
  |  id   |  
 string 
   |   
 
  |  imageUrl   |  
 string 
   |  
Deprecated - Available in the "avatar" entry of the IdentityRef "_links" dictionary
  
 
  |  inactive   |  
 boolean 
   |  
Deprecated - Can be retrieved by querying the Graph membership state referenced in the "membershipState" entry of the GraphUser "_links" dictionary
  
 
  |  isAadIdentity   |  
 boolean 
   |  
Deprecated - Can be inferred from the subject type of the descriptor (Descriptor.IsAadUserType/Descriptor.IsAadGroupType)
  
 
  |  isContainer   |  
 boolean 
   |  
Deprecated - Can be inferred from the subject type of the descriptor (Descriptor.IsGroupType)
  
 
  |  isDeletedInOrigin   |  
 boolean 
   |   
 
  |  profileUrl   |  
 string 
   |  
Deprecated - not in use in most preexisting implementations of ToIdentityRef
  
 
  |  uniqueName   |  
 string 
   |  
Deprecated - use Domain+PrincipalName instead
  
 
  |  url   |  
 string 
   |  
This url is the full route to the source resource of this graph subject.
  
  

### IdentityRefWithVote 
Object 
Identity information including a vote on a pull request.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
This field contains zero or more interesting links about the graph subject. These links may be invoked to obtain additional relationships or more detailed information about this graph subject.
  
 
  |  descriptor   |  
 string 
   |  
The descriptor is the primary way to reference the graph subject while the system is running. This field will uniquely identify the same graph subject across both Accounts and Organizations.
  
 
  |  directoryAlias   |  
 string 
   |  
Deprecated - Can be retrieved by querying the Graph user referenced in the "self" entry of the IdentityRef "_links" dictionary
  
 
  |  displayName   |  
 string 
   |  
This is the non-unique display name of the graph subject. To change this field, you must alter its value in the source provider.
  
 
  |  hasDeclined   |  
 boolean 
   |  
Indicates if this reviewer has declined to review this pull request.
  
 
  |  id   |  
 string 
   |   
 
  |  imageUrl   |  
 string 
   |  
Deprecated - Available in the "avatar" entry of the IdentityRef "_links" dictionary
  
 
  |  inactive   |  
 boolean 
   |  
Deprecated - Can be retrieved by querying the Graph membership state referenced in the "membershipState" entry of the GraphUser "_links" dictionary
  
 
  |  isAadIdentity   |  
 boolean 
   |  
Deprecated - Can be inferred from the subject type of the descriptor (Descriptor.IsAadUserType/Descriptor.IsAadGroupType)
  
 
  |  isContainer   |  
 boolean 
   |  
Deprecated - Can be inferred from the subject type of the descriptor (Descriptor.IsGroupType)
  
 
  |  isDeletedInOrigin   |  
 boolean 
   |   
 
  |  isFlagged   |  
 boolean 
   |  
Indicates if this reviewer is flagged for attention on this pull request.
  
 
  |  isReapprove   |  
 boolean 
   |  
Indicates if this approve vote should still be handled even though vote didn't change.
  
 
  |  isRequired   |  
 boolean 
   |  
Indicates if this is a required reviewer for this pull request. 
 Branches can have policies that require particular reviewers are required for pull requests.
  
 
  |  profileUrl   |  
 string 
   |  
Deprecated - not in use in most preexisting implementations of ToIdentityRef
  
 
  |  reviewerUrl   |  
 string 
   |  
URL to retrieve information about this identity
  
 
  |  uniqueName   |  
 string 
   |  
Deprecated - use Domain+PrincipalName instead
  
 
  |  url   |  
 string 
   |  
This url is the full route to the source resource of this graph subject.
  
 
  |  vote   |  
 integer (int16) 
   |  
Vote on a pull request:
 10 - approved 5 - approved with suggestions 0 - no vote -5 - waiting for author -10 - rejected
  
 
  |  votedFor   |  
 IdentityRefWithVote[] 
   |  
Groups or teams that this reviewer contributed to. 
 Groups and teams can be reviewers on pull requests but can not vote directly. When a member of the group or team votes, that vote is rolled up into the group or team vote. VotedFor is a list of such votes.
  
  

### ItemContent 
Object  
  | Name  | Type  | Description 
 
  |  content   |  
 string 
   |   
 
  |  contentType   |  
 ItemContentType 
   |   
  

### ItemContentType 
Enumeration  
  | Value  | Description 
 
  |  rawText   |   
 
  |  base64Encoded   |   
  

### ProjectState 
Enumeration 
Project state.
  
  | Value  | Description 
 
  |  deleting   |  
Project is in the process of being deleted.
  
 
  |  new   |  
Project is in the process of being created.
  
 
  |  wellFormed   |  
Project is completely created and ready to use.
  
 
  |  createPending   |  
Project has been queued for creation, but the process has not yet started.
  
 
  |  all   |  
All projects regardless of state except Deleted.
  
 
  |  unchanged   |  
Project has not been changed.
  
 
  |  deleted   |  
Project has been deleted.
  
  

### ProjectVisibility 
Enumeration 
Project visibility.
  
  | Value  | Description 
 
  |  private   |  
The project is only visible to users with explicit access.
  
 
  |  public   |  
The project is visible to all.
  
  

### PullRequestAsyncStatus 
Enumeration 
The current status of the pull request merge.
  
  | Value  | Description 
 
  |  notSet   |  
Status is not set. Default state.
  
 
  |  queued   |  
Pull request merge is queued.
  
 
  |  conflicts   |  
Pull request merge failed due to conflicts.
  
 
  |  succeeded   |  
Pull request merge succeeded.
  
 
  |  rejectedByPolicy   |  
Pull request merge rejected by policy.
  
 
  |  failure   |  
Pull request merge failed.
  
  

### PullRequestMergeFailureType 
Enumeration 
The type of failure (if any) of the pull request merge.
  
  | Value  | Description 
 
  |  none   |  
Type is not set. Default type.
  
 
  |  unknown   |  
Pull request merge failure type unknown.
  
 
  |  caseSensitive   |  
Pull request merge failed due to case mismatch.
  
 
  |  objectTooLarge   |  
Pull request merge failed due to an object being too large.
  
  

### PullRequestStatus 
Enumeration 
The status of the pull request.
  
  | Value  | Description 
 
  |  notSet   |  
Status not set. Default state.
  
 
  |  active   |  
Pull request is active.
  
 
  |  abandoned   |  
Pull request is abandoned.
  
 
  |  completed   |  
Pull request is completed.
  
 
  |  all   |  
Used in pull request search criteria to include all statuses.
  
  

### ReferenceLinks 
Object 
The class to represent a collection of REST reference links.
  
  | Name  | Type  | Description 
 
  |  links   |  
 object 
   |  
The readonly view of the links. Because Reference links are readonly, we only want to expose them as read only.
  
  

### ResourceRef 
Object  
  | Name  | Type  | Description 
 
  |  id   |  
 string 
   |   
 
  |  url   |  
 string 
   |   
  

### TeamProjectCollectionReference 
Object 
Reference object for a TeamProjectCollection.
  
  | Name  | Type  | Description 
 
  |  avatarUrl   |  
 string 
   |  
Collection avatar Url.
  
 
  |  id   |  
 string (uuid) 
   |  
Collection Id.
  
 
  |  name   |  
 string 
   |  
Collection Name.
  
 
  |  url   |  
 string 
   |  
Collection REST Url.
  
  

### TeamProjectReference 
Object 
Represents a shallow reference to a TeamProject.
  
  | Name  | Type  | Description 
 
  |  abbreviation   |  
 string 
   |  
Project abbreviation.
  
 
  |  defaultTeamImageUrl   |  
 string 
   |  
Url to default team identity image.
  
 
  |  description   |  
 string 
   |  
The project's description (if any).
  
 
  |  id   |  
 string (uuid) 
   |  
Project identifier.
  
 
  |  lastUpdateTime   |  
 string (date-time) 
   |  
Project last update time.
  
 
  |  name   |  
 string 
   |  
Project name.
  
 
  |  revision   |  
 integer (int64) 
   |  
Project revision.
  
 
  |  state   |  
 ProjectState 
   |  
Project state.
  
 
  |  url   |  
 string 
   |  
Url to the full version of the object.
  
 
  |  visibility   |  
 ProjectVisibility 
   |  
Project visibility.
  
  

### VersionControlChangeType 
Enumeration 
The type of change that was made to the item.
  
  | Value  | Description 
 
  |  none   |   
 
  |  add   |   
 
  |  edit   |   
 
  |  encoding   |   
 
  |  rename   |   
 
  |  delete   |   
 
  |  undelete   |   
 
  |  branch   |   
 
  |  merge   |   
 
  |  lock   |   
 
  |  rollback   |   
 
  |  sourceRename   |   
 
  |  targetRename   |   
 
  |  property   |   
 
  |  all   |   
  

### WebApiTagDefinition 
Object 
The representation of a tag definition which is sent across the wire.
  
  | Name  | Type  | Description 
 
  |  active   |  
 boolean 
   |  
Whether or not the tag definition is active.
  
 
  |  id   |  
 string (uuid) 
   |  
ID of the tag definition.
  
 
  |  name   |  
 string 
   |  
The name of the tag definition.
  
 
  |  url   |  
 string 
   |  
Resource URL for the Tag Definition.
  
    
 
 
    

## Feedback 
 
 Was this page helpful? 
 
        No  
 
 Need help with this topic? 
 
 Want to try using Ask Learn to clarify or guide you through this topic? 
 
 
                 
 
    
 
 Was this page helpful? 
 
         
 
 Need help with this topic? 
 
 Want to try using Ask Learn to clarify or guide you through this topic? 
 
 
                
  
  
 en-us 
   Your Privacy Choices  
 
  
  
-    
-    
-          
-  
-  AI Disclaimer 
-  Previous Versions  
-  Blog  
-  Contribute 
-  Privacy 
-  Consumer Health Privacy 
-  Terms of Use 
-  Trademarks  
- © Microsoft 2026
