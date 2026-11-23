<!-- Vendored from https://learn.microsoft.com/en-us/azure/devops/service-hooks/overview?view=azure-devops on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    

# Integrate with service hooks 
 
    
 
       
   

Azure DevOps Services | Azure DevOps Server | Azure DevOps Server 2022
 
You can use service hooks to run tasks on other services when events happen in your Azure DevOps project.
 
For example, you can use a service hook to automatically create a card in Trello when a work item gets created in Azure DevOps. Or you can automatically send a push notification to your team's mobile devices when a build fails. You can also use service hooks in custom apps and services as a more efficient way to drive activities when events happen in your projects.
 

## How do service hooks work? 
Service hook publishers define a set of events that you can subscribe to. Subscriptions listen for these events and define actions to take based on events.
 
Subscriptions also target consumers, which are external services that can run their own actions when events occur.
 
   
 
 
 
Note
 
To use service hooks, you must allow specific IP address ranges for inbound connections to service endpoints. A service endpoint is a set of properties provided to a service hook. For more information, see IP addresses and range restrictions.
   

## Available services 
The following services are available as targets of service hooks. For more information about other apps and services that integrate with Azure DevOps, see the Visual Studio Marketplace.
 
   
  | Service  | Supported events  | Supported actions  | Subscription management 
   
  | Visual Studio App Center  | Work item updated  | Send a notification  | In App Center 
 
  | AppVeyor  | Code pushed  | Trigger an AppVeyor build  | In AppVeyor 
 
  | Azuqua  | All  | Post an event to a flow language object (FLO)  | In Azuqua 
 
  | Azure App Service  | Code pushed  | Deploy a web app  | In App Service 
 
  | Azure Service Bus  | All  | Send a message to Azure Notification Hubs, a Service Bus queue, or a Service Bus topic  | In Azure DevOps 
 
  | Azure Storage  | All  | Insert a message in a Storage queue  | In Azure DevOps 
 
  | Bamboo  | Build completed, code pushed  | Queue a build  | In Azure DevOps 
 
  | Datadog  | All  | Post an event in Datadog  | In Azure DevOps 
 
  | Grafana  | Release deployment completed  | Add an annotation to a Grafana database  | In Azure DevOps 
 
  | Jenkins  | Build completed, code pushed, PR merge attempted  | Trigger a generic or Git build  | In Azure DevOps 
 
  | Microsoft Teams  | All  | Post a message to a channel  | In Teams 
 
  | MyGet  | Build completed, code pushed  | Publish a NuGet package to MyGet, trigger a MyGet build  | In MyGet 
 
  | Office 365  | All  | Post a message to a group  | In Office 365 
 
  | Slack  | All  | Post a message to a channel  | In Azure DevOps 
 
  | Trello  | All  | Create a card or list  | In Azure DevOps 
 
  | UserVoice  | Work item created, work item updated  | Send a linked work item event  | In UserVoice 
 
  | Webhooks  | All  | Post a request through HTTP  | In Azure DevOps 
 
  | Workplace messaging apps  | All  | Send a notification  | In workplace messaging apps 
 
  | Zapier  | All  | Send a notification  | In Zapier 
 
  | Zendesk  | Work item commented on  | Create a private comment in a ticket  | In Azure DevOps 
    
   
  | Service  | Supported events  | Supported actions  | Subscription management 
   
  | Azure Service Bus  | All  | Send a message to Azure Notification Hubs, a Service Bus queue, or a Service Bus topic  | In Azure DevOps 
 
  | Azure Storage  | All  | Insert a message in a Storage queue  | In Azure DevOps 
 
  | Bamboo  | Build completed, code pushed  | Queue a build  | In Azure DevOps 
 
  | Datadog  | All  | Post an event in Datadog  | In Azure DevOps 
 
  | Grafana  | Release deployment completed  | Add an annotation to a Grafana database  | In Azure DevOps 
 
  | Jenkins  | Build completed, code pushed, PR merge attempted  | Trigger a generic or Git build  | In Azure DevOps 
 
  | Microsoft Teams  | All  | Post a message to a channel  | In Teams 
 
  | Office 365  | All  | Post a message to a group  | In Office 365 
 
  | Slack  | All  | Post a message to a channel  | In Azure DevOps 
 
  | Trello  | All  | Create a card or list  | In Azure DevOps 
 
  | UserVoice  | Work item created, work item updated  | Send a linked work item event  | In UserVoice 
 
  | Webhooks  | All  | Post a request through HTTP  | In Azure DevOps 
 
  | Zendesk  | Work item commented on  | Create a private comment in a ticket  | In Azure DevOps 
    

## Create a subscription 
To integrate one of these services with Azure DevOps, you create a subscription. In many cases, you also need to configure the target service. For detailed information, see the documentation for the service that you want to integrate.
  
- 
Go to your project, select Project settings, and then select Service hooks.
 
   
  
- 
Select Create subscription.
 
   
  
- 
Select the service that you want to integrate.
 
   
  
- 
Select the event to trigger on and any applicable filters.
 
   
  
- 
Select an action to run on the target service.
 
 
Note
 
The available actions depend on the type of event that you select.
  
   
  
- 
To confirm the settings are correct, test the subscription and then finish the wizard.
 
   
 
   
   

## FAQs 

 

### Q: What permissions do I need to set up a subscription? 
A: You need the Edit subscriptions and View subscriptions permissions. By default, only project administrators have these permissions. To grant them to other users directly, you can use a command-line tool or the Security REST API.
 
To grant the View permission to a group, see Set View permission for a group in service hooks.
 

### Q: What are the security implications of granting Edit subscriptions and View subscriptions permissions? 
A: A user who has the Edit subscriptions and View subscriptions permissions can:
  
- See all subscriptions in the project. 
- See the notification history for all subscriptions in the project. 
- Create any type of service hook subscription in the project.  
If the user sets up a subscription for a resource that they don't otherwise have permission to access, the subscription doesn't get triggered.
 
For example, suppose you create a subscription to send a notification when a work item in a specific area path gets updated. If you don't have access to the work items in that area path, the notifications don't get sent. However, if other users have access to the work items, you can see the notification history for subscriptions that alert them about updates.
 

### Q: Can I create service hook subscriptions for a project programmatically? 
A: Yes. For more information, see Create a service hooks subscription programmatically.
 

### Q: Can I remove an app's access to my organization after I authorize it? 
A: Yes. You can revoke authorizations from your profile.
  
- 
Go to https://visualstudio.microsoft.com. Select your profile photo, and then select Visual Studio profile.
 
Ensure you start from the Visual Studio site, https://visualstudio.microsoft.com, when you manage authorizations. From that site, you can access the correct implementation of the authorizations management feature. Don't start from your organization (`https://dev.azure.com/{organization-name}`).
  
- 
Select Manage authorizations.
 
   
  
- 
Revoke any authorizations you no longer want to allow.
 
   
   

### Q: Why can't I set up service hooks for HipChat anymore? 
A: Atlassian no longer supports HipChat. For more information, see Atlassian Frequently Asked Questions.
 

## Related content  
- Troubleshoot service hooks 
- Visual Studio Marketplace 
- Billing overview   
 
 
    

## Feedback 
 
 Was this page helpful? 
 
        No  
 
 Need help with this topic? 
 
 Want to try using Ask Learn to clarify or guide you through this topic? 
 
 
              
  

##  Additional resources      
   
-   Last updated on  2026-05-08         
 
    
 
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
