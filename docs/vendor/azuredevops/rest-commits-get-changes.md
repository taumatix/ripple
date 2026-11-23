<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get-changes?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Commits - Get Changes 
  Service:  Git    API Version:  7.1      
Retrieve changes for a particular commit.
 

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/commits/{commitId}/changes?api-version=7.1
```
 
 
 With optional parameters:  

```
GET https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/commits/{commitId}/changes?top={top}&skip={skip}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
commitId   | path  |  True   |  
 string 
   |  
The id of the commit.
  
 
  |  
organization   | path  |  True   |  
 string 
   |  
The name of the Azure DevOps organization.
  
 
  |  
repositoryId   | path  |  True   |  
 string 
   |  
The id or friendly name of the repository. To use the friendly name, projectId must also be specified.
  
 
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
skip   | query  |    |  
 integer (int32) 
   |  
The number of changes to skip.
  
 
  |  
top   | query  |    |  
 integer (int32) 
   |  
The maximum number of changes to return.
  
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 GitCommitChanges 
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

### With changes 
 

#### Sample request 
  
-  HTTP    

```
GET https://dev.azure.com/fabrikam/_apis/git/repositories/278d5cd2-584d-4b63-824a-2ba458937249/commits/be67f8871a4d2c75f13a51c1d3c30ac0d74d4ef4/changes?top=2&skip=10&api-version=7.1

```
    
 

#### Sample response 
  Status code:   200    

```
{
  "changeCounts": {
    "Add": 456
  },
  "changes": [
    {
      "item": {
        "gitObjectType": "blob",
        "path": "/MyWebSite/MyWebSite/favicon.ico",
        "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/278d5cd2-584d-4b63-824a-2ba458937249/items/MyWebSite/MyWebSite/favicon.ico?versionType=Commit"
      },
      "changeType": "add"
    },
    {
      "item": {
        "gitObjectType": "tree",
        "path": "/MyWebSite/MyWebSite/fonts",
        "isFolder": true,
        "url": "https://dev.azure.com/fabrikam/_apis/git/repositories/278d5cd2-584d-4b63-824a-2ba458937249/items/MyWebSite/MyWebSite/fonts?versionType=Commit"
      },
      "changeType": "add"
    }
  ]
}
```
  

## Definitions  
  | Name  | Description 
 
  |  ChangeCountDictionary   |   
 
  |  GitChange   |   
 
  |  GitCommitChanges   |   
 
  |  GitTemplate   |   
 
  |  ItemContent   |   
 
  |  ItemContentType   |   
 
  |  VersionControlChangeType   |  
The type of change that was made to the item.
  
  

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
  
  

### GitCommitChanges 
Object  
  | Name  | Type  | Description 
 
  |  changeCounts   |  
 ChangeCountDictionary 
   |   
 
  |  changes   |  
 GitChange[] 
   |   
  

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
