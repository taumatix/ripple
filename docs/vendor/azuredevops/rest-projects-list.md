<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/core/projects/list?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script). (c) Microsoft. -->
Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Projects - List 
  Service:  Core    API Version:  7.1      
Get all projects in the organization that the authenticated user has access to.
 

```
GET https://dev.azure.com/{organization}/_apis/projects?api-version=7.1
```
 
 
 With optional parameters:  

```
GET https://dev.azure.com/{organization}/_apis/projects?stateFilter={stateFilter}&$top={$top}&$skip={$skip}&continuationToken={continuationToken}&getDefaultTeamImageUrl={getDefaultTeamImageUrl}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
organization   | path  |  True   |  
 string 
   |  
The name of the Azure DevOps organization.
  
 
  |  
api-version   | query  |  True   |  
 string 
   |  
Version of the API to use. This should be set to '7.1' to use this version of the api.
  
 
  |  
$skip   | query  |    |  
 integer (int32) 
   |   
 
  |  
$top   | query  |    |  
 integer (int32) 
   |   
 
  |  
continuationToken   | query  |    |  
 integer (int32) 
   |  
Pointer that shows how many projects already been fetched.
  
 
  |  
getDefaultTeamImageUrl   | query  |    |  
 boolean 
   |   
 
  |  
stateFilter   | query  |    |  
 ProjectState 
   |  
Filter on team projects in a specific team project state (default: WellFormed).
  
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 TeamProjectReference[] 
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
 
  |  vso.profile   |  Grants the ability to read your profile, accounts, collections, projects, teams, and other top-level organizational artifacts.  
 
  |  vso.project   |  Grants the ability to read projects and teams.  
  

## Examples 
 

#### Sample request 
  
-  HTTP    

```
GET https://dev.azure.com/fabrikam/_apis/projects?api-version=7.1

```
    
 

#### Sample response 
  Status code:   200    

```
{
  "count": 3,
  "value": [
    {
      "id": "eb6e4656-77fc-42a1-9181-4c6d8e9da5d1",
      "name": "Fabrikam-Fiber-TFVC",
      "description": "Team Foundation Version Control projects.",
      "url": "https://dev.azure.com/fabrikam/_apis/projects/eb6e4656-77fc-42a1-9181-4c6d8e9da5d1",
      "state": "wellFormed"
    },
    {
      "id": "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
      "name": "Fabrikam-Fiber-Git",
      "description": "Git projects",
      "url": "https://dev.azure.com/fabrikam/_apis/projects/6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
      "state": "wellFormed"
    },
    {
      "id": "281f9a5b-af0d-49b4-a1df-fe6f5e5f84d0",
      "name": "TestGit",
      "url": "https://dev.azure.com/fabrikam/_apis/projects/281f9a5b-af0d-49b4-a1df-fe6f5e5f84d0",
      "state": "wellFormed"
    }
  ]
}
```
  

## Definitions  
  | Name  | Description 
 
  |  ProjectState   |  
The current state of the project.
  
 
  |  ProjectVisibility   |  
Indicates whom the project is visible to.
  
 
  |  TeamProjectReference   |  
Represents a shallow reference to a TeamProject.
  
  

### ProjectState 
Enumeration 
The current state of the project.
  
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
Indicates whom the project is visible to.
  
  | Value  | Description 
 
  |  private   |  
The project is only visible to users with explicit access.
  
 
  |  public   |  
The project is visible to all.
  
  

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
