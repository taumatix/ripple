# GitHub GraphQL and REST pages read before head polling was written

Fetched 2026-11-26. The first block is GitHub's own public GraphQL schema, `https://docs.github.com/public/fpt/schema.docs.graphql`
(the file behind docs.github.com/en/graphql/reference; the per-type pages `.../reference/queries` and `.../reference/objects`
now redirect or return an index, so the SDL is the page). Quoted verbatim, blank lines dropped. The second block is what
the docs.github.com fetch tool returned for the prose pages, as returned (it summarises).

## Schema (SDL)

```graphql
type Query {
  """Lookup a given repository by the owner and repository name."""
  repository(
    """Follow repository renames. If disabled, a repository referenced by its old name will return an error."""
    followRenames: Boolean = true
    """The name of the repository"""
    name: String!
    """The login field of a user or organization"""
    owner: String!
  ): Repository

  """The client's rate limit information."""
  rateLimit(
    """If true, calculate the cost for the query without evaluating it"""
    dryRun: Boolean = false
  ): RateLimit
}

type Repository {
  """The Ref associated with the repository's default branch."""
  defaultBranchRef: Ref
}

type Ref implements Node {
  """The ref name."""
  name: String!
  """The object the ref points to. Returns null when object does not exist."""
  target: GitObject
}

interface GitObject {
  """The Git object ID"""
  oid: GitObjectID!
}

type RateLimit {
  """The point cost for the current query counting against the rate limit."""
  cost: Int!
  """The maximum number of points the client is permitted to consume in a 60 minute window."""
  limit: Int!
  """The maximum number of nodes this query may return"""
  nodeCount: Int!
  """The number of points remaining in the current rate limit window."""
  remaining: Int!
  """The time at which the current rate limit window resets in UTC epoch seconds."""
  resetAt: DateTime!
  """The number of points used in the current rate limit window."""
  used: Int!
}
```

`Query.nodes(ids: [ID!]!): [Node]!` exists but the schema states **no maximum** number of ids, so ripple does not use it.
`Commit implements GitObject & Node & ...`; `defaultBranchRef` is null on an empty repository (assumed from "Ref"
being nullable; not shown by a real empty repository this session: assumed, unverified).

## `/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api` (fetch tool summary)

- "5,000 points per hour per installation" (non-Enterprise, scaling up to 12,500 points/hour); Enterprise Cloud
  "10,000 points per hour per installation".
- Cost: "Add up the number of requests needed to fulfill each unique connection in the call", then "Divide the number by
  **100** and round the result to the nearest whole number." A query with no connection (aliased `repository(...)`
  fields) therefore costs the minimum, 1 point (the minimum of 1 is assumed, unverified).
- Node limit: "500,000 total nodes" per call; connections need `first`/`last` "within 1-100".
- Primary-limit error: "the response status will still be `200`" with an error message and `x-ratelimit-remaining`
  `0`. The `errors[].type` of it (RATE_LIMITED) is **assumed, unverified**; ripple also reads `rateLimit.remaining`.
- Secondary limits: at most 100 concurrent requests, 2,000 points per minute for GraphQL, CPU time limits.
- "GitHub terminates requests exceeding 10 seconds, returning status codes `502` or `504`".

## `/en/rest/using-the-rest-api/rate-limits-for-the-rest-api` (fetch tool summary)

- Installation token: minimum 5,000 requests per hour; "Installations that have more than 20 repositories receive
  another 50 requests per hour for each repository", ceiling 12,500.
- Exceeded: "a `403` or `429` response, and the `x-ratelimit-remaining` header will be `0`"; `x-ratelimit-reset`; for a
  secondary limit a `retry-after` header may appear.

## `/en/rest/apps/apps` and `/en/rest/apps/installations` (fetch tool summary)

- `GET /app/installations`: "You must use a JWT to access this endpoint." `per_page` max 100 (default 30); response
  items carry `id`, `account.login`, `suspended_at` (null when active).
- `GET /installation/repositories`: `per_page` max 100 (default 30), `page`; response `total_count` and
  `repositories[]` each with `id`, `node_id`, `full_name`, `default_branch`, `archived`, `disabled`.
