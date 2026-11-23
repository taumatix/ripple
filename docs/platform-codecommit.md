# AWS CodeCommit

Host `codecommit`. Repositories are stored as `codecommit:<region>/<name>` (the region is part of
the identity, since a name is only unique per region and account). One AWS account per ripple.

## Availability

AWS announced in July 2024 that CodeCommit was closed to new customers and de-emphasised, then
reopened it: the 24 Nov 2025 announcement says new sign-ups are open again and the service is
generally available in 29 regions
(`docs/vendor/codecommit/aws-blog-the-future-of-codecommit.txt`). Support is therefore worth
having for existing and new accounts alike, but see "Unverified": it has never been run against
real AWS.

## Configuration

| Variable | Meaning |
|---|---|
| `RIPPLE_CODECOMMIT_REGIONS` | Comma-separated regions to discover repositories in. Unset turns the host off. |
| `RIPPLE_CODECOMMIT_SNS_TOPIC_ARNS` | Comma-separated SNS topic ARNs the webhook accepts. Required when regions are set; the receiver refuses everything else. |
| `RIPPLE_CODECOMMIT_ACCESS_KEY_ID`, `..._SECRET_ACCESS_KEY`, `..._SESSION_TOKEN` | Static or temporary credentials. If both key variables are absent the AWS default chain is used (environment, shared config, instance or task role). |
| `RIPPLE_CODECOMMIT_ENDPOINT`, `RIPPLE_CODECOMMIT_GIT_ENDPOINT` | Override `https://codecommit.<region>.amazonaws.com` and `https://git-codecommit.<region>.amazonaws.com` (VPC endpoints, test doubles). |

IAM needs, on the repositories: `codecommit:ListRepositories`, `GetRepository`, `GitPull`,
`GitPush`, `ListPullRequests`, `GetPullRequest`, `CreatePullRequest`, `GetDifferences`.

## Events: EventBridge, SNS, `/webhook/codecommit`

CodeCommit has no webhooks. Its own notification rules and triggers target SNS or Lambda
(`ug-how-to-notify.md`), and repository state changes are published to EventBridge
(`ug-monitoring-events.md`). The path chosen is the one that needs no extra infrastructure of
ripple's own: an EventBridge rule on `source: aws.codecommit`, `detail-type: CodeCommit Repository
State Change`, with an SNS topic as target and an HTTPS subscription to
`https://<ripple>/webhook/codecommit`. SQS or Lambda would need ripple to poll a queue or run
code in the account, for no gain in reliability.

The receiver:

1. refuses a request whose `x-amz-sns-topic-arn` header is not a configured topic, before reading the body;
2. checks that the signed `TopicArn` is configured too and that the type header matches the body;
3. verifies the RSA signature (SignatureVersion 1, SHA1, or 2, SHA256) against the certificate at
   `SigningCertURL`, which must be an `https://sns.<region>.amazonaws.com[.cn]` URL, as must `SubscribeURL`;
4. confirms a `SubscriptionConfirmation` by visiting `SubscribeURL`;
5. turns `referenceCreated` and `referenceUpdated` on a branch into a push of `refs/heads/<branch>`.

The event names the branch and the old and new commit but no files. For `referenceUpdated` ripple
makes one `GetDifferences` call to learn the changed paths, so a push that touches nothing relevant
costs one call and not a clone. If that call fails, or for a new branch, the file list stays empty
and the repository is scanned in full: slower, never wrong.

### Why there is no polling fallback

SNS retries a failed HTTPS delivery on a schedule (`sns-SendMessageToHttp.retry.md`), and ripple
answers 503 when it cannot handle an event so that SNS does retry. Repositories are still
discovered by listing on every poll tick, so a new repository does not depend on an event. What a
poller would add is protection against a misconfigured rule, which it could not distinguish from
a quiet repository; a wrong setup is reported by the missing subscription confirmation instead.

## How it works

- **Why the AWS SDK is a dependency**: only for `aws/signer/v4` and the credentials providers
  (already in `go.mod` for the DynamoDB store). The API is called with plain JSON 1.1 over
  `net/http` (`X-Amz-Target: CodeCommit_20150413.<Op>`), not with the service SDK.
- **Git** uses a custom password: SigV4 over `GIT\n/v1/repos/<name>\n\nhost:<host>\n\nhost\n`,
  the timestamp (no trailing `Z` in the string to sign) followed by `Z` and the signature; the
  user name is the access key, with `%<session token>` for temporary credentials
  (`grc-init.py`). It is valid for minutes, so it is signed for every git command.
- **Draft PRs** are a `Draft: ` title prefix; there are no draft pull requests. **Labels** are not
  supported and are skipped. **Auto-merge** does not exist, so the host does not implement it.
- **Limits**: the title is cut to 100 characters (the API page says 150 in one place and 100 in
  another; the lower is safe) and the description to 10240. Creation is idempotent: the
  `clientRequestToken` is derived from the repository, branches and title, so a retry after a lost
  answer does not open a second PR.
- **Throttling** is `ThrottlingException` with HTTP 400 and no `Retry-After`, so ripple holds a
  region back for a fixed minute from the clock.

## Superseded pull requests

When a newer ripple pull request replaces a stale one, ripple closes the stale one and comments
on it (`close-superseded-pull-requests`, default on), only when it knows nobody touched it. It
calls `UpdatePullRequestStatus` to `CLOSED`, then `PostCommentForPullRequest`. The comment is best
effort: if it fails the pull request stays closed and a warning is logged. The branch is not
deleted.

**Counted as touched** (the pull request is left open): a comment of any kind, deleted or not
(`GetCommentsForPullRequest`); any pull request event other than created, source branch updated and
merge state changed, so a status change, an approval, or an approval rule created, edited, deleted
or overridden, and any event type ripple does not know (`DescribePullRequestEvents`); any commit
from the source tip back to the merge base whose author or committer email is not ripple's, or
that is a merge (`GetCommit`); a branch more than 50 commits long, or with none; and a pull
request that is no longer open or whose source branch is not the one ripple recorded. A read
that fails is an error and nothing is closed.

**Not detected**: an edited title or description (no CodeCommit API reports either); a person
who commits under ripple's own author and committer email; a person who only viewed it; and a change made between ripple's last read and the close,
which `UpdatePullRequestStatus` does not guard against. Events are not filtered by actor, because
the ARN ripple acts as is not known to it.

All of this is verified only against the fake in `internal/testutil/codecommit.go`. Unconfirmed
against AWS: that `PostCommentForPullRequest` is accepted on a closed pull request, and that the
event types above are the only ones the service emits for system activity.

## Limitations

One AWS account per ripple; a second account needs a second instance.

## Unverified

Nothing has run against real AWS; the fake in `internal/testutil/codecommit.go` is written from
the vendored docs, and its SigV4 and git-password checks are independent implementations of the
same documents, not AWS's. Unconfirmed: that AWS accepts these signatures (the git password
vectors in the tests were computed from the botocore algorithm, not by AWS); the China and EU
sovereign cloud endpoints; the SNS certificate chain (the receiver verifies the signature with the
certificate it fetched, not the chain); the title limit; the exact
EventBridge input shape as delivered through SNS; and clock-skew rejection, which the fake does
not model.
