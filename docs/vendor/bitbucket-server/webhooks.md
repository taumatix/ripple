<!-- Source: https://confluence.atlassian.com/bitbucketserver/manage-webhooks-938025878.html (Bitbucket Data Center 10.5)
     Fetched 2026-11-23 as the page's rendered text (navigation removed). Atlassian's text, vendored for reference. -->

Webhooks provide a way to configure Bitbucket Data Center to make requests to your server or another external service, whenever certain events occur. A webhook consists of:
One or more events – the default event is a repository push, but you can select multiple events to trigger the webhook.
A URL – the endpoint where you want Bitbucket to send the event payloads when a matching event happens. You can add additional event context such as project or repo key as URL variables and send data to multiple URLs using a single webhook.
There are two stages to the webhook: Creating webhooks and Triggering webhooks. Once you've created a webhook for an event, every time that event occurs, Bitbucket sends a payload request that describes the event to the specified URL.
You can create webhooks both at the project and the repository level. A webhooks created at the project level is inherited by all repositories in that project.
If you're having problems with a webhook, see Troubleshooting webhooks.
On this page:
When to use webhooks
Use webhooks to integrate applications with Bitbucket. For example:
Every time a user pushes commits in a repository, you may want to notify your CI server to start a build.
Every time a user pushes commits or creates a pull request, you may want to display a notification in your application.
Every time a user pushes commits to a repository, and a mirror synchronizes those changes, you may want to notify your continuous integration system to start a build.
Advantages of webhooks
Without webhooks, you need to poll the API if you want to detect when events occur in Bitbucket. However, polling the API is inconvenient, inefficient, and error-prone.
Webhooks mean the API doesn't have to check for the same activity every minute.
Securing your webhook
You can secure your webhook using a secret token or by using basic authentication.
Secret token: Use secret tokens to authenticate the payload and ensure that contents are not tampered between Bitbucket and your endpoint. Combined with HTTPS,
it helps ensure the message transmitted is the one that Bitbucket intended to send.
When you define a secret for a webhook, each request is signed via a Hash-based Message Authentication Code (HMAC). The default for this algorithm is HMACSha256. The header X-Hub-Signature is defined and contains the HMAC. For example, the header of the POST request would be plain text encoded as follows:
x-hub-signature: sha256=c3383246d4fd871e66e962b50cc12222222222222222222222222222222222
To authenticate the validity of the message payload, the receiver can perform the HMAC algorithm on the received body with the secret as the key to the HMAC algorithm. If the results do not match, it may indicate there was a problem with transmission that has caused the message payload to change. 
Basic authentication: If your endpoint uses basic authentication (a username and password), use this method to secure your webhook. When the webhook data is sent, the authorisation header will be included  in the HTTP request. The authentication credentials for the Authorization header are base64 encoded.
Creating webhooks
You can create a webhook through Bitbucket, or with the API.
Project admins can create project-level webhooks (which are inherited and displayed at the repository-level) and repository admins can create repository-level webhooks using the below steps:
Navigate to the project or repository based on where you want to create the webhook.
Select Project Settings > Webhook or Repository Settings > Webhook, and then select Create webhook.
Enter a Title with a short description, and the URL of the application server.
You can also add URL variable. Learn how to add URL variables
Optional: Select an authentication method and add additional details based on the authentication method you've selected.
Optional: To integrate with internal URLs that are using a self-signed certificate, clear the enable SSL/TLS certificate verification checkbox. Learn more about using self-signed certificates
Webhook events: Select the event/s to trigger the webhook. 
By default, the event for the webhook is a repository push, as demonstrated by the Repository push field.
Optional: Use the Test connection button if required.
Optional: If you don't want the webhook to be active after you create it, toggle off Active.
Select Create.
To create a webhook using the API, you need to know the format of the HTTP request that Bitbucket expects and the format of the HTTP response that Bitbucket returns to your server. 
Adding URL variables
You can add the following URL variables:
 | URL variables | Can be used with
 | project.key | All events
 | repo.slug | Repository and pull request events
 | repo.ref.branchName | Repository push events
 | pr.fromRef.branchName | Pull request events
 | pr.toRef.branchName | Pull request events
Triggering webhooks
When an event associated with a webhook occurs, Bitbucket sends a request to the webhook URL containing the event payload.
You can create webhooks for the following events:
Project events
Modified (available in project-level webhooks only)
Repository events
Push
Modified
Forked
Comment added to commit
Comment edited on commit
Comment deleted on commit
Mirror synchronized
Secret detected
 Pull request events 
Opened
Source branch updated
Modified
Reviewers updated
Approved
Unapproved
Changes requested
Merged
Declined
Deleted
Comment added
Comment edited
Comment deleted
When changes to multiple branches are pushed together, a separate webhook request is sent for each branch change.
Disabling webhooks
As a system administrator, you might want to disable webhooks for security reasons.
When you disable webhooks, the webhook configurations won’t be deleted or lost in any other way. You can re-enable webhooks at any time and have all the webhook configurations up and running.
By disabling webhooks, you prevent full-read server-side request forgeries (SSRF) that can be performed through webhooks by any user with permission to create repositories or with access to project settings. You can also prevent blind SSRFs through the repository importation functionality by users with minimum permissions.
A server-side request forgery is an attack on a server when the server is tricked into connecting to a malicious resource by reading or submitting data to a URL that an attacker provided. As a result, the attacker can access sensitive information like machine credentials, connect to internal services like HTTP databases, send POST requests to internal services, cause internal DoS attacks, or do remote code execution.
To prevent these problems and disable webhooks:
Go to Administration and select Manage apps. Open UPM (Universal Plugin Manager).
Find and disable the following plugins:
Atlassian webhooks plugin provides webhooks capability.
Bitbucket Server webhooks plugin provides webhooks support for Bitbucket.
Troubleshooting webhooks
When you perform an action to trigger a webhook and it doesn't work, you can use the Event log page to figure out what went wrong.
To view the event log and troubleshoot, navigate to the Webhooks page and select View details (from the Actions column) against the webhook that doesn't work.
Click through the latest request results (such as Webhook event details) to troubleshoot the issue.  
Circuit breaking
To help protect your instance of Bitbucket, circuit breaking has been implemented in the Bitbucket webhooks system. This means badly behaving webhooks are skipped for a period of time if they are consistently failing.
By default, when a webhook fails five times, it is considered unhealthy and is skipped.
Initially, it will only be skipped for a short period (10 seconds) but as it continues to fail it will gradually skip for longer periods, up to a max of 10 hours. 
A webhook may also be skipped if there are too many webhooks in flight. If there are 250 webhooks being invoked, further requests will be skipped until the number in flight drops below 250.
These limits are entirely configurable if your instance has different requirements. For more information, see Configuration properties.
If a webhook is being skipped, you can see so via the JMX metrics output by Bitbucket, or via the logs.
 Last modified on Jan 30, 2024
Was this helpful?
 Yes
 No
 It wasn't accurate
 It wasn't clear
 It wasn't relevant
 Provide feedback about this article
In this section
Event payload
If you use self-signed certificates
