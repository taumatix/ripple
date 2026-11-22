# Roadmap

Ordered by how much each entry limits real deployments. The top is next.

1. **OS-level sandbox for `go` and `git`.** A hostile repo's build runs as the server's uid with the whole filesystem readable. Run each job under bwrap/nsjail or in a per-job container with a separate uid, a read-only root, only the workspace writable and no network except the proxy.
2. **Per-installation workers and a durable event queue.** Webhook events are handled one at a time behind a queue of 1024, then the receiver answers 503; GitHub does not redeliver, so those events are lost (one noisy installation starves the rest). Also, the poll loop's `RunDue` waits on slow major verifies and stalls the global feed poll: run jobs off the poll path.
3. **Bound the scan and the fan-out.** A repo with a million files or dependencies costs unbounded memory and time. Cap files and dependencies per repo, clone with `--filter=blob:limit=1m`, and memoise the per-repo config decision in `FanOut` (`internal/engine/fanout.go`) instead of re-parsing it per dependency.
4. **Bound the webhook body read.** Up to 25MB is buffered per request before the HMAC is checked, so concurrent unauthenticated posts exhaust memory. Gate the read with a semaphore and check the signature on a stream or a smaller limit.
5. **Fetch the installation token per network git command.** The token is minted once per job and can expire mid-job on a slow clone or push. Use a credential helper that asks `auth.go` each time (`git.go` clone, fetch and push).
6. **Dedupe `X-GitHub-Delivery` with a TTL.** A captured signed event can be replayed indefinitely; remember delivery ids for a window and drop repeats.
7. **`go.sum` and `go mod tidy` refresh for every update.** Major bumps already tidy, build and vet
   in the branch; minor and patch PRs still edit `go.mod` only, so many fail CI until someone runs
   `go mod tidy`. Reuse the same verification and draft-on-failure path.
8. **Major bumps in repos with several modules, `go.work`, or `vendor/`.** Today only the module
   whose `go.mod` lists the dependency is rewritten and verified; a workspace, other modules that
   require it, and a vendor directory (`go mod vendor`) are not refreshed.
9. **Go toolchain and `go` directive bumps.** Propose `toolchain` and `go` line updates as their own
   update type, from the Go release feed.
10. **Rewrite `go:generate` lines and `// import` comments on a major bump.** Only import specs are
   rewritten now; `go run example.com/m/v2/cmd/x` in a directive and canonical import comments are not.
11. **Verify with cgo and private modules.** The sandbox builds with `CGO_ENABLED=0` (cgo files are
   rewritten but not compiled) and only from `RIPPLE_GOPROXY`; support a toolchain image with C
   headers and credentials for private proxies, without exposing the App token to the build.
12. **Supersede and close stale PRs.** A newer release leaves the older ripple PR open next to it.
13. **Backfill for newly registered dependencies.** A repo that starts importing a package only hears
   about releases published afterwards; compare against the latest stored release on registration.
   This also closes the DynamoDB GSI eventual-consistency window.
14. **Default-branch rename handling.** Handle the `repository` rename and default-branch change
   events; today a stale `DefaultBranch` is corrected only on the next push.
15. **Dependency dashboard issue.** One issue per repo listing pending, deferred and failed jobs.
16. **Dynamo hot partition.** Shard `JOBS#pending` and very popular `PKG#` partitions; revisit the
   eventual-consistency notes in `docs/store-dynamodb.md` under load.
17. **More datasources and feeds.** npm replicate, PyPI, crates.io sparse index, Docker/OCI tags.
18. **More managers, toward Renovate parity.** Dockerfile, GitHub Actions, npm, Cargo, pip, Helm.
19. **OSV advisories as an event source.** A security advisory becomes a job that bypasses the cooldown.
20. **GitLab, Bitbucket and Gitea** behind the existing `Platform` interface.
21. **GHES GraphQL** differences and per-host API base handling.
22. **Plugin mechanism** for out-of-tree managers and datasources.
23. **Presets, org-level defaults and packageRules-like matching.** Later and strictly opt-in: a repo
    file must opt into a preset by name, and none of these may ever change the meaning of a plain
    per-repo `.github/ripple.yml`. No implicit inheritance, no rule precedence.
24. **MIGRATION notes policy.** Rules for config-schema and API changes: additive first, deprecate
    across a minor release, `MIGRATION.md` for any break.
