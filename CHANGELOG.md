# Changelog

All notable changes are listed here. The format follows Keep a Changelog; versions follow SemVer.

## [Unreleased]

### Added
- Event-driven updater: polls the Go module index once, finds importing repositories through a
  reverse index, and opens pull requests only there.
- `.github/ripple.yml` per-repo configuration with strict validation; `ripple config validate`.
- Managers: `gomod` and a generic `regex` manager.
- Stores: Postgres and DynamoDB (single table, documented GSI), plus an in-memory store.
- minimumReleaseAge, grouping, per-rule automerge, schedule windows and an open-PR limit.
- GitHub App webhook receiver and `ripple serve`; Dockerfile.
- Go major-version updates: the reverse index keys on the module line, so `/vN` to `/vN+1`, an
  unsuffixed path to `/v2` and `gopkg.in/x.v2` to `.v3` are found; `go.mod` and all import paths are
  rewritten with `go/parser`, verified with `go mod tidy`, `go build` and `go vet`, opened as a
  labelled `[major]` PR (a draft with the compiler output if verification fails), never auto-merged
  unless the repo file says `automerge: major`. New server setting `RIPPLE_GOPROXY`.

### Security
- `go` is no longer run in a checkout containing a symlinked `go.mod`, `go.sum`, `go.work` or `.go`
  file, or a `replace` to an absolute or out-of-repository path; the job fails permanently, no PR.

### Fixed
- The regex manager replaced only some matches when several patterns matched out of offset order;
  matches are now sorted, and overlapping ones are rejected.
- A major bump no longer fails because one `.go` file does not parse (it is listed in the PR body),
  and no longer rewrites `testdata/` or directories starting with `_` or `.`.
- Corrected the `webhook.Sink` comment: GitHub does not redeliver a failed webhook by itself.

### Documentation
- README section "Known limitations and security model"; ROADMAP entries for an OS sandbox,
  durable per-installation queues, scan bounds, webhook body bounds, per-command tokens and delivery dedupe.
