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
| `TestRealtoolHelm` | `helm dependency update`, `helm dependency list`, `helm lint` | helm |
| `TestRealtoolBundler`, `TestRealtoolBundlerTwoSegment`, `TestRealtoolBundlerOneSegment` | `bundle lock`; the last two also assert which rack the untouched `~> 1` / edited constraint resolves to | bundler |
| `TestRealtoolComposer`, `TestRealtoolComposerTwoSegment` | `composer validate`, `composer update --dry-run`, `composer update --no-install` | php, composer |
| `TestRealtoolComposerConstraintMeaning` | `composer update --no-install` on one constraint (`~1`, `^1`, `~1.2`, `^1.2`, `~0.3`, `^0.3`, `^0.7`, `^0.8`, `^0.9`, `^0`) and the version it locks | php, composer |
| `TestRealtoolNuget` | `dotnet restore`, `dotnet build --no-restore` | dotnet 8 |
| `TestRealtoolMaven` | `mvn validate`, `dependency:resolve`, `compile` | mvn, JDK 17 |
| `TestRealtoolTerraform` | `terraform init -backend=false`, `terraform validate` | terraform |
| `TestRealtoolGitHubActions` | `actionlint`, plus `git ls-remote` that the new tag exists | actionlint, git |
| `TestRealtoolDocker` | `hadolint` (no daemon), plus a Docker Hub check that the new tag exists | hadolint |

Lock files: ripple edits the manifest and reports the lock file stale (the PR body says which to refresh).
These tests assert the second half: the tool's own refresh command accepts the edited manifest and writes
the lock at the new version. The stale note itself is the engine's, covered by its own tests.

Flakes: a step that reaches a public registry is retried (3 attempts, 15 s then 30 s apart, 5 minutes each)
and, when the attempts run out, fails with the tool's own output of the last one; a step that touches no
registry (`go build`, `helm lint`, `terraform validate`, ...) fails at once. A real rejection of ripple's edit
fails every attempt the same way, so retrying does not hide it. The Maven test names the dependency plugin by
its full coordinates so no plugin-prefix lookup against Maven Central is needed, and the Cargo, Composer,
NuGet and Maven jobs cache their download directories. What still depends on a live registry at run time:
every ecosystem's registry, the Terraform module's source on GitHub, the tag checks against GitHub and Docker
Hub, and the floating tool versions (`dtolnay/rust-toolchain@stable`, Node 22, Ruby 3.3, .NET 8.0.x, the
latest Terraform).
