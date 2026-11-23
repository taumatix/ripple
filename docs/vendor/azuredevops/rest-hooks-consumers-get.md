<!-- Vendored from https://learn.microsoft.com/en-us/rest/api/azure/devops/hooks/consumers/get?view=azure-devops-rest-7.1 on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    
 
 

# Consumers - Get 
  Service:  Service Hooks    API Version:  7.1      
Get a specific consumer service. Optionally filter out consumer actions that do not support any event types for the specified publisher.
 

```
GET https://dev.azure.com/{organization}/_apis/hooks/consumers/{consumerId}?api-version=7.1
```
 
 
 With optional parameters:  

```
GET https://dev.azure.com/{organization}/_apis/hooks/consumers/{consumerId}?publisherId={publisherId}&api-version=7.1
```
  

## URI Parameters  
  | Name  | In  | Required  | Type  | Description 
 
  |  
consumerId   | path  |  True   |  
 string 
   |  
ID for a consumer.
  
 
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
publisherId   | query  |    |  
 string 
   |   
  

## Responses  
  | Name  | Type  | Description 
 
  |  200 OK   |  
 Consumer 
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
GET https://dev.azure.com/fabrikam/_apis/hooks/consumers/zendesk?api-version=7.1

```
    
 

#### Sample response 
  Status code:   200    

```
{
  "id": "zendesk",
  "url": "https://dev.azure.com/fabrikam/_apis/hooks/consumers/zendesk",
  "name": "Zendesk",
  "description": "Zendesk is a SaaS suite that offers help desk ticketing, issue tracking, and customer service support.",
  "imageUrl": "",
  "informationUrl": "http://go.microsoft.com/fwlink/?LinkID=396756",
  "authenticationType": "external",
  "inputDescriptors": [
    {
      "id": "accountName",
      "name": "Account name",
      "description": "Zendesk account name like https://<account name>.zendesk.com",
      "inputMode": "textBox",
      "isConfidential": false,
      "useInDefaultDescription": true,
      "validation": {
        "dataType": "string",
        "isRequired": true,
        "pattern": "^([A-Za-z0-9][A-Za-z0-9\\-]{0,61}[A-Za-z0-9]|[A-Za-z0-9]{1,63})$",
        "maxLength": 63
      }
    },
    {
      "id": "username",
      "name": "User name",
      "description": "The Zendesk user name of a user who will update tickets",
      "inputMode": "textBox",
      "isConfidential": false,
      "useInDefaultDescription": false,
      "validation": {
        "dataType": "string",
        "isRequired": true,
        "pattern": "^.+\\@.+\\..+$",
        "maxLength": 254
      }
    },
    {
      "id": "apiToken",
      "name": "API token",
      "description": "The Zendesk API token (can be found in Zendesk app in Admin > Channels > API)",
      "inputMode": "passwordBox",
      "isConfidential": true,
      "useInDefaultDescription": false,
      "validation": {
        "dataType": "string",
        "isRequired": true,
        "maxLength": 100
      }
    }
  ],
  "actions": [
    {
      "id": "createPrivateComment",
      "consumerId": "zendesk",
      "url": "https://dev.azure.com/fabrikam/_apis/hooks/consumers/zendesk/actions/createPrivateComment",
      "name": "Create a private comment in a ticket",
      "description": "Create a private comment in a ticket. <a href='http://go.microsoft.com/fwlink/?LinkId=396756'>Learn more.</a>",
      "supportedEventTypes": [
        "workitem.commented"
      ],
      "supportedResourceVersions": {
        "workitem.commented": [
          "1.0-preview.1"
        ]
      },
      "inputDescriptors": []
    }
  ]
}
```
  

## Definitions  
  | Name  | Description 
 
  |  AuthenticationType   |  
Gets or sets this consumer's authentication type.
  
 
  |  Consumer   |  
Defines the data contract of a consumer.
  
 
  |  ConsumerAction   |  
Defines the data contract of a consumer action.
  
 
  |  ExternalConfigurationDescriptor   |  
Describes how to configure a subscription that is managed externally.
  
 
  |  InputDataType   |  
Gets or sets the data type to validate.
  
 
  |  InputDescriptor   |  
Describes an input for subscriptions.
  
 
  |  InputMode   |  
Mode in which the value of this input should be entered
  
 
  |  InputValidation   |  
Describes what values are valid for a subscription input
  
 
  |  InputValue   |  
Information about a single value for an input
  
 
  |  InputValues   |  
Information about the possible/allowed values for a given subscription input
  
 
  |  InputValuesError   |  
Error information related to a subscription input value.
  
 
  |  ReferenceLinks   |  
The class to represent a collection of REST reference links.
  
  

### AuthenticationType 
Enumeration 
Gets or sets this consumer's authentication type.
  
  | Value  | Description 
 
  |  none   |  
No authentication is required.
  
 
  |  oAuth   |  
OAuth authentication.
  
 
  |  external   |  
Externally-configured authentication.
  
  

### Consumer 
Object 
Defines the data contract of a consumer.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Reference Links
  
 
  |  actions   |  
 ConsumerAction[] 
   |  
Gets this consumer's actions.
  
 
  |  authenticationType   |  
 AuthenticationType 
   |  
Gets or sets this consumer's authentication type.
  
 
  |  description   |  
 string 
   |  
Gets or sets this consumer's localized description.
  
 
  |  externalConfiguration   |  
 ExternalConfigurationDescriptor 
   |  
Non-null only if subscriptions for this consumer are configured externally.
  
 
  |  id   |  
 string 
   |  
Gets or sets this consumer's identifier.
  
 
  |  imageUrl   |  
 string 
   |  
Gets or sets this consumer's image URL, if any.
  
 
  |  informationUrl   |  
 string 
   |  
Gets or sets this consumer's information URL, if any.
  
 
  |  inputDescriptors   |  
 InputDescriptor[] 
   |  
Gets or sets this consumer's input descriptors.
  
 
  |  name   |  
 string 
   |  
Gets or sets this consumer's localized name.
  
 
  |  url   |  
 string 
   |  
The url for this resource
  
  

### ConsumerAction 
Object 
Defines the data contract of a consumer action.
  
  | Name  | Type  | Description 
 
  |  _links   |  
 ReferenceLinks 
   |  
Reference Links
  
 
  |  allowResourceVersionOverride   |  
 boolean 
   |  
Gets or sets the flag indicating if resource version can be overridden when creating or editing a subscription.
  
 
  |  consumerId   |  
 string 
   |  
Gets or sets the identifier of the consumer to which this action belongs.
  
 
  |  description   |  
 string 
   |  
Gets or sets this action's localized description.
  
 
  |  id   |  
 string 
   |  
Gets or sets this action's identifier.
  
 
  |  inputDescriptors   |  
 InputDescriptor[] 
   |  
Gets or sets this action's input descriptors.
  
 
  |  name   |  
 string 
   |  
Gets or sets this action's localized name.
  
 
  |  supportedEventTypes   |  
 string[] 
   |  
Gets or sets this action's supported event identifiers.
  
 
  |  supportedResourceVersions   |  
 object 
   |  
Gets or sets this action's supported resource versions.
  
 
  |  url   |  
 string 
   |  
The url for this resource
  
  

### ExternalConfigurationDescriptor 
Object 
Describes how to configure a subscription that is managed externally.
  
  | Name  | Type  | Description 
 
  |  createSubscriptionUrl   |  
 string 
   |  
Url of the site to create this type of subscription.
  
 
  |  editSubscriptionPropertyName   |  
 string 
   |  
The name of an input property that contains the URL to edit a subscription.
  
 
  |  hostedOnly   |  
 boolean 
   |  
True if the external configuration applies only to hosted.
  
  

### InputDataType 
Enumeration 
Gets or sets the data type to validate.
  
  | Value  | Description 
 
  |  none   |  
No data type is specified.
  
 
  |  string   |  
Represents a textual value.
  
 
  |  number   |  
Represents a numeric value.
  
 
  |  boolean   |  
Represents a value of true or false.
  
 
  |  guid   |  
Represents a Guid.
  
 
  |  uri   |  
Represents a URI.
  
  

### InputDescriptor 
Object 
Describes an input for subscriptions.
  
  | Name  | Type  | Description 
 
  |  dependencyInputIds   |  
 string[] 
   |  
The ids of all inputs that the value of this input is dependent on.
  
 
  |  description   |  
 string 
   |  
Description of what this input is used for
  
 
  |  groupName   |  
 string 
   |  
The group localized name to which this input belongs and can be shown as a header for the container that will include all the inputs in the group.
  
 
  |  hasDynamicValueInformation   |  
 boolean 
   |  
If true, the value information for this input is dynamic and should be fetched when the value of dependency inputs change.
  
 
  |  id   |  
 string 
   |  
Identifier for the subscription input
  
 
  |  inputMode   |  
 InputMode 
   |  
Mode in which the value of this input should be entered
  
 
  |  isConfidential   |  
 boolean 
   |  
Gets whether this input is confidential, such as for a password or application key
  
 
  |  name   |  
 string 
   |  
Localized name which can be shown as a label for the subscription input
  
 
  |  properties   |  
 object 
   |  
Custom properties for the input which can be used by the service provider
  
 
  |  type   |  
 string 
   |  
Underlying data type for the input value. When this value is specified, InputMode, Validation and Values are optional.
  
 
  |  useInDefaultDescription   |  
 boolean 
   |  
Gets whether this input is included in the default generated action description.
  
 
  |  validation   |  
 InputValidation 
   |  
Information to use to validate this input's value
  
 
  |  valueHint   |  
 string 
   |  
A hint for input value. It can be used in the UI as the input placeholder.
  
 
  |  values   |  
 InputValues 
   |  
Information about possible values for this input
  
  

### InputMode 
Enumeration 
Mode in which the value of this input should be entered
  
  | Value  | Description 
 
  |  none   |  
This input should not be shown in the UI
  
 
  |  textBox   |  
An input text box should be shown
  
 
  |  passwordBox   |  
An password input box should be shown
  
 
  |  combo   |  
A select/combo control should be shown
  
 
  |  radioButtons   |  
Radio buttons should be shown
  
 
  |  checkBox   |  
Checkbox should be shown(for true/false values)
  
 
  |  textArea   |  
A multi-line text area should be shown
  
  

### InputValidation 
Object 
Describes what values are valid for a subscription input
  
  | Name  | Type  | Description 
 
  |  dataType   |  
 InputDataType 
   |  
Gets or sets the data type to validate.
  
 
  |  isRequired   |  
 boolean 
   |  
Gets or sets if this is a required field.
  
 
  |  maxLength   |  
 integer (int32) 
   |  
Gets or sets the maximum length of this descriptor.
  
 
  |  maxValue   |  
 string (decimal) 
   |  
Gets or sets the minimum value for this descriptor.
  
 
  |  minLength   |  
 integer (int32) 
   |  
Gets or sets the minimum length of this descriptor.
  
 
  |  minValue   |  
 string (decimal) 
   |  
Gets or sets the minimum value for this descriptor.
  
 
  |  pattern   |  
 string 
   |  
Gets or sets the pattern to validate.
  
 
  |  patternMismatchErrorMessage   |  
 string 
   |  
Gets or sets the error on pattern mismatch.
  
  

### InputValue 
Object 
Information about a single value for an input
  
  | Name  | Type  | Description 
 
  |  data   |  
 object 
   |  
Any other data about this input
  
 
  |  displayValue   |  
 string 
   |  
The text to show for the display of this value
  
 
  |  value   |  
 string 
   |  
The value to store for this input
  
  

### InputValues 
Object 
Information about the possible/allowed values for a given subscription input
  
  | Name  | Type  | Description 
 
  |  defaultValue   |  
 string 
   |  
The default value to use for this input
  
 
  |  error   |  
 InputValuesError 
   |  
Errors encountered while computing dynamic values.
  
 
  |  inputId   |  
 string 
   |  
The id of the input
  
 
  |  isDisabled   |  
 boolean 
   |  
Should this input be disabled
  
 
  |  isLimitedToPossibleValues   |  
 boolean 
   |  
Should the value be restricted to one of the values in the PossibleValues (True) or are the values in PossibleValues just a suggestion (False)
  
 
  |  isReadOnly   |  
 boolean 
   |  
Should this input be made read-only
  
 
  |  possibleValues   |  
 InputValue[] 
   |  
Possible values that this input can take
  
  

### InputValuesError 
Object 
Error information related to a subscription input value.
  
  | Name  | Type  | Description 
 
  |  message   |  
 string 
   |  
The error message.
  
  

### ReferenceLinks 
Object 
The class to represent a collection of REST reference links.
  
  | Name  | Type  | Description 
 
  |  links   |  
 object 
   |  
The readonly view of the links. Because Reference links are readonly, we only want to expose them as read only.
  
    
 
 
    

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
