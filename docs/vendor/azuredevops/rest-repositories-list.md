<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/git/repositories/list?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Repositories - List 
  Service:  Git    API Version:  7.1      
Retrieve git repositories.
 

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories?api-version=7.1
```
 
 
 With optional parameters:  

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories?includeLinks={includeLinks}&includeAllUrls={includeAllUrls}&includeHidden={includeHidden}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
organization   | path  |  True   |  
 string 
   |  
The name of the Azure DevOps organization.
  
 
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
includeAllUrls   | query  |    |  
 boolean 
   |  
[optional] True to include all remote URLs. The default value is false.
  
 
  |  
includeHidden   | query  |    |  
 boolean 
   |  
[optional] True to include hidden repositories. The default value is false.
  
 
  |  
includeLinks   | query  |    |  
 boolean 
   |  
[optional] True to include reference links. The default value is false.
  
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 GitRepository[] 
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
 
  |  vso.code   |  Grants the ability to read source code and metadata about commits, changesets, branches, and other version control artifacts. Also grants the ability to search code and get notified about version control events via service hooks.  
  

## Examples 
 

#### Sample request 
  
-  HTTP    

```
GET https://dev.azure.com/fabrikam/_apis/git/repositories?api-version=7.1

```
    
 

#### Sample response 
  Status code:   200    

```
{
  "count": 3,
  "value": [
    {
      "id": "5febef5a-833d-4e14-b9c0-14cb638f91e6",
      "name": "AnotherRepository",
      "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/5febef5a-833d-4e14-b9c0-14cb638f91e6",
      "project": {
        "id": "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
        "name": "Fabrikam-Fiber-Git",
        "url": "https://dev.azure.com/fabrikam/_apis/projects/6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
        "state": "wellFormed"
      },
      "remoteUrl": "https://dev.azure.com/fabrikam/Fabrikam-Fiber-Git/_git/AnotherRepository"
    },
    {
      "id": "278d5cd2-584d-4b63-824a-2ba458937249",
      "name": "Fabrikam-Fiber-Git",
      "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/278d5cd2-584d-4b63-824a-2ba458937249",
      "project": {
        "id": "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
        "name": "Fabrikam-Fiber-Git",
        "url": "https://dev.azure.com/fabrikam/_apis/projects/6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
        "state": "wellFormed"
      },
      "defaultBranch": "refs/heads/master",
      "remoteUrl": "https://dev.azure.com/fabrikam/_git/Fabrikam-Fiber-Git"
    },
    {
      "id": "66efb083-777a-4cac-a350-a24b046be6be",
      "name": "TestGit",
      "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/66efb083-777a-4cac-a350-a24b046be6be",
      "project": {
        "id": "281f9a5b-af0d-49b4-a1df-fe6f5e5f84d0",
        "name": "TestGit",
        "url": "https://dev.azure.com/fabrikam/_apis/projects/281f9a5b-af0d-49b4-a1df-fe6f5e5f84d0",
        "state": "wellFormed"
      },
      "defaultBranch": "refs/heads/master",
      "remoteUrl": "https://dev.azure.com/fabrikam/_git/TestGit"
    }
  ]
}
```
  

## Definitions  
  | Name  | Description 
 
  |  GitRepository   |   
 
  |  GitRepositoryRef   |   
 
  |  ProjectState   |  
Project state.
  
 
  |  ProjectVisibility   |  
Project visibility.
  
 
  |  ReferenceLinks   |  
The class to represent a collection of REST reference links.
  
 
  |  TeamProjectCollectionReference   |  
Reference object for a TeamProjectCollection.
  
 
  |  TeamProjectReference   |  
Represents a shallow reference to a TeamProject.
  
  

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
  
  

### ReferenceLinks 
Object 
The class to represent a collection of REST reference links.
  
  | Name  | Type  | Description 
 
  |  links   |  
 object 
   |  
The readonly view of the links. Because Reference links are readonly, we only want to expose them as read only.
  
  

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
