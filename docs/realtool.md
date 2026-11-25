# Real-tool tests

The manager tests compare ripple's edit with text. The real-tool tests judge it by the tool that will
consume it: a small genuine project under `internal/manager/testdata/realtool/<ecosystem>/` is pinned to an
old version of one public dependency, the manager's `Extract` and `Apply` (the entry points the engine
uses) move it to a newer real version, the test asserts that exactly the intended line changed and every
other line is byte for byte as it was, and then the ecosystem's own tool runs on the updated copy. The same
commands first run on the untouched fixture, so a failure after the edit cannot be the fixture's own.

They are behind the build tag `realtool` (`go test ./...` does not compile them), need the ecosystem's tool
and the public registry, and fail rather than skip when the tool is missing. CI runs them in
`.github/workflows/realtool.yml`, one job per ecosystem, on pull requests that change `internal/manager`,
weekly, and on demand.

Run one locally (from the repository root; `-tags realtool` is added for you):

    scripts/realtool.sh TestRealtoolGoMod

| Test | Tool run on the updated copy | Needs |
|---|---|---|
| `TestRealtoolGoMod` | `go mod tidy`, `go build ./...`, `go mod verify` | go |
| `TestRealtoolCargo` | `cargo metadata`, `cargo check` | cargo |
| `TestRealtoolNpm` | `npm install --package-lock-only --ignore-scripts`, `npm ls` | npm |
| `TestRealtoolPip` | `pip download --no-deps` | python3 with pip |

Lock files: ripple edits the manifest and reports the lock file stale (the PR body says which to refresh).
These tests assert the second half: the tool's own refresh command accepts the edited manifest and writes
the lock at the new version. The stale note itself is the engine's, covered by its own tests.
