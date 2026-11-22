# Roadmap

Ordered by how much each entry limits real deployments. The top is next.

1. **`go.sum` and `go mod tidy` refresh for every update.** Major bumps already tidy, build and vet
   in the branch; minor and patch PRs still edit `go.mod` only, so many fail CI until someone runs
   `go mod tidy`. Reuse the same verification and draft-on-failure path.
2. **Major bumps in repos with several modules, `go.work`, or `vendor/`.** Today only the module
   whose `go.mod` lists the dependency is rewritten and verified; a workspace, other modules that
   require it, and a vendor directory (`go mod vendor`) are not refreshed.
3. **Go toolchain and `go` directive bumps.** Propose `toolchain` and `go` line updates as their own
   update type, from the Go release feed.
4. **Rewrite `go:generate` lines and `// import` comments on a major bump.** Only import specs are
   rewritten now; `go run example.com/m/v2/cmd/x` in a directive and canonical import comments are not.
5. **Verify with cgo and private modules.** The sandbox builds with `CGO_ENABLED=0` (cgo files are
   rewritten but not compiled) and only from `RIPPLE_GOPROXY`; support a toolchain image with C
   headers and credentials for private proxies, without exposing the App token to the build.
6. **Supersede and close stale PRs.** A newer release leaves the older ripple PR open next to it.
7. **Backfill for newly registered dependencies.** A repo that starts importing a package only hears
   about releases published afterwards; compare against the latest stored release on registration.
   This also closes the DynamoDB GSI eventual-consistency window.
8. **Default-branch rename handling.** Handle the `repository` rename and default-branch change
   events; today a stale `DefaultBranch` is corrected only on the next push.
9. **Dependency dashboard issue.** One issue per repo listing pending, deferred and failed jobs.
10. **Dynamo hot partition.** Shard `JOBS#pending` and very popular `PKG#` partitions; revisit the
   eventual-consistency notes in `docs/store-dynamodb.md` under load.
11. **More datasources and feeds.** npm replicate, PyPI, crates.io sparse index, Docker/OCI tags.
12. **More managers, toward Renovate parity.** Dockerfile, GitHub Actions, npm, Cargo, pip, Helm.
13. **OSV advisories as an event source.** A security advisory becomes a job that bypasses the cooldown.
14. **GitLab, Bitbucket and Gitea** behind the existing `Platform` interface.
15. **GHES GraphQL** differences and per-host API base handling.
16. **Plugin mechanism** for out-of-tree managers and datasources.
17. **Presets, org-level defaults and packageRules-like matching.** Later and strictly opt-in: a repo
    file must opt into a preset by name, and none of these may ever change the meaning of a plain
    per-repo `.github/ripple.yml`. No implicit inheritance, no rule precedence.
18. **MIGRATION notes policy.** Rules for config-schema and API changes: additive first, deprecate
    across a minor release, `MIGRATION.md` for any break.
