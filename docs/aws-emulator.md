# AWS Emulator and ripple

[AWS Emulator](https://github.com/aws/aws-emulator) ([user guide](https://docs.aws.amazon.com/emulator/latest/userguide/what-is-service.html))
is a local, in-memory emulator of AWS APIs: no account, one HTTPS endpoint (`https://127.0.0.1:9297`, a
self-signed certificate it writes to `~/.aws/emulator/<port>/ca.pem`), SigV4 required with keys it generates
at start (`emulator_credentials`). ripple uses it as one more check on its AWS-dependent code, next to
DynamoDB Local. This page records what ripple needs from AWS, what the emulator says it supports, and what
it cannot tell us. Everything quoted from the emulator's repository and documentation is data we read, not
instructions we follow.

## Facts about the emulator that shape the CI jobs

- **Linux package only.** It is installed from a `.deb`/`.rpm`, or a Docker image built from the documented
  Dockerfile. No prebuilt image is published. It does not run on a developer's Mac without Docker, so every
  emulator run is **CI-only** (`.github/workflows/emulator.yml`). Building from source needs about 100 GB and
  is never done.
- **Pinned, not "latest".** Packages are listed in
  `https://downloads.aws.com/aws-emulator/packages/aws-emulator/LTS/versions.json`; the job installs
  `aws-emulator_<version>-1_amd64.deb` for the version in [UPSTREAM.md](../UPSTREAM.md). Releases change
  fidelity many times a day, so an unpinned run would change results without a ripple commit.
- **Telemetry is on by default.** The jobs start it with `--telemetry off` and `EMULATOR_TELEMETRY=off`.
- **Verified in `us-west-2` only, one region and one account.** Throttling, capacity, latency and index lag
  are not AWS's (see "What it cannot tell us").
- **Not implemented means HTTP 501 `NotImplemented`**, so a gap fails a test loudly instead of passing
  silently.
- Fidelity scores are published on each release as `scores.json.gz` (a pass/fail per fidelity test and
  service); known gaps are filed under the repository's `issues/things-that-do-not-work/`.

## What ripple uses from AWS

| Use | Where | AWS service |
|---|---|---|
| Store (`RIPPLE_STORE=dynamodb`) | `internal/store/dynamo*.go` | DynamoDB |
| AWS CodeCommit host | `internal/platform/codecommit` | CodeCommit API, git over HTTPS, SNS delivery |
| Credentials | the SDK default chain | none called by ripple itself (no STS call) |

Nothing else: no S3, SQS, Lambda, KMS or EventBridge API call. EventBridge and SNS are inputs to the
CodeCommit receiver (events arrive as SNS HTTP posts); ripple verifies the SNS signature and fetches the
`SigningCertURL`/`SubscribeURL` of a delivery, and never calls the SNS or EventBridge APIs.

### DynamoDB operations

One table (on-demand, `PAY_PER_REQUEST`, `PK`/`SK` string keys) with one global secondary index `GSI1`
(`GSI1PK`/`GSI1SK`). No LSI, no TTL, no streams, no `UpdateTable`.

- Table: `DescribeTable`, `CreateTable` plus the `TableExists` waiter, `DeleteTable` (tests only), `ListTables`
  (tests only).
- Items: `GetItem` (consistent), `PutItem`, `UpdateItem`, `DeleteItem`, with condition expressions
  (`attribute_exists`, `attribute_not_exists`), update expressions (`SET`, `ADD` on the dependents counter),
  `ReturnValues`, projection and filter expressions.
- `Query` on the base table and on `GSI1`, with `begins_with` on the sort key, paging by `ExclusiveStartKey`
  and `Limit`. `Scan` (whole-table: `check-counts`, `recount`, `backfill-failed-jobs`, the drift check, the
  hygiene sweeps) with paging.
- `BatchGetItem` and `BatchWriteItem` (with `UnprocessedItems` handling).
- `TransactWriteItems` with `Put`, `Delete` and `ConditionCheck` items (no `Update`): event enqueue with its
  marker items, the fenced writes guarded by a lease `ConditionCheck`, and the event sequence.

### What the emulator reports for each

From the scores of release v1.0.597 (commit `428a0a66`, scored 2026-11-24). The scores count the emulator's
own fidelity tests, not ripple's behaviour, so they say where to look, not that ripple works.

| Service | Passing | Gaps that touch ripple |
|---|---|---|
| DynamoDB (`DynamoDB_20120810`) | 2127 of 2398 | The failing tests are mostly validation-error wording (`ValidationException` text differs) and rarely used operations (PartiQL, bulk operations, exports, global tables, `UpdateTable`). None names the operation set above as unimplemented, but several failures are in `Query`/`Scan`/`TransactWriteItems` validation precedence, so a ripple bug that depends on an exact error message would be hidden. |
| DynamoDB Streams | 20 of 30 | Not used. |
| CodeCommit (`CodeCommit_20150413`) | 194 of 225 | The ten operations ripple calls (`ListRepositories`, `CreatePullRequest`, `GetPullRequest`, `ListPullRequests`, `UpdatePullRequestStatus`, `GetDifferences`, `GetCommit`, `PostCommentForPullRequest`, `GetCommentsForPullRequest`, `DescribePullRequestEvents`) appear in no failing step. Failing steps are `BatchGetCommits`, `BatchGetRepositories`, `ListFileCommitHistory`, `TestRepositoryTriggers`, `UpdatePullRequestApprovalRuleContent`. |
| SNS | 318 of 334 | ripple does not call SNS. A real SNS message would have to be produced by the emulator to test the receiver end to end; the receiver rejects any `SigningCertURL` outside an AWS domain, so a loopback certificate URL is refused by design (see CodeCommit below). |
| EventBridge | 663 of 708 | Not called. |

**CodeCommit git transport.** Ripple's host (API plus git) was run against it on 2026-11-24 (emulator 1.0.598,
`codecommit` job, `internal/platform/codecommit/emulator_test.go`, build tag `emulator`):

- API: `ListRepositories`, `CreatePullRequest`, `ListPullRequests`, `GetPullRequest`, `GetDifferences`, `GetCommit`,
  `GetCommentsForPullRequest`, `DescribePullRequestEvents`, `UpdatePullRequestStatus` and `PostCommentForPullRequest`
  worked through ripple's client. `CreateBranch` and `PutFile`, used only for setup, also work.
- Git credentials: the emulator's git transport answers 401 to any user name that one of its own stores did not
  mint, even with `--auth off`. The test mints an IAM user with `codecommit:*` and an access key, and ripple's
  SigV4-derived git password for that key is accepted by `git ls-remote`; an unknown user is refused. The emulator
  checks the identity, not the signature, so this proves ripple's header shape and user name, not that its
  signature is the one AWS verifies (the signature itself is checked against botocore's output in
  `client_test.go`).
- **`git clone --depth 1` fails** ("Server does not support shallow clients"); `gitws.Checkout` now falls back to a
  whole clone. Whether the emulator serves that is only known from the workflow's run: the test asserts the
  checkout's files when it does and logs the refusal when it does not (ROADMAP 182). Push is not driven.
  Not covered either: the SNS/EventBridge receiver is not driven: the receiver only trusts AWS
  certificate URLs.
- The job installs the emulator's CA into the runner's system store because ripple's git runs with a clean
  environment and no CA override.

## Result of the first run (2026-11-24, emulator 1.0.598)

The `emulator` workflow ran `./internal/store/...`, `./internal/e2e/...` and `./internal/app/...` with
`--auth off --telemetry off`. All 95 DynamoDB store subtests passed on both the fake and the emulator, including the
sharding, counter-stripe, recount, backfill and hygiene-scan tests, and `scripts/check-dynamo-parity.sh --backend
emulator` found the two runs identical. No divergence between `dynamofake` and the emulator turned up, so neither
the fake nor ripple changed. The emulator's own list of failing validation-order cases did not touch any call ripple makes.

This is correctness evidence only. The emulator is in-memory and has no throttling model, so ROADMAP 113, 114 and 176
keep their measurement halves for a real table.

## What it cannot tell us

- **Throttling and capacity.** The emulator documents no throttling model. A clean run says nothing about the
  retry count a burst needs on a real on-demand table (ROADMAP 113) or the write ceiling of one hot partition
  (ROADMAP 114).
- **Latency and scan cost.** It runs in memory on a CI runner. Page timing and the cost of a Scan of a large
  table (ROADMAP 176) are not AWS's. It can show a Scan terminates, pages correctly and returns the same
  counts as the fake, at the size we can create there.
- **Index lag.** Whether a `GSI1` read is eventually consistent, and for how long, is unknown; treat a
  consistent GSI read as an emulator property, not DynamoDB's.
- **IAM.** Policies are not evaluated: a missing permission in a real deployment is invisible here
  (the operations above are the actions ripple needs).
- **Certificates, regions, partitions.** One region and account; the `cn` and `eusc` endpoints, and the SNS
  certificate chain, are not exercised.
- **The service itself.** An emulator that agrees with `dynamofake` shows the two agree on the operations we
  call. It is not AWS; the roadmap entries that say "real AWS" stay open for the parts above.
