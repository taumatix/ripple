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
