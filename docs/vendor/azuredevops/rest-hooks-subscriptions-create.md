<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/hooks/subscriptions/create?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Subscriptions - Create 
  Service:  Service Hooks    API Version:  7.1      
Create a subscription.
 

```
POST https://dev.azure.com/{organization}/_apis/hooks/subscriptions?api-version=7.1
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
  
  

## Request Body  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Reference Links
  
 
  |  actionDescription   |  
 string 
   |   
 
  |  consumerActionId   |  
 string 
   |   
 
  |  consumerId   |  
 string 
   |   
 
  |  consumerInputs   |  
 object 
   |  
Consumer input values
  
 
  |  createdBy   |  
 IdentityRef 
   |   
 
  |  createdDate   |  
 string (date-time) 
   |   
 
  |  eventDescription   |  
 string 
   |   
 
  |  eventType   |  
 string 
   |   
 
  |  id   |  
 string (uuid) 
   |   
 
  |  lastProbationRetryDate   |  
 string (date-time) 
   |   
 
  |  modifiedBy   |  
 IdentityRef 
   |   
 
  |  modifiedDate   |  
 string (date-time) 
   |   
 
  |  probationRetries   |  
 string (byte) 
   |   
 
  |  publisherId   |  
 string 
   |   
 
  |  publisherInputs   |  
 object 
   |  
Publisher input values
  
 
  |  resourceVersion   |  
 string 
   |   
 
  |  status   |  
 SubscriptionStatus 
   |   
 
  |  subscriber   |  
 IdentityRef 
   |   
 
  |  url   |  
 string 
   |   
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 Subscription 
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
 
  |  vso.work   |  Grants the ability to read work items, queries, boards, area and iterations paths, and other work item tracking related metadata. Also grants the ability to execute queries, search work items and to receive notifications about work item events via service hooks.  
 
  |  vso.build   |  Grants the ability to access build artifacts, including build results, definitions, and requests, and the ability to receive notifications about build events via service hooks.  
 
  |  vso.code   |  Grants the ability to read source code and metadata about commits, changesets, branches, and other version control artifacts. Also grants the ability to search code and get notified about version control events via service hooks.  
  

## Examples 
 

#### Sample request 
  
-  HTTP    

```
POST https://dev.azure.com/fabrikam/_apis/hooks/subscriptions?api-version=7.1

{
  "publisherId": "tfs",
  "eventType": "build.complete",
  "resourceVersion": "1.0-preview.1",
  "consumerId": "webHooks",
  "consumerActionId": "httpRequest",
  "publisherInputs": {
    "buildStatus": "Failed",
    "definitionName": "MyWebSite CI",
    "projectId": "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"
  },
  "consumerInputs": {
    "url": "https://myservice/newreceiver"
  }
}

```
    
 

#### Sample response 
  Status code:   200    

```
{
  "id": "fd672255-8b6b-4769-9260-beea83d752ce",
  "url": "https://dev.azure.com/fabrikam/_apis/hooks/subscriptions/fd672255-8b6b-4769-9260-beea83d752ce",
  "publisherId": "tfs",
  "eventType": "build.complete",
  "resourceVersion": "1.0-preview.1",
  "eventDescription": "Build MyWebSite CI, status Failed",
  "consumerId": "webHooks",
  "consumerActionId": "httpRequest",
  "actionDescription": "To host myservice",
  "createdBy": {
    "id": "00ca946b-2fe9-4f2a-ae2f-40d5c48001bc"
  },
  "createdDate": "2014-10-27T15:37:24.873Z",
  "modifiedBy": {
    "id": "00ca946b-2fe9-4f2a-ae2f-40d5c48001bc"
  },
  "modifiedDate": "2014-10-27T15:37:26.23Z",
  "publisherInputs": {
    "buildStatus": "Failed",
    "definitionName": "MyWebSite CI",
    "hostId": "d81542e4-cdfa-4333-b082-1ae2d6c3ad16",
    "projectId": "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c",
    "tfsSubscriptionId": "3e8b33e7-426d-4c92-9bf9-58e163dd7dd5"
  },
  "consumerInputs": {
    "url": "https://myservice/newreceiver"
  }
}
```
  

## Definitions  
  | Name  | Description 
 
  |  IdentityRef   |   
 
  |  ReferenceLinks   |  
The class to represent a collection of REST reference links.
  
 
  |  Subscription   |  
Encapsulates an event subscription.
  
 
  |  SubscriptionStatus   |   
  

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
  
  

### ReferenceLinks 
Object 
The class to represent a collection of REST reference links.
  
  | Name  | Type  | Description 
 
  |  links   |  
 object 
   |  
The readonly view of the links. Because Reference links are readonly, we only want to expose them as read only.
  
  

### Subscription 
Object 
Encapsulates an event subscription.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Reference Links
  
 
  |  actionDescription   |  
 string 
   |   
 
  |  consumerActionId   |  
 string 
   |   
 
  |  consumerId   |  
 string 
   |   
 
  |  consumerInputs   |  
 object 
   |  
Consumer input values
  
 
  |  createdBy   |  
 IdentityRef 
   |   
 
  |  createdDate   |  
 string (date-time) 
   |   
 
  |  eventDescription   |  
 string 
   |   
 
  |  eventType   |  
 string 
   |   
 
  |  id   |  
 string (uuid) 
   |   
 
  |  lastProbationRetryDate   |  
 string (date-time) 
   |   
 
  |  modifiedBy   |  
 IdentityRef 
   |   
 
  |  modifiedDate   |  
 string (date-time) 
   |   
 
  |  probationRetries   |  
 string (byte) 
   |   
 
  |  publisherId   |  
 string 
   |   
 
  |  publisherInputs   |  
 object 
   |  
Publisher input values
  
 
  |  resourceVersion   |  
 string 
   |   
 
  |  status   |  
 SubscriptionStatus 
   |   
 
  |  subscriber   |  
 IdentityRef 
   |   
 
  |  url   |  
 string 
   |   
  

### SubscriptionStatus 
Enumeration  
  | Value  | Description 
 
  |  enabled   |  
The subscription is enabled.
  
 
  |  onProbation   |  
The subscription is temporarily on probation by the system.
  
 
  |  disabledByUser   |  
The subscription is disabled by a user.
  
 
  |  disabledBySystem   |  
The subscription is disabled by the system.
  
 
  |  disabledByInactiveIdentity   |  
The subscription is disabled because the owner is inactive or is missing permissions.
  
    
 
 
    

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
