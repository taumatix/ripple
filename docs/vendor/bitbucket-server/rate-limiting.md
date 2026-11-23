<!-- Source: https://confluence.atlassian.com/bitbucketserver/improving-instance-stability-with-rate-limiting-976171954.html
     Fetched 2026-11-23 as the page's rendered text (navigation removed). Atlassian's text, vendored for reference. -->

When automated integrations or scripts send requests to Bitbucket in huge bursts, it can affect its stability, leading to drops in performance or even downtime. Rate limiting allows your instance to self-protect. It keeps it stable by giving you control over how many requests automations can make, and how often they can make them.
On this page: 
Related pages:
Enabling JMX counters for performance monitoring
How rate limiting works
Rate limiting targets HTTP requests with basic or bearer authentication (such as REST API requests). It operates using the token bucket algorithm. When it’s turned on:
Each user is given a ‘token bucket’ containing the maximum number of tokens they can hold (this is their token bucket size).
Every time they make an HTTP request one token is taken out of their bucket.
New tokens are added into their bucket at a constant rate until it is full (this is their token bucket refill rate)
If their bucket is empty, they cannot make requests.
When rate limiting is on and set up correctly for your instance, your users should experience improved stability and performance, and those using it reasonably won’t experience any change in their ability to send it requests. The only time users will be rate limited is when they act in a way that will negatively impact Bitbucket, such as when they make requests in large bursts or when they make requests too frequently.
Note that rate limiting is applicable to HTTP requests with basic or bearer authentication (such as REST API requests and Git over HTTP). Rate limit for Git SSH operations is a separate feature. You can track it from 
 BSERV-12496
 -
 Getting issue details...
 STATUS
Example
An admin has turned on rate limiting and has set a token bucket size of 60 and a refill rate of 5.
One of their developers sends Bitbucket 60 requests in a single burst. As this developer starts with a full token bucket, all 60 requests will be successful. Their token bucket, however, will now be empty and it will begin refilling at a speed of 5 tokens per second. As it refills, they can send more requests and they won’t be rate limited as long as they have enough tokens in their bucket.
Another developer sends Bitbucket 100 requests in a single burst. As their bucket can only hold 60 tokens, only their first 60 requests will be successful and then they’ll be rate limited. Their token bucket will now be empty and it will begin refilling at 5 tokens per second. If they try to send the remaining 40 requests and they don’t have enough tokens in their bucket they’ll be rate limited again. To send all their requests successfully they'll have to rewrite their script.
How to turn on rate limiting
You need to be a System Admin or Admin to turn on rate limiting. The first time you do this you’ll see default values for:
Token bucket size - This is the number of tokens that are available to a user. It determines the maximum number of requests they can send in a burst. By default, this is set to 60
Token bucket refill rate - This is the number of tokens added to a user’s bucket per second. It determines their throughput. By default, this is set to 5
These values should be suitable for most instances and we recommend starting with them before adjusting up or down.
To turn on rate limiting:
Go to  > Rate limiting.
Change the status to On.
If necessary, change the token bucket size and token bucket fill rate.
Click Save.
Rate limiting can be disabled instance-wide
If required, System Admins can disable rate limiting instance-wide through the Bitbucket properties file. To find out how to do this, look for feature.rate.limiting in the config properties documentation.
How rate limiting works in a cluster
If your instance consists of a cluster of nodes behind a load balancer, each of your users will have a separate token bucket on each node. In other words, if you have three nodes, your users will have three token buckets.
In addition, the global settings for token bucket size and refill rate apply separately to each of these buckets. In other words, If you’ve set them to 60 and 5, they’ll be 60 and 5 on each bucket, unless the user has an exemption.
This means that each user’s ability to send requests won’t change, and Bitbucket will remain protected and stable regardless of which node their requests are routed to.
Identifying users who have been rate limited
When a user is rate limited, they’ll know immediately as they’ll receive an HTTP 429 error message (too many requests).
There are also two ways you can identify which users have been rate limited:
You can look at the Users rate limited in the past 24 hours table on the rate limiting settings page.
For more detailed information you can look in the Bitbucket access log. Every time a user is rate limited an event with the label rate-limited will be recorded.
Lastly, you can use JMX metrics for an aggregated view of how many users have been rate limited. To allow you to do this, metrics for the number of rate limited requests and current user map size have been exposed.
