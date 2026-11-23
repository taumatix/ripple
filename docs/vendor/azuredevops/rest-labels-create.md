<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-labels/create?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Pull Request Labels - Create 
  Service:  Git    API Version:  7.1      
Create a tag (if that does not exists yet) and add that as a label (tag) for a specified pull request. The only required field is the name of the new label (tag).
 

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}/labels?api-version=7.1
```
 
 
 With optional parameters:  

```
POST https://dev.azure.com/{organization}/{project}/_apis/git/repositories/{repositoryId}/pullRequests/{pullRequestId}/labels?projectId={projectId}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
organization   | path  |  True   |  
 string 
   |  
The name of the Azure DevOps organization.
  
 
  |  
pullRequestId   | path  |  True   |  
 integer (int32) 
   |  
ID of the pull request.
  
 
  |  
repositoryId   | path  |  True   |  
 string 
   |  
The repository ID of the pull request’s target branch.
  
 
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
projectId   | query  |    |  
 string 
   |  
Project ID or project name.
  
  

## Request Body  
  | Name  | Type  | Description 
 
  |  name   |  
 string 
   |  
Name of the tag definition that will be created.
  
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 WebApiTagDefinition 
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
  

## Definitions  
  | Name  | Description 
 
  |  WebApiCreateTagRequestData   |  
The representation of data needed to create a tag definition which is sent across the wire.
  
 
  |  WebApiTagDefinition   |  
The representation of a tag definition which is sent across the wire.
  
  

### WebApiCreateTagRequestData 
Object 
The representation of data needed to create a tag definition which is sent across the wire.
  
  | Name  | Type  | Description 
 
  |  name   |  
 string 
   |  
Name of the tag definition that will be created.
  
  

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
