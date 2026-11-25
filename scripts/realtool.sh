#!/usr/bin/env bash
# Runs one real-tool test (internal/manager, build tag realtool) and fails unless it ran and passed:
# a missing tool fails inside the test, and a test that matched nothing or skipped fails here.
# Usage: scripts/realtool.sh TestRealtoolGoMod [TestRealtoolOther ...]   (docs/realtool.md)
set -euo pipefail
cd "$(dirname "$0")/.."
log=$(mktemp)
for t in "$@"; do
  go test -tags realtool -count=1 -v -timeout 20m -run "^${t}\$" ./internal/manager/ | tee -a "$log"
  grep -q -- "--- PASS: ${t} " "$log" || { echo "${t} did not pass"; exit 1; }
done
if grep -q -- '--- SKIP' "$log"; then echo "real-tool tests were skipped"; exit 1; fi
