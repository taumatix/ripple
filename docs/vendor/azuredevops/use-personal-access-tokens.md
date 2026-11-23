<!-- Vendored from https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate?view=azure-devops on 2026-11-23 (HTML converted to text by a script; tables and links are flattened). (c) Microsoft; kept here as a reference for tests that cite sections. -->

Read in English        Edit           
  
 
 
  Note 
 
 Access to this page requires authorization. You can try signing in or changing directories. 
 
 Access to this page requires authorization. You can try changing directories. 
    

# Use personal access tokens 
 
    
 
       
   

Azure DevOps Services | Azure DevOps Server | Azure DevOps Server 2022
 
A personal access token (PAT) is an alternative password for Azure DevOps authentication. A PAT identifies you and determines the resources and operations available to you. Treat a PAT with the same care as a password.
 
 
Caution
 
Avoid PATs when a more secure authentication method is available. For applications and automation, use Microsoft Entra tokens, managed identities, or service principals whenever possible.
  
If a tool doesn't support Microsoft Entra authentication, use an organization-scoped PAT with the minimum scopes and shortest practical lifetime.
 
 
 
Important
 
Azure DevOps is retiring global PATs. All existing global PATs stop working on January 27, 2027. Migrate them now to organization-scoped PATs or Microsoft Entra authentication. For more information, see Retirement of Global Personal Access Tokens in Azure DevOps.
   

## Prerequisites  
- Access to an Azure DevOps organization or Azure DevOps Server collection. 
- The permissions required for the resources and operations that the PAT accesses. A PAT can't grant more permissions than its owner has.  
  
- Permission to create PATs under your organization's policies. An administrator can restrict PAT creation, scopes, and lifetimes. For more information, see Manage PATs with policies.   

## Create a PAT 
  
- 
Sign in to your organization (`https://dev.azure.com/{organization}`).
  
- 
On the home page, open user settings  and select Personal access tokens.
 
   
  
- 
Select + New Token.
 
   
  
- 
Enter a name, select one organization, and set an expiration date.
 
   
  
- 
Select only the scopes required for the intended tasks.
 
For example, to register a self-hosted agent, select Agent Pools (Read & manage). Your administrator might restrict full-scoped PATs or require an allow list entry for specific scopes.
 
   
  
- 
Select Create, copy the token, and store it in a secure location. The token value doesn't appear again.
 
   
    
 
 
Note
 
The following screenshots show Azure DevOps Services. The Azure DevOps Server interface might differ slightly.
   
- 
Sign in to your Azure DevOps Server web portal (`https://{server}/{collection}` or `http://{server}:{port}/tfs/{collection}`).
  
- 
On the home page, open user settings  and select Personal access tokens.
 
   
  
- 
Select + New Token.
 
   
  
- 
Enter a name, select the collection, and set an expiration date.
 
   
  
- 
Select only the scopes required for the intended tasks.
 
For example, to register a self-hosted agent, select Agent Pools (Read & manage).
 
   
  
- 
Select Create, copy the token, and store it in a secure location. The token value doesn't appear again.
 
   
    
For organizations backed by Microsoft Entra ID, sign in with a new PAT within 90 days or it becomes inactive. For more information, see User sign-in frequency for Conditional Access.
 

### Notifications 
Azure DevOps sends a notification when you create a PAT and another notification before it expires.
 
   
 
 
If an organization policy no longer allows you to regenerate a PAT, the expiration email identifies the restriction. Contact your project collection administrator if you need an allow list entry.
  
 
For information about email configuration, see Configure an SMTP server and customize email for alerts and feedback requests.
  

#### Unexpected notification 
An unexpected notification might mean that an administrator or tool created a PAT for you. Common token names include:
  
- `git: https://dev.azure.com/{organization} on {machine}`, created when Git connects to an Azure Repos repository. 
- `Service Hooks: Azure App Service: Deploy web app`, created when an Azure App Service deployment is configured. 
- `Microsoft Teams Integration`, created when a Microsoft Teams integration is configured.  
If you don't recognize a PAT, revoke it and change your password. For a Microsoft Entra account, ask your administrator to review unfamiliar sign-in activity.
 

## Use a PAT 
Use a PAT for one-time requests, local prototypes, or tools that don't support Microsoft Entra authentication. Don't use PATs as long-term credentials for production applications or services.
 
Azure DevOps accepts a PAT through an HTTP Basic authentication header. The username can be empty. Base64-encode the string `:{PAT}`, and use the resulting value in this header:
 

```
Authorization: Basic BASE64_ENCODED_PAT

```
 
The following examples expect the `AZURE_DEVOPS_PAT` environment variable to be populated from a secure secret source.
 
  
-  Windows  
-  Linux/macOS    

```
$patBytes = [Text.Encoding]::ASCII.GetBytes(":$env:AZURE_DEVOPS_PAT")
$headers = @{ Authorization = "Basic $([Convert]::ToBase64String($patBytes))" }

Invoke-RestMethod `
  -Uri "https://dev.azure.com/{organization}/{project}/_apis/build/builds?api-version=7.1" `
  -Headers $headers

```
   

```
curl --user ":${AZURE_DEVOPS_PAT}" \
  "https://dev.azure.com/{organization}/{project}/_apis/build/builds?api-version=7.1"

```
   
After a prototype works, migrate it to Microsoft Entra OAuth for delegated user access or a service principal or managed identity for application access.
 

## Modify a PAT 
You can change a PAT's name, expiration, or scopes. Regenerating a PAT creates a new token value and invalidates the previous value. You can't modify or regenerate an expired or revoked PAT; create a new one instead.
  
- 
On the home page, open user settings  and select Personal access tokens.
  
- 
Select the PAT, and then select Edit.
 
   
  
- 
Change the token name, expiration, or scopes, and then select Save.
 
   
   

## Revoke a PAT 
Revoke a PAT when it's compromised, no longer needed, or has more access than required.
  
- 
On the home page, open user settings  and select Personal access tokens.
  
- 
Select the PAT, and then select Revoke.
 
   
  
- 
In the confirmation dialog, select Revoke.
 
   
   

## Replace an expiring or revoked PAT 
After a PAT expires or is revoked, Azure DevOps rejects subsequent authentication attempts that use it. A pipeline, script, package client, or Git operation typically fails the next time it authenticates. Azure DevOps doesn't guarantee that revocation terminates every connection that is already established.
 

### Rotate a PAT before it expires 
To replace a PAT without an avoidable service interruption:
  
- 
Create an organization-scoped PAT with only the required scopes and a short lifetime.
  
- 
Store the new value in your secret store. Don't embed PATs in Git remote URLs, `.git/config`, source code, pipeline YAML, or logs.
  
- 
Test the new PAT with a nonproduction operation or one integration before you update every dependency.
  
- 
Update each service or tool that stores the old PAT:
   
  | Integration  | Where to update the credential 
   
  | Git  | Git Credential Manager or another secure credential manager 
 
  | Azure Pipelines  | A secret variable, variable group, or service connection 
 
  | Azure Artifacts  | The Azure Artifacts Credential Provider or the package manager's secure configuration 
 
  | Scripts and REST clients  | A secret store or protected environment variable 
 
  | Third-party tools  | The tool's secure credential store 
    
- 
Test every updated integration. Run the affected pipeline, Git operation, package operation, script, or API request, and check for authentication failures.
  
- 
Revoke the old PAT.
   
Keep an inventory of each PAT's owner, purpose, scopes, expiration, and dependent integrations. Replace PATs before expiration based on your organization's policy and the risk of the scenario.
 

### Recover after expiration or revocation 
If a PAT stops working unexpectedly:
  
- On the Personal access tokens page, check the PAT's status and expiration. 
- Create a replacement PAT with only the scopes required by the affected integrations. 
- Update the integrations that stored the unavailable PAT. 
- Test each integration and monitor its next scheduled operation for authentication failures.  
 
If the reason for the failure is unclear, review the PAT audit events to determine whether the PAT expired, was updated, or was revoked.
  
 

## Manage PATs with the Lifecycle Management APIs 
Use the PAT Lifecycle Management APIs to list, create, update, and revoke your own PATs programmatically.
 
These APIs require a Microsoft Entra access token from a user-delegated flow. Use the `vso.pats` scope. Service principals and managed identities can't create or manage PATs because they don't represent a user.
   
  | Operation  | Request 
   
  | List PATs  | `GET https://vssps.dev.azure.com/{organization}/_apis/tokens/pats?api-version=7.1` 
 
  | Create a PAT  | `POST https://vssps.dev.azure.com/{organization}/_apis/tokens/pats?api-version=7.1` 
 
  | Revoke a PAT  | `DELETE https://vssps.dev.azure.com/{organization}/_apis/tokens/pats?authorizationId={authorizationId}&api-version=7.1` 
   
Send the Microsoft Entra token as a bearer token. A create request uses this body shape:
 

```
{
  "displayName": "CI build access",
  "scope": "vso.build",
  "validTo": "{expiration-in-UTC}",
  "allOrgs": false
}

```
 
The list response returns tokens in the `patTokens` array. A create response returns the secret once in `patToken.token`. Store that value securely and never write it to logs. To revoke the old token, pass its `authorizationId` as the query parameter shown in the table.
 
For complete schemas and examples, see List PATs, Create a PAT, and Revoke a PAT.
 

## Review PAT audit events 
Azure DevOps Services records PAT lifecycle events in the organization audit log. In Organization settings, select Auditing, and then filter for these actions:
   
  | Action  | Event 
   
  | A PAT is created  | `Token.PatCreateEvent` 
 
  | A PAT expires  | `Token.PatExpiredEvent` 
 
  | A PAT is found in a public repository  | `Token.PatPublicDiscoveryEvent` 
 
  | A user revokes a PAT  | `Token.PatRevokeEvent` 
 
  | Azure DevOps revokes a PAT  | `Token.PatSystemRevokeEvent` 
 
  | A PAT is modified or regenerated  | `Token.PatUpdateEvent` 
   
Token access events aren't currently logged. Use the audit log to investigate lifecycle changes, not to determine when or whether a PAT was used.
 

### Audit log retention 
Audit events are retained for 90 days and then deleted. The retention period isn't configurable. To retain events longer, export them to CSV or JSON or configure an audit stream to an external security information and event management (SIEM) system.
 
For more information, see Access, export, and filter audit logs and Azure DevOps auditing events.
 

## Configure PAT policies 
Tenant administrators can restrict global PATs and full-scoped PATs, set a maximum PAT lifetime, and control automatic revocation of leaked PATs. Configure tenant-level PAT policies in Organization settings > Microsoft Entra.
 
Organization owners can restrict PAT creation for an individual organization in Organization settings > Policies. For roles, policy behavior, and allow-list procedures, see Manage PATs with policies.
  

## PAT format 
PAT strings use an identifiable format that improves secret detection and reduces false positives.
  
- PATs are 84 characters long, including 52 randomized characters. 
- Azure DevOps PATs contain the fixed `AZDO` signature at positions 76 through 80.  
If your integration validates PATs, support the 84-character format. For leaked-token controls, see Revoke leaked PATs automatically.
 

## Best practices for using PATs 

### Consider alternatives  
- Use Microsoft Entra tokens for applications and automation. 
- For one-time commands, acquire a Microsoft Entra token through the Azure CLI. 
- For Git, use Git Credential Manager. For Azure Artifacts, use the Azure Artifacts Credential Provider.  

### Create PATs  
- Create only organization-scoped PATs. Global PATs stop working on December 1, 2026. 
- Use a different PAT for each tool or workflow. 
- Select only the scopes required for the task. 
- Keep the lifetime as short as practical. 
- Don't include personal data or any part of the token value in its display name.  

### Manage PATs  
- Don't share PATs. 
- Store PATs in a secure secret store, such as Azure Key Vault. 
- Replace PATs before they expire, and revoke them when they're no longer needed. 
- Revoke a PAT immediately if it's exposed or compromised.  
 

### For administrators  
- Restrict full-scoped PATs and set a maximum lifetime based on your security requirements. 
- Restrict PAT creation in organizations that don't require it. 
- Use the supported Token Administration APIs to revoke user PATs. 
- Prefer Microsoft Entra authentication for services and automation.  

#### Review and respond to risky PATs 
As part of your security review:
  
- Identify PATs with full scopes, long lifetimes, global access, or no documented owner and purpose. 
- Review PAT creation, update, public-discovery, expiration, and revocation events in the audit log. Because token access isn't logged, don't use the audit log to classify a PAT as active or stale. 
- Revoke exposed, compromised, or unnecessary PATs. For offboarding, use the supported Token Administration APIs to revoke the user's PATs and notify owners of affected integrations. 
- Configure tenant and organization PAT policies to restrict global and full-scoped PATs, set maximum lifetimes, and control automatic revocation of leaked PATs. 
- Export audit events or configure an audit stream when your retention requirements exceed 90 days.   

## FAQs 
 

### Why can't I edit or regenerate a PAT scoped to one organization? 
Sign in to the organization where the PAT is scoped. You can list your PATs from any organization in the same Microsoft Entra tenant by changing the Access scope filter, but you can edit an organization-scoped PAT only from that organization.
  

### What happens to PATs when a user account is disabled? 
When an account is disabled or removed from an organization, its PATs can no longer access that organization's resources. Azure DevOps doesn't automatically delete the PATs. If access is restored, a PAT that hasn't expired or been revoked works with the user's current permissions.
 

### Can I use PATs with all Azure DevOps REST APIs? 
No. PATs work with most Azure DevOps REST APIs. Some APIs, including the Organizations, Profiles, and PAT Lifecycle Management APIs, require Microsoft Entra tokens.
 
 

### What happens if I commit a PAT to a public GitHub repository? 
Azure DevOps scans public GitHub repositories for leaked PATs. When it detects a token, Azure DevOps notifies the owner and records an audit event. Unless the tenant policy disables automatic revocation, Azure DevOps revokes the leaked PAT. For more information, see Revoke leaked PATs automatically.
  

### Can I use a PAT as an API key to publish NuGet packages? 
No. Azure Artifacts doesn't support passing a PAT as an API key. In a local development environment, install the Azure Artifacts Credential Provider. In Azure Pipelines, use the NuGet Authenticate task.
 
For examples, see Publish with dotnet, Publish with NuGet.exe, and Publish NuGet packages with Azure Pipelines.
 

### Why did my PAT stop working? 
Check whether the PAT expired, was revoked, became inactive, has insufficient scopes, or belongs to a user whose permissions changed. For an organization backed by Microsoft Entra ID, sign in to Azure DevOps and complete the full authentication prompt, and then try again. Your Conditional Access configuration might require more frequent sign-in.
 
For Azure DevOps Server, IIS Basic Authentication prevents PAT authentication. Keep IIS Basic Authentication disabled.
 

### How do I create an access token that isn't tied to a user? 
PATs are always associated with the user who created them. For a non-user identity, use a Microsoft Entra token issued to an application service principal or managed identity. For pipelines, use a service connection.
 
 

### How do I rotate a PAT through the API? 
The Lifecycle Management APIs don't regenerate a PAT in one operation. List the old PAT to obtain its metadata, create a new PAT with the required scope and expiration, update and test every dependent integration, and then revoke the old PAT by using its `authorizationId`. For the correct routes, see Manage PATs with the Lifecycle Management APIs.
 

### Why does an admin approval message appear when I call the PAT Lifecycle Management APIs? 
Your tenant's security policies require administrator consent before the Microsoft Entra application can access organization resources. Contact your tenant administrator.
  

## Related articles  
- Authentication guidance 
- Authenticate with Microsoft Entra tokens 
- Use service principals and managed identities 
- Manage PATs with policies 
- Revoke user PATs   
 
 
    

## Feedback 
 
 Was this page helpful? 
 
        No  
 
 Need help with this topic? 
 
 Want to try using Ask Learn to clarify or guide you through this topic? 
 
 
              
  

##  Additional resources      
   
-   Last updated on  2026-09-21         
 
    
 
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
